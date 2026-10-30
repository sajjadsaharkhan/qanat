package control

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"qanat/internal/core/secretbox"
	"qanat/internal/proxy"
	"qanat/internal/testkit/proxytest"
)

// PRD §8: proxies are managed entities: CRUD with a sealed password, one
// global default, per-account assignment, refusal to delete one in use, and
// the Test button (REQ-PROXY-05).
func TestProxies_ManageAssignAndTest(t *testing.T) {
	h := newHarness(t)
	k := make([]byte, 32)
	rand.Read(k)
	h.s.opts.Box, _ = secretbox.New(base64.StdEncoding.EncodeToString(k))

	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("198.51.100.4")) }))
	defer echo.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) }))
	defer up.Close()
	h.s.opts.ProxyIPEchoURL = echo.URL
	h.s.opts.ProxyProbeTargets = []proxy.Target{{Provider: "claude", URL: up.URL + "/"}}
	tp := proxytest.New(t, "qanat", "hunter2")
	host, port := tp.HostPort()

	root := h.signInRoot()
	for _, bad := range []string{
		`{"type":"ftp","host":"h","port":1}`,
		`{"type":"http","host":"http://h","port":1}`,
		`{"type":"http","host":"h","port":70000}`,
	} {
		if st := h.do(root, "POST", "/api/admin/proxies", bad, nil); st != 400 {
			t.Errorf("%s: %d", bad, st)
		}
	}

	var created struct{ ID string }
	body := `{"label":"office","type":"http","host":"` + host + `","port":` + strconv.Itoa(port) + `,"username":"qanat","password":"hunter2","is_default":true}`
	if st := h.do(root, "POST", "/api/admin/proxies", body, &created); st != 201 {
		t.Fatalf("create: %d", st)
	}
	var second struct{ ID string }
	h.do(root, "POST", "/api/admin/proxies", `{"type":"socks5h","host":"10.0.0.9","port":1080,"is_default":true}`, &second)

	var list []proxyResponse
	h.do(root, "GET", "/api/admin/proxies", "", &list)
	if len(list) != 2 || list[0].IsDefault || !list[1].IsDefault || !list[0].HasPassword || list[1].Label != "10.0.0.9:1080" {
		t.Fatalf("one default at a time, password kept: %+v", list)
	}
	if raw := h.rawGet(root, "/api/admin/proxies"); strings.Contains(raw, "hunter2") {
		t.Fatal("password leaked in the list")
	}
	var sealed []byte
	h.s.pg.QueryRow(t.Context(), `SELECT password FROM proxies WHERE id = $1`, created.ID).Scan(&sealed)
	if len(sealed) == 0 || bytes.Contains(sealed, []byte("hunter2")) {
		t.Fatal("password not sealed at rest")
	}

	// REQ-PROXY-05: Test goes through the proxy with the stored password.
	var res proxy.Result
	if st := h.do(root, "POST", "/api/admin/proxies/"+created.ID+"/test", "", &res); st != 200 {
		t.Fatalf("test: %d", st)
	}
	if res.EgressIP != "198.51.100.4" || !res.Providers[0].OK || tp.Hits() != 2 {
		t.Fatalf("test result %+v, proxy hits %d", res, tp.Hits())
	}

	// Edit without a password keeps it; Test still authenticates.
	update := `{"label":"office 2","type":"http","host":"` + host + `","port":` + strconv.Itoa(port) + `,"username":"qanat","password":null}`
	if st := h.do(root, "PUT", "/api/admin/proxies/"+created.ID, update, nil); st != 204 {
		t.Fatalf("update: %d", st)
	}
	h.do(root, "POST", "/api/admin/proxies/"+created.ID+"/test", "", &res)
	if !res.Providers[0].OK {
		t.Fatalf("password lost on edit: %+v", res)
	}

	// Accounts: assign on create, change later, reject unknown proxies.
	var acct struct{ ID string }
	if st := h.do(root, "POST", "/api/admin/accounts", `{"provider":"claude","label":"a","proxy":"`+created.ID+`"}`, &acct); st != 201 {
		t.Fatalf("create account: %d", st)
	}
	if st := h.do(root, "POST", "/api/admin/accounts", `{"provider":"claude","proxy":"00000000-0000-0000-0000-000000000000"}`, nil); st != 400 {
		t.Fatalf("unknown proxy: %d", st)
	}
	var accounts []accountResponse
	h.do(root, "GET", "/api/admin/accounts", "", &accounts)
	if accounts[0].Proxy != created.ID {
		t.Fatalf("account proxy: %q", accounts[0].Proxy)
	}

	// REQ-PROXY-01: deleting a proxy in use is refused, naming the accounts.
	var apiErr apiError
	if st := h.do(root, "DELETE", "/api/admin/proxies/"+created.ID, "", &apiErr); st != 409 || !strings.Contains(apiErr.Error.Message, "(a)") {
		t.Fatalf("delete in use: %d %+v", st, apiErr)
	}
	if st := h.do(root, "PATCH", "/api/admin/accounts/"+acct.ID, `{"proxy":"direct"}`, nil); st != 204 {
		t.Fatalf("set direct: %d", st)
	}
	h.do(root, "GET", "/api/admin/accounts", "", &accounts)
	if accounts[0].Proxy != "direct" {
		t.Fatalf("account proxy: %q", accounts[0].Proxy)
	}
	if st := h.do(root, "DELETE", "/api/admin/proxies/"+created.ID, "", nil); st != 204 {
		t.Fatalf("delete: %d", st)
	}
}

