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
	"sync/atomic"
	"time"

	"qanat/internal/core/netstats"
	"qanat/internal/core/oauth"
	"qanat/internal/core/quota"
	"qanat/internal/core/usage"
	"qanat/internal/protocol"
	"qanat/internal/proxy"
)

// defaultBaseURL per provider; an account's base_url overrides it.
var defaultBaseURL = map[string]string{"claude": "https://api.anthropic.com", "codex": oauth.CodexAPIURL, "antigravity": oauth.AntigravityAPIURL}

// Request headers never forwarded upstream: hop-by-hop, the caller's Qanat
// key (it must never reach a provider), cookies, and client network details.
var dropRequest = map[string]bool{
	"Connection": true, "Proxy-Connection": true, "Keep-Alive": true, "Proxy-Authorization": true,
	"Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true, "Host": true,
	"Content-Length": true, "Accept-Encoding": true,
	"Authorization": true, "X-Api-Key": true, "Cookie": true,
	"X-Forwarded-For": true, "X-Forwarded-Proto": true, "X-Forwarded-Host": true, "X-Real-Ip": true, "Forwarded": true,
	"X-Qanat-Account": true,
	// A browser (the dashboard's Playground) adds these; Anthropic reads an
	// Origin as a CORS request, and none of them come from the CLI.
	"Origin": true, "Referer": true, "Accept-Language": true, "Dnt": true,
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

// requestRefresh benches an OAuth account that answered 401 and asks the
// control plane to refresh its token now (REQ-OAUTH-03); the refreshed token
// arrives with the next snapshot reload.
func (g *Gateway) requestRefresh(a *Account) {
	g.mu.Lock()
	g.cooldown[a.ID] = time.Now().Add(30 * time.Second)
	g.mu.Unlock()
	if g.pg == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := g.pg.Exec(ctx, "SELECT pg_notify($1, $2)", oauth.RefreshChannel, a.ID); err != nil {
			g.log.Warn("can't request oauth refresh", "account", a.ID, "err", err)
		}
	}()
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
	// An admin's request may pin one account (the Playground tests accounts
	// one by one); other callers' header is ignored. The connect time of the
	// upstream attempt is read back through the context, in microseconds.
	pin := ""
	if rt.user.Admin {
		pin = r.Header.Get("X-Qanat-Account")
	}
	connect := new(atomic.Int64)
	r = r.WithContext(context.WithValue(r.Context(), connectKey{}, connect))
	for _, m := range rt.members {
		if _, ok := defaultBaseURL[m.Provider]; !ok {
			unsupported = append(unsupported, m.Provider)
			continue
		}
		body := rt.body
		if m.Model != rt.name { // REQ-PASS-03: only the model value changes
			body = protocol.ReplaceRange(rt.body, rt.mStart, rt.mEnd, m.Model)
		}
		accounts := g.candidates(rt.snap, rt.user.ID, m.Provider)
		if pin != "" {
			accounts = g.pinned(rt.snap, m.Provider, pin)
		} else {
			accounts = g.order(r.Context(), rt, m.Provider, accounts)
		}
		for _, a := range accounts {
			tried++
			connect.Store(-1)
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
					clientDisconnects.WithLabelValues(endpoint, "before_response").Inc()
					return // client went away before anything was sent
				}
				proxyErrors.WithLabelValues(proxyLabel(sp), "connect").Inc()
				g.log.Warn("upstream unreachable", "account", a.ID, "proxy", proxyLabel(sp), "err", err)
			}
			if err != nil {
				upstreamErrors.WithLabelValues(m.Provider, a.ID, "connect").Inc()
				g.net.record(m.Provider, netstats.Event{Account: a.ID, Model: m.Model, Outcome: netstats.Failed, Reason: "connect", DurMS: time.Since(attempt).Milliseconds()})
				continue
			}
			upstreamResponses.WithLabelValues(m.Provider, a.ID, m.Model, strconv.Itoa(resp.StatusCode)).Inc()
			if resp.StatusCode == http.StatusUnauthorized && a.AuthKind == "oauth" {
				// An expired or revoked sign-in: nothing reached the client,
				// so fail over and have the control plane refresh it.
				g.requestRefresh(a)
				upstreamErrors.WithLabelValues(m.Provider, a.ID, "unauthorized").Inc()
				g.net.record(m.Provider, netstats.Event{Account: a.ID, Model: m.Model, Status: resp.StatusCode, Outcome: netstats.Failed, Reason: "unauthorized",
					DurMS: time.Since(attempt).Milliseconds()})
				g.logRefusal(a, resp)
				continue
			}
			if retryable(resp.StatusCode) {
				reason := "status_" + strconv.Itoa(resp.StatusCode)
				if resp.StatusCode == http.StatusTooManyRequests {
					g.coolDown(a, resp)
					reason = "rate_limited"
				}
				upstreamErrors.WithLabelValues(m.Provider, a.ID, reason).Inc()
				g.net.record(m.Provider, netstats.Event{Account: a.ID, Model: m.Model, Status: resp.StatusCode, Outcome: netstats.Failed, Reason: reason,
					DurMS: time.Since(attempt).Milliseconds()})
				g.logRefusal(a, resp)
				continue
			}
			g.served(rt, m.Provider, a.ID)
			if rt.user.Admin {
				routingHeaders(w.Header(), m, a, sp, o.start, attempt, connect.Load())
			}
			g.stream(w, r, rt, o, m, a, sp, resp, attempt, endpoint)
			return
		}
	}

	switch {
	case tried == 0 && len(unsupported) > 0:
		apiError(w, http.StatusNotFound, "not_found_error",
			fmt.Sprintf("The model %q routes to %s, which this Qanat doesn't support yet.", rt.name, strings.Join(unsupported, ", ")))
		requestsTotal.WithLabelValues(endpoint, "404").Inc()
		rejections.WithLabelValues("unsupported_provider").Inc()
		return
	case tried == 0:
		// REQ-ERR-04: distinct from "your quota is exhausted".
		w.Header().Set("Retry-After", "60")
		apiError(w, 529, "overloaded_error", "No upstream account is available for this model right now. This is not your quota; ask an admin to connect or check the accounts.")
		requestsTotal.WithLabelValues(endpoint, "529").Inc()
		rejections.WithLabelValues("no_account").Inc() // every account benched, cooling down or out of headroom
	default:
		w.Header().Set("Retry-After", "30")
		apiError(w, 529, "overloaded_error", "Every upstream account is busy or rate-limited right now (the shared pool is exhausted). This is not your quota; try again shortly.")
		requestsTotal.WithLabelValues(endpoint, "529").Inc()
		rejections.WithLabelValues("pool_exhausted").Inc() // accounts were tried and all failed
	}
	seen := map[string]bool{}
	for _, m := range rt.members {
		if _, ok := defaultBaseURL[m.Provider]; ok && !seen[m.Provider] {
			seen[m.Provider] = true
			g.net.record(m.Provider, netstats.Event{Model: m.Model, Status: 529, Outcome: netstats.Rejected, Reason: "pool_exhausted"})
		}
	}
}

