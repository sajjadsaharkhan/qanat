package netstats

import (
	"strconv"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

func ms(v int64) *int64 { return &v }

func usable(ids ...string) []Account {
	var out []Account
	for _, id := range ids {
		out = append(out, Account{ID: id, State: "healthy", Usable: true})
	}
	return out
}

func okProbe(total int64) Probe {
	return Probe{At: now, Route: "direct", Origin: "https://api.anthropic.com", OK: true, TotalMS: total}
}

func events(n int, outcome, reason string) []Event {
	out := make([]Event, n)
	for i := range out {
		out[i] = Event{At: now.Add(-time.Duration(i) * time.Second), Outcome: outcome, Reason: reason, TTFTMS: 400, DurMS: 2000}
	}
	return out
}

func TestAssess(t *testing.T) {
	inetOK := &Probe{OK: true, TotalMS: 12}
	inetDown := &Probe{Error: "tcp", Fails: 2}
	future := now.Add(time.Minute)
	cases := []struct {
		name          string
		accounts      []Account
		probes        []Probe
		internet      *Probe
		events        []Event
		state, reason string
	}{
		{"no accounts", nil, nil, inetOK, nil, "no_accounts", ""},
		{"all cooling", []Account{{ID: "a", State: "healthy", CoolingUntil: &future}}, nil, inetOK, nil, "unavailable", "cooling"},
		{"all disconnected", []Account{{ID: "a", State: "disconnected"}, {ID: "b", Disabled: true}}, nil, inetOK, nil, "unavailable", "disconnected"},
		{"nothing measured", usable("a"), nil, inetOK, nil, "checking", ""},
		{"healthy", usable("a"), []Probe{okProbe(180)}, inetOK, events(10, OK, ""), "healthy", ""},
		// Tower's debounce: one failed sample only degrades.
		{"one failed probe", usable("a"), []Probe{{Error: "tls", Fails: 1}}, inetOK, nil, "degraded", "probe_failed"},
		{"route down", usable("a"), []Probe{{Error: "tls", Fails: 2}}, inetOK, nil, "unreachable", "tls"},
		{"gateway offline", usable("a"), []Probe{{Error: "timeout", Fails: 3}}, inetDown, nil, "offline", ""},
		{"one route of two down", usable("a"), []Probe{{Route: "p1", Error: "proxy", Fails: 4}, okProbe(200)}, inetOK, nil, "degraded", "probe_failed"},
		{"slow handshake", usable("a"), []Probe{okProbe(1400)}, inetOK, nil, "degraded", "slow_handshake"},
		{"slow internet", usable("a"), []Probe{okProbe(1400)}, &Probe{OK: true, TotalMS: 450}, nil, "degraded", "slow_internet"},
		// path_unstable: resets beat the handshake, which still looks fine.
		{"unstable", usable("a"), []Probe{okProbe(150)}, inetOK, append(events(3, Failed, "mid_stream"), events(2, OK, "")...), "unstable", "upstream_failures"},
		{"faults below share", usable("a"), []Probe{okProbe(150)}, inetOK, append(events(3, Failed, "status_502"), events(20, OK, "")...), "degraded", "error_rate"},
		{"rate limited", usable("a"), []Probe{okProbe(150)}, inetOK, append(events(4, Failed, "rate_limited"), events(6, OK, "")...), "degraded", "rate_limited"},
		{"one account out", append(usable("a"), Account{ID: "b", State: "disconnected"}), []Probe{okProbe(150)}, inetOK, nil, "degraded", "accounts_out"},
		{"one account disabled", append(usable("a"), Account{ID: "b", Disabled: true}), []Probe{okProbe(150)}, inetOK, nil, "healthy", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state, reason := Assess(c.accounts, c.probes, c.internet, c.events, now)
			if state != c.state || reason != c.reason {
				t.Fatalf("got %s/%s, want %s/%s", state, reason, c.state, c.reason)
			}
		})
	}
}

func TestSummarize(t *testing.T) {
	evs := []Event{
		{At: now.Add(-10 * time.Minute), Outcome: OK, TTFTMS: 9999}, // outside the window
		{At: now.Add(-4 * time.Minute), Outcome: OK, TTFTMS: 100, OverheadMS: 2, DurMS: 1000, OutTPS: 50},
		{At: now.Add(-3 * time.Minute), Outcome: OK, TTFTMS: 300, OverheadMS: 4, DurMS: 3000, OutTPS: 70},
		{At: now.Add(-2 * time.Minute), Outcome: Failed, Reason: "connect"},
		{At: now.Add(-1 * time.Minute), Outcome: Rejected, Reason: "pool_exhausted"},
		{At: now.Add(-30 * time.Second), Outcome: Aborted, TTFTMS: 200, DurMS: 500},
	}
	s := Summarize(evs, now, Window)
	if s.Attempts != 4 || s.OK != 2 || s.Errors != 1 || s.Rejected != 1 || s.Aborted != 1 {
		t.Fatalf("counts %+v", s)
	}
	if s.ErrorRate != 0.25 || s.PerMinute != 0.8 {
		t.Fatalf("rate %v per minute %v", s.ErrorRate, s.PerMinute)
	}
	if *s.TTFT.P50 != 200 || *s.TTFT.P95 != 300 || *s.Latency.P50 != 1000 || *s.OutTPS != 50 {
		t.Fatalf("percentiles ttft %v/%v latency %v tps %v", *s.TTFT.P50, *s.TTFT.P95, *s.Latency.P50, *s.OutTPS)
	}
	if s.Reasons["connect"] != 1 || s.Reasons["pool_exhausted"] != 1 || s.LastReason != "connect" || !s.LastSuccess.Equal(now.Add(-3*time.Minute)) {
		t.Fatalf("reasons %v last %v %v", s.Reasons, s.LastReason, s.LastSuccess)
	}
}