// §8.1/§8.2: proxy groups (failover order kept, members validated, refusal
// to delete in use), provider-level settings, and gateway health in the list.
func TestProxyGroupsAndProviderProxies(t *testing.T) {
	h := newHarness(t)
	root := h.signInRoot()
	var p1, p2 struct{ ID string }
	h.do(root, "POST", "/api/admin/proxies", `{"type":"http","host":"10.0.0.1","port":3128}`, &p1)
	h.do(root, "POST", "/api/admin/proxies", `{"type":"socks5h","host":"10.0.0.2","port":1080}`, &p2)

	for _, bad := range []string{
		`{"label":"g","policy":"random","members":["` + p1.ID + `"]}`,
		`{"label":"g","policy":"failover","members":[]}`,
		`{"label":"g","policy":"failover","members":["` + p1.ID + `","` + p1.ID + `"]}`,
		`{"label":"g","policy":"failover","members":["00000000-0000-0000-0000-000000000000"]}`,
		`{"label":"g","policy":"failover","members":["nope"]}`,
	} {
		if st := h.do(root, "POST", "/api/admin/proxy-groups", bad, nil); st != 400 {
			t.Errorf("%s: %d", bad, st)
		}
	}
	var g struct{ ID string }
	if st := h.do(root, "POST", "/api/admin/proxy-groups", `{"label":"pool","policy":"failover","members":["`+p2.ID+`","`+p1.ID+`"]}`, &g); st != 201 {
		t.Fatalf("create group: %d", st)
	}
	var groups []proxyGroupResponse
	h.do(root, "GET", "/api/admin/proxy-groups", "", &groups)
	if len(groups) != 1 || groups[0].Policy != "failover" || len(groups[0].Members) != 2 || groups[0].Members[0] != p2.ID {
		t.Fatalf("groups: %+v", groups)
	}
	if st := h.do(root, "PUT", "/api/admin/proxy-groups/"+g.ID, `{"label":"pool","policy":"sticky","members":["`+p1.ID+`"]}`, nil); st != 204 {
		t.Fatalf("update group: %d", st)
	}

	// Accounts and providers point at the group with "group:<id>".
	var acct struct{ ID string }
	if st := h.do(root, "POST", "/api/admin/accounts", `{"provider":"claude","label":"acct","proxy":"group:`+g.ID+`"}`, &acct); st != 201 {
		t.Fatalf("account on group: %d", st)
	}
	if st := h.do(root, "PUT", "/api/admin/provider-proxies/codex", `{"proxy":"group:`+g.ID+`"}`, nil); st != 204 {
		t.Fatalf("provider on group: %d", st)
	}
	if st := h.do(root, "PUT", "/api/admin/provider-proxies/claude", `{"proxy":"direct"}`, nil); st != 204 {
		t.Fatalf("provider direct: %d", st)
	}
	if st := h.do(root, "PUT", "/api/admin/provider-proxies/nope", `{"proxy":"direct"}`, nil); st != 404 {
		t.Fatalf("unknown provider: %d", st)
	}
	if st := h.do(root, "PUT", "/api/admin/provider-proxies/claude", `{"proxy":"group:00000000-0000-0000-0000-000000000000"}`, nil); st != 400 {
		t.Fatalf("unknown group: %d", st)
	}
	var pps []providerProxyResponse
	h.do(root, "GET", "/api/admin/provider-proxies", "", &pps)
	if len(pps) != 3 || pps[0].Proxy != "direct" || pps[1].Proxy != "group:"+g.ID || pps[2].Proxy != "inherit" {
		t.Fatalf("provider proxies: %+v", pps)
	}

	// In use by an account and a provider: deleting the group or its member
	// is refused and names them.
	var apiErr apiError
	if st := h.do(root, "DELETE", "/api/admin/proxy-groups/"+g.ID, "", &apiErr); st != 409 || !strings.Contains(apiErr.Error.Message, "acct") || !strings.Contains(apiErr.Error.Message, "codex") {
		t.Fatalf("delete group in use: %d %+v", st, apiErr)
	}
	if st := h.do(root, "DELETE", "/api/admin/proxies/"+p1.ID, "", &apiErr); st != 409 || !strings.Contains(apiErr.Error.Message, "group pool") {
		t.Fatalf("delete member: %d %+v", st, apiErr)
	}
	h.do(root, "PATCH", "/api/admin/accounts/"+acct.ID, `{"proxy":"inherit"}`, nil)
	h.do(root, "PUT", "/api/admin/provider-proxies/codex", `{"proxy":"inherit"}`, nil)
	if st := h.do(root, "DELETE", "/api/admin/proxy-groups/"+g.ID, "", nil); st != 204 {
		t.Fatalf("delete group: %d", st)
	}

	// REQ-PROXY-03: the list carries what the gateways reported.
	b, _ := json.Marshal(proxy.CheckResult{ProxyID: p1.ID, Origin: "https://api.anthropic.com", Healthy: true, OK: true, LatencyMS: 42})
	h.s.rdb.HSet(t.Context(), proxy.HealthKey(p1.ID), "https://api.anthropic.com", b)
	t.Cleanup(func() { h.s.rdb.Del(context.Background(), proxy.HealthKey(p1.ID)) })
	var list []proxyResponse
	h.do(root, "GET", "/api/admin/proxies", "", &list)
	if len(list[0].Health) != 1 || list[0].Health[0].LatencyMS != 42 || len(list[1].Health) != 0 {
		t.Fatalf("health: %+v", list)
	}
}
