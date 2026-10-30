package control

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"qanat/internal/core/apikey"
	"qanat/internal/core/quota"
	"qanat/internal/platform"
	"qanat/internal/store/storetest"
	"qanat/internal/testkit/itenv"
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
