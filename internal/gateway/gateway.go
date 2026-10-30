// Package gateway is Qanat's data plane: the Anthropic Messages surface that
// Claude Code talks to (PRD §4). Rule zero (§9): apart from the upstream
// connection there is no blocking I/O on the hot path. Auth, mapping, access
// and pricing answer from the in-memory snapshot; quota is one Redis round
// trip; usage is published after the stream, asynchronously.
package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"qanat/internal/core/apikey"
	"qanat/internal/core/provider"
	"qanat/internal/core/quota"
	"qanat/internal/protocol"
	"qanat/internal/proxy"
	"qanat/internal/store/db"
)

// maxBody matches Nginx client_max_body_size (§12.1).
const maxBody = 32 << 20

// Gateway serves the Claude Code surface.
type Gateway struct {
	snap     *Store
	quota    *quota.Store
	pub      *Publisher
	pg       *pgxpool.Pool
	log      *slog.Logger
	proxies  *proxy.Pool
	health   *proxy.Health
	retained atomic.Int64 // snapshot generation proxies were last pruned at
	testMode bool

	// cooldown and sticky overlay the snapshot with what this replica learned
	// since its last reload (REQ-POOL-02/03/04).
	mu       sync.Mutex
	cooldown map[string]time.Time // account ID → until
	sticky   map[string]string    // user|provider → account ID
}

// New wires the gateway.
func New(snap *Store, q *quota.Store, pub *Publisher, pg *pgxpool.Pool, log *slog.Logger, testMode bool) *Gateway {
	return &Gateway{
		snap: snap, quota: q, pub: pub, pg: pg, log: log, testMode: testMode,
		proxies: proxy.NewPool(), health: proxy.NewHealth(),
		cooldown: map[string]time.Time{}, sticky: map[string]string{},
	}
}

// Register mounts the routes.
func (g *Gateway) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/messages", g.messages)
	mux.HandleFunc("POST /v1/messages/count_tokens", g.countTokens)
	mux.HandleFunc("GET /v1/models", g.models)
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, _ *http.Request) {
		apiError(w, http.StatusNotFound, "not_found_error", "Qanat serves /v1/messages, /v1/messages/count_tokens and /v1/models.")
	})
	if g.testMode {
		mux.HandleFunc("GET /test/generation", func(w http.ResponseWriter, _ *http.Request) {
			var gen int64
			if s := g.snap.Current(); s != nil {
				gen = s.Gen
			}
			json.NewEncoder(w).Encode(map[string]int64{"generation": gen})
		})
	}
}

// Ready reports whether the first snapshot has loaded.
func (g *Gateway) Ready(context.Context) error {
	if g.snap.Current() == nil {
		return errors.New("config snapshot not loaded yet")
	}
	return nil
}

// apiError writes the Anthropic error envelope (REQ-ERR-01).
func apiError(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	b, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]string{"type": typ, "message": msg}})
	w.Write(b)
}

func newRequestID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return "qreq_" + hex.EncodeToString(b)
}

// presentedKey reads the key Claude Code sends: ANTHROPIC_AUTH_TOKEN as a
// bearer token, or ANTHROPIC_API_KEY as x-api-key.
func presentedKey(r *http.Request) string {
	if a := r.Header.Get("Authorization"); len(a) > 7 && strings.EqualFold(a[:7], "Bearer ") {
		return a[7:]
	}
	return r.Header.Get("X-Api-Key")
}

// caller is an authenticated request's identity.
type caller struct {
	snap *Snapshot
	key  *Key
	user *User
	plan *Plan
}

// authenticate resolves the key from memory (budget 0.2 ms, §9).
func (g *Gateway) authenticate(w http.ResponseWriter, r *http.Request, endpoint string) (*caller, bool) {
	s := g.snap.Current()
	if s == nil {
		apiError(w, http.StatusServiceUnavailable, "overloaded_error", "Qanat is starting up. Try again in a few seconds.")
		requestsTotal.WithLabelValues(endpoint, "503").Inc()
		return nil, false
	}
	presented := presentedKey(r)
	k, u, p, ok := s.Lookup(presented)
	if !apikey.LooksValid(presented) || !ok {
		apiError(w, http.StatusUnauthorized, "authentication_error", "Invalid Qanat API key. Create one on the Keys page of the Qanat dashboard.")
		requestsTotal.WithLabelValues(endpoint, "401").Inc()
		return nil, false
	}
	if k.ExpiresAt != nil && time.Now().After(*k.ExpiresAt) {
		apiError(w, http.StatusUnauthorized, "authentication_error", "This Qanat API key has expired. Create a new one on the Keys page.")
		requestsTotal.WithLabelValues(endpoint, "401").Inc()
		return nil, false
	}
	return &caller{snap: s, key: k, user: u, plan: p}, true
}

