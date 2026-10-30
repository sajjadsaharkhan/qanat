package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"

	"qanat/internal/core/apikey"
	"qanat/internal/core/quota"
	"qanat/internal/core/usage"
	"qanat/internal/gateway"
	"qanat/internal/store/db"
)

type meResponse struct {
	ID       string  `json:"id"`
	Email    string  `json:"email"`
	Name     string  `json:"name"`
	Username *string `json:"username"`
	Role     string  `json:"role"`
	PlanID   string  `json:"plan_id"`
	PlanName string  `json:"plan_name"`
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request, u *db.GetUserRow) {
	writeJSON(w, http.StatusOK, meResponse{u.ID, u.Email, u.Name, u.Username, u.Role, u.PlanID, u.PlanName})
}

// --- quotas ------------------------------------------------------------------

type quotaWindow struct {
	Window       quota.Kind `json:"window"`
	ModelScope   *string    `json:"model_scope"`
	LimitUSD     float64    `json:"limit_usd"`
	UsedUSD      float64    `json:"used_usd"`
	RemainingUSD float64    `json:"remaining_usd"`
	// ResetsAt is when a fixed window starts over; null for the rolling
	// window, which frees capacity bucket by bucket.
	ResetsAt *time.Time `json:"resets_at"`
}

type quotasResponse struct {
	Timezone string        `json:"timezone"`
	Windows  []quotaWindow `json:"windows"`
}