func TestPct(t *testing.T) {
	if p := Pct(nil); p.P50 != nil {
		t.Fatal("empty")
	}
	vs := make([]int64, 100)
	for i := range vs {
		vs[i] = int64(100 - i)
	}
	if p := Pct(vs); *p.P50 != 50 || *p.P95 != 95 {
		t.Fatalf("got %d/%d", *p.P50, *p.P95)
	}
}

// Two gateways: counts add up, the newest probe per route wins, and the
// verdict is reassessed on everything together.
func TestMerge(t *testing.T) {
	t0 := now.Add(-time.Minute)
	a := Report{Instance: "gw1", At: now.Add(-2 * time.Second), Internet: &Probe{OK: true, TotalMS: 10},
		InternetHistory: []Point{{T: t0, MS: ms(10)}},
		Providers: []Provider{{ID: "claude", Active: 2,
			Accounts: []Account{{ID: "a", State: "healthy", Usable: true, Active: 2}},
			Probes:   []Probe{{At: now.Add(-8 * time.Second), Route: "direct", Origin: "o", OK: false, Error: "tls", Fails: 1}},
			Events:   events(2, Failed, "mid_stream"),
			History:  []Bucket{{T: t0, Requests: 2, Errors: 2, TTFTP50: ms(100), Active: 2}},
			Status:   Status{State: "unstable", Since: now.Add(-time.Minute)}}}}
	b := Report{Instance: "gw2", At: now, Internet: &Probe{OK: true, TotalMS: 30},
		InternetHistory: []Point{{T: t0, MS: nil}},
		Providers: []Provider{{ID: "claude", Active: 1,
			Accounts: []Account{{ID: "a", State: "healthy", Usable: false, Active: 1}},
			Probes:   []Probe{{At: now.Add(-1 * time.Second), Route: "direct", Origin: "o", OK: true, TotalMS: 200}},
			Events:   events(2, Failed, "connect"),
			History:  []Bucket{{T: t0, Requests: 6, Errors: 1, TTFTP50: ms(300), HandshakeMS: ms(200), Active: 1}},
			Status:   Status{State: "unstable", Since: now.Add(-30 * time.Second)}}}}
	v := Merge([]Report{a, b}, now)
	m := v.Providers["claude"]
	if m.Active != 3 || len(m.Accounts) != 1 || m.Accounts[0].Active != 3 || !m.Accounts[0].Usable {
		t.Fatalf("accounts %+v active %d", m.Accounts, m.Active)
	}
	if len(m.Probes) != 1 || !m.Probes[0].OK {
		t.Fatalf("newest probe should win: %+v", m.Probes)
	}
	h := m.History[0]
	if h.Requests != 8 || h.Errors != 3 || *h.TTFTP50 != 250 || *h.HandshakeMS != 200 || h.Active != 3 {
		t.Fatalf("history %+v", h)
	}
	if v.Internet.TotalMS != 30 || v.InternetHistory[0].MS != nil {
		t.Fatalf("internet: the worse reading wins, got %+v / %+v", v.Internet, v.InternetHistory)
	}
	if m.Status.State != "unstable" || !m.Status.Since.Equal(now.Add(-time.Minute)) || !m.RefreshedAt.Equal(now) {
		t.Fatalf("status %+v refreshed %v", m.Status, m.RefreshedAt)
	}
	for i := 1; i < len(m.Events); i++ {
		if m.Events[i].At.Before(m.Events[i-1].At) {
			t.Fatal("events not oldest first")
		}
	}
}

func TestModelAgg(t *testing.T) {
	var a ModelAgg
	for i := 1; i <= 10; i++ {
		a.Add(Event{Outcome: OK, DurMS: 1000, TTFTMS: int64(i) * 100})
	}
	if a.Add(Event{Outcome: Failed, Reason: "rate_limited"}) || a.Add(Event{Outcome: Aborted}) || a.Add(Event{Outcome: Rejected}) {
		t.Fatal("non-faults counted")
	}
	if !a.Add(Event{Outcome: Failed, Reason: "status_503"}) || !a.Add(Event{Outcome: Failed, Reason: "connect"}) {
		t.Fatal("faults not counted")
	}
	if a.Attempts() != 12 || *a.Availability() < 0.83 || *a.Availability() > 0.84 || *a.AvgLatencyMS() != 1000 {
		t.Fatalf("agg %+v", a)
	}
	if p := *a.TTFTP90MS(); p < 500 || p > 1000 {
		t.Fatalf("p90 %d", p)
	}

	id := ModelKeyID{"claude", "claude-opus-4-7"}
	h := map[string]string{}
	for f, n := range a.Fields(id) {
		h[f] = strconv.FormatInt(n, 10)
	}
	got := ParseModelHash(h)[id]
	if got == nil || *got != a {
		t.Fatalf("round trip: %+v vs %+v", got, a)
	}
}
