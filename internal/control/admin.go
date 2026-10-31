package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"qanat/internal/core/quota"
	"qanat/internal/store/db"
)

// --- users -------------------------------------------------------------------

type userResponse struct {
	ID         string     `json:"id"`
	Email      string     `json:"email"`
	Name       string     `json:"name"`
	Username   *string    `json:"username"`
	Role       string     `json:"role"`
	PlanID     string     `json:"plan_id"`
	PlanName   string     `json:"plan_name"`
	ActiveKeys int64      `json:"active_keys"`
	CreatedAt  time.Time  `json:"created_at"`
	DisabledAt *time.Time `json:"disabled_at"`
	BannedAt   *time.Time `json:"banned_at"`
	// DisabledReason is the admin's note when disabling; required for a ban.
	DisabledReason string `json:"disabled_reason"`
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	rows, err := s.q.ListUsers(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]userResponse, 0, len(rows))
	for _, u := range rows {
		out = append(out, userResponse{u.ID, u.Email, u.Name, u.Username, u.Role, u.PlanID, u.PlanName, u.ActiveKeys, u.CreatedAt, u.DisabledAt, u.BannedAt, u.DisabledReason})
	}
	writeJSON(w, http.StatusOK, out)
}

type updateUserRequest struct {
	Role   *string `json:"role"`
	PlanID *string `json:"plan_id"`
}

// updateUser changes a user's role or Plan; both are audited (REQ-USER-02).
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	var req updateUserRequest
	if !decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	target, err := s.q.GetUser(ctx, r.PathValue("id"))
	if notFound(err) || isBadUUID(err) {
		fail(w, http.StatusNotFound, "not_found", "That user doesn't exist.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if req.Role != nil && *req.Role != target.Role {
		if *req.Role != "user" && *req.Role != "admin" {
			fail(w, http.StatusBadRequest, "invalid_role", `Role must be "user" or "admin".`)
			return
		}
		if target.ID == actor.ID {
			fail(w, http.StatusBadRequest, "own_role", "You can't change your own role. Ask another admin.")
			return
		}
		if target.Username != nil && *target.Username == "root" {
			fail(w, http.StatusBadRequest, "root_role", "The root admin is always an admin.")
			return
		}
		if _, err := s.q.SetUserRole(ctx, db.SetUserRoleParams{ID: target.ID, Role: *req.Role}); err != nil {
			s.internal(w, r, err)
			return
		}
		s.audit(ctx, &actor.ID, "user.role", "user", target.ID, map[string]string{"role": target.Role}, map[string]string{"role": *req.Role})
	}
	if req.PlanID != nil && *req.PlanID != target.PlanID {
		plan, err := s.q.GetPlan(ctx, *req.PlanID)
		if notFound(err) || isBadUUID(err) {
			fail(w, http.StatusBadRequest, "invalid_plan", "That Plan doesn't exist. Pick one from the Plans page.")
			return
		}
		if err != nil {
			s.internal(w, r, err)
			return
		}
		if _, err := s.q.SetUserPlan(ctx, db.SetUserPlanParams{ID: target.ID, PlanID: plan.ID}); err != nil {
			s.internal(w, r, err)
			return
		}
		s.audit(ctx, &actor.ID, "user.plan", "user", target.ID,
			map[string]string{"plan_id": target.PlanID, "plan": target.PlanName}, map[string]string{"plan_id": plan.ID, "plan": plan.Name})
	}
	w.WriteHeader(http.StatusNoContent)
}

// userTarget loads the {id} user for an admin action, refusing the root
// admin and the acting admin themselves. It writes the error when it fails.
func (s *Server) userTarget(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow, verb string) (db.GetUserRow, bool) {
	target, err := s.q.GetUser(r.Context(), r.PathValue("id"))
	if notFound(err) || isBadUUID(err) {
		fail(w, http.StatusNotFound, "not_found", "That user doesn't exist.")
		return target, false
	}
	if err != nil {
		s.internal(w, r, err)
		return target, false
	}
	if target.Username != nil && *target.Username == "root" {
		fail(w, http.StatusBadRequest, "root_user", "The root admin can't be "+verb+".")
		return target, false
	}
	if target.ID == actor.ID {
		fail(w, http.StatusBadRequest, "own_account", "You can't do this to your own account. Ask another admin.")
		return target, false
	}
	return target, true
}

