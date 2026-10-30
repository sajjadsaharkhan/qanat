package gateway

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"qanat/internal/core/secretbox"
	"qanat/internal/proxy"
	"qanat/internal/store/storetest"
	"qanat/internal/testkit/proxytest"
)

func newTestGateway() *Gateway {
	return &Gateway{proxies: proxy.NewPool(), cooldown: map[string]time.Time{}, sticky: map[string]string{}}
}

// §8.2: an account's upstream traffic goes through its proxy; others go direct.
func TestSend_UsesAccountProxy(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") }))
	defer up.Close()
	p := proxytest.New(t, "u", "pw")
	host, port := p.HostPort()
	g := newTestGateway()

	viaProxy := &Account{ID: "a1", BaseURL: up.URL}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	resp, err := g.send(r, viaProxy, &proxy.Spec{ID: "p1", Type: "http", Host: host, Port: port, Username: "u", Password: "pw"}, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if p.Hits() != 1 || resp.Header.Get("X-Via-Test-Proxy") != "1" {
		t.Fatalf("request didn't use the proxy (hits %d)", p.Hits())
	}

	resp, err = g.send(r, &Account{ID: "a2", BaseURL: up.URL}, nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if p.Hits() != 1 {
		t.Fatal("a direct account went through the proxy")
	}
}

// REQ-PROXY-01/03: an account with no usable proxy sits out instead of
// silently going out from another IP; so does one whose only proxy is
// unhealthy.
func TestCandidates_SkipBlockedOrUnhealthyProxy(t *testing.T) {
	sick := &proxy.Spec{ID: "sick"}
	s := &Snapshot{Gen: 1, accounts: map[string][]*Account{"claude": {
		{ID: "blocked", State: "healthy"},
		{ID: "unhealthy", State: "healthy", Origin: "https://up", Proxies: []*proxy.Spec{sick}},
		{ID: "failover", State: "healthy", Origin: "https://up", Proxies: []*proxy.Spec{sick, {ID: "ok"}}},
		{ID: "direct", State: "healthy", Origin: "https://up", Proxies: []*proxy.Spec{nil}},
	}}}
	g := newTestGateway()
	g.health = proxy.NewHealth()
	g.health.FailAfter = 1
	g.health.Timeout = time.Second
	g.health.Round(t.Context(), []proxy.Check{{Spec: &proxy.Spec{ID: "sick", Type: "http", Host: "127.0.0.1", Port: 1}, Origin: "https://up"}})

	var ids []string
	for _, a := range g.candidates(s, "u1", "claude") {
		ids = append(ids, a.ID)
	}
	if len(ids) != 2 || ids[0] != "failover" || ids[1] != "direct" {
		t.Fatalf("candidates: %v", ids)
	}
	if r := g.routes(s.accounts["claude"][2]); len(r) != 1 || r[0].ID != "ok" {
		t.Fatalf("failover must skip the unhealthy member: %v", r)
	}
}

// REQ-PROXY-03: when a failover group's first member can't connect, the next
// member carries the request before any other account is tried.
func TestForward_FailoverToNextMember(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"usage":{}}`)
	}))
	defer up.Close()
	tp := proxytest.New(t, "u", "pw")
	host, port := tp.HostPort()
	dead := &proxy.Spec{ID: "dead", Type: "http", Host: "127.0.0.1", Port: 1}
	live := &proxy.Spec{ID: "live", Type: "http", Host: host, Port: port, Username: "u", Password: "pw"}
	s := &Snapshot{Gen: 1, accounts: map[string][]*Account{"claude": {
		{ID: "a1", Provider: "claude", State: "healthy", BaseURL: up.URL, Origin: up.URL, Proxies: []*proxy.Spec{dead, live}},
	}}, sticky: map[string]string{}}
	g := newTestGateway()
	g.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	rt := &route{caller: &caller{snap: s, user: &User{ID: "u1"}, key: &Key{}, plan: &Plan{}}, name: "m", body: []byte(`{}`),
		members: []Member{{Provider: "claude", Model: "m"}}}
	g.sticky["u1|claude"] = "a1" // no persistence in this test

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	g.forward(w, r, rt, forwardOpts{reqID: "r", start: time.Now(), remaining: -1})
	if w.Code != 200 || tp.Hits() != 1 {
		t.Fatalf("status %d, live proxy hits %d: %s", w.Code, tp.Hits(), w.Body)
	}
}

// REQ-OAUTH-03: an OAuth account answering 401 has an expired or revoked
// token; the request fails over to the next account (nothing reached the
// client) and the 401'd account sits out until its refresh lands.
func TestForward_OAuth401FailsOver(t *testing.T) {
	expired := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"OAuth token has expired."}}`)
	}))
	defer expired.Close()
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"usage":{}}`)
	}))
	defer ok.Close()
	s := &Snapshot{Gen: 1, accounts: map[string][]*Account{"claude": {
		{ID: "a1", Provider: "claude", State: "healthy", AuthKind: "oauth", Token: "old", BaseURL: expired.URL, Origin: expired.URL, Proxies: []*proxy.Spec{nil}},
		{ID: "a2", Provider: "claude", State: "healthy", AuthKind: "oauth", Token: "t2", BaseURL: ok.URL, Origin: ok.URL, Proxies: []*proxy.Spec{nil}},
	}}, sticky: map[string]string{}}
	g := newTestGateway()
	g.health = proxy.NewHealth()
	g.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	rt := &route{caller: &caller{snap: s, user: &User{ID: "u1"}, key: &Key{}, plan: &Plan{}}, name: "m", body: []byte(`{}`),
		members: []Member{{Provider: "claude", Model: "m"}}}
	g.sticky["u1|claude"] = "a1" // no persistence in this test; a1 goes first

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	g.forward(w, r, rt, forwardOpts{reqID: "r", start: time.Now(), remaining: -1})
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	g.mu.Lock()
	until := g.cooldown["a1"]
	g.mu.Unlock()
	if time.Until(until) <= 0 {
		t.Fatal("the 401'd account must sit out until its refresh lands")
	}
}

// §8.2 resolution order, loaded from the real schema: own proxy, then the
// global default, then direct; "direct" overrides the default; a disabled
// proxy blocks its accounts.
func TestSnapshot_ProxyResolution(t *testing.T) {
	pg := storetest.DB(t)
	ctx := context.Background()
	k := make([]byte, 32)
	rand.Read(k)
	box, _ := secretbox.New(base64.StdEncoding.EncodeToString(k))
	sealed, _ := proxy.SealPassword(box, "00000000-0000-0000-0000-00000000000a", "s3cret")

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pg.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO proxies (id, label, type, host, port, username, password, is_default) VALUES
		('00000000-0000-0000-0000-00000000000a', 'own', 'socks5', 'own.example', 1080, 'u', $1, false),
		('00000000-0000-0000-0000-00000000000b', 'default', 'http', 'default.example', 3128, NULL, NULL, true)`, sealed)
	exec(`INSERT INTO proxies (id, label, type, host, port, enabled) VALUES
		('00000000-0000-0000-0000-00000000000c', 'off', 'http', 'off.example', 3128, false)`)
	exec(`INSERT INTO provider_accounts (id, provider, label, position, proxy_id, proxy_direct) VALUES
		('10000000-0000-0000-0000-000000000001', 'claude', 'own', 1, '00000000-0000-0000-0000-00000000000a', false),
		('10000000-0000-0000-0000-000000000002', 'claude', 'inherit', 2, NULL, false),
		('10000000-0000-0000-0000-000000000003', 'claude', 'direct', 3, NULL, true),
		('10000000-0000-0000-0000-000000000004', 'claude', 'disabled', 4, '00000000-0000-0000-0000-00000000000c', false)`)

	s, err := Load(ctx, pg, box, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]*Account{}
	for _, a := range s.accounts["claude"] {
		by[a.ID[len(a.ID)-1:]] = a
	}
	one := func(a *Account) *proxy.Spec {
		t.Helper()
		if len(a.Proxies) != 1 {
			t.Fatalf("%s: want one route, got %v", a.ID, a.Proxies)
		}
		return a.Proxies[0]
	}
	if p := one(by["1"]); p == nil || p.Host != "own.example" || p.Password != "s3cret" || p.Type != "socks5" {
		t.Fatalf("own proxy: %+v", p)
	}
	if p := one(by["2"]); p == nil || p.Host != "default.example" {
		t.Fatalf("inherit should use the default: %+v", p)
	}
	if p := one(by["3"]); p != nil {
		t.Fatalf("direct: %+v", p)
	}
	if len(by["4"].Proxies) != 0 {
		t.Fatalf("disabled proxy must block: %+v", by["4"])
	}
	if by["1"].Origin != "https://api.anthropic.com" {
		t.Fatalf("origin: %q", by["1"].Origin)
	}

	// Deleting a proxy in use is refused, so accounts never move IP silently.
	if _, err := pg.Exec(ctx, `DELETE FROM proxies WHERE id = '00000000-0000-0000-0000-00000000000a'`); err == nil {
		t.Fatal("deleting a proxy in use must fail")
	}
}

