package gateway

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"qanat/internal/core/netstats"
	"qanat/internal/core/provider"
	"qanat/internal/proxy"
)

// internetRefs are the internet baseline: IP literals of two operators, so
// DNS isn't involved (Tower's NET_REFS). The fastest answer counts.
var internetRefs = []string{"1.1.1.1:443", "8.8.8.8:53"}

// Probe deadlines, after Tower's adaptive API probe timeout: 3× the slowest
// recent good handshake within [probeMin, probeMax], and the full probeMax
// right after a timeout, so a slow path is never read as a dead one.
const (
	probeMin  = 3 * time.Second
	probeMax  = 10 * time.Second
	probeMult = 3
	probeLook = 12
)

// netTracker is this gateway's live network view of each provider for the
// admin Network page (internal/core/netstats). It is fed by the hot path
// (stream counts, attempt outcomes) and by its own probes, and is reported
// to Redis every netstats.Interval.
type netTracker struct {
	instance string
	started  time.Time

	mu       sync.Mutex
	prov     map[string]*provNet
	internet *netstats.Probe
	inetHist []netstats.Point
	lastTick time.Time
	inflight map[string]bool // probes running, by route key
	nextInet time.Time
}

type provNet struct {
	active     int
	acctActive map[string]int
	events     []netstats.Event
	probes     map[string]*routeProbe // route key → latest
	history    []netstats.Bucket
	status     netstats.Status
}

type routeProbe struct {
	last netstats.Probe
	good []int64 // recent good totals, for the deadline
}

func newNetTracker() *netTracker {
	host, _ := os.Hostname()
	if host == "" {
		host = "gateway"
	}
	return &netTracker{instance: host, started: time.Now(), prov: map[string]*provNet{}, inflight: map[string]bool{}}
}

// p returns the provider's state; the caller holds mu.
func (t *netTracker) p(id string) *provNet {
	p := t.prov[id]
	if p == nil {
		p = &provNet{acctActive: map[string]int{}, probes: map[string]*routeProbe{}}
		t.prov[id] = p
	}
	return p
}

// streamStart counts an open response until the returned func runs.
func (t *netTracker) streamStart(providerID, accountID string) func() {
	if t == nil {
		return func() {}
	}
	t.mu.Lock()
	p := t.p(providerID)
	p.active++
	p.acctActive[accountID]++
	t.mu.Unlock()
	return func() {
		t.mu.Lock()
		p.active--
		p.acctActive[accountID]--
		if p.acctActive[accountID] <= 0 {
			delete(p.acctActive, accountID)
		}
		t.mu.Unlock()
	}
}

// record keeps one attempt's outcome; At defaults to now.
func (t *netTracker) record(providerID string, e netstats.Event) {
	if t == nil {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	t.mu.Lock()
	p := t.p(providerID)
	p.events = append(p.events, e)
	if over := len(p.events) - netstats.MaxEvents; over > 0 {
		p.events = slices.Delete(p.events, 0, over)
	}
	t.mu.Unlock()
}

// netPath is one (provider, proxy, origin) path to probe.
type netPath struct {
	provider string
	spec     *proxy.Spec
	origin   string
}

func (r netPath) key() string { return proxyLabel(r.spec) + " " + r.origin }

// defaultOrigin is where a provider's accounts go without a base URL.
func defaultOrigin(providerID string) string {
	if u, ok := defaultBaseURL[providerID]; ok {
		return proxy.Origin(u)
	}
	for _, h := range proxy.ProviderHosts {
		if h.Provider == providerID {
			return proxy.Origin(h.URL)
		}
	}
	return ""
}

// netRoutes lists every path the snapshot's enabled accounts may take.
func netRoutes(s *Snapshot) []netPath {
	if s == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []netPath
	for pid, accts := range s.accounts {
		for _, a := range accts {
			if a.Disabled {
				continue
			}
			origin := a.Origin
			if origin == "" {
				origin = defaultOrigin(pid)
			}
			if origin == "" {
				continue
			}
			for _, sp := range a.Proxies {
				r := netPath{provider: pid, spec: sp, origin: origin}
				if k := pid + " " + r.key(); !seen[k] {
					seen[k] = true
					out = append(out, r)
				}
			}
		}
	}
	return out
}

// deadline is the next probe's timeout for a route (caller holds mu).
func deadline(last *netstats.Probe, good []int64) time.Duration {
	if last != nil && last.Error == "timeout" {
		return probeMax
	}
	var slowest int64
	for _, v := range good {
		slowest = max(slowest, v)
	}
	return min(max(probeMin, probeMult*time.Duration(slowest)*time.Millisecond), probeMax)
}

// due reports whether a probe should start now, and claims it.
func (t *netTracker) due(r netPath, now time.Time) (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := r.provider + " " + r.key()
	if t.inflight[k] {
		return 0, false
	}
	rp := t.p(r.provider).probes[r.key()]
	if rp != nil {
		every := netstats.Interval
		if !rp.last.OK {
			every = netstats.BadInterval
		}
		if now.Sub(rp.last.At) < every {
			return 0, false
		}
	}
	t.inflight[k] = true
	var last *netstats.Probe
	var good []int64
	if rp != nil {
		last, good = &rp.last, rp.good
	}
	return deadline(last, good), true
}

func toProbe(h proxy.Handshake, at time.Time, timeout time.Duration) netstats.Probe {
	msp := func(d *time.Duration) *int64 {
		if d == nil {
			return nil
		}
		v := d.Milliseconds()
		return &v
	}
	return netstats.Probe{At: at, OK: h.OK(), DNSMS: msp(h.DNS), ConnectMS: msp(h.Connect), TLSMS: msp(h.TLS),
		TotalMS: h.Total.Milliseconds(), Error: h.Kind, Detail: h.Detail, TimeoutMS: timeout.Milliseconds()}
}

// observe stores a finished probe.
func (t *netTracker) observe(r netPath, p netstats.Probe) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.inflight, r.provider+" "+r.key())
	p.Route, p.Origin = proxyLabel(r.spec), r.origin
	pv := t.p(r.provider)
	rp := pv.probes[r.key()]
	if rp == nil {
		rp = &routeProbe{}
		pv.probes[r.key()] = rp
	}
	if !p.OK {
		p.Fails = rp.last.Fails + 1
	} else {
		rp.good = append(rp.good, p.TotalMS)
		if len(rp.good) > probeLook {
			rp.good = rp.good[1:]
		}
	}
	rp.last = p
}