type disableUserRequest struct {
	Ban    bool   `json:"ban"`
	Reason string `json:"reason"`
}

// disableUser blocks a user from the dashboard and the gateway. A ban also
// revokes every API key and needs a reason; Enable doesn't bring keys back.
func (s *Server) disableUser(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	var req disableUserRequest
	if !decode(w, r, &req) {
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Ban && req.Reason == "" {
		fail(w, http.StatusBadRequest, "reason_required", "Say why you're banning this user. The reason is kept in the audit log.")
		return
	}
	if len(req.Reason) > 500 {
		fail(w, http.StatusBadRequest, "reason_too_long", "Keep the reason under 500 characters.")
		return
	}
	verb := "disabled"
	if req.Ban {
		verb = "banned"
	}
	target, ok := s.userTarget(w, r, actor, verb)
	if !ok {
		return
	}
	ctx := r.Context()
	var revoked int64
	err := pgx.BeginFunc(ctx, s.pg, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.DisableUser(ctx, db.DisableUserParams{ID: target.ID, Ban: req.Ban, Reason: req.Reason}); err != nil {
			return err
		}
		if !req.Ban {
			return nil
		}
		var err error
		revoked, err = q.RevokeUserKeys(ctx, target.ID)
		return err
	})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	action := "user.disable"
	after := map[string]any{"reason": req.Reason}
	if req.Ban {
		action = "user.ban"
		after["keys_revoked"] = revoked
	}
	s.audit(ctx, &actor.ID, action, "user", target.ID, map[string]bool{"disabled": target.DisabledAt != nil}, after)
	w.WriteHeader(http.StatusNoContent)
}

// enableUser lifts a disable or ban. Keys revoked by a ban stay revoked.
func (s *Server) enableUser(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	target, ok := s.userTarget(w, r, actor, "enabled")
	if !ok {
		return
	}
	if target.DisabledAt == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ctx := r.Context()
	if _, err := s.q.EnableUser(ctx, target.ID); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(ctx, &actor.ID, "user.enable", "user", target.ID, map[string]bool{"disabled": true}, map[string]bool{"disabled": false})
	w.WriteHeader(http.StatusNoContent)
}

