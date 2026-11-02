package control

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"qanat/internal/core/netstats"
)

// The Network page merges every live gateway report, names accounts and
// routes, and ignores gateways that stopped reporting.
func TestAdminNetwork(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	var acctID string
	if err := h.s.pg.QueryRow(ctx, `INSERT INTO provider_accounts (provider, label, position) VALUES ('claude', 'Team 1', 1) RETURNING id::text`).Scan(&acctID); err != nil {
		t.Fatal(err)
	}
	h.s.rdb.Del(ctx, netstats.Gateways)
	t.Cleanup(func() {
		h.s.rdb.Del(context.Background(), netstats.Gateways, netstats.Key("gw-a"), netstats.Key("gw-b"), netstats.Key("gw-old"))
	})

	root := h.signInRoot()
	var empty networkResponse
	if st := h.do(root, "GET", "/api/admin/network", "", &empty); st != 200 {
		t.Fatalf("status %d", st)
	}
	if len(empty.Gateways) != 0 || len(empty.Providers) != 3 || empty.Providers[0].Status.Reason != "no_gateway" || empty.Providers[1].Status.State != "no_accounts" {
		t.Fatalf("without gateways: %+v", empty)
	}
	if len(empty.Providers[0].Accounts) != 1 || empty.Providers[0].Accounts[0].Label != "Team 1" {
		t.Fatalf("accounts still listed from the database: %+v", empty.Providers[0].Accounts)
	}

	// Mid-bucket, so the reports at now-1s and now share one history bucket.
	now := time.Now().Truncate(netstats.Interval).Add(-netstats.Interval / 2)
	ttft := int64(400)
	report := func(instance string, at time.Time, active int, evs []netstats.Event) {
		rep := netstats.Report{Instance: instance, At: at, StartedAt: at.Add(-time.Hour),
			Internet: &netstats.Probe{At: at, OK: true, TotalMS: 9},
			Providers: []netstats.Provider{{ID: "claude", Active: active,
				Accounts: []netstats.Account{{ID: acctID, State: "healthy", Usable: true, Active: active}},
				Probes:   []netstats.Probe{{At: at, Route: "direct", Origin: "https://api.anthropic.com", OK: true, TotalMS: 180}},
				Events:   evs,
				History:  []netstats.Bucket{{T: at.Truncate(10 * time.Second), Requests: int64(len(evs)), TTFTP50: &ttft}},
				Status:   netstats.Status{State: "healthy", Since: at.Add(-time.Minute)}}}}
		b, _ := json.Marshal(rep)
		h.s.rdb.Set(ctx, netstats.Key(instance), b, netstats.ReportTTL)
		h.s.rdb.ZAdd(ctx, netstats.Gateways, redis.Z{Score: float64(at.Unix()), Member: instance})
	}
	ok := func(d time.Duration, ms int64) netstats.Event {
		return netstats.Event{At: now.Add(-d), Account: acctID, Model: "claude-opus-4-7", Status: 200, Outcome: netstats.OK, TTFTMS: ms, DurMS: ms * 5}
	}
	report("gw-a", now.Add(-time.Second), 2, []netstats.Event{ok(90*time.Second, 300), ok(30*time.Second, 500)})
	report("gw-b", now, 1, []netstats.Event{ok(60*time.Second, 400), {At: now.Add(-10 * time.Second), Account: acctID, Outcome: netstats.Failed, Reason: "connect"}})
	report("gw-old", now.Add(-time.Hour), 9, nil) // stopped long ago

	var out networkResponse
	h.do(root, "GET", "/api/admin/network", "", &out)
	if len(out.Gateways) != 2 {
		t.Fatalf("gateways: %+v", out.Gateways)
	}
	c := out.Providers[0]
	if c.ID != "claude" || c.ActiveConnections != 3 || c.Status.State != "healthy" {
		t.Fatalf("claude: active %d status %+v", c.ActiveConnections, c.Status)
	}
	if c.Stats.Attempts != 4 || c.Stats.Errors != 1 || *c.Stats.TTFT.P50 != 400 || c.Stats.LastReason != "connect" {
		t.Fatalf("stats %+v", c.Stats)
	}
	if len(c.Accounts) != 1 || c.Accounts[0].Label != "Team 1" || c.Accounts[0].Active != 3 || c.Accounts[0].Attempts != 4 {
		t.Fatalf("accounts %+v", c.Accounts)
	}
	if len(c.Probes) != 1 || c.Probes[0].RouteLabel != "Direct" || c.LastProbeAt == nil || c.RefreshedAt == nil {
		t.Fatalf("probes %+v", c.Probes)
	}
	if len(c.Recent) != 4 || c.Recent[0].Outcome != netstats.Failed || c.Recent[0].AccountLabel != "Team 1" {
		t.Fatalf("recent, newest first: %+v", c.Recent)
	}
	if len(c.History) != 1 || c.History[0].Requests != 4 {
		t.Fatalf("history %+v", c.History)
	}
	if n, _ := h.s.rdb.ZCard(ctx, netstats.Gateways).Result(); n != 2 {
		t.Fatalf("stale gateway not pruned: %d left", n)
	}

	// Any signed-in user may look, but upstream account labels stay hidden.
	var uid string
	if err := h.s.pg.QueryRow(ctx, `INSERT INTO users (email, plan_id) SELECT 'dev@example.com', id FROM plans WHERE is_default RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	var seen networkResponse
	if st := h.do(h.signInAs(uid), "GET", "/api/admin/network", "", &seen); st != 200 {
		t.Fatalf("user: %d", st)
	}
	if got := seen.Providers[0].Accounts[0].Label; got != "Account 1" {
		t.Fatalf("user sees label %q", got)
	}
	if got := seen.Providers[0].Recent[0].AccountLabel; got != "Account 1" {
		t.Fatalf("user sees event label %q", got)
	}

	// Signed in only.
	if st := h.do(h.client(), "GET", "/api/admin/network", "", nil); st != 401 {
		t.Fatalf("anonymous: %d", st)
	}
}

// Model Status sums the gateways' per-bucket counters over the last 24 h and
// lists only models that were used.
func TestNetworkModels(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	now := time.Now()
	cur := netstats.ModelBucketStart(now)
	old := cur.Add(-3 * netstats.ModelBucket)
	gone := cur.Add(-30 * time.Hour)
	keys := []string{netstats.ModelKey(cur), netstats.ModelKey(old), netstats.ModelKey(gone)}
	h.s.rdb.Del(ctx, keys...)
	t.Cleanup(func() { h.s.rdb.Del(context.Background(), keys...) })

	add := func(at time.Time, model string, a netstats.ModelAgg) {
		for f, n := range a.Fields(netstats.ModelKeyID{Provider: "claude", Model: model}) {
			h.s.rdb.HIncrBy(ctx, netstats.ModelKey(at), f, n)
		}
	}
	ok := netstats.ModelAgg{}
	for range 9 {
		ok.Add(netstats.Event{Outcome: netstats.OK, DurMS: 2000, TTFTMS: 300})
	}
	ok.Add(netstats.Event{Outcome: netstats.Failed, Reason: "connect"})
	add(cur, "claude-opus-4-7", ok)
	add(old, "claude-opus-4-7", ok) // a second gateway or bucket sums
	add(gone, "claude-old", ok)     // outside the window

	var out modelStatusResponse
	if st := h.do(h.signInRoot(), "GET", "/api/network/models", "", &out); st != 200 {
		t.Fatalf("status %d", st)
	}
	if len(out.Models) != 1 {
		t.Fatalf("models: %+v", out.Models)
	}
	m := out.Models[0]
	if m.Model != "claude-opus-4-7" || m.Prefix != "cc" || m.Attempts != 20 || *m.Availability != 0.9 || *m.AvgLatencyMS != 2000 || len(m.Buckets) != netstats.ModelBuckets {
		t.Fatalf("model %+v", m)
	}
	if last := m.Buckets[netstats.ModelBuckets-1]; last.Attempts != 10 || last.Faults != 1 {
		t.Fatalf("current bucket %+v", last)
	}
	if out.SuccessRate == nil || *out.SuccessRate != 0.9 {
		t.Fatalf("success rate %v", out.SuccessRate)
	}
}
