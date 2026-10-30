package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"qanat/internal/testkit/proxytest"
)

func spec(p *proxytest.Proxy, pass string) *Spec {
	host, port := p.HostPort()
	return &Spec{ID: "p1", Type: "http", Host: host, Port: port, Username: p.User, Password: pass}
}

func TestPool_ReusesUntilSettingsChange(t *testing.T) {
	pool := NewPool()
	s := &Spec{ID: "p1", Type: "socks5", Host: "10.0.0.1", Port: 1080}
	a := pool.Client(s)
	if pool.Client(&Spec{ID: "p1", Type: "socks5", Host: "10.0.0.1", Port: 1080}) != a {
		t.Fatal("same settings must reuse the warm client (REQ-PROXY-02)")
	}
	if pool.Client(&Spec{ID: "p1", Type: "socks5", Host: "10.0.0.1", Port: 1081}) == a {
		t.Fatal("changed settings must get a new transport (REQ-PROXY-04)")
	}
	if pool.Client(nil) != pool.Client(nil) {
		t.Fatal("direct client must be shared")
	}
	pool.Retain(map[string]bool{})
	if len(pool.byID) != 0 {
		t.Fatal("Retain kept an unused proxy")
	}
}

func TestSpecURL_CarriesCredentials(t *testing.T) {
	u := (&Spec{Type: "socks5h", Host: "::1", Port: 1080, Username: "u", Password: "p@ss"}).URL()
	if u.String() != "socks5h://u:p%40ss@[::1]:1080" {
		t.Fatalf("got %s", u)
	}
}

// REQ-PROXY-05: the Test button reports egress IP and per-provider results,
// and both actually go through the proxy.
func TestTest_ThroughProxy(t *testing.T) {
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("203.0.113.7\n")) }))
	defer echo.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) }))
	defer up.Close()
	p := proxytest.New(t, "qanat", "secret")

	res := Test(context.Background(), spec(p, "secret"), echo.URL, []Target{{Provider: "claude", URL: up.URL + "/"}})
	if res.EgressIP != "203.0.113.7" || !res.ProxyReachable {
		t.Fatalf("egress: %+v", res)
	}
	if pr := res.Providers[0]; !pr.OK || pr.Status != 404 {
		t.Fatalf("provider: %+v", pr)
	}
	if p.Hits() != 2 {
		t.Fatalf("proxy saw %d requests, want 2", p.Hits())
	}

	bad := Test(context.Background(), spec(p, "wrong"), echo.URL, []Target{{Provider: "claude", URL: up.URL + "/"}})
	if bad.ProxyReachable || !strings.Contains(bad.Providers[0].Error, "407") || !strings.Contains(bad.EgressError, "407") {
		t.Fatalf("bad credentials: %+v", bad)
	}
}

func TestParseEcho(t *testing.T) {
	ip, loc := parseEcho([]byte(`{"ip":"195.72.60.76","city":"Stockholm","region":"Stockholm","country":"SE","org":"AS215960 Example","timezone":"Europe/Stockholm","readme":"x"}`))
	if ip != "195.72.60.76" || loc == nil || loc.City != "Stockholm" || loc.Country != "SE" || loc.Org != "AS215960 Example" {
		t.Fatalf("json: %q %+v", ip, loc)
	}
	if ip, loc := parseEcho([]byte("203.0.113.7\n")); ip != "203.0.113.7" || loc != nil {
		t.Fatalf("plain: %q %+v", ip, loc)
	}
	if ip, loc := parseEcho([]byte(`{"ip":"203.0.113.7"}`)); ip != "203.0.113.7" || loc != nil {
		t.Fatalf("json without location: %q %+v", ip, loc)
	}
}

func TestTest_UnreachableProxy(t *testing.T) {
	res := Test(context.Background(), &Spec{ID: "p", Type: "http", Host: "127.0.0.1", Port: 1}, "", []Target{{Provider: "claude", URL: "https://example.invalid/"}})
	if res.ProxyReachable || res.Providers[0].OK || res.Providers[0].Error == "" {
		t.Fatalf("%+v", res)
	}
}
