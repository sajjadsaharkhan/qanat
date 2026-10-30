package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"qanat/internal/core/apikey"
	"qanat/internal/core/quota"
	"qanat/internal/core/secretbox"
	"qanat/internal/platform"
	"qanat/internal/store/db"
	"qanat/internal/store/storetest"
	"qanat/internal/testkit/itenv"
	"qanat/internal/testkit/oidctest"
)

const rootPW = "correct horse battery staple"

type harness struct {
	t   *testing.T
	s   *Server
	srv *httptest.Server
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pg := storetest.DB(t)
	rdb := platform.Redis(itenv.Redis(t))
	t.Cleanup(func() { rdb.Close() })
	rdb.Del(context.Background(), "qanat:login:fail:root")

	s := New(pg, rdb, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{RootPassword: rootPW, GatewayURL: "http://gw.test"})
	if err := s.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &harness{t, s, srv}
}

// client is a browser: it keeps cookies.
func (h *harness) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 5 * time.Second}
}

func (h *harness) do(c *http.Client, method, path, body string, out any) int {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if method != http.MethodGet {
		req.Header.Set(csrfHeader, "1")
	}
	resp, err := c.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			h.t.Fatalf("%s %s: %v: %s", method, path, err, raw)
		}
	}
	return resp.StatusCode
}

func (h *harness) signInRoot() *http.Client {
	h.t.Helper()
	c := h.client()
	if st := h.do(c, "POST", "/api/auth/login", `{"username":"root","password":"`+rootPW+`"}`, nil); st != 204 {
		h.t.Fatalf("root login: %d", st)
	}
	return c
}

// D23: root signs in with a password; wrong credentials and missing CSRF
// header are refused; sessions end on logout.
func TestAuth_RootPasswordLogin(t *testing.T) {
	h := newHarness(t)
	c := h.client()

	var methods authMethods
	h.do(c, "GET", "/api/auth/methods", "", &methods)
	if !methods.Password {
		t.Fatal("password login should be enabled")
	}
	if st := h.do(c, "GET", "/api/me", "", nil); st != 401 {
		t.Fatalf("anonymous /me: %d", st)
	}
	if st := h.do(c, "POST", "/api/auth/login", `{"username":"root","password":"nope"}`, nil); st != 401 {
		t.Fatalf("wrong password: %d", st)
	}
	if st := h.do(c, "POST", "/api/auth/login", `{"username":"admin","password":"`+rootPW+`"}`, nil); st != 401 {
		t.Fatalf("wrong username: %d", st)
	}

	req, _ := http.NewRequest("POST", h.srv.URL+"/api/auth/login", strings.NewReader(`{"username":"root","password":"`+rootPW+`"}`))
	if resp, _ := c.Do(req); resp.StatusCode != 403 {
		t.Fatalf("login without %s: %d", csrfHeader, resp.StatusCode)
	}

	c = h.signInRoot()
	var me meResponse
	if st := h.do(c, "GET", "/api/me", "", &me); st != 200 || me.Role != "admin" || me.Username == nil || *me.Username != "root" {
		t.Fatalf("me: %d %+v", st, me)
	}
	h.do(c, "POST", "/api/auth/logout", "", nil)
	if st := h.do(c, "GET", "/api/me", "", nil); st != 401 {
		t.Fatalf("after logout: %d", st)
	}
}

func TestAuth_RootLoginThrottled(t *testing.T) {
	h := newHarness(t)
	c := h.client()
	for range loginMaxFailures {
		h.do(c, "POST", "/api/auth/login", `{"username":"root","password":"nope"}`, nil)
	}
	if st := h.do(c, "POST", "/api/auth/login", `{"username":"root","password":"`+rootPW+`"}`, nil); st != 429 {
		t.Fatalf("want 429 after %d failures, got %d", loginMaxFailures, st)
	}
}

