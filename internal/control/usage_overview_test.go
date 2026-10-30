package control

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"qanat/internal/core/usage"
)

// The 9router-layout Overview: grouped rows, zero-filled chart buckets,
// account and key names, and recent requests from the ring.
func TestUsageOverview(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	fixed := time.Now().UTC().Truncate(time.Hour).Add(30 * time.Minute)
	h.s.now = func() time.Time { return fixed }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	var userID, keyID, acctID string
	must(h.s.pg.QueryRow(ctx, `INSERT INTO users (email, plan_id) SELECT 'dev@example.com', id FROM plans WHERE is_default RETURNING id`).Scan(&userID))
	must(h.s.pg.QueryRow(ctx, `INSERT INTO api_keys (user_id, name, hash, prefix) VALUES ($1, 'Laptop', '\x01', 'qnt_live_abcd') RETURNING id`, userID).Scan(&keyID))
	must(h.s.pg.QueryRow(ctx, `INSERT INTO provider_accounts (provider, label, position) VALUES ('claude', 'Team 1', 1) RETURNING id::text`).Scan(&acctID))
	insert := func(table string, at time.Time, model string, in, cr, out int64, cost float64, n int64) {
		_, err := h.s.pg.Exec(ctx, `INSERT INTO `+table+` (bucket_start, user_id, key_id, provider, upstream_model, account_id, aborted, price_version,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, request_count)
			VALUES ($1, $2, $3, 'claude', $4, $5, false, 1, $6, $7, $8, 0, $9, $10)`, at, userID, keyID, model, acctID, in, out, cr, cost, n)
		must(err)
	}
	day := time.Date(fixed.Year(), fixed.Month(), fixed.Day(), 0, 0, 0, 0, time.UTC)
	insert("usage_daily", day, "claude-sonnet-4-6", 100, 400, 50, 2, 3)
	insert("usage_daily", day.AddDate(0, 0, -3), "claude-opus-4-7", 10, 0, 5, 1, 1)
	insert("usage_hourly", fixed.Truncate(time.Hour), "claude-sonnet-4-6", 100, 400, 50, 2, 3)
	insert("usage_5m", fixed.Truncate(5*time.Minute), "claude-sonnet-4-6", 100, 400, 50, 2, 3)

	ev := func(min int, tokens int64, status int) string {
		b, _ := json.Marshal(usage.Event{RequestID: "r", At: fixed.Add(-time.Duration(min) * time.Minute), UserID: userID, Provider: "claude",
			UpstreamModel: "claude-sonnet-4-6", InputTokens: tokens, OutputTokens: tokens, Status: status})
		return string(b)
	}
	must(h.s.rdb.Del(ctx, usage.RecentKey(userID)).Err())
	must(h.s.rdb.RPush(ctx, usage.RecentKey(userID), ev(1, 10, 200), ev(2, 0, 529), ev(3, 7, 200)).Err())

	root := h.signInRoot()
	var week usageOverview
	if st := h.do(root, "GET", "/api/admin/usage/overview?period=7d&user_id="+userID, "", &week); st != 200 {
		t.Fatalf("7d: %d", st)
	}
	if week.TotalRequests != 4 || week.TotalPromptTokens != 510 || week.TotalCachedTokens != 400 || week.TotalCost != 3 {
		t.Fatalf("totals: %+v", week)
	}
	if len(week.Chart) != 7 || week.Chart[6].Requests != 3 || week.Chart[3].Requests != 1 || week.Granularity != "day" {
		t.Fatalf("chart: %+v", week.Chart)
	}
	if len(week.ByAccount) != 2 || week.ByAccount[0].AccountName != "Team 1" || week.ByAPIKey[0].KeyName != "Laptop" || week.ByEndpoint[0].Endpoint != "/v1/messages" {
		t.Fatalf("groups: %+v %+v", week.ByAccount, week.ByAPIKey)
	}
	// Last used comes from the 5-minute bucket, not the end of the day.
	if lu := week.ByModel[0].LastUsed; lu.After(fixed) || fixed.Sub(lu) > 5*time.Minute {
		t.Fatalf("last used %v (now %v)", lu, fixed)
	}
	if len(week.RecentRequests) != 2 || week.RecentRequests[0].PromptTokens != 10 || week.LastProvider != "claude" {
		t.Fatalf("recent (zero-token 529 dropped): %+v", week.RecentRequests)
	}
	if len(week.Providers) != 1 || week.Providers[0] != "claude" {
		t.Fatalf("providers: %v", week.Providers)
	}

	var hours usageOverview
	h.do(root, "GET", "/api/admin/usage/overview?period=24h&user_id="+userID, "", &hours)
	if len(hours.Chart) != 24 || hours.Chart[23].Requests != 3 || hours.TotalRequests != 3 || hours.Granularity != "hour" {
		t.Fatalf("24h: %+v", hours)
	}
	var all usageOverview
	h.do(root, "GET", "/api/admin/usage/overview?period=all", "", &all)
	if len(all.Chart) != 4 || all.TotalRequests != 4 {
		t.Fatalf("all: %d buckets, %d requests", len(all.Chart), all.TotalRequests)
	}
	for _, bad := range []string{"?period=1y", "?period=today&tz=Mars/Base"} {
		if st := h.do(root, "GET", "/api/admin/usage/overview"+bad, "", nil); st != 400 {
			t.Errorf("%s: %d", bad, st)
		}
	}
}
