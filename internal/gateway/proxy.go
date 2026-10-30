package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"qanat/internal/core/oauth"
	"qanat/internal/core/quota"
	"qanat/internal/core/usage"
	"qanat/internal/protocol"
	"qanat/internal/proxy"
)

// defaultBaseURL per provider; an account's base_url overrides it.
var defaultBaseURL = map[string]string{"claude": "https://api.anthropic.com"}

// Request headers never forwarded upstream: hop-by-hop, the caller's Qanat
// key (it must never reach a provider), cookies, and client network details.
var dropRequest = map[string]bool{
	"Connection": true, "Proxy-Connection": true, "Keep-Alive": true, "Proxy-Authorization": true,
	"Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true, "Host": true,
	"Content-Length": true, "Accept-Encoding": true,
	"Authorization": true, "X-Api-Key": true, "Cookie": true,
	"X-Forwarded-For": true, "X-Forwarded-Proto": true, "X-Forwarded-Host": true, "X-Real-Ip": true, "Forwarded": true,
}

// Response headers not passed back: hop-by-hop and account-scoped cookies.
var dropResponse = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Proxy-Authenticate": true, "Te": true, "Trailer": true,
	"Transfer-Encoding": true, "Upgrade": true, "Set-Cookie": true,
}

// chunkSize is the read buffer; each read is written and flushed at once.
const chunkSize = 32 << 10

var bufPool = sync.Pool{New: func() any { b := make([]byte, chunkSize); return &b }}

type forwardOpts struct {
	reqID     string
	start     time.Time
	windows   []quota.Window // charged as usage arrives
	remaining int64          // micro-USD for x-qanat-quota-remaining-usd; -1 = unknown
	usage     bool           // record and charge usage (Messages only)
}

// baseURL is the account's upstream, or the provider's default.
func (a *Account) baseURL() string {
	if a.BaseURL != "" {
		return a.BaseURL
	}
	return defaultBaseURL[a.Provider]
}

// routes are the account's proxies that may carry traffic now: unhealthy
// ones are skipped (REQ-PROXY-03).
func (g *Gateway) routes(a *Account) []*proxy.Spec {
	ok := a.Proxies[:0:0]
	for _, sp := range a.Proxies {
		if g.health.Healthy(sp, a.Origin) {
			ok = append(ok, sp)
		}
	}
	return ok
}

// candidates orders a provider's accounts for this user: sticky account
// first, then the rest in registration order; skips accounts that are
// disconnected, cooling down, or have no usable proxy (REQ-POOL-02/03,
// REQ-PROXY-01/03).
func (g *Gateway) candidates(s *Snapshot, userID, provider string) []*Account {
	if old := g.retained.Load(); old != s.Gen && g.retained.CompareAndSwap(old, s.Gen) {
		g.proxies.Retain(s.proxyIDs) // close transports of proxies no longer used
	}
	now := time.Now()
	g.mu.Lock()
	stickyID, ok := g.sticky[userID+"|"+provider]
	if !ok {
		stickyID = s.sticky[userID+"|"+provider]
	}
	var out []*Account
	for _, a := range s.accounts[provider] {
		if a.Disabled || a.State == "disconnected" || a.CooldownUntil.After(now) || g.cooldown[a.ID].After(now) || len(g.routes(a)) == 0 {
			continue
		}
		if a.ID == stickyID {
			out = append([]*Account{a}, out...)
		} else {
			out = append(out, a)
		}
	}
	g.mu.Unlock()
	return out
}

func (g *Gateway) coolDown(a *Account, resp *http.Response) {
	d := time.Minute
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
		d = time.Duration(s) * time.Second
	}
	g.mu.Lock()
	g.cooldown[a.ID] = time.Now().Add(d)
	g.mu.Unlock()
}