// logRefusal drains and closes a response the gateway is failing over from,
// logging its status and the start of its error body: the client only ever
// sees "pool exhausted", so this is the one place the real reason shows.
func (g *Gateway) logRefusal(a *Account, resp *http.Response) {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if len(b) > 300 {
		b = b[:300]
	}
	g.log.Warn("upstream refused; failing over", "account", a.ID, "provider", a.Provider, "status", resp.StatusCode, "body", string(b))
}

// pinned is the one account the Playground asked for: it must belong to the
// provider and be usable, but a cooldown doesn't exclude it (the point is to
// see how that account behaves).
func (g *Gateway) pinned(s *Snapshot, provider, id string) []*Account {
	for _, a := range s.accounts[provider] {
		if a.ID == id && !a.Disabled && a.State != "disconnected" && len(g.routes(a)) > 0 {
			return []*Account{a}
		}
	}
	return nil
}

// routingHeaders tells an admin where a request went and what each leg cost:
// the gateway's own work before the upstream call, the upstream's time to
// response headers, and the time a new connection took (through the proxy, if
// any). Everything is in milliseconds; connect is absent on a reused connection.
func routingHeaders(h http.Header, m Member, a *Account, sp *proxy.Spec, start, attempt time.Time, connectUS int64) {
	ms := func(d time.Duration) string { return strconv.FormatFloat(float64(d.Microseconds())/1000, 'f', 1, 64) }
	h.Set("X-Qanat-Account", a.ID)
	h.Set("X-Qanat-Provider", m.Provider)
	h.Set("X-Qanat-Upstream-Model", m.Model)
	h.Set("X-Qanat-Proxy", proxyLabel(sp))
	h.Set("X-Qanat-Gateway-Ms", ms(attempt.Sub(start)))
	h.Set("X-Qanat-Upstream-Ms", ms(time.Since(attempt)))
	if connectUS >= 0 {
		h.Set("X-Qanat-Connect-Ms", ms(time.Duration(connectUS)*time.Microsecond))
	}
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
	switch a.Provider {
	case "codex":
		return g.sendCodex(r, a, sp, body)
	case "antigravity":
		return g.sendAntigravity(r, a, sp, body)
	}
	url := strings.TrimSuffix(a.baseURL(), "/") + r.URL.Path
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range r.Header { // REQ-PASS-05: anthropic-* pass unchanged
		if !dropRequest[k] && !strings.HasPrefix(k, "Sec-") {
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
			// A subscription token is only for the CLI: any other client's UA
			// (a browser's, an SDK's) is replaced, as is a missing one.
			if !strings.HasPrefix(req.Header.Get("User-Agent"), "claude-cli/") {
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
	defer g.net.streamStart(m.Provider, a.ID)()

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
	out := &sseWriter{w: w, rc: rc}
	if sse && m.Provider != "claude" { // Claude passes through untouched and pings itself
		out.keepAlive()
		defer out.stopKeepAlive()
	}
	var firstByte time.Time
	aborted, errMsg := false, ""
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if firstByte.IsZero() {
				firstByte = time.Now()
				upstreamTTFT.WithLabelValues(m.Provider, m.Model).Observe(firstByte.Sub(attempt).Seconds())
				clientTTFT.WithLabelValues(m.Provider, m.Model).Observe(firstByte.Sub(o.start).Seconds())
			}
			if werr := out.write(buf[:n]); werr != nil {
				aborted, errMsg = true, "client disconnected"
				clientDisconnects.WithLabelValues(endpoint, "mid_stream").Inc()
				break // returning cancels r.Context(), which cancels upstream
			}
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
				clientDisconnects.WithLabelValues(endpoint, "mid_stream").Inc()
			} else {
				errMsg = "upstream connection lost"
				upstreamErrors.WithLabelValues(m.Provider, a.ID, "mid_stream").Inc()
				proxyErrors.WithLabelValues(proxyLabel(sp), "mid_stream").Inc()
				if sse {
					out.write([]byte(errorEvent))
				}
			}
			break
		}
	}
	out.stopKeepAlive() // nothing may follow message_stop
	end := time.Now()
	nev := netstats.Event{At: end, Account: a.ID, Model: m.Model, Status: resp.StatusCode, Outcome: netstats.OK, DurMS: end.Sub(attempt).Milliseconds()}
	switch {
	case errMsg == "upstream connection lost":
		nev.Outcome, nev.Reason = netstats.Failed, "mid_stream"
	case aborted:
		nev.Outcome = netstats.Aborted
	}
	if !firstByte.IsZero() {
		// Overhead = client TTFT − upstream TTFT (§15.1).
		overhead.WithLabelValues(m.Provider).Observe((firstByte.Sub(o.start) - firstByte.Sub(attempt)).Seconds())
		nev.TTFTMS = firstByte.Sub(attempt).Milliseconds()
		nev.OverheadMS = attempt.Sub(o.start).Milliseconds()
	}
	if !o.usage {
		g.net.record(m.Provider, nev)
		return
	}

	u := scanner.Usage
	if !sse && charge {
		u, _ = protocol.MessageUsage(body.Bytes())
	}
	if gen := end.Sub(firstByte).Seconds(); !firstByte.IsZero() && u.OutputTokens > 0 && gen > 0.05 {
		nev.OutTPS = float64(u.OutputTokens) / gen
	}
	g.net.record(m.Provider, nev)
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