// §8.1/§8.2: provider-level settings sit between the account and the global
// default; failover groups keep priority order and drop disabled members;
// sticky groups pin each account to one member.
func TestSnapshot_GroupsAndProviderLevel(t *testing.T) {
	pg := storetest.DB(t)
	ctx := context.Background()
	exec := func(sql string) {
		t.Helper()
		if _, err := pg.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO proxies (id, label, type, host, port, enabled, is_default) VALUES
		('00000000-0000-0000-0000-00000000000a', 'a', 'http', 'a.example', 3128, true, true),
		('00000000-0000-0000-0000-00000000000b', 'b', 'http', 'b.example', 3128, true, false),
		('00000000-0000-0000-0000-00000000000c', 'c', 'http', 'c.example', 3128, false, false),
		('00000000-0000-0000-0000-00000000000d', 'd', 'http', 'd.example', 3128, true, false)`)
	exec(`INSERT INTO proxy_groups (id, label, policy) VALUES
		('20000000-0000-0000-0000-000000000001', 'fo', 'failover'),
		('20000000-0000-0000-0000-000000000002', 'st', 'sticky')`)
	exec(`INSERT INTO proxy_group_members (group_id, proxy_id, priority) VALUES
		('20000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-00000000000d', 0),
		('20000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-00000000000c', 1),
		('20000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-00000000000b', 2),
		('20000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-00000000000b', 0),
		('20000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-00000000000d', 1)`)
	exec(`INSERT INTO provider_proxies (provider, proxy_group_id) VALUES ('claude', '20000000-0000-0000-0000-000000000001')`)
	exec(`INSERT INTO provider_proxies (provider, proxy_direct) VALUES ('antigravity', true)`)
	exec(`INSERT INTO provider_accounts (id, provider, label, position, proxy_group_id) VALUES
		('10000000-0000-0000-0000-000000000001', 'claude', 'inherits provider', 5, NULL),
		('10000000-0000-0000-0000-000000000002', 'claude', 'sticky', 6, '20000000-0000-0000-0000-000000000002'),
		('10000000-0000-0000-0000-000000000003', 'codex', 'inherits default', 7, NULL),
		('10000000-0000-0000-0000-000000000004', 'antigravity', 'provider direct', 8, NULL)`)

	s, err := Load(ctx, pg, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	hosts := func(provider string, i int) []string {
		var out []string
		for _, sp := range s.accounts[provider][i].Proxies {
			if sp == nil {
				out = append(out, "direct")
			} else {
				out = append(out, sp.Host)
			}
		}
		return out
	}
	if h := hosts("claude", 0); len(h) != 2 || h[0] != "d.example" || h[1] != "b.example" {
		t.Fatalf("provider failover group, priority order, disabled member dropped: %v", h)
	}
	st := s.accounts["claude"][1]
	want := proxy.Pick(st.ID, []*proxy.Spec{{ID: "00000000-0000-0000-0000-00000000000b"}, {ID: "00000000-0000-0000-0000-00000000000d"}})
	if len(st.Proxies) != 1 || st.Proxies[0].ID != want.ID {
		t.Fatalf("sticky: %v, want %s", hosts("claude", 1), want.ID)
	}
	if h := hosts("codex", 0); len(h) != 1 || h[0] != "a.example" {
		t.Fatalf("no provider setting falls to the default: %v", h)
	}
	if h := hosts("antigravity", 0); len(h) != 1 || h[0] != "direct" {
		t.Fatalf("provider direct overrides the default: %v", h)
	}

	// A member in use can't be deleted out from under a group.
	if _, err := pg.Exec(ctx, `DELETE FROM proxies WHERE id = '00000000-0000-0000-0000-00000000000d'`); err == nil {
		t.Fatal("deleting a group member must fail")
	}
	// At most one of proxy, group, direct per account.
	if _, err := pg.Exec(ctx, `UPDATE provider_accounts SET proxy_direct = true WHERE id = '10000000-0000-0000-0000-000000000002'`); err == nil {
		t.Fatal("group and direct together must fail")
	}
}

// Accounts authenticate by kind: api_key sends x-api-key (filling in
// anthropic-version when the caller left it out), token sends Bearer. A
// disabled account never becomes a candidate.
func TestSend_AuthKindAndDisabledSkip(t *testing.T) {
	var auth, apiKey, version string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, apiKey, version = r.Header.Get("Authorization"), r.Header.Get("X-Api-Key"), r.Header.Get("anthropic-version")
		io.WriteString(w, "ok")
	}))
	defer up.Close()
	g := newTestGateway()
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	resp, err := g.send(r, &Account{ID: "a1", BaseURL: up.URL, AuthKind: "api_key", Token: "sk-1"}, nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if apiKey != "sk-1" || auth != "" || version != "2023-06-01" {
		t.Fatalf("api_key account: auth=%q x-api-key=%q version=%q", auth, apiKey, version)
	}

	resp, err = g.send(r, &Account{ID: "a2", BaseURL: up.URL, Token: "tok-1"}, nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if auth != "Bearer tok-1" || apiKey != "" {
		t.Fatalf("token account: auth=%q x-api-key=%q", auth, apiKey)
	}

	s := &Snapshot{Gen: 1, accounts: map[string][]*Account{"claude": {
		{ID: "off", Disabled: true, Proxies: []*proxy.Spec{nil}},
		{ID: "on", Proxies: []*proxy.Spec{nil}},
	}}}
	var ids []string
	for _, a := range g.candidates(s, "u1", "claude") {
		ids = append(ids, a.ID)
	}
	if len(ids) != 1 || ids[0] != "on" {
		t.Fatalf("candidates must skip disabled accounts: %v", ids)
	}
}
