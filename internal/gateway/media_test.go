package gateway

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"qanat/internal/proxy"
)

func TestInlineURLMedia(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n0000")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(png)
		case "/doc":
			w.Header().Set("Content-Type", "application/pdf")
			w.Write([]byte("%PDF-1.7"))
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html>"))
		}
	}))
	defer srv.Close()
	g := &Gateway{proxies: proxy.NewPool()}
	ctx := context.Background()

	plain := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	if out, err := g.inlineURLMedia(ctx, nil, plain); err != nil || &out[0] != &plain[0] {
		t.Fatal("a body without URL media must pass through untouched")
	}

	// Loopback is refused by default: the httptest server is on 127.0.0.1.
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"` + srv.URL + `/a.png"}}]}]}`)
	if _, err := g.inlineURLMedia(ctx, nil, body); err == nil || !strings.Contains(err.Error(), "private or local") {
		t.Fatalf("loopback must be refused, got %v", err)
	}

	defer func(f func(net.IP) bool) { mediaIPAllowed = f }(mediaIPAllowed)
	mediaIPAllowed = func(net.IP) bool { return true }
	body = []byte(`{"model":"m","max_tokens":10,"messages":[{"role":"user","content":[
	  {"type":"image","source":{"type":"url","url":"` + srv.URL + `/a.png"}},
	  {"type":"tool_result","tool_use_id":"t1","content":[{"type":"document","source":{"type":"url","url":"` + srv.URL + `/doc"}}]}]}]}`)
	out, err := g.inlineURLMedia(ctx, nil, body)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `"data":"`+base64.StdEncoding.EncodeToString(png)+`"`) || !strings.Contains(s, `"media_type":"image/png"`) ||
		!strings.Contains(s, `"media_type":"application/pdf"`) || strings.Contains(s, `"type":"url"`) || !strings.Contains(s, `"max_tokens":10`) {
		t.Fatalf("inlined body: %s", s)
	}

	for url, want := range map[string]string{srv.URL + "/page": "not an image", "file:///etc/passwd": "only http"} {
		b := []byte(`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"` + url + `"}}]}]}`)
		if _, err := g.inlineURLMedia(ctx, nil, b); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", url, err, want)
		}
	}
}

// A public address that redirects to a private one is refused at the dial.
func TestInlineURLMedia_RedirectToPrivate(t *testing.T) {
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("secret")) }))
	defer inner.Close()
	outer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(inner.URL, "127.0.0.1", "localhost", 1)+"/x", http.StatusFound)
	}))
	defer outer.Close()
	defer func(f func(net.IP) bool) { mediaIPAllowed = f }(mediaIPAllowed)
	_, outerPort, _ := net.SplitHostPort(strings.TrimPrefix(outer.URL, "http://"))
	// Only the outer server's dial passes: localhost resolves to loopback, which stays refused.
	calls := 0
	mediaIPAllowed = func(ip net.IP) bool { calls++; return calls == 1 }
	g := &Gateway{proxies: proxy.NewPool()}
	b := []byte(`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"http://127.0.0.1:` + outerPort + `/r"}}]}]}`)
	if _, err := g.inlineURLMedia(context.Background(), nil, b); err == nil || !strings.Contains(err.Error(), "private or local") {
		t.Fatalf("redirect to a private address must be refused, got %v", err)
	}
}