var errNoRoute = errors.New("no healthy proxy")

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// forward sends the request to the first account that accepts it and streams
// the response back unbuffered. Failover happens only before the first byte
// reaches the client (REQ-POOL-06, REQ-COMBO-02).
func (g *Gateway) forward(w http.ResponseWriter, r *http.Request, rt *route, o forwardOpts) {
	endpoint := "messages"
	if strings.HasSuffix(r.URL.Path, "count_tokens") {
		endpoint = "count_tokens"
	}
	var unsupported []string
	tried := 0
	for _, m := range rt.members {
		if _, ok := defaultBaseURL[m.Provider]; !ok {
			unsupported = append(unsupported, m.Provider)
			continue
		}
		body := rt.body
		if m.Model != rt.name { // REQ-PASS-03: only the model value changes
			body = protocol.ReplaceRange(rt.body, rt.mStart, rt.mEnd, m.Model)
		}
		for _, a := range g.candidates(rt.snap, rt.user.ID, m.Provider) {
			tried++
			attempt := time.Now()
			// A failover group's next member is tried before the next account
			// (REQ-PROXY-03); nothing has reached the client yet.
			var resp *http.Response
			var sp *proxy.Spec
			err := errNoRoute // health changed since candidates ran
			for _, sp = range g.routes(a) {
				if resp, err = g.send(r, a, sp, body); err == nil {
					break
				}
				if r.Context().Err() != nil {
					requestsTotal.WithLabelValues(endpoint, "499").Inc()
					return // client went away before anything was sent
				}
				proxyErrors.WithLabelValues(proxyLabel(sp), "connect").Inc()
				g.log.Warn("upstream unreachable", "account", a.ID, "proxy", proxyLabel(sp), "err", err)
			}
			if err != nil {
				upstreamErrors.WithLabelValues(m.Provider, a.ID, "connect").Inc()
				continue
			}
			if retryable(resp.StatusCode) {
				reason := "status_" + strconv.Itoa(resp.StatusCode)
				if resp.StatusCode == http.StatusTooManyRequests {
					g.coolDown(a, resp)
					reason = "rate_limited"
				}
				upstreamErrors.WithLabelValues(m.Provider, a.ID, reason).Inc()
				io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
				resp.Body.Close()
				continue
			}
			g.bindSticky(rt, m.Provider, a.ID)
			g.stream(w, r, rt, o, m, a, sp, resp, attempt, endpoint)
			return
		}
	}

	switch {
	case tried == 0 && len(unsupported) > 0:
		apiError(w, http.StatusNotFound, "not_found_error",
			fmt.Sprintf("The model %q routes to %s, which this Qanat doesn't support yet.", rt.name, strings.Join(unsupported, ", ")))
		requestsTotal.WithLabelValues(endpoint, "404").Inc()
	case tried == 0:
		// REQ-ERR-04: distinct from "your quota is exhausted".
		w.Header().Set("Retry-After", "60")
		apiError(w, 529, "overloaded_error", "No upstream account is available for this model right now. This is not your quota; ask an admin to connect or check the accounts.")
		requestsTotal.WithLabelValues(endpoint, "529").Inc()
	default:
		w.Header().Set("Retry-After", "30")
		apiError(w, 529, "overloaded_error", "Every upstream account is busy or rate-limited right now (the shared pool is exhausted). This is not your quota; try again shortly.")
		requestsTotal.WithLabelValues(endpoint, "529").Inc()
	}
	rejections.WithLabelValues("pool_exhausted").Inc()
}

func (g *Gateway) bindSticky(rt *route, provider, accountID string) {
	k := rt.user.ID + "|" + provider
	g.mu.Lock()
	prev, ok := g.sticky[k]
	if !ok {
		prev = rt.snap.sticky[k]
	}
	g.sticky[k] = accountID
	g.mu.Unlock()
	if prev != accountID {
		g.persistSticky(rt.user.ID, provider, accountID)
	}
}

// send issues the upstream request through sp (§8.2; nil = direct), tied to
// the client's context so a disconnect cancels it immediately
// (REQ-PERF-03).
func (g *Gateway) send(r *http.Request, a *Account, sp *proxy.Spec, body []byte) (*http.Response, error) {
	url := strings.TrimSuffix(a.baseURL(), "/") + r.URL.Path
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range r.Header { // REQ-PASS-05: anthropic-* pass unchanged
		if !dropRequest[k] {
			req.Header[k] = v
		}
	}
	// Auth by the account's kind (migration 0007): an API key uses Anthropic's
	// x-api-key form, everything else (a stored base-URL token, a token from
	// `claude setup-token`, an OAuth access token) is a bearer token.
	// anthropic-* headers pass through from the caller, so only fill the
	// version in when absent.
	if a.AuthKind == "api_key" {
		req.Header.Set("X-Api-Key", a.Token)
		if req.Header.Get("anthropic-version") == "" {
			req.Header.Set("anthropic-version", "2023-06-01")
		}
	} else if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
		// OAuth accounts (migration 0008) authenticate as the Claude Code CLI
		// subscription they came from: the oauth beta must ride along, and a
		// caller without a UA gets the CLI's.
		if a.AuthKind == "oauth" {
			if beta := req.Header.Get("anthropic-beta"); !strings.Contains(beta, oauth.OAuthBeta) {
				if beta == "" {
					req.Header.Set("anthropic-beta", oauth.OAuthBeta)
				} else {
					req.Header.Set("anthropic-beta", beta+","+oauth.OAuthBeta)
				}
			}
			if req.Header.Get("User-Agent") == "" {
				req.Header.Set("User-Agent", oauth.ClaudeUserAgent)
			}
		}
	}
	req.ContentLength = int64(len(body))
	return g.proxies.Client(sp).Do(withConnectTrace(req, sp))
}

// errorEvent reports an upstream failure after the first byte (REQ-POOL-06).
const errorEvent = "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"The upstream connection was lost mid-response. Retry the request.\"}}\n\n"