// route is the resolved request: who, what, and where it may go.
type route struct {
	*caller
	name    string // Qanat name the client asked for
	body    []byte
	mStart  int // byte range of the model value in body
	mEnd    int
	members []Member // priced candidates, in fallback order
}

// resolve reads the body and applies mapping, access and pricing.
func (g *Gateway) resolve(w http.ResponseWriter, r *http.Request, c *caller, endpoint string) (*route, bool) {
	fail := func(status int, typ, msg string) (*route, bool) {
		apiError(w, status, typ, msg)
		requestsTotal.WithLabelValues(endpoint, strconv.Itoa(status)).Inc()
		return nil, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		return fail(http.StatusRequestEntityTooLarge, "request_too_large", "The request body is larger than 32 MB.")
	}
	name, s, e, err := protocol.ModelName(body)
	if err != nil {
		return fail(http.StatusBadRequest, "invalid_request_error", "Invalid request body: "+err.Error())
	}
	if !Allowed(c.plan, c.key, name) {
		rejections.WithLabelValues("model_not_allowed").Inc()
		return fail(http.StatusForbidden, "permission_error",
			fmt.Sprintf("Your Plan doesn't include the model %q. Run /model to pick an allowed model, or ask an admin.", name))
	}
	t, ok := c.snap.Resolve(name)
	if !ok {
		return fail(http.StatusNotFound, "not_found_error", fmt.Sprintf("Qanat has no model called %q.", name))
	}
	rt := &route{caller: c, name: name, body: body, mStart: s, mEnd: e}
	var unpriced []string
	for _, m := range t.Members {
		if m.Model == "" {
			m.Model = name
		}
		if _, ok := c.snap.prices[m.Model]; ok { // D13: unpriced models are unusable
			rt.members = append(rt.members, m)
		} else if !slices.Contains(unpriced, m.Model) {
			unpriced = append(unpriced, m.Model)
		}
	}
	if len(rt.members) == 0 {
		// Name the upstream ids that need a price: that is what the admin
		// prices, not the prefixed or mapped name the client sent.
		return fail(http.StatusForbidden, "permission_error",
			fmt.Sprintf("The model %q has no confirmed price yet, so it can't be used. Ask an admin to confirm a price for %s.", name, quoteAll(unpriced)))
	}
	return rt, true
}

// windows returns the Plan's windows that apply to name, with top-ups added.
// A per-model window binds the prefixed name too ("cc/opus" counts as "opus").
func windows(p *Plan, grantMicro int64, name string) []quota.Window {
	bare := provider.Bare(name)
	var ws []quota.Window
	for _, w := range p.Quotas {
		if w.Scope == "" || w.Scope == name || w.Scope == bare {
			w.LimitMicro += grantMicro
			ws = append(ws, w)
		}
	}
	return ws
}

var windowNames = map[quota.Kind]string{quota.Rolling5h: "5-hour", quota.Daily: "Daily", quota.Weekly: "Weekly", quota.Monthly: "Monthly"}

