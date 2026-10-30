package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptrace"
	"time"

	"github.com/redis/go-redis/v9"

	"qanat/internal/proxy"
)

func proxyLabel(sp *proxy.Spec) string {
	if sp == nil {
		return "direct"
	}
	return sp.ID
}

// withConnectTrace observes how long a new upstream connection takes
// (REQ-PROXY-06). Reused connections cost nothing and are not observed.
func withConnectTrace(req *http.Request, sp *proxy.Spec) *http.Request {
	var start time.Time
	trace := &httptrace.ClientTrace{
		GetConn: func(string) { start = time.Now() },
		GotConn: func(info httptrace.GotConnInfo) {
			if !info.Reused && !start.IsZero() {
				proxyConnect.WithLabelValues(proxyLabel(sp)).Observe(time.Since(start).Seconds())
			}
		},
	}
	return req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
}

// healthChecks lists every (proxy, upstream origin) pair the current
// snapshot routes through.
func (g *Gateway) healthChecks() []proxy.Check {
	s := g.snap.Current()
	if s == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []proxy.Check
	for _, accts := range s.accounts {
		for _, a := range accts {
			for _, sp := range a.Proxies {
				if sp == nil || a.Origin == "" || seen[sp.ID+" "+a.Origin] {
					continue
				}
				seen[sp.ID+" "+a.Origin] = true
				out = append(out, proxy.Check{Spec: sp, Origin: a.Origin})
			}
		}
	}
	return out
}

// RunProxyHealth checks every proxy in use until ctx ends (REQ-PROXY-03),
// exporting metrics and leaving results in Redis for the dashboard.
func (g *Gateway) RunProxyHealth(ctx context.Context, rdb *redis.Client) {
	h := g.health
	h.OnRound = func(rs []proxy.CheckResult) {
		proxyHealthy.Reset() // forget proxies no longer in use
		proxyCheckLatency.Reset()
		for _, r := range rs {
			healthy := 0.0
			if r.Healthy {
				healthy = 1
			}
			proxyHealthy.WithLabelValues(r.ProxyID, r.Origin).Set(healthy)
			proxyCheckLatency.WithLabelValues(r.ProxyID, r.Origin).Set(float64(r.LatencyMS) / 1000)
			if !r.OK {
				g.log.Warn("proxy health check failed", "proxy", r.ProxyID, "origin", r.Origin, "healthy", r.Healthy, "err", r.Error)
			}
		}
		if rdb == nil || len(rs) == 0 {
			return
		}
		wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		pipe := rdb.Pipeline()
		for _, r := range rs {
			b, _ := json.Marshal(r)
			pipe.HSet(wctx, proxy.HealthKey(r.ProxyID), r.Origin, b)
			pipe.Expire(wctx, proxy.HealthKey(r.ProxyID), 3*h.Interval)
		}
		if _, err := pipe.Exec(wctx); err != nil {
			redisErrors.Inc()
		}
	}
	h.Run(ctx, g.healthChecks)
}
