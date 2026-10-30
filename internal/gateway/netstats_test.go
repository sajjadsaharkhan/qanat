package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"qanat/internal/core/netstats"
	"qanat/internal/core/usage"
	"qanat/internal/proxy"
)

func netGateway(s *Snapshot) *Gateway {
	g := newTestGateway()
	g.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	g.net = newNetTracker()
	g.pub = &Publisher{ch: make(chan usage.Event, 100)} // events are dropped here
	g.snap = &Store{}
	g.snap.cur.Store(s)
	return g
}

func providerReport(t *testing.T, rep netstats.Report, id string) netstats.Provider {
	t.Helper()
	for _, p := range rep.Providers {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("no %s in report", id)
	return netstats.Provider{}
}

// A streamed response and a failed failover attempt both land in the
// provider's events; the stream is counted while it is open.
func TestNetStats_RecordsAttempts(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Fail") != "" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3}}}\n\n")
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":40}}\n\n")
	}))
	defer up.Close()
	s := &Snapshot{Gen: 1, accounts: map[string][]*Account{"claude": {
		{ID: "a1", Provider: "claude", State: "healthy", BaseURL: up.URL, Origin: up.URL, Proxies: []*proxy.Spec{nil}},
	}}, sticky: map[string]string{}}
	g := netGateway(s)
	g.sticky["u1|claude"] = "a1"
	rt := &route{caller: &caller{snap: s, user: &User{ID: "u1"}, key: &Key{}, plan: &Plan{}}, name: "m", body: []byte(`{}`),
		members: []Member{{Provider: "claude", Model: "m"}}}

	done := make(chan struct{})
	go func() {
		defer close(done)
		r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		g.forward(httptest.NewRecorder(), r, rt, forwardOpts{reqID: "r", start: time.Now(), remaining: -1, usage: true})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if p := providerReport(t, g.netReport(time.Now()), "claude"); p.Active == 1 && p.Accounts[0].Active == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("open stream not counted")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	<-done

	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("X-Fail", "1")
	g.forward(httptest.NewRecorder(), r, rt, forwardOpts{reqID: "r2", start: time.Now(), remaining: -1, usage: true})

	p := providerReport(t, g.netReport(time.Now()), "claude")
	if p.Active != 0 || len(p.Events) != 3 {
		t.Fatalf("active %d events %+v", p.Active, p.Events)
	}
	ok, failed, rejected := p.Events[0], p.Events[1], p.Events[2]
	if ok.Outcome != netstats.OK || ok.Account != "a1" || ok.TTFTMS < 0 || ok.DurMS <= 0 || ok.Status != 200 {
		t.Fatalf("ok event %+v", ok)
	}
	if failed.Outcome != netstats.Failed || failed.Reason != "status_502" || rejected.Outcome != netstats.Rejected || rejected.Reason != "pool_exhausted" {
		t.Fatalf("failed %+v rejected %+v", failed, rejected)
	}
	if h := p.History[len(p.History)-1]; h.Requests != 2 || h.Errors != 1 || h.Rejected != 1 {
		t.Fatalf("bucket %+v", h)
	}
}

// Probes run per (route, origin), back off to BadInterval on failure and
// count consecutive failures; the report folds them into a verdict.
func TestNetStats_ProbesAndVerdict(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	s := &Snapshot{Gen: 1, accounts: map[string][]*Account{"claude": {
		{ID: "a1", Provider: "claude", State: "healthy", Origin: srv.URL, Proxies: []*proxy.Spec{nil}},
		{ID: "a2", Provider: "claude", State: "healthy", Origin: srv.URL, Proxies: []*proxy.Spec{nil}}, // same path
		{ID: "off", Provider: "claude", Disabled: true, Origin: "https://elsewhere", Proxies: []*proxy.Spec{nil}},
	}}}
	g := netGateway(s)
	paths := netRoutes(s)
	if len(paths) != 1 || paths[0].key() != "direct "+srv.URL {
		t.Fatalf("paths %+v", paths)
	}
	now := time.Now()
	timeout, ok := g.net.due(paths[0], now)
	if !ok || timeout != probeMin {
		t.Fatalf("first probe: due %v timeout %v", ok, timeout)
	}
	if _, again := g.net.due(paths[0], now); again {
		t.Fatal("a running probe must not start twice")
	}
	// The test certificate isn't trusted: a tls failure, like a portal.
	for i := range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		g.net.observe(paths[0], toProbe(proxy.ProbeHandshake(ctx, nil, srv.URL), now, time.Second))
		cancel()
		p := providerReport(t, g.netReport(now), "claude")
		want := []string{"degraded", "unreachable"}[i]
		if p.Status.State != want || p.Probes[0].Error != "tls" || p.Probes[0].Fails != i+1 {
			t.Fatalf("probe %d: status %+v probe %+v", i+1, p.Status, p.Probes[0])
		}
		if _, ok := g.net.due(paths[0], now.Add(netstats.BadInterval)); !ok {
			t.Fatal("a failing path is re-probed after BadInterval")
		}
	}

	// Trust it: the path recovers and the verdict follows.
	prev := tlsConfigForTest(srv)
	defer prev()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	g.net.observe(paths[0], toProbe(proxy.ProbeHandshake(ctx, nil, srv.URL), now, time.Second))
	p := providerReport(t, g.netReport(now.Add(time.Second)), "claude")
	if p.Status.State != "healthy" || !p.Probes[0].OK || p.Probes[0].Fails != 0 || p.Probes[0].ConnectMS == nil {
		t.Fatalf("recovered: status %+v probe %+v", p.Status, p.Probes[0])
	}
	if _, ok := g.net.due(paths[0], now.Add(netstats.BadInterval)); ok {
		t.Fatal("a good path waits the full Interval")
	}
	if cx := providerReport(t, g.netReport(now), "codex"); cx.Status.State != "no_accounts" {
		t.Fatalf("codex without accounts: %+v", cx.Status)
	}
}

func TestDeadline(t *testing.T) {
	if d := deadline(nil, []int64{200, 1500}); d != 4500*time.Millisecond {
		t.Fatalf("3× slowest: %v", d)
	}
	if d := deadline(nil, []int64{50}); d != probeMin {
		t.Fatalf("floor: %v", d)
	}
	if d := deadline(&netstats.Probe{Error: "timeout"}, []int64{50}); d != probeMax {
		t.Fatalf("after a timeout: %v", d)
	}
}

// tlsConfigForTest makes the gateway trust srv; the returned func undoes it.
func tlsConfigForTest(srv *httptest.Server) func() {
	return proxy.SetHandshakeRootsForTest(srv.Certificate())
}