// probeInternet times the internet baseline: the fastest reference wins.
func (t *netTracker) probeInternet(ctx context.Context, now time.Time) {
	t.mu.Lock()
	if now.Before(t.nextInet) || t.inflight["internet"] {
		t.mu.Unlock()
		return
	}
	t.inflight["internet"] = true
	t.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(ctx, probeMin)
		defer cancel()
		var mu sync.Mutex
		var best time.Duration
		var lastErr error
		var wg sync.WaitGroup
		for _, addr := range internetRefs {
			wg.Go(func() {
				d, err := proxy.TCPPing(ctx, addr)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					lastErr = err
				} else if best == 0 || d < best {
					best = d
				}
			})
		}
		wg.Wait()
		p := netstats.Probe{At: time.Now(), Route: "direct", Origin: "internet", OK: best > 0, TotalMS: best.Milliseconds(), TimeoutMS: probeMin.Milliseconds()}
		if best == 0 {
			p.Error, p.Detail = "tcp", "no reference host answered"
			if lastErr != nil {
				p.Detail = lastErr.Error()
			}
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		delete(t.inflight, "internet")
		if !p.OK && t.internet != nil {
			p.Fails = t.internet.Fails + 1
		} else if !p.OK {
			p.Fails = 1
		}
		t.internet = &p
		every := netstats.Interval
		if !p.OK {
			every = netstats.BadInterval
		}
		t.nextInet = p.At.Add(every)
	}()
}

// accountView is an account's routing state as candidates() would see it.
func (g *Gateway) accountView(a *Account, now time.Time) netstats.Account {
	g.mu.Lock()
	until := g.cooldown[a.ID]
	g.mu.Unlock()
	if a.CooldownUntil.After(until) {
		until = a.CooldownUntil
	}
	v := netstats.Account{ID: a.ID, State: a.State, Disabled: a.Disabled}
	if until.After(now) {
		v.CoolingUntil = &until
	}
	v.Usable = !a.Disabled && a.State != "disconnected" && v.CoolingUntil == nil && len(g.routes(a)) > 0
	return v
}