// stream copies the upstream response to the client, flushing every read
// (REQ-STREAM-01, REQ-PERF-01/02), and settles usage at the end.
func (g *Gateway) stream(w http.ResponseWriter, r *http.Request, rt *route, o forwardOpts, m Member, a *Account, sp *proxy.Spec, resp *http.Response, attempt time.Time, endpoint string) {
	defer resp.Body.Close()
	activeStreams.Inc()
	defer activeStreams.Dec()
	proxyStreams := proxyActiveStreams.WithLabelValues(proxyLabel(sp))
	proxyStreams.Inc()
	defer proxyStreams.Dec()

	h := w.Header()
	for k, v := range resp.Header {
		if !dropResponse[k] {
			h[k] = v
		}
	}
	sse := strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream")
	if sse {
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Accel-Buffering", "no")
		h.Del("Content-Length")
	}
	if o.remaining >= 0 {
		h.Set("x-qanat-quota-remaining-usd", strconv.FormatFloat(quota.FromMicro(o.remaining), 'f', 4, 64))
	}
	w.WriteHeader(resp.StatusCode)
	rc := http.NewResponseController(w)
	rc.Flush()
	requestsTotal.WithLabelValues(endpoint, strconv.Itoa(resp.StatusCode)).Inc()

	// Usage settlement (PRD §10.4): input is charged the moment
	// message_start arrives, so parallel requests see it; the rest at the end.
	price, priced := rt.snap.prices[m.Model]
	charge := o.usage && priced && resp.StatusCode == http.StatusOK
	var charged int64
	var chargeWG sync.WaitGroup
	scanner := &protocol.UsageScanner{}
	if charge {
		scanner.OnStart = func(u protocol.Usage) {
			charged = price.CostMicro(u.InputTokens, 0, u.CacheReadTokens, u.CacheWriteTokens)
			chargeWG.Add(1)
			go func(micro int64) {
				defer chargeWG.Done()
				g.chargeAsync(rt.user.ID, o.windows, rt.plan.Anchors, micro)
			}(charged)
		}
	}
	var body bytes.Buffer // non-streaming responses: kept (bounded) to read usage

	bufp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bufp)
	buf := *bufp
	var firstByte time.Time
	aborted, errMsg := false, ""
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if firstByte.IsZero() {
				firstByte = time.Now()
				upstreamTTFT.WithLabelValues(m.Provider).Observe(firstByte.Sub(attempt).Seconds())
			}
			if _, werr := w.Write(buf[:n]); werr != nil {
				aborted, errMsg = true, "client disconnected"
				break // returning cancels r.Context(), which cancels upstream
			}
			rc.Flush()
			if sse {
				scanner.Write(buf[:n])
			} else if charge && body.Len() < 4<<20 {
				body.Write(buf[:n])
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			aborted = true
			if r.Context().Err() != nil {
				errMsg = "client disconnected"
			} else {
				errMsg = "upstream connection lost"
				upstreamErrors.WithLabelValues(m.Provider, a.ID, "mid_stream").Inc()
				proxyErrors.WithLabelValues(proxyLabel(sp), "mid_stream").Inc()
				if sse {
					io.WriteString(w, errorEvent)
					rc.Flush()
				}
			}
			break
		}
	}
	if !firstByte.IsZero() {
		// Overhead = client TTFT − upstream TTFT (§15.1).
		overhead.WithLabelValues(m.Provider).Observe((firstByte.Sub(o.start) - firstByte.Sub(attempt)).Seconds())
	}
	if !o.usage {
		return
	}

	u := scanner.Usage
	if !sse && charge {
		u, _ = protocol.MessageUsage(body.Bytes())
	}
	var total int64
	if priced {
		total = price.CostMicro(u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens)
	}
	ev := usage.Event{
		RequestID: o.reqID, At: o.start, UserID: rt.user.ID, KeyID: rt.key.ID, ClientIP: clientIP(r),
		Model: rt.name, Provider: m.Provider, UpstreamModel: m.Model, AccountID: a.ID,
		InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens,
		CostMicro: total, PriceVersion: price.Version, Status: resp.StatusCode, Aborted: aborted, StopReason: scanner.StopReason,
		DurMillis: time.Since(o.start).Milliseconds(), Error: errMsg,
	}
	if !firstByte.IsZero() {
		ev.TTFTMillis = firstByte.Sub(o.start).Milliseconds()
	}
	for kind, n := range map[string]int64{"input": u.InputTokens, "output": u.OutputTokens, "cache_read": u.CacheReadTokens, "cache_write": u.CacheWriteTokens} {
		tokensTotal.WithLabelValues(m.Provider, kind).Add(float64(n))
	}
	rest := total - charged
	go func() {
		chargeWG.Wait()
		if charge {
			g.chargeAsync(rt.user.ID, o.windows, rt.plan.Anchors, rest)
		}
		g.pub.Publish(ev) // REQ-QUOTA-01: aborted streams are recorded too
	}()
}

func (g *Gateway) chargeAsync(userID string, ws []quota.Window, a quota.Anchors, micro int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := g.quota.Charge(ctx, userID, ws, a, micro, time.Now()); err != nil {
		redisErrors.Inc()
		g.log.Warn("quota charge failed; aggregates still record the usage", "err", err)
	}
}

// clientIP is the caller's address as seen by Nginx (for the Keys page).
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-Ip"); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(ip)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
