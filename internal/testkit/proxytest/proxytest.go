// Package proxytest is an in-process HTTP forward proxy for tests: plain
// requests (absolute URI) and CONNECT tunnels, optional basic auth, and a
// count of what passed through, so a test can prove traffic used the proxy.
package proxytest

import (
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
)

// Proxy is a running test proxy.
type Proxy struct {
	*httptest.Server
	User, Pass string // required credentials; empty = none
	hits       atomic.Int64
}

// New starts a proxy, closed when the test ends.
func New(t testing.TB, user, pass string) *Proxy {
	p := &Proxy{User: user, Pass: pass}
	p.Server = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.Close)
	return p
}

// Hits is how many requests or tunnels the proxy accepted.
func (p *Proxy) Hits() int64 { return p.hits.Load() }

// HostPort splits the listen address for a proxy spec.
func (p *Proxy) HostPort() (string, int) {
	u, _ := url.Parse(p.URL)
	port, _ := strconv.Atoi(u.Port())
	return u.Hostname(), port
}

func (p *Proxy) serve(w http.ResponseWriter, r *http.Request) {
	if p.User != "" && r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(p.User+":"+p.Pass)) {
		w.Header().Set("Proxy-Authenticate", `Basic realm="test"`)
		w.WriteHeader(http.StatusProxyAuthRequired)
		return
	}
	p.hits.Add(1)
	if r.Method == http.MethodConnect {
		up, err := net.Dial("tcp", r.Host)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
		conn, buf, err := http.NewResponseController(w).Hijack()
		if err != nil {
			up.Close()
			return
		}
		go func() { io.Copy(up, buf); up.Close() }()
		io.Copy(conn, up)
		conn.Close()
		return
	}
	out, _ := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
	out.Header = r.Header.Clone()
	out.Header.Del("Proxy-Authorization")
	resp, err := http.DefaultTransport.RoundTrip(out)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.Header().Set("X-Via-Test-Proxy", "1")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
