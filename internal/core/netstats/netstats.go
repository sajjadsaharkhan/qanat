// Package netstats carries live network analytics per provider from the
// gateways to the admin Network page, after Tower's net monitor
// (github.com/ghhrmnzdh/tower, towerd.py NetMonitor and path_unstable):
// handshake probes to each upstream, an internet baseline, path stability
// judged from real traffic, and request latency.
//
// Each gateway keeps its own figures in memory and leaves a Report in Redis
// every Interval, expiring when it stops; the control plane merges the
// reports it finds. Nothing is persisted: the page shows the last
// HistoryLen×Interval (30 minutes).
package netstats

import (
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	// Interval is the report cadence and the probe cadence on a good path.
	Interval = 10 * time.Second
	// BadInterval is the probe cadence on a failing path (Tower: 3 s).
	BadInterval = 3 * time.Second
	// Window is how far back the summary statistics look.
	Window = 5 * time.Minute
	// HistoryLen buckets of Interval: 30 minutes.
	HistoryLen = 180
	// MaxEvents bounds the events a gateway keeps per provider.
	MaxEvents = 2000
	// ReportTTL lets a stopped gateway's report expire.
	ReportTTL = 45 * time.Second

	keyPrefix = "qanat:netstats:"
	// Gateways is a sorted set of reporting gateways, scored by the Unix
	// time of their last report, so readers find reports without a scan.
	Gateways = keyPrefix + "gateways"
)

// Key is the Redis key holding one gateway's latest Report.
func Key(instance string) string { return keyPrefix + "report:" + instance }

// Thresholds, after Tower's DEGRADED_INTERNET_MS / DEGRADED_API_MS and
// TUNNEL_FAIL_* lines.
const (
	SlowInternet  = 300 * time.Millisecond  // TCP connect to a public IP
	SlowHandshake = 1000 * time.Millisecond // DNS + connect + TLS ≈ 2 RTT + crypto
	// UnstableMin upstream failures inside UnstableWindow that are also
	// UnstableShare of its attempts mark the path unstable.
	UnstableMin    = 3
	UnstableShare  = 0.5
	UnstableWindow = 2 * time.Minute
	// DegradedErrorRate over Window, once there are DegradedMinAttempts.
	DegradedErrorRate   = 0.1
	DegradedMinAttempts = 5
	// FailAfter consecutive failed probes make a route count as down; one
	// alone only degrades (Tower's two-sample debounce).
	FailAfter = 2
)

// Report is one gateway's view.
type Report struct {
	Instance  string    `json:"instance"`
	At        time.Time `json:"at"`
	StartedAt time.Time `json:"started_at"`
	// Internet is the gateway's own link: a direct TCP connect to public IP
	// literals, so a slow host network isn't blamed on a provider.
	Internet        *Probe     `json:"internet,omitempty"`
	InternetHistory []Point    `json:"internet_history"`
	Providers       []Provider `json:"providers"`
}

// Provider is one provider as one gateway sees it.
type Provider struct {
	ID       string    `json:"id"`
	Active   int       `json:"active"` // responses streaming now
	Accounts []Account `json:"accounts"`
	Probes   []Probe   `json:"probes"` // latest per route
	Events   []Event   `json:"events"` // the last Window, oldest first
	History  []Bucket  `json:"history"`
	Status   Status    `json:"status"`
}

// Account is an upstream account's routing state on this gateway.
type Account struct {
	ID           string     `json:"id"`
	State        string     `json:"state"`
	Disabled     bool       `json:"disabled,omitempty"`
	CoolingUntil *time.Time `json:"cooling_until,omitempty"`
	Active       int        `json:"active"`
	// Usable is whether the gateway would route to it now.
	Usable bool `json:"usable"`
}

// Probe is one handshake to an upstream origin over one route.
type Probe struct {
	At     time.Time `json:"at"`
	Route  string    `json:"route"` // proxy ID or "direct"
	Origin string    `json:"origin"`
	OK     bool      `json:"ok"`
	// Phase times in milliseconds; nil when the phase didn't run.
	DNSMS     *int64 `json:"dns_ms,omitempty"`
	ConnectMS *int64 `json:"connect_ms,omitempty"`
	TLSMS     *int64 `json:"tls_ms,omitempty"`
	TotalMS   int64  `json:"total_ms"`
	Error     string `json:"error,omitempty"` // dns, tcp, proxy, proxy_auth, tls, timeout
	Detail    string `json:"detail,omitempty"`
	Fails     int    `json:"fails"`      // consecutive failures, this one included
	TimeoutMS int64  `json:"timeout_ms"` // the deadline it ran under
}

