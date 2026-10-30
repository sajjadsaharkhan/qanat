package control

import (
	"context"
	"testing"
	"time"
)

// PRD §13.3: usage by day and model, by key, and per user with quota state.
func TestUsage_ReportsAndAdminOverview(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	fixed := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	h.s.now = func() time.Time { return fixed }

	var planID, userID, keyID string
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(h.s.pg.QueryRow(ctx, `INSERT INTO plans (name) VALUES ('Team') RETURNING id`).Scan(&planID))
	must(h.s.pg.QueryRow(ctx, `INSERT INTO plan_quotas (plan_id, window_kind, limit_usd) VALUES ($1, 'daily', 10) RETURNING plan_id`, planID).Scan(&planID))
	must(h.s.pg.QueryRow(ctx, `INSERT INTO users (email, plan_id) VALUES ('dev@example.com', $1) RETURNING id`, planID).Scan(&userID))
	must(h.s.pg.QueryRow(ctx, `INSERT INTO api_keys (user_id, name, hash, prefix) VALUES ($1, 'Laptop', '\x01', 'qnt_live_abcd') RETURNING id`, userID).Scan(&keyID))
	insert := func(day time.Time, model string, in, cr int64, cost float64, n int64, aborted bool) {
		_, err := h.s.pg.Exec(ctx, `INSERT INTO usage_daily (bucket_start, user_id, key_id, provider, upstream_model, aborted, price_version,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, request_count)
			VALUES ($1, $2, $3, 'claude', $4, $5, 1, $6, 100, $7, 0, $8, $9)`, day, userID, keyID, model, aborted, in, cr, cost, n)
		must(err)
	}
	today := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	insert(today, "claude-sonnet-4-6", 1000, 3000, 1.5, 3, false)
	insert(today, "claude-opus-4-7", 1000, 0, 4, 2, false)
	insert(today.AddDate(0, 0, -2), "claude-sonnet-4-6", 500, 500, 0.5, 1, true)
	insert(today.AddDate(0, 0, -10), "claude-sonnet-4-6", 1, 1, 99, 1, false) // outside 7 days

	root := h.signInRoot()
	var rep usageReport
	if st := h.do(root, "GET", "/api/admin/usage/"+userID+"?days=7", "", &struct{ Report *usageReport }{&rep}); st != 200 {
		t.Fatalf("user usage: %d", st)
	}
	if len(rep.Series) != 7 || rep.Series[6].Date != "2026-10-30" || len(rep.Series[6].Models) != 2 || len(rep.Series[0].Models) != 0 {
		t.Fatalf("series: %+v", rep.Series)
	}
	if rep.Totals.CostUSD != 6 || rep.Totals.RequestCount != 6 || rep.Totals.AbortedCount != 1 {
		t.Fatalf("totals: %+v", rep.Totals)
	}
	if hr := *rep.Totals.CacheHitRate; hr < 0.58 || hr > 0.59 { // 3500 / (2500 + 3500)
		t.Fatalf("cache hit rate %v", hr)
	}
	if rep.ByModel[0].Model != "claude-opus-4-7" || len(rep.ByKey) != 1 || rep.ByKey[0].Name != "Laptop" {
		t.Fatalf("breakdowns: %+v %+v", rep.ByModel, rep.ByKey)
	}

	var all adminUsageResponse
	if st := h.do(root, "GET", "/api/admin/usage?days=30", "", &all); st != 200 {
		t.Fatalf("admin usage: %d", st)
	}
	top := all.Users[0]
	if top.ID != userID || top.CostUSD != 105 || top.PlanName != "Team" || len(top.Quotas) != 1 || top.Tightest == nil || *top.LastActive != "2026-10-30" {
		t.Fatalf("top user: %+v", top)
	}
	for _, u := range all.Users[1:] {
		if u.LastActive != nil || u.CostUSD != 0 {
			t.Fatalf("idle user: %+v", u)
		}
	}
	if st := h.do(root, "GET", "/api/admin/usage?days=12", "", nil); st != 400 {
		t.Fatalf("bad range: %d", st)
	}
}