// refusal turns an admission decision into the Anthropic 429 (REQ-ERR-02).
func refusal(w http.ResponseWriter, d quota.Decision, p *Plan) {
	secs := int(d.RetryAfter.Round(time.Second) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
	var msg string
	switch d.Reason {
	case quota.ReasonZeroQuota:
		msg = "Your Plan has no quota yet, so requests are refused. Ask an admin to assign you a Plan."
	case quota.ReasonQuota:
		what := windowNames[d.Window.Kind] + " quota"
		if d.Window.Scope != "" {
			what = d.Window.Scope + " " + strings.ToLower(what)
		}
		reset := d.ResetAt.In(p.Anchors.Location)
		when := reset.Format("15:04")
		if d.ResetAt.Sub(time.Now()) > 24*time.Hour {
			when = reset.Format("Mon 15:04")
		}
		msg = fmt.Sprintf("%s exhausted; resets at %s (%s).", what, when, p.Anchors.Location)
	case quota.ReasonRPM:
		msg = fmt.Sprintf("Rate limit: your Plan allows %d requests per minute. Try again in %ds.", p.RPM, max(secs, 1))
	case quota.ReasonConcurrency:
		msg = fmt.Sprintf("Too many requests at once: your Plan allows %d concurrent streams. Wait for one to finish.", p.MaxConcurrent)
	}
	rejections.WithLabelValues(string(d.Reason)).Inc()
	apiError(w, http.StatusTooManyRequests, "rate_limit_error", msg)
}

func (g *Gateway) messages(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	c, ok := g.authenticate(w, r, "messages")
	if !ok {
		return
	}
	rt, ok := g.resolve(w, r, c, "messages")
	if !ok {
		return
	}

	reqID := newRequestID()
	ws := windows(c.plan, c.snap.grants[c.user.ID], rt.name)
	policy := quota.Policy{Windows: ws, RPM: c.plan.RPM, MaxConcurrent: c.plan.MaxConcurrent, Anchors: c.plan.Anchors}
	qctx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
	d, err := g.quota.Admit(qctx, c.user.ID, reqID, policy, start)
	cancel()
	remaining := int64(-1)
	switch {
	case err != nil:
		// Fail open: a Redis blip must not become an outage (§15.3). Usage is
		// still recorded, and counters are rebuilt from aggregates.
		redisErrors.Inc()
		g.log.Warn("quota check unavailable; admitting", "err", err)
	case !d.Admitted:
		refusal(w, d, c.plan)
		requestsTotal.WithLabelValues("messages", "429").Inc()
		return
	default:
		remaining = d.RemainingMicro
		defer func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Second)
			defer cancel()
			if err := g.quota.Release(ctx, c.user.ID, reqID); err != nil {
				redisErrors.Inc()
			}
		}()
	}
	g.forward(w, r, rt, forwardOpts{reqID: reqID, start: start, windows: ws, remaining: remaining, usage: true})
}

func (g *Gateway) countTokens(w http.ResponseWriter, r *http.Request) {
	c, ok := g.authenticate(w, r, "count_tokens")
	if !ok {
		return
	}
	rt, ok := g.resolve(w, r, c, "count_tokens")
	if !ok {
		return
	}
	// Passed through to Claude; no quota charge (§4.2). Local estimation for
	// other providers arrives with their translators (P1).
	g.forward(w, r, rt, forwardOpts{reqID: newRequestID(), start: time.Now(), remaining: -1})
}

// models lists exactly the names the caller's Plan allows (REQ-ACCESS-02).
func (g *Gateway) models(w http.ResponseWriter, r *http.Request) {
	c, ok := g.authenticate(w, r, "models")
	if !ok {
		return
	}
	names := c.snap.ModelNames(c.plan, c.key)
	sort.Strings(names)
	type model struct {
		Type        string `json:"type"`
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		CreatedAt   string `json:"created_at"`
	}
	out := struct {
		Data    []model `json:"data"`
		HasMore bool    `json:"has_more"`
		FirstID *string `json:"first_id"`
		LastID  *string `json:"last_id"`
	}{Data: []model{}}
	for _, n := range names {
		out.Data = append(out.Data, model{Type: "model", ID: n, DisplayName: n, CreatedAt: "2025-01-01T00:00:00Z"})
	}
	if len(names) > 0 {
		out.FirstID, out.LastID = &names[0], &names[len(names)-1]
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
	requestsTotal.WithLabelValues("models", "200").Inc()
}

// persistSticky records a new binding in the background (REQ-POOL-04).
func (g *Gateway) persistSticky(userID, provider, accountID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := db.New(g.pg).UpsertStickyBinding(ctx, db.UpsertStickyBindingParams{UserID: userID, Provider: provider, AccountID: accountID}); err != nil {
			g.log.Warn("sticky binding not saved", "err", err)
		}
	}()
}

// quoteAll renders names as "a", "b".
func quoteAll(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = strconv.Quote(n)
	}
	return strings.Join(q, ", ")
}