// Down reports whether the route has failed enough probes in a row.
func (p Probe) Down() bool { return p.Fails >= FailAfter }

// Outcomes of an Event.
const (
	OK       = "ok"       // the response streamed to the end
	Failed   = "error"    // the upstream failed (before or during the stream)
	Rejected = "rejected" // no account could take it (pool exhausted)
	Aborted  = "aborted"  // the client went away
)

// Event is one upstream attempt (failovers are separate attempts), or one
// request refused because no account could take it.
type Event struct {
	At      time.Time `json:"at"`
	Account string    `json:"account,omitempty"`
	Model   string    `json:"model,omitempty"`
	Status  int       `json:"status,omitempty"`
	Outcome string    `json:"outcome"`
	// Reason for a failure: connect, mid_stream, rate_limited,
	// unauthorized, status_NNN, pool_exhausted.
	Reason     string  `json:"reason,omitempty"`
	TTFTMS     int64   `json:"ttft_ms,omitempty"`     // upstream time to first byte
	OverheadMS int64   `json:"overhead_ms,omitempty"` // time Qanat added before it
	DurMS      int64   `json:"duration_ms,omitempty"`
	OutTPS     float64 `json:"output_tps,omitempty"` // output tokens per second of streaming
}

// upstreamFault is a failure the path or provider caused, as opposed to a
// rate limit, a revoked sign-in or a refusal.
func (e Event) upstreamFault() bool {
	if e.Outcome != Failed {
		return false
	}
	return e.Reason == "connect" || e.Reason == "mid_stream" || strings.HasPrefix(e.Reason, "status_5")
}

// Bucket is one Interval of history.
type Bucket struct {
	T        time.Time `json:"t"`
	Requests int64     `json:"requests"` // attempts
	Errors   int64     `json:"errors"`
	Rejected int64     `json:"rejected"`
	TTFTP50  *int64    `json:"ttft_p50_ms,omitempty"`
	TTFTP95  *int64    `json:"ttft_p95_ms,omitempty"`
	// HandshakeMS is the fastest good probe in the bucket.
	HandshakeMS *int64 `json:"handshake_ms,omitempty"`
	Active      int    `json:"active"`
}

// Point is one internet baseline sample.
type Point struct {
	T  time.Time `json:"t"`
	MS *int64    `json:"ms,omitempty"` // nil: unreachable
}

// Status is a provider's verdict.
type Status struct {
	State  string    `json:"state"`
	Reason string    `json:"reason,omitempty"`
	Since  time.Time `json:"since"`
}

// Verdict states, worst first.
var States = []string{"offline", "unreachable", "unstable", "unavailable", "degraded", "checking", "healthy", "no_accounts"}

// Worse reports whether state a outranks b.
func Worse(a, b string) bool { return slices.Index(States, a) < slices.Index(States, b) }

// Assess is Tower's classification applied to one provider: is it the
// gateway's network, the path to the provider, or the provider itself?
// probes are the latest per route; events cover at least UnstableWindow.
func Assess(accounts []Account, probes []Probe, internet *Probe, events []Event, now time.Time) (state, reason string) {
	if len(accounts) == 0 {
		return "no_accounts", ""
	}
	usable, enabled := 0, 0
	why := map[string]int{}
	for _, a := range accounts {
		if !a.Disabled {
			enabled++
		}
		switch {
		case a.Usable:
			usable++
		case a.Disabled:
			why["disabled"]++
		case a.State == "disconnected":
			why["disconnected"]++
		case a.CoolingUntil != nil && a.CoolingUntil.After(now):
			why["cooling"]++
		default:
			why["no_route"]++ // every proxy it may use is unhealthy
		}
	}
	if usable == 0 {
		return "unavailable", mostCommon(why)
	}
	if len(probes) == 0 && len(events) == 0 {
		return "checking", ""
	}

	down, failing := 0, 0
	kind := ""
	for _, p := range probes {
		if !p.OK {
			failing++
			kind = p.Error
		}
		if p.Down() {
			down++
		}
	}
	if len(probes) > 0 && down == len(probes) {
		if internet != nil && internet.Down() {
			return "offline", "" // the gateway's own network is out
		}
		return "unreachable", kind
	}

	var faults, attempts int
	for _, e := range events {
		if now.Sub(e.At) > UnstableWindow || e.Outcome == Rejected || e.Outcome == Aborted {
			continue
		}
		attempts++
		if e.upstreamFault() {
			faults++
		}
	}
	if faults >= UnstableMin && float64(faults) >= UnstableShare*float64(attempts) {
		return "unstable", "upstream_failures"
	}

	if failing > 0 {
		return "degraded", "probe_failed"
	}
	if internet != nil && internet.OK && time.Duration(internet.TotalMS)*time.Millisecond >= SlowInternet {
		return "degraded", "slow_internet"
	}
	if best := fastest(probes); best != nil && time.Duration(*best)*time.Millisecond >= SlowHandshake {
		return "degraded", "slow_handshake"
	}
	s := Summarize(events, now, Window)
	if s.Attempts >= DegradedMinAttempts && s.ErrorRate >= DegradedErrorRate {
		if s.Reasons["rate_limited"] > 0 && s.Reasons["rate_limited"]*2 >= s.Errors {
			return "degraded", "rate_limited"
		}
		return "degraded", "error_rate"
	}
	if usable < enabled { // a disabled account is the admin's choice, not a fault
		return "degraded", "accounts_out"
	}
	return "healthy", ""
}

