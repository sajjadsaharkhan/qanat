//go:build reference

// Reference driver: a deliberately small, in-memory gateway that implements
// just enough of the PRD to pass the acceptance suite. It exists to prove the
// suite asserts real behavior (a test that cannot fail is worthless) and to
// show implementers what each assertion expects. It is NOT the production
// design: no Redis, no PostgreSQL, no async pipeline, naive locking.
//
//	go test -tags reference ./test/acceptance/...
package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strconv"
	"sync"
	"testing"

	"qanat/internal/testkit/sse"
)

func init() {
	RegisterDriver(func(t testing.TB) Driver {
		g := &refGateway{}
		g.Reset(context.Background())
		g.srv = httptest.NewServer(g)
		t.Cleanup(g.srv.Close)
		return g
	})
}

type refAccount struct{ provider, baseURL, token string }

type refGateway struct {
	srv *httptest.Server

	mu       sync.Mutex
	users    map[string]Plan
	keys     map[string]string // key -> userID
	accounts []refAccount
	mapping  []MappingRule
	prices   map[string]Price
	usage    map[string]Usage
	seq      int
}

func (g *refGateway) GatewayURL() string { return g.srv.URL }

func (g *refGateway) Reset(context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.users, g.keys, g.prices, g.usage = map[string]Plan{}, map[string]string{}, map[string]Price{}, map[string]Usage{}
	g.accounts, g.mapping = nil, nil
	return nil
}

func (g *refGateway) CreateUser(_ context.Context, email string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seq++
	id := fmt.Sprintf("u%d", g.seq)
	g.users[id] = Plan{} // REQ-PLAN-03: default Plan has zero quota
	return id, nil
}

func (g *refGateway) SetPlan(_ context.Context, id string, p Plan) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.users[id] = p
	return nil
}

func (g *refGateway) CreateKey(_ context.Context, id string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seq++
	k := fmt.Sprintf("qnt_live_ref%d", g.seq)
	g.keys[k] = id
	return k, nil
}

func (g *refGateway) AddUpstreamAccount(_ context.Context, provider, url string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seq++
	g.accounts = append(g.accounts, refAccount{provider, url, fmt.Sprintf("oauth-access-%d", g.seq)})
	return strconv.Itoa(g.seq), nil
}

func (g *refGateway) SetMapping(_ context.Context, r []MappingRule) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.mapping = r
	return nil
}

func (g *refGateway) SetPrice(_ context.Context, m string, p Price) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.prices[m] = p
	return nil
}

func (g *refGateway) Usage(_ context.Context, id string) (Usage, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.usage[id], nil
}

// --- HTTP surface -----------------------------------------------------------

func refError(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"type":"error","error":{"type":%q,"message":%q}}`, typ, msg)
}

func (g *refGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("x-api-key")
	if a := r.Header.Get("Authorization"); len(a) > 7 && a[:7] == "Bearer " {
		key = a[7:]
	}
	g.mu.Lock()
	user, ok := g.keys[key]
	plan := g.users[user]
	g.mu.Unlock()
	if !ok {
		refError(w, 401, "authentication_error", "invalid API key")
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		g.models(w, plan)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages":
		g.messages(w, r, user, plan)
	default:
		refError(w, 404, "not_found_error", "unknown route")
	}
}

func allowed(p Plan, name string) bool {
	if len(p.AllowedModels) == 0 {
		return true
	}
	for _, m := range p.AllowedModels {
		if m == name {
			return true
		}
	}
	return false
}

func (g *refGateway) models(w http.ResponseWriter, p Plan) {
	g.mu.Lock()
	var names []string
	seen := map[string]bool{}
	add := func(n string) {
		if !seen[n] && allowed(p, n) {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, r := range g.mapping {
		if !regexp.MustCompile(`[*?\[]`).MatchString(r.Pattern) {
			add(r.Pattern)
		}
	}
	for m := range g.prices {
		add(m)
	}
	g.mu.Unlock()
	type model struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	out := struct {
		Data    []model `json:"data"`
		HasMore bool    `json:"has_more"`
	}{}
	for _, n := range names {
		out.Data = append(out.Data, model{n, "model"})
	}
	json.NewEncoder(w).Encode(out)
}

var modelField = regexp.MustCompile(`"model"\s*:\s*"((?:[^"\\]|\\.)*)"`)

