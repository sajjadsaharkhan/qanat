package control

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"qanat/internal/store/db"
)

// --- usage reports (PRD §13.3 "Usage") ------------------------------------------

// reportDays are the ranges the dashboard offers, read from ?days=.
var reportDays = map[int]bool{7: true, 30: true, 90: true}

func parseDays(w http.ResponseWriter, r *http.Request) (int, bool) {
	v := r.URL.Query().Get("days")
	if v == "" {
		return 30, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || !reportDays[n] {
		fail(w, http.StatusBadRequest, "invalid_range", "days must be 7, 30 or 90.")
		return 0, false
	}
	return n, true
}

// usageSums is the token, cost and request totals of a slice of usage.
type usageSums struct {
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	RequestCount     int64   `json:"request_count"`
	AbortedCount     int64   `json:"aborted_count"` // REQ-QUOTA-01: streams cut mid-way
}

func (a *usageSums) add(b usageSums) {
	a.InputTokens += b.InputTokens
	a.OutputTokens += b.OutputTokens
	a.CacheReadTokens += b.CacheReadTokens
	a.CacheWriteTokens += b.CacheWriteTokens
	a.CostUSD += b.CostUSD
	a.RequestCount += b.RequestCount
	a.AbortedCount += b.AbortedCount
}

type usageTotalsReport struct {
	usageSums
	// CacheHitRate is cache reads over all input-side tokens (input, cache
	// read, cache write); null with no input.
	CacheHitRate *float64 `json:"cache_hit_rate"`
}

// dayModel is one model's usage on one day; the chart stacks these.
type dayModel struct {
	Model    string `json:"model"`
	Provider string `json:"provider"`
	usageSums
}

type usageDay struct {
	Date   string     `json:"date"` // YYYY-MM-DD, UTC
	Models []dayModel `json:"models"`
}

type modelUsage struct {
	Model    string `json:"model"`
	Provider string `json:"provider"`
	usageSums
}

type keyUsage struct {
	KeyID        string  `json:"key_id"`
	Name         string  `json:"name"`
	UserEmail    string  `json:"user_email"`
	Revoked      bool    `json:"revoked"`
	Tokens       int64   `json:"tokens"`
	CostUSD      float64 `json:"cost_usd"`
	RequestCount int64   `json:"request_count"`
}

type usageReport struct {
	Days    int               `json:"days"`
	Since   string            `json:"since"` // first day, YYYY-MM-DD (UTC)
	Totals  usageTotalsReport `json:"totals"`
	Series  []usageDay        `json:"series"` // one entry per day, zero-filled
	ByModel []modelUsage      `json:"by_model"`
	ByKey   []keyUsage        `json:"by_key"`
}

// since is the first UTC day of a days-long range ending today.
func (s *Server) since(days int) time.Time {
	now := s.now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(days - 1))
}

// buildReport reads one user's usage, or everyone's with userID nil.
func (s *Server) buildReport(ctx context.Context, userID *string, days int) (usageReport, error) {
	since := s.since(days)
	rep := usageReport{Days: days, Since: since.Format(time.DateOnly), Series: make([]usageDay, days), ByModel: []modelUsage{}, ByKey: []keyUsage{}}
	for i := range rep.Series {
		rep.Series[i] = usageDay{Date: since.AddDate(0, 0, i).Format(time.DateOnly), Models: []dayModel{}}
	}

	rows, err := s.q.UsageDailyByModel(ctx, db.UsageDailyByModelParams{UserID: userID, Since: since})
	if err != nil {
		return rep, err
	}
	byModel := map[string]*modelUsage{}
	for _, row := range rows {
		sums := usageSums{row.InputTokens, row.OutputTokens, row.CacheReadTokens, row.CacheWriteTokens, row.CostUsd, row.RequestCount, row.AbortedCount}
		if i := int(row.BucketStart.UTC().Sub(since) / (24 * time.Hour)); i >= 0 && i < days {
			rep.Series[i].Models = append(rep.Series[i].Models, dayModel{row.UpstreamModel, row.Provider, sums})
		}
		m := byModel[row.UpstreamModel]
		if m == nil {
			m = &modelUsage{Model: row.UpstreamModel, Provider: row.Provider}
			byModel[row.UpstreamModel] = m
		}
		m.add(sums)
		rep.Totals.add(sums)
	}
	for _, m := range byModel {
		rep.ByModel = append(rep.ByModel, *m)
	}
	sort.Slice(rep.ByModel, func(i, j int) bool { return rep.ByModel[i].CostUSD > rep.ByModel[j].CostUSD })
	if in := rep.Totals.InputTokens + rep.Totals.CacheReadTokens + rep.Totals.CacheWriteTokens; in > 0 {
		r := float64(rep.Totals.CacheReadTokens) / float64(in)
		rep.Totals.CacheHitRate = &r
	}

	keys, err := s.q.UsageByKey(ctx, db.UsageByKeyParams{UserID: userID, Since: since})
	if err != nil {
		return rep, err
	}
	for _, k := range keys {
		rep.ByKey = append(rep.ByKey, keyUsage{k.KeyID, k.KeyName, k.UserEmail, k.Revoked, k.Tokens, k.CostUsd, k.RequestCount})
	}
	return rep, nil
}