// myQuotas reports every window of the user's Plan from the hot counters the
// gateway maintains (PRD §10.6: quota reads come from these counters only).
func (s *Server) myQuotas(w http.ResponseWriter, r *http.Request, u *db.GetUserRow) {
	out, err := s.quotasFor(r.Context(), u.ID, u.PlanID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// quotasFor reads one user's quota windows; admin views use it per user.
func (s *Server) quotasFor(ctx context.Context, userID, planID string) (quotasResponse, error) {
	plan, err := s.q.GetPlan(ctx, planID)
	if err != nil {
		return quotasResponse{}, err
	}
	rows, err := s.q.ListPlanQuotas(ctx, planID)
	if err != nil {
		return quotasResponse{}, err
	}
	a := anchorsOf(plan.Timezone, plan.DailyAnchor.Microseconds, plan.WeeklyAnchor, plan.MonthlyAnchor)
	now := s.now()
	out := quotasResponse{Timezone: plan.Timezone, Windows: []quotaWindow{}}
	for _, row := range rows {
		k := quota.Kind(row.WindowKind)
		scope := ""
		if row.ModelScope != nil {
			scope = *row.ModelScope
		}
		used, err := s.usedMicro(ctx, userID, k, scope, a, now)
		if err != nil {
			return quotasResponse{}, err
		}
		win := quotaWindow{Window: k, ModelScope: row.ModelScope, LimitUSD: row.LimitUsd, UsedUSD: quota.FromMicro(used)}
		win.RemainingUSD = max(0, win.LimitUSD-win.UsedUSD)
		if _, reset := quota.Bounds(k, a, now); !reset.IsZero() {
			win.ResetsAt = &reset
		}
		out.Windows = append(out.Windows, win)
	}
	return out, nil
}

func (s *Server) usedMicro(ctx context.Context, userID string, k quota.Kind, scope string, a quota.Anchors, now time.Time) (int64, error) {
	if k == quota.Rolling5h {
		vals, err := s.rdb.MGet(ctx, quota.BucketKeys(userID, scope, now)...).Result()
		if err != nil {
			return 0, err
		}
		var sum int64
		for _, v := range vals {
			if str, ok := v.(string); ok {
				n, _ := strconv.ParseInt(str, 10, 64)
				sum += n
			}
		}
		return sum, nil
	}
	start, _ := quota.Bounds(k, a, now)
	n, err := s.rdb.Get(ctx, quota.FixedKey(userID, k, scope, start)).Int64()
	if err != nil && !errors.Is(err, redis.Nil) {
		return 0, err
	}
	return n, nil
}

func anchorsOf(tz string, dailyMicros int64, weekday, monthDay int16) quota.Anchors {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	mins := int(dailyMicros / int64(time.Minute/time.Microsecond))
	return quota.Anchors{Location: loc, Hour: mins / 60, Minute: mins % 60, Weekday: time.Weekday(weekday), MonthDay: int(monthDay)}
}

// --- keys --------------------------------------------------------------------

type keyResponse struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Prefix     string      `json:"prefix"`
	ExpiresAt  *time.Time  `json:"expires_at"`
	LastUsedAt *time.Time  `json:"last_used_at"`
	LastIP     *netip.Addr `json:"last_ip"`
	RevokedAt  *time.Time  `json:"revoked_at"`
	CreatedAt  time.Time   `json:"created_at"`
}

func (s *Server) listKeys(w http.ResponseWriter, r *http.Request, u *db.GetUserRow) {
	rows, err := s.q.ListKeys(r.Context(), u.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]keyResponse, 0, len(rows))
	for _, k := range rows {
		out = append(out, keyResponse{k.ID, k.Name, k.Prefix, k.ExpiresAt, k.LastUsedAt, k.LastIp, k.RevokedAt, k.CreatedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

type createKeyRequest struct {
	Name          string `json:"name"`
	ExpiresInDays *int   `json:"expires_in_days"`
}

type createKeyResponse struct {
	keyResponse
	// Key is the full secret, returned only here and never again (§13.2).
	Key string `json:"key"`
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request, u *db.GetUserRow) {
	var req createKeyRequest
	if !decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 64 {
		fail(w, http.StatusBadRequest, "invalid_name", "Give the key a name of 1 to 64 characters, such as the machine it's for.")
		return
	}
	var expires *time.Time
	if req.ExpiresInDays != nil {
		if *req.ExpiresInDays < 1 || *req.ExpiresInDays > 3650 {
			fail(w, http.StatusBadRequest, "invalid_expiry", "Expiry must be between 1 and 3650 days, or empty for no expiry.")
			return
		}
		t := s.now().Add(time.Duration(*req.ExpiresInDays) * 24 * time.Hour)
		expires = &t
	}
	key, display, hash := apikey.New()
	row, err := s.q.CreateKey(r.Context(), db.CreateKeyParams{UserID: u.ID, Name: req.Name, Hash: hash, Prefix: display, ExpiresAt: expires})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r.Context(), &u.ID, "key.create", "api_key", row.ID, nil, map[string]any{"name": row.Name, "prefix": row.Prefix, "expires_at": row.ExpiresAt})
	writeJSON(w, http.StatusCreated, createKeyResponse{
		keyResponse: keyResponse{row.ID, row.Name, row.Prefix, row.ExpiresAt, row.LastUsedAt, row.LastIp, row.RevokedAt, row.CreatedAt},
		Key:         key,
	})
}

func (s *Server) revokeKey(w http.ResponseWriter, r *http.Request, u *db.GetUserRow) {
	row, err := s.q.RevokeKey(r.Context(), db.RevokeKeyParams{ID: r.PathValue("id"), UserID: u.ID})
	if notFound(err) || isBadUUID(err) {
		fail(w, http.StatusNotFound, "not_found", "That key doesn't exist or is already revoked.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r.Context(), &u.ID, "key.revoke", "api_key", row.ID, map[string]string{"name": row.Name, "prefix": row.Prefix}, nil)
	w.WriteHeader(http.StatusNoContent)
}

// --- setup -------------------------------------------------------------------

type setupResponse struct {
	GatewayURL string `json:"gateway_url"`
}

func (s *Server) setup(w http.ResponseWriter, _ *http.Request, _ *db.GetUserRow) {
	writeJSON(w, http.StatusOK, setupResponse{GatewayURL: s.opts.GatewayURL})
}

type setupModelsRequest struct {
	Key string `json:"key"`
}

type setupModelsResponse struct {
	Models []string `json:"models"`
}

// setupModels answers what the gateway's /v1/models would list for one of
// the caller's own keys, so the Setup page can build a model picker without
// the dashboard talking to the data plane (design.md §10.3 rule 7). It reads
// a fresh snapshot, the same one the gateway builds, so the lists match.
func (s *Server) setupModels(w http.ResponseWriter, r *http.Request, u *db.GetUserRow) {
	var req setupModelsRequest
	if !decode(w, r, &req) {
		return
	}
	key := strings.TrimSpace(req.Key)
	invalid := func() {
		fail(w, http.StatusBadRequest, "invalid_key", "That isn't one of your active Qanat API keys. Paste a key from the Keys page.")
	}
	if !apikey.LooksValid(key) {
		invalid()
		return
	}
	snap, err := gateway.Load(r.Context(), s.pg, s.opts.Box, s.log)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	k, _, p, ok := snap.Lookup(key)
	// Someone else's key reads as invalid: the page never confirms a key
	// exists unless it is the caller's.
	if !ok || k.UserID != u.ID || (k.ExpiresAt != nil && s.now().After(*k.ExpiresAt)) {
		invalid()
		return
	}
	names := snap.ModelNames(p, k)
	sort.Strings(names)
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, setupModelsResponse{Models: names})
}

// --- audit -------------------------------------------------------------------

// audit records a change (PRD §14.2). A failed audit write is logged, not
// surfaced: the change itself has already happened.
func (s *Server) audit(ctx context.Context, actor *string, action, entity, entityID string, before, after any) {
	enc := func(v any) []byte {
		if v == nil {
			return nil
		}
		b, _ := json.Marshal(v)
		return b
	}
	err := s.q.InsertAudit(context.WithoutCancel(ctx), db.InsertAuditParams{
		ActorID: actor, Action: action, Entity: entity, EntityID: &entityID, Before: enc(before), After: enc(after),
	})
	if err != nil {
		s.log.ErrorContext(ctx, "audit write failed", "action", action, "err", err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// isBadUUID reports PostgreSQL's invalid-uuid error (SQLSTATE 22P02), which
// a malformed id in the path produces.
func isBadUUID(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "22P02"
}

// --- recent requests -----------------------------------------------------------

// recentRequests returns the caller's last requests from the ring buffer the
// gateway keeps (PRD §10.6 point 7). Metadata only; never content.
func (s *Server) recentRequests(w http.ResponseWriter, r *http.Request, u *db.GetUserRow) {
	raw, err := s.rdb.LRange(r.Context(), usage.RecentKey(u.ID), 0, usage.RecentLen-1).Result()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]json.RawMessage, 0, len(raw))
	for _, e := range raw {
		out = append(out, json.RawMessage(e))
	}
	writeJSON(w, http.StatusOK, out)
}