func (g *refGateway) resolve(in string) (provider, upstream string, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, exact := range []bool{true, false} { // REQ-MAP-02: exact before glob
		for _, r := range g.mapping {
			isExact := r.Pattern == in
			globHit, _ := path.Match(r.Pattern, in)
			if (exact && isExact) || (!exact && globHit) {
				if r.Model == "" {
					return r.Provider, in, true
				}
				return r.Provider, r.Model, true
			}
		}
	}
	return "", "", false
}

func (g *refGateway) messages(w http.ResponseWriter, r *http.Request, user string, plan Plan) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return
	}
	m := modelField.FindSubmatchIndex(body)
	if m == nil {
		refError(w, 400, "invalid_request_error", "missing model")
		return
	}
	in := string(body[m[2]:m[3]])
	if !allowed(plan, in) {
		refError(w, 403, "permission_error", fmt.Sprintf("model %q is not allowed by your plan", in))
		return
	}
	g.mu.Lock()
	spent := g.usage[user].CostUSD
	g.mu.Unlock()
	if plan.DailyUSD <= 0 || spent >= plan.DailyUSD {
		w.Header().Set("retry-after", "3600")
		refError(w, 429, "rate_limit_error", "daily quota exhausted; resets at 00:00")
		return
	}
	provider, upstreamModel, ok := g.resolve(in)
	if !ok {
		refError(w, 404, "not_found_error", "no mapping for model")
		return
	}
	if upstreamModel != in { // REQ-PASS-03: replace the value bytes only
		body = append(append(append([]byte{}, body[:m[2]]...), upstreamModel...), body[m[3]:]...)
	}

	g.mu.Lock()
	var pool []refAccount
	for _, a := range g.accounts {
		if a.provider == provider {
			pool = append(pool, a)
		}
	}
	g.mu.Unlock()

	var resp *http.Response
	for i, acct := range pool { // REQ-POOL-06: failover only before first byte
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, acct.baseURL+"/v1/messages", bytes.NewReader(body))
		for k, v := range r.Header {
			if k != "Authorization" && k != "X-Api-Key" {
				req.Header[k] = v
			}
		}
		req.Header.Set("Authorization", "Bearer "+acct.token)
		resp, err = http.DefaultClient.Do(req)
		if err == nil && (resp.StatusCode == 429 || resp.StatusCode >= 500) && i+1 < len(pool) {
			resp.Body.Close()
			continue
		}
		break
	}
	if err != nil || resp == nil {
		refError(w, 503, "overloaded_error", "upstream pool exhausted")
		return
	}
	defer resp.Body.Close()

	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(resp.StatusCode)
	fl := w.(http.Flusher)
	fl.Flush()

	var seen bytes.Buffer // reference only: kept to read usage at the end
	buf := make([]byte, 32*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return // client gone; r.Context() cancels upstream
			}
			fl.Flush()
			seen.Write(buf[:n])
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if r.Context().Err() == nil {
				io.WriteString(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"upstream connection lost\"}}\n\n")
				fl.Flush()
			}
			break
		}
	}
	g.record(user, upstreamModel, seen.Bytes())
}

func (g *refGateway) record(user, model string, stream []byte) {
	evs, _ := sse.ReadAll(bytes.NewReader(stream))
	var in, out int64
	for _, ev := range evs {
		var v struct {
			Message struct {
				Usage struct {
					InputTokens int64 `json:"input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage struct {
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		json.Unmarshal(ev.Data, &v)
		switch ev.Name {
		case "message_start":
			in += v.Message.Usage.InputTokens
		case "message_delta":
			out = v.Usage.OutputTokens // cumulative per Anthropic semantics
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	p := g.prices[model]
	u := g.usage[user]
	u.InputTokens += in
	u.OutputTokens += out
	u.CostUSD += (float64(in)*p.Input + float64(out)*p.Output) / 1e6
	g.usage[user] = u
}