// report closes the current history bucket and builds the Report.
func (g *Gateway) netReport(now time.Time) netstats.Report {
	t := g.net
	s := g.snap.Current()
	accounts := map[string][]netstats.Account{}
	if s != nil {
		for pid, accts := range s.accounts {
			for _, a := range accts {
				accounts[pid] = append(accounts[pid], g.accountView(a, now))
			}
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	from := t.lastTick
	if from.IsZero() {
		from = now.Add(-netstats.Interval)
	}
	t.lastTick = now
	bucketT := now.Truncate(netstats.Interval)
	rep := netstats.Report{Instance: t.instance, At: now, StartedAt: t.started, Internet: t.internet}

	var inetMS *int64
	if t.internet != nil && t.internet.OK {
		v := t.internet.TotalMS
		inetMS = &v
	}
	t.inetHist = appendCapped(t.inetHist, netstats.Point{T: bucketT, MS: inetMS})
	rep.InternetHistory = t.inetHist

	for _, pv := range provider.All {
		p := t.p(pv.ID)
		cut := now.Add(-netstats.Window)
		i := 0
		for i < len(p.events) && p.events[i].At.Before(cut) {
			i++
		}
		p.events = p.events[i:]

		probes := make([]netstats.Probe, 0, len(p.probes))
		var handshake *int64
		for _, rp := range p.probes {
			probes = append(probes, rp.last)
			if rp.last.OK && rp.last.At.After(from) && (handshake == nil || rp.last.TotalMS < *handshake) {
				v := rp.last.TotalMS
				handshake = &v
			}
		}
		slices.SortFunc(probes, func(a, b netstats.Probe) int { return strings.Compare(a.Route+a.Origin, b.Route+b.Origin) })

		b := netstats.Bucket{T: bucketT, HandshakeMS: handshake, Active: p.active}
		var ttft []int64
		for _, e := range p.events {
			if !e.At.After(from) {
				continue
			}
			switch e.Outcome {
			case netstats.Rejected:
				b.Rejected++
				continue
			case netstats.Failed:
				b.Errors++
			}
			b.Requests++
			if e.TTFTMS > 0 {
				ttft = append(ttft, e.TTFTMS)
			}
		}
		pc := netstats.Pct(ttft)
		b.TTFTP50, b.TTFTP95 = pc.P50, pc.P95
		p.history = appendCapped(p.history, b)

		accts := accounts[pv.ID]
		for i := range accts {
			accts[i].Active = p.acctActive[accts[i].ID]
		}
		state, reason := netstats.Assess(accts, probes, t.internet, p.events, now)
		if state != p.status.State || reason != p.status.Reason {
			since := now
			if state == p.status.State {
				since = p.status.Since // same state, new reason
			}
			p.status = netstats.Status{State: state, Reason: reason, Since: since}
		}
		rep.Providers = append(rep.Providers, netstats.Provider{
			ID: pv.ID, Active: p.active, Accounts: orEmpty(accts), Probes: probes,
			Events: slices.Clone(p.events), History: p.history, Status: p.status,
		})
	}
	return rep
}

func appendCapped[T any](s []T, v T) []T {
	s = append(s, v)
	if over := len(s) - netstats.HistoryLen; over > 0 {
		s = slices.Delete(s, 0, over)
	}
	return s
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// restore picks up this host's history from its last report, so a
// restarted gateway keeps the page's charts.
func (t *netTracker) restore(rep netstats.Report) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inetHist = rep.InternetHistory
	for _, p := range rep.Providers {
		pv := t.p(p.ID)
		pv.history = p.History
		pv.status = p.Status
	}
}

// RunNetStats probes every provider route and the internet baseline, and
// leaves this gateway's report in Redis every netstats.Interval, until ctx
// ends (admin Network page).
func (g *Gateway) RunNetStats(ctx context.Context, rdb *redis.Client) {
	t := g.net
	if rdb != nil {
		rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if b, err := rdb.Get(rctx, netstats.Key(t.instance)).Bytes(); err == nil {
			var rep netstats.Report
			if json.Unmarshal(b, &rep) == nil {
				t.restore(rep)
			}
		}
		cancel()
	}
	probeTick := time.NewTicker(time.Second)
	defer probeTick.Stop()
	reportTick := time.NewTicker(netstats.Interval)
	defer reportTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-probeTick.C:
			t.probeInternet(ctx, now)
			for _, r := range netRoutes(g.snap.Current()) {
				timeout, ok := t.due(r, now)
				if !ok {
					continue
				}
				go func() {
					pctx, cancel := context.WithTimeout(ctx, timeout)
					defer cancel()
					at := time.Now()
					t.observe(r, toProbe(proxy.ProbeHandshake(pctx, r.spec, r.origin), at, timeout))
				}()
			}
		case now := <-reportTick.C:
			rep := g.netReport(now)
			if rdb == nil {
				continue
			}
			b, err := json.Marshal(rep)
			if err != nil {
				continue
			}
			wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			pipe := rdb.TxPipeline()
			pipe.Set(wctx, netstats.Key(t.instance), b, netstats.ReportTTL)
			pipe.ZAdd(wctx, netstats.Gateways, redis.Z{Score: float64(now.Unix()), Member: t.instance})
			if _, err := pipe.Exec(wctx); err != nil {
				redisErrors.Inc()
			}
			cancel()
		}
	}
}