func (s *Server) myUsage(w http.ResponseWriter, r *http.Request, u *db.GetUserRow) {
	days, ok := parseDays(w, r)
	if !ok {
		return
	}
	rep, err := s.buildReport(r.Context(), &u.ID, days)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// --- admin: everyone's usage and quota ---------------------------------------------

type userUsageRow struct {
	ID           string        `json:"id"`
	Email        string        `json:"email"`
	Name         string        `json:"name"`
	Username     *string       `json:"username"`
	Role         string        `json:"role"`
	PlanName     string        `json:"plan_name"`
	Disabled     bool          `json:"disabled"`
	CostUSD      float64       `json:"cost_usd"`
	RequestCount int64         `json:"request_count"`
	Tokens       int64         `json:"tokens"`
	LastActive   *string       `json:"last_active"` // last day with usage in range, YYYY-MM-DD
	Quotas       []quotaWindow `json:"quotas"`
	// Tightest is the highest used/limit ratio across the windows (1 = a
	// window is exhausted); null when the Plan has no quota.
	Tightest *float64 `json:"tightest"`
}

type adminUsageResponse struct {
	usageReport
	Users []userUsageRow `json:"users"`
}

func (s *Server) adminUsage(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	days, ok := parseDays(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	rep, err := s.buildReport(ctx, nil, days)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	rows, err := s.q.UsageByUser(ctx, s.since(days))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := adminUsageResponse{usageReport: rep, Users: make([]userUsageRow, 0, len(rows))}
	for _, u := range rows {
		row, err := s.userUsageRow(ctx, u)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		out.Users = append(out.Users, row)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) userUsageRow(ctx context.Context, u db.UsageByUserRow) (userUsageRow, error) {
	row := userUsageRow{ID: u.ID, Email: u.Email, Name: u.Name, Username: u.Username, Role: u.Role, PlanName: u.PlanName,
		Disabled: u.DisabledAt != nil, CostUSD: u.CostUsd, RequestCount: u.RequestCount, Tokens: u.Tokens}
	if u.LastActiveDay.Year() > 1970 {
		d := u.LastActiveDay.UTC().Format(time.DateOnly)
		row.LastActive = &d
	}
	q, err := s.quotasFor(ctx, u.ID, u.PlanID)
	if err != nil {
		return row, err
	}
	row.Quotas = q.Windows
	for _, win := range q.Windows {
		ratio := 1.0
		if win.LimitUSD > 0 {
			ratio = win.UsedUSD / win.LimitUSD
		}
		if row.Tightest == nil || ratio > *row.Tightest {
			row.Tightest = &ratio
		}
	}
	return row, nil
}

type adminUserUsageResponse struct {
	User     userUsageRow `json:"user"`
	Timezone string       `json:"timezone"`
	Report   usageReport  `json:"report"`
}

// adminUserUsage is one person's usage and quota, as they see it themselves.
func (s *Server) adminUserUsage(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	days, ok := parseDays(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	rows, err := s.q.UsageByUser(ctx, s.since(days))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	var found *db.UsageByUserRow
	for i := range rows {
		if rows[i].ID == id {
			found = &rows[i]
		}
	}
	if found == nil {
		fail(w, http.StatusNotFound, "not_found", "That user doesn't exist.")
		return
	}
	row, err := s.userUsageRow(ctx, *found)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	plan, err := s.q.GetPlan(ctx, found.PlanID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	rep, err := s.buildReport(ctx, &id, days)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, adminUserUsageResponse{User: row, Timezone: plan.Timezone, Report: rep})
}