// userQuotas is a user's quota windows as the admin sees them before a reset.
func (s *Server) userQuotas(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	ctx := r.Context()
	target, err := s.q.GetUser(ctx, r.PathValue("id"))
	if notFound(err) || isBadUUID(err) {
		fail(w, http.StatusNotFound, "not_found", "That user doesn't exist.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out, err := s.quotasFor(ctx, target.ID, target.PlanID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type resetQuotaRequest struct {
	Windows []struct {
		Window     quota.Kind `json:"window"`
		ModelScope *string    `json:"model_scope"`
	} `json:"windows"`
}

// resetQuota zeroes the chosen windows' current counters (PRD §10.3 manual
// reset). Recorded usage and reports are untouched; only headroom returns.
func (s *Server) resetQuota(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	var req resetQuotaRequest
	if !decode(w, r, &req) {
		return
	}
	if len(req.Windows) == 0 {
		fail(w, http.StatusBadRequest, "no_windows", "Pick at least one quota window to reset.")
		return
	}
	ctx := r.Context()
	target, err := s.q.GetUser(ctx, r.PathValue("id"))
	if notFound(err) || isBadUUID(err) {
		fail(w, http.StatusNotFound, "not_found", "That user doesn't exist.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	cur, err := s.quotasFor(ctx, target.ID, target.PlanID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	plan, err := s.q.GetPlan(ctx, target.PlanID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	a := anchorsOf(plan.Timezone, plan.DailyAnchor.Microseconds, plan.WeeklyAnchor, plan.MonthlyAnchor)
	now := s.now()
	var keys []string
	type resetNote struct {
		Window     quota.Kind `json:"window"`
		ModelScope *string    `json:"model_scope"`
		UsedUSD    float64    `json:"used_usd"`
	}
	notes := []resetNote{}
	for _, want := range req.Windows {
		var hit *quotaWindow
		for i, win := range cur.Windows {
			if win.Window == want.Window && deref(win.ModelScope) == deref(want.ModelScope) {
				hit = &cur.Windows[i]
			}
		}
		if hit == nil {
			fail(w, http.StatusBadRequest, "invalid_window", "That quota window isn't on this user's Plan. Reload and pick again.")
			return
		}
		keys = append(keys, counterKeys(target.ID, hit.Window, deref(hit.ModelScope), a, now)...)
		notes = append(notes, resetNote{hit.Window, hit.ModelScope, hit.UsedUSD})
	}
	if err := s.rdb.Del(ctx, keys...).Err(); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(ctx, &actor.ID, "user.quota_reset", "user", target.ID, map[string]any{"windows": notes}, nil)
	w.WriteHeader(http.StatusNoContent)
}

// --- plans -------------------------------------------------------------------

type planQuota struct {
	Window     quota.Kind `json:"window"`
	ModelScope *string    `json:"model_scope"`
	LimitUSD   float64    `json:"limit_usd"`
}

type planLimits struct {
	RPM           int32 `json:"rpm"`
	MaxConcurrent int32 `json:"max_concurrent"`
}

// planBody is what an admin edits (REQ-PLAN-02: quotas, limits, allowlist,
// plus the reset schedule from §10.3).
type planBody struct {
	Name          string      `json:"name"`
	Timezone      string      `json:"timezone"`
	DailyAnchor   string      `json:"daily_anchor"`   // "HH:MM"
	WeeklyAnchor  int16       `json:"weekly_anchor"`  // 0 = Sunday … 6 = Saturday
	MonthlyAnchor int16       `json:"monthly_anchor"` // 1–28
	Quotas        []planQuota `json:"quotas"`
	Limits        planLimits  `json:"limits"`
	Models        []string    `json:"models"` // empty = all models
}

type planResponse struct {
	ID string `json:"id"`
	planBody
	IsDefault bool      `json:"is_default"`
	UserCount int64     `json:"user_count"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Server) listPlans(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	rows, err := s.q.ListPlans(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]planResponse, 0, len(rows))
	for _, p := range rows {
		resp, err := s.loadPlan(r, p.ID)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		resp.UserCount = p.UserCount
		out = append(out, *resp)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	resp, err := s.loadPlan(r, r.PathValue("id"))
	if notFound(err) || isBadUUID(err) {
		fail(w, http.StatusNotFound, "not_found", "That Plan doesn't exist.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) loadPlan(r *http.Request, id string) (*planResponse, error) {
	ctx := r.Context()
	p, err := s.q.GetPlan(ctx, id)
	if err != nil {
		return nil, err
	}
	quotas, err := s.q.ListPlanQuotas(ctx, id)
	if err != nil {
		return nil, err
	}
	models, err := s.q.ListPlanModels(ctx, id)
	if err != nil {
		return nil, err
	}
	limits, err := s.q.GetPlanLimits(ctx, id)
	if err != nil && !notFound(err) {
		return nil, err
	}
	resp := &planResponse{
		ID: p.ID, IsDefault: p.IsDefault, UpdatedAt: p.UpdatedAt,
		planBody: planBody{
			Name: p.Name, Timezone: p.Timezone, DailyAnchor: formatAnchor(p.DailyAnchor),
			WeeklyAnchor: p.WeeklyAnchor, MonthlyAnchor: p.MonthlyAnchor,
			Quotas: make([]planQuota, 0, len(quotas)), Models: models,
			Limits: planLimits{RPM: limits.Rpm, MaxConcurrent: limits.MaxConcurrent},
		},
	}
	for _, q := range quotas {
		resp.Quotas = append(resp.Quotas, planQuota{quota.Kind(q.WindowKind), q.ModelScope, q.LimitUsd})
	}
	return resp, nil
}

// validate normalizes b and returns a readable problem, or "".
func (b *planBody) validate(isDefault bool) (anchor pgtype.Time, problem string) {
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" || len(b.Name) > 64 {
		return anchor, "Give the Plan a name of 1 to 64 characters."
	}
	if b.Timezone == "" {
		b.Timezone = "Asia/Tehran"
	}
	if _, err := time.LoadLocation(b.Timezone); err != nil {
		return anchor, fmt.Sprintf("%q is not a known time zone. Use an IANA name such as Asia/Tehran.", b.Timezone)
	}
	if b.DailyAnchor == "" {
		b.DailyAnchor = "00:00"
	}
	t, err := time.Parse("15:04", b.DailyAnchor)
	if err != nil {
		return anchor, "The daily reset time must look like 00:00 (24-hour)."
	}
	anchor = pgtype.Time{Microseconds: int64(t.Hour()*60+t.Minute()) * int64(time.Minute/time.Microsecond), Valid: true}
	if b.WeeklyAnchor < 0 || b.WeeklyAnchor > 6 {
		return anchor, "The weekly reset day must be 0 (Sunday) to 6 (Saturday)."
	}
	if b.MonthlyAnchor == 0 {
		b.MonthlyAnchor = 1
	}
	if b.MonthlyAnchor < 1 || b.MonthlyAnchor > 28 {
		return anchor, "The monthly reset day must be 1 to 28, so it exists in every month."
	}
	if isDefault && len(b.Quotas) > 0 {
		return anchor, "The default Plan must keep zero quota, so new users can't spend until you assign them a Plan (REQ-PLAN-03)."
	}
	seen := map[string]bool{}
	for i := range b.Quotas {
		q := &b.Quotas[i]
		if !q.Window.Valid() {
			return anchor, fmt.Sprintf("%q is not a quota window. Use rolling_5h, daily, weekly or monthly.", q.Window)
		}
		if q.LimitUSD < 0 {
			return anchor, "Quota limits can't be negative."
		}
		if q.ModelScope != nil {
			if trimmed := strings.TrimSpace(*q.ModelScope); trimmed == "" {
				q.ModelScope = nil
			} else {
				q.ModelScope = &trimmed
			}
		}
		key := string(q.Window) + "/" + deref(q.ModelScope)
		if seen[key] {
			return anchor, "Each window can appear once per model scope."
		}
		seen[key] = true
	}
	if b.Limits.RPM < 0 || b.Limits.MaxConcurrent < 0 {
		return anchor, "Rate limits can't be negative; use 0 for unlimited."
	}
	models := b.Models[:0]
	dup := map[string]bool{}
	for _, m := range b.Models {
		if m = strings.TrimSpace(m); m != "" && !dup[m] {
			dup[m] = true
			models = append(models, m)
		}
	}
	b.Models = models
	return anchor, ""
}

func (s *Server) createPlan(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	var b planBody
	if !decode(w, r, &b) {
		return
	}
	anchor, problem := b.validate(false)
	if problem != "" {
		fail(w, http.StatusBadRequest, "invalid_plan", problem)
		return
	}
	var id string
	err := pgx.BeginFunc(r.Context(), s.pg, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var err error
		id, err = q.CreatePlan(r.Context(), db.CreatePlanParams{Name: b.Name, Timezone: b.Timezone, DailyAnchor: anchor, WeeklyAnchor: b.WeeklyAnchor, MonthlyAnchor: b.MonthlyAnchor})
		if err != nil {
			return err
		}
		return writePlanChildren(r, q, id, &b)
	})
	if isUniqueViolation(err) {
		fail(w, http.StatusConflict, "duplicate_name", "A Plan with that name already exists. Pick another name.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r.Context(), &actor.ID, "plan.create", "plan", id, nil, b)
	resp, err := s.loadPlan(r, id)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) updatePlan(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	var b planBody
	if !decode(w, r, &b) {
		return
	}
	before, err := s.loadPlan(r, r.PathValue("id"))
	if notFound(err) || isBadUUID(err) {
		fail(w, http.StatusNotFound, "not_found", "That Plan doesn't exist.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	anchor, problem := b.validate(before.IsDefault)
	if problem != "" {
		fail(w, http.StatusBadRequest, "invalid_plan", problem)
		return
	}
	err = pgx.BeginFunc(r.Context(), s.pg, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.UpdatePlan(r.Context(), db.UpdatePlanParams{ID: before.ID, Name: b.Name, Timezone: b.Timezone, DailyAnchor: anchor, WeeklyAnchor: b.WeeklyAnchor, MonthlyAnchor: b.MonthlyAnchor}); err != nil {
			return err
		}
		if err := q.DeletePlanQuotas(r.Context(), before.ID); err != nil {
			return err
		}
		if err := q.DeletePlanModels(r.Context(), before.ID); err != nil {
			return err
		}
		return writePlanChildren(r, q, before.ID, &b)
	})
	if isUniqueViolation(err) {
		fail(w, http.StatusConflict, "duplicate_name", "A Plan with that name already exists. Pick another name.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r.Context(), &actor.ID, "plan.update", "plan", before.ID, before.planBody, b)
	resp, err := s.loadPlan(r, before.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func writePlanChildren(r *http.Request, q *db.Queries, id string, b *planBody) error {
	for _, qt := range b.Quotas {
		if err := q.InsertPlanQuota(r.Context(), db.InsertPlanQuotaParams{PlanID: id, WindowKind: string(qt.Window), ModelScope: qt.ModelScope, LimitUsd: qt.LimitUSD}); err != nil {
			return err
		}
	}
	for _, m := range b.Models {
		if err := q.InsertPlanModel(r.Context(), db.InsertPlanModelParams{PlanID: id, ModelName: m}); err != nil {
			return err
		}
	}
	return q.UpsertPlanLimits(r.Context(), db.UpsertPlanLimitsParams{PlanID: id, Rpm: b.Limits.RPM, MaxConcurrent: b.Limits.MaxConcurrent})
}

func (s *Server) deletePlan(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	before, err := s.loadPlan(r, r.PathValue("id"))
	if notFound(err) || isBadUUID(err) {
		fail(w, http.StatusNotFound, "not_found", "That Plan doesn't exist.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if before.IsDefault {
		fail(w, http.StatusBadRequest, "default_plan", "The default Plan can't be deleted; new users are placed on it.")
		return
	}
	n, err := s.q.DeletePlan(r.Context(), before.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if n == 0 {
		fail(w, http.StatusConflict, "plan_in_use", "Users are still on this Plan. Move them to another Plan first.")
		return
	}
	s.audit(r.Context(), &actor.ID, "plan.delete", "plan", before.ID, before.planBody, nil)
	w.WriteHeader(http.StatusNoContent)
}

// --- audit -------------------------------------------------------------------

type auditEntry struct {
	ID         int64           `json:"id"`
	ActorID    *string         `json:"actor_id"`
	ActorEmail string          `json:"actor_email"`
	Action     string          `json:"action"`
	Entity     string          `json:"entity"`
	EntityID   *string         `json:"entity_id"`
	Before     json.RawMessage `json:"before"`
	After      json.RawMessage `json:"after"`
	At         time.Time       `json:"at"`
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	limit := int32(50)
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = int32(v)
	}
	var before pgtype.Int8
	if v, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64); err == nil {
		before = pgtype.Int8{Int64: v, Valid: true}
	}
	rows, err := s.q.ListAudit(r.Context(), db.ListAuditParams{BeforeID: before, RowLimit: limit})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]auditEntry, 0, len(rows))
	raw := func(b []byte) json.RawMessage {
		if b == nil {
			return json.RawMessage("null")
		}
		return b
	}
	for _, a := range rows {
		out = append(out, auditEntry{a.ID, a.ActorID, a.ActorEmail, a.Action, a.Entity, a.EntityID, raw(a.Before), raw(a.After), a.At})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- helpers -----------------------------------------------------------------

func formatAnchor(t pgtype.Time) string {
	mins := t.Microseconds / int64(time.Minute/time.Microsecond)
	return fmt.Sprintf("%02d:%02d", mins/60, mins%60)
}

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