// §13.2: the full key is returned once; only the hash is stored.
func TestKeys_ShownOnceAndRevocable(t *testing.T) {
	h := newHarness(t)
	c := h.signInRoot()

	var created createKeyResponse
	if st := h.do(c, "POST", "/api/me/keys", `{"name":"laptop","expires_in_days":30}`, &created); st != 201 {
		t.Fatalf("create: %d", st)
	}
	if !apikey.LooksValid(created.Key) || !strings.HasPrefix(created.Key, created.Prefix) || created.ExpiresAt == nil {
		t.Fatalf("created %+v", created)
	}
	var stored []byte
	if err := h.s.pg.QueryRow(context.Background(), `SELECT hash FROM api_keys WHERE id = $1`, created.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(apikey.Hash(created.Key)) {
		t.Fatal("stored value is not the key hash")
	}

	raw := h.rawGet(c, "/api/me/keys")
	if strings.Contains(raw, created.Key) {
		t.Fatal("list must never return the full key")
	}

	if st := h.do(c, "DELETE", "/api/me/keys/"+created.ID, "", nil); st != 204 {
		t.Fatalf("revoke: %d", st)
	}
	if st := h.do(c, "DELETE", "/api/me/keys/"+created.ID, "", nil); st != 404 {
		t.Fatalf("second revoke: %d", st)
	}
	if st := h.do(c, "DELETE", "/api/me/keys/not-a-uuid", "", nil); st != 404 {
		t.Fatalf("bad id: %d", st)
	}
	if st := h.do(c, "POST", "/api/me/keys", `{"name":"  "}`, nil); st != 400 {
		t.Fatalf("blank name: %d", st)
	}
}

func (h *harness) rawGet(c *http.Client, path string) string {
	resp, err := c.Get(h.srv.URL + path)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// REQ-PLAN-02/03, REQ-USER-02, §10.3: Plans carry quotas and resets; the
// default Plan keeps zero quota; assigning a Plan shows its windows and is
// audited.
func TestPlans_CreateAssignAndQuotaWindows(t *testing.T) {
	h := newHarness(t)
	c := h.signInRoot()

	var plans []planResponse
	h.do(c, "GET", "/api/admin/plans", "", &plans)
	if len(plans) != 1 || !plans[0].IsDefault || len(plans[0].Quotas) != 0 {
		t.Fatalf("seed plans %+v", plans)
	}
	def := plans[0]
	body := `{"name":"Default","timezone":"Asia/Tehran","daily_anchor":"00:00","weekly_anchor":6,"monthly_anchor":1,
		"quotas":[{"window":"daily","model_scope":null,"limit_usd":5}],"limits":{"rpm":0,"max_concurrent":0},"models":[]}`
	if st := h.do(c, "PUT", "/api/admin/plans/"+def.ID, body, nil); st != 400 {
		t.Fatalf("quota on default Plan: %d", st)
	}

	var team planResponse
	body = `{"name":"Team","timezone":"Asia/Tehran","daily_anchor":"00:00","weekly_anchor":6,"monthly_anchor":1,
		"quotas":[{"window":"daily","model_scope":null,"limit_usd":10},{"window":"rolling_5h","model_scope":null,"limit_usd":4},
		          {"window":"daily","model_scope":"opus","limit_usd":5}],
		"limits":{"rpm":60,"max_concurrent":4},"models":["sonnet","opus","sonnet"]}`
	if st := h.do(c, "POST", "/api/admin/plans", body, &team); st != 201 {
		t.Fatalf("create: %d", st)
	}
	if len(team.Quotas) != 3 || team.Limits.RPM != 60 || len(team.Models) != 2 {
		t.Fatalf("team %+v", team)
	}
	if st := h.do(c, "POST", "/api/admin/plans", body, nil); st != 409 {
		t.Fatalf("duplicate name: %d", st)
	}

	var me meResponse
	h.do(c, "GET", "/api/me", "", &me)
	if st := h.do(c, "PATCH", "/api/admin/users/"+me.ID, `{"plan_id":"`+team.ID+`"}`, nil); st != 204 {
		t.Fatalf("assign: %d", st)
	}
	if st := h.do(c, "DELETE", "/api/admin/plans/"+team.ID, "", nil); st != 409 {
		t.Fatalf("delete in-use plan: %d", st)
	}

	// Spend $2.50 today, as the gateway would record it.
	a := quota.DefaultAnchors()
	start, reset := quota.Bounds(quota.Daily, a, time.Now())
	h.s.rdb.Set(context.Background(), quota.FixedKey(me.ID, quota.Daily, "", start), quota.ToMicro(2.5), time.Minute)

	var q quotasResponse
	h.do(c, "GET", "/api/me/quotas", "", &q)
	if len(q.Windows) != 3 {
		t.Fatalf("windows %+v", q)
	}
	var daily *quotaWindow
	for i := range q.Windows {
		if q.Windows[i].Window == quota.Daily && q.Windows[i].ModelScope == nil {
			daily = &q.Windows[i]
		}
	}
	if daily == nil || daily.UsedUSD != 2.5 || daily.RemainingUSD != 7.5 || daily.ResetsAt == nil || !daily.ResetsAt.Equal(reset) {
		t.Fatalf("daily window %+v (want reset %v)", daily, reset)
	}
	if q.Windows[0].Window != quota.Rolling5h || q.Windows[0].ResetsAt != nil {
		t.Fatalf("rolling window first, without a reset instant: %+v", q.Windows[0])
	}

	var audit []auditEntry
	h.do(c, "GET", "/api/admin/audit", "", &audit)
	var actions []string
	for _, e := range audit {
		actions = append(actions, e.Action)
	}
	if got := strings.Join(actions, ","); !strings.Contains(got, "user.plan") || !strings.Contains(got, "plan.create") {
		t.Fatalf("audit actions %s", got)
	}
}

// Roles live in Qanat (D10); admin pages need role admin.
func TestAdmin_RegularUserIsForbidden(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	var id string
	if err := h.s.pg.QueryRow(ctx, `INSERT INTO users (email, plan_id) SELECT 'dev@example.com', id FROM plans WHERE is_default RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := h.s.startSession(ctx, rec, id); err != nil {
		t.Fatal(err)
	}
	c := h.client()
	req, _ := http.NewRequest("GET", h.srv.URL+"/api/admin/users", nil)
	req.Header.Set("Cookie", rec.Header().Get("Set-Cookie"))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("user on admin API: %d", resp.StatusCode)
	}

	root := h.signInRoot()
	var me meResponse
	h.do(root, "GET", "/api/me", "", &me)
	if st := h.do(root, "PATCH", "/api/admin/users/"+me.ID, `{"role":"user"}`, nil); st != 400 {
		t.Fatalf("demoting yourself: %d", st)
	}
	if st := h.do(root, "PATCH", "/api/admin/users/"+id, `{"role":"admin"}`, nil); st != 204 {
		t.Fatalf("promote: %d", st)
	}
}

func TestOIDCLogin_Flow(t *testing.T) {
	pg := storetest.DB(t)
	rdb := platform.Redis(itenv.Redis(t))
	t.Cleanup(func() { rdb.Close() })

	idp := oidctest.New(t)

	var srv *httptest.Server
	s := New(pg, rdb, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		OIDCIssuer:          idp.IssuerURL(),
		OIDCClientID:        idp.ClientID,
		BootstrapAdminEmail: "bootstrap-admin@example.com",
		GatewayURL:          "http://gw.test",
	})
	mux := http.NewServeMux()
	s.Register(mux)
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	s.opts.WebURL = srv.URL

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if strings.HasSuffix(req.URL.Path, "/access-denied") || req.URL.Path == "/" {
				return http.ErrUseLastResponse
			}
			return nil
		},
		Timeout: 5 * time.Second,
	}

	// 1. User outside qanat-users is rejected (REQ-AUTH-02)
	idp.LoginAs(oidctest.Claims{
		Subject: "outsider-1",
		Email:   "outsider@example.com",
		Name:    "Outsider",
		Groups:  []string{"other-team"},
	})
	resp, err := client.Get(srv.URL + "/api/auth/oidc/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/access-denied" {
		t.Fatalf("outsider redirect: status=%d, location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// 2. Regular user in qanat-users is JIT-provisioned as "user" (REQ-AUTH-03)
	jar, _ = cookiejar.New(nil)
	client.Jar = jar
	idp.LoginAs(oidctest.Claims{
		Subject: "dev-sub-1",
		Email:   "dev@example.com",
		Name:    "Dev User",
		Groups:  []string{"qanat-users"},
	})
	resp, err = client.Get(srv.URL + "/api/auth/oidc/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/" {
		t.Fatalf("dev redirect: status=%d, location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Session is set and /api/me works
	req, _ := http.NewRequest("GET", srv.URL+"/api/me", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var me meResponse
	json.NewDecoder(resp.Body).Decode(&me)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/me status=%d", resp.StatusCode)
	}
	if me.Email != "dev@example.com" || me.Role != "user" {
		t.Fatalf("dev /api/me: %+v", me)
	}

	// 3. User matching BootstrapAdminEmail is JIT-provisioned as "admin"
	jar, _ = cookiejar.New(nil)
	client.Jar = jar
	idp.LoginAs(oidctest.Claims{
		Subject: "admin-sub-1",
		Email:   "bootstrap-admin@example.com",
		Name:    "Bootstrap Admin",
		Groups:  []string{"qanat-users"},
	})
	resp, err = client.Get(srv.URL + "/api/auth/oidc/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/" {
		t.Fatalf("admin redirect: status=%d, location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}

	req, _ = http.NewRequest("GET", srv.URL+"/api/me", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(resp.Body).Decode(&me)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/me status=%d", resp.StatusCode)
	}
	if me.Email != "bootstrap-admin@example.com" || me.Role != "admin" {
		t.Fatalf("admin /api/me: %+v", me)
	}
}

// REQ-MAP-04: the provider registry is listed; mapping rules for prefixed
// names are refused because the prefix routes on its own.
func TestMappings_ProvidersAndPrefixedPatterns(t *testing.T) {
	h := newHarness(t)
	c := h.signInRoot()

	var ps []providerInfo
	if st := h.do(c, "GET", "/api/admin/providers", "", &ps); st != 200 || len(ps) != 3 || ps[0].Prefix != "cc" || ps[1].Prefix != "cx" || ps[2].Prefix != "ag" {
		t.Fatalf("providers: %d %+v", st, ps)
	}
	if st := h.do(c, "PUT", "/api/admin/mappings", `[{"pattern":"cc/opus","provider":"claude","upstream_model":"claude-opus-4-7","priority":0}]`, nil); st != 400 {
		t.Fatalf("prefixed pattern: %d", st)
	}
	if st := h.do(c, "PUT", "/api/admin/mappings", `[{"pattern":"opus","provider":"claude","upstream_model":"claude-opus-4-7","priority":0}]`, nil); st != 204 {
		t.Fatalf("bare alias: %d", st)
	}
	var rules []mappingRule
	if h.do(c, "GET", "/api/admin/mappings", "", &rules); len(rules) != 1 || rules[0].Pattern != "opus" {
		t.Fatalf("rules: %+v", rules)
	}
}

// The account section (not-done G1 rewrite): credential kinds with sealing, a
// partial PATCH, the Test button against a stub upstream, and pool reordering.
func TestAccounts_KindsPatchTestMove(t *testing.T) {
	h := newHarness(t)
	box, err := secretbox.New(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	h.s.opts.Box = box
	c := h.signInRoot()

	// Codex and Antigravity take API keys only; a pasted token is Claude-only.
	if st := h.do(c, "POST", "/api/admin/accounts", `{"provider":"codex","label":"cx","auth_kind":"token","access_token":"t"}`, nil); st != 400 {
		t.Fatalf("codex token kind: %d", st)
	}
	var keyAcct, tokAcct struct {
		ID string `json:"id"`
	}
	if st := h.do(c, "POST", "/api/admin/accounts", `{"provider":"codex","label":"cx key","auth_kind":"api_key","access_token":"sk-cx","proxy":"direct"}`, &keyAcct); st != 201 {
		t.Fatalf("codex api_key: %d", st)
	}
	if st := h.do(c, "POST", "/api/admin/accounts", `{"provider":"claude","label":"cc token","access_token":"tok-1","expires_at":"2027-06-01T00:00:00Z","proxy":"direct"}`, &tokAcct); st != 201 {
		t.Fatalf("claude token default kind: %d", st)
	}

	var accounts []accountResponse
	if st := h.do(c, "GET", "/api/admin/accounts", "", &accounts); st != 200 {
		t.Fatalf("list: %d", st)
	}
	if len(accounts) != 2 || accounts[0].ID != keyAcct.ID || accounts[0].Position != 1 || accounts[1].Position != 2 {
		t.Fatalf("list order/positions: %+v", accounts)
	}
	if accounts[0].AuthKind != "api_key" || accounts[1].AuthKind != "token" || accounts[1].ExpiresAt == nil {
		t.Fatalf("auth kinds/expiry: %+v", accounts)
	}

	// Partial PATCH: rename and disable in one call; nothing else moves.
	if st := h.do(c, "PATCH", "/api/admin/accounts/"+keyAcct.ID, `{"label":"renamed","disabled":true}`, nil); st != 204 {
		t.Fatalf("patch: %d", st)
	}
	accounts = nil
	h.do(c, "GET", "/api/admin/accounts", "", &accounts)
	if accounts[0].Label != "renamed" || accounts[0].DisabledAt == nil || accounts[0].Proxy != "direct" || accounts[0].AuthKind != "api_key" {
		t.Fatalf("after patch: %+v", accounts[0])
	}

	// Test button: the stub upstream checks the header shape per auth kind.
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/messages" && r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") == "Bearer tok-1" || r.Header.Get("X-Api-Key") != "" {
			w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"invalid key"}}`))
	}))
	defer up.Close()
	if st := h.do(c, "PATCH", "/api/admin/accounts/"+tokAcct.ID, `{"base_url":"`+up.URL+`"}`, nil); st != 204 {
		t.Fatalf("set base url: %d", st)
	}
	if st := h.do(c, "PATCH", "/api/admin/accounts/"+keyAcct.ID, `{"base_url":"`+up.URL+`"}`, nil); st != 204 {
		t.Fatalf("set base url: %d", st)
	}
	var testRes accountTestResponse
	if st := h.do(c, "POST", "/api/admin/accounts/"+tokAcct.ID+"/test", "", &testRes); st != 200 || !testRes.OK || testRes.Status != 200 {
		t.Fatalf("token test: %d %+v", st, testRes)
	}
	// A bad key still answers 200, with ok=false and the provider's message.
	var badRes accountTestResponse
	if st := h.do(c, "POST", "/api/admin/accounts/"+keyAcct.ID+"/test", "", &badRes); st != 200 || badRes.OK || badRes.Error != "401: invalid key" {
		t.Fatalf("bad key test: %d %+v", st, badRes)
	}
	accounts = nil
	h.do(c, "GET", "/api/admin/accounts", "", &accounts)
	if accounts[1].LastCheckOk == nil || !*accounts[1].LastCheckOk || accounts[1].LastCheckError != nil {
		t.Fatalf("check recorded: %+v", accounts[1])
	}

	// Move: per-provider pool order, so two Claude accounts are needed.
	var key2 struct {
		ID string `json:"id"`
	}
	if st := h.do(c, "POST", "/api/admin/accounts", `{"provider":"claude","label":"cc key2","auth_kind":"api_key","access_token":"sk-2","proxy":"direct"}`, &key2); st != 201 {
		t.Fatalf("second claude account: %d", st)
	}
	idx := func(id string) int {
		accounts = nil
		h.do(c, "GET", "/api/admin/accounts", "", &accounts)
		for i, a := range accounts {
			if a.ID == id {
				return i
			}
		}
		return -1
	}
	if st := h.do(c, "POST", "/api/admin/accounts/"+tokAcct.ID+"/move", `{"direction":"up"}`, nil); st != 409 {
		t.Fatalf("move at top of its provider: %d", st)
	}
	if st := h.do(c, "POST", "/api/admin/accounts/"+tokAcct.ID+"/move", `{"direction":"down"}`, nil); st != 204 {
		t.Fatalf("move down: %d", st)
	}
	if !(idx(key2.ID) < idx(tokAcct.ID)) {
		t.Fatalf("after move, key2 should come first")
	}
	if st := h.do(c, "POST", "/api/admin/accounts/"+tokAcct.ID+"/move", `{"direction":"down"}`, nil); st != 409 {
		t.Fatalf("move at bottom: %d", st)
	}
}