func mostCommon(m map[string]int) string {
	best, n := "", 0
	for _, k := range []string{"disconnected", "cooling", "no_route", "disabled"} {
		if m[k] > n {
			best, n = k, m[k]
		}
	}
	return best
}

// fastest is the lowest total of the good probes.
func fastest(probes []Probe) *int64 {
	var best *int64
	for _, p := range probes {
		if p.OK && (best == nil || p.TotalMS < *best) {
			v := p.TotalMS
			best = &v
		}
	}
	return best
}

// Stats summarizes events over a window.
type Stats struct {
	Attempts  int64            `json:"attempts"`
	OK        int64            `json:"ok"`
	Errors    int64            `json:"errors"`
	Rejected  int64            `json:"rejected"`
	Aborted   int64            `json:"aborted"`
	ErrorRate float64          `json:"error_rate"` // errors / attempts
	PerMinute float64          `json:"per_minute"` // attempts
	Reasons   map[string]int64 `json:"reasons"`
	TTFT      Percentiles      `json:"ttft_ms"`
	Latency   Percentiles      `json:"latency_ms"`
	Overhead  Percentiles      `json:"overhead_ms"`
	OutTPS    *float64         `json:"output_tps_p50,omitempty"`

	LastSuccess *time.Time `json:"last_success_at,omitempty"`
	LastError   *time.Time `json:"last_error_at,omitempty"`
	LastReason  string     `json:"last_error,omitempty"`
}

// Percentiles in milliseconds; nil without samples.
type Percentiles struct {
	P50 *int64 `json:"p50"`
	P95 *int64 `json:"p95"`
}

// Summarize computes Stats over the events in (now-window, now].
func Summarize(events []Event, now time.Time, window time.Duration) Stats {
	s := Stats{Reasons: map[string]int64{}}
	var ttft, lat, over []int64
	var tps []float64
	for _, e := range events {
		if e.At.After(now) || now.Sub(e.At) > window {
			continue
		}
		at := e.At
		switch e.Outcome {
		case OK:
			s.Attempts++
			s.OK++
			if s.LastSuccess == nil || at.After(*s.LastSuccess) {
				s.LastSuccess = &at
			}
		case Failed:
			s.Attempts++
			s.Errors++
			s.Reasons[e.Reason]++
			if s.LastError == nil || at.After(*s.LastError) {
				s.LastError, s.LastReason = &at, e.Reason
			}
		case Rejected:
			s.Rejected++
			s.Reasons[e.Reason]++
			continue
		case Aborted:
			s.Attempts++
			s.Aborted++
		}
		if e.TTFTMS > 0 {
			ttft = append(ttft, e.TTFTMS)
			over = append(over, e.OverheadMS)
		}
		if e.DurMS > 0 && e.Outcome != Failed {
			lat = append(lat, e.DurMS)
		}
		if e.OutTPS > 0 {
			tps = append(tps, e.OutTPS)
		}
	}
	if s.Attempts > 0 {
		s.ErrorRate = float64(s.Errors) / float64(s.Attempts)
	}
	s.PerMinute = float64(s.Attempts) / window.Minutes()
	s.TTFT, s.Latency, s.Overhead = Pct(ttft), Pct(lat), Pct(over)
	if len(tps) > 0 {
		sort.Float64s(tps)
		v := tps[(len(tps)-1)/2]
		s.OutTPS = &v
	}
	return s
}

// Pct is the nearest-rank p50 and p95 of vs (vs is reordered).
func Pct(vs []int64) Percentiles {
	if len(vs) == 0 {
		return Percentiles{}
	}
	slices.Sort(vs)
	at := func(q float64) *int64 {
		i := int(q*float64(len(vs))+0.999999) - 1
		v := vs[max(0, min(i, len(vs)-1))]
		return &v
	}
	return Percentiles{P50: at(0.5), P95: at(0.95)}
}
