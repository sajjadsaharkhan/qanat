package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// Check is one (proxy, upstream origin) pair to probe. Health is tracked per
// origin, because a proxy can reach one provider and not another.
type Check struct {
	Spec   *Spec
	Origin string // scheme://host[:port] of the upstream, e.g. https://api.anthropic.com
}

// CheckResult is one probe's outcome after the failure threshold is applied.
type CheckResult struct {
	ProxyID   string    `json:"proxy_id"`
	Origin    string    `json:"origin"`
	Healthy   bool      `json:"healthy"`
	OK        bool      `json:"ok"` // this probe's raw result
	LatencyMS int64     `json:"latency_ms"`
	Error     string    `json:"error,omitempty"`
	At        time.Time `json:"at"`
}

// Health remembers which (proxy, origin) pairs are unhealthy (REQ-PROXY-03).
// The hot path reads it without locks: the checker publishes an immutable
// set after every round.
type Health struct {
	unhealthy atomic.Pointer[map[string]bool]

	// Interval between rounds; Timeout per probe; FailAfter consecutive
	// failures mark a pair unhealthy, one success marks it healthy again.
	Interval  time.Duration
	Timeout   time.Duration
	FailAfter int
	// OnRound receives every round's outcomes (metrics, dashboard).
	OnRound func([]CheckResult)

	mu    sync.Mutex
	fails map[string]int
}

// NewHealth has the defaults: a round every 30 s, 10 s per probe,
// unhealthy after 2 failures in a row.
func NewHealth() *Health {
	return &Health{Interval: 30 * time.Second, Timeout: 10 * time.Second, FailAfter: 2, fails: map[string]int{}}
}

// HealthKey is the Redis hash where gateways leave a proxy's latest checks
// for the dashboard: origin → JSON CheckResult, expiring when checks stop.
func HealthKey(proxyID string) string { return "qanat:proxyhealth:" + proxyID }

func healthKey(proxyID, origin string) string { return proxyID + " " + origin }

// Healthy reports whether spec may carry traffic to origin. Direct (nil) and
// pairs not checked yet count as healthy.
func (h *Health) Healthy(spec *Spec, origin string) bool {
	if h == nil || spec == nil {
		return true
	}
	m := h.unhealthy.Load()
	return m == nil || !(*m)[healthKey(spec.ID, origin)]
}

// Run probes the pairs checks returns every Interval until ctx ends.
func (h *Health) Run(ctx context.Context, checks func() []Check) {
	t := time.NewTicker(h.Interval)
	defer t.Stop()
	for {
		h.Round(ctx, checks())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Round probes every pair in parallel, each on a throwaway connection so the
// result covers connect, proxy handshake and TLS, then publishes the new
// unhealthy set. Pairs no longer checked are forgotten.
func (h *Health) Round(ctx context.Context, checks []Check) {
	results := make([]CheckResult, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Go(func() { results[i] = h.probe(ctx, c) })
	}
	wg.Wait()

	h.mu.Lock()
	next := map[string]int{}
	unhealthy := map[string]bool{}
	for i, r := range results {
		k := healthKey(checks[i].Spec.ID, checks[i].Origin)
		n := 0
		if !r.OK {
			n = h.fails[k] + 1
		}
		next[k] = n
		results[i].Healthy = n < h.FailAfter
		if !results[i].Healthy {
			unhealthy[k] = true
		}
	}
	h.fails = next
	h.unhealthy.Store(&unhealthy)
	h.mu.Unlock()

	if h.OnRound != nil {
		h.OnRound(results)
	}
}

func (h *Health) probe(ctx context.Context, c Check) CheckResult {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	tr := NewTransport(c.Spec)
	tr.DisableKeepAlives = true
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	p := probe(ctx, client, Target{URL: c.Origin + "/"})
	return CheckResult{ProxyID: c.Spec.ID, Origin: c.Origin, OK: p.OK, LatencyMS: p.LatencyMS, Error: p.Error, At: time.Now()}
}

// Origin is scheme://host[:port] of a base URL ("" if it doesn't parse).
func Origin(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// Pick chooses the member an account sticks to in a sticky group: the
// highest rendezvous hash of (account, proxy). It depends only on the IDs, so
// it survives restarts and replicas agree; removing one member moves only the
// accounts that were on it.
func Pick(accountID string, members []*Spec) *Spec {
	var best *Spec
	var bestScore uint64
	for _, m := range members {
		sum := sha256.Sum256([]byte(accountID + "\x00" + m.ID))
		if s := binary.BigEndian.Uint64(sum[:8]); best == nil || s > bestScore {
			best, bestScore = m, s
		}
	}
	return best
}