// The Setup page lists what /v1/models would for one of the caller's own
// keys; other users' keys and malformed keys read as invalid.
func TestSetupModels_OwnKeyOnly(t *testing.T) {
	h := newHarness(t)
	c := h.signInRoot()
	if st := h.do(c, "PUT", "/api/admin/mappings", `[{"pattern":"opus","provider":"claude","upstream_model":"claude-opus-4-7","priority":0}]`, nil); st != 204 {
		t.Fatalf("mapping: %d", st)
	}
	var created createKeyResponse
	if st := h.do(c, "POST", "/api/me/keys", `{"name":"laptop"}`, &created); st != 201 {
		t.Fatalf("create: %d", st)
	}
	var out setupModelsResponse
	if st := h.do(c, "POST", "/api/me/setup/models", `{"key":"`+created.Key+`"}`, &out); st != 200 || !slices.Contains(out.Models, "opus") {
		t.Fatalf("own key: %d %+v", st, out)
	}
	if st := h.do(c, "POST", "/api/me/setup/models", `{"key":"qnt_live_nope"}`, nil); st != 400 {
		t.Fatalf("malformed key: %d", st)
	}

	ctx := context.Background()
	var other string
	if err := h.s.pg.QueryRow(ctx, `INSERT INTO users (email, plan_id) SELECT 'dev@example.com', id FROM plans WHERE is_default RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	otherKey, display, hash := apikey.New()
	if _, err := h.s.q.CreateKey(ctx, db.CreateKeyParams{UserID: other, Name: "theirs", Hash: hash, Prefix: display}); err != nil {
		t.Fatal(err)
	}
	if st := h.do(c, "POST", "/api/me/setup/models", `{"key":"`+otherKey+`"}`, nil); st != 400 {
		t.Fatalf("someone else's key: %d", st)
	}
}
