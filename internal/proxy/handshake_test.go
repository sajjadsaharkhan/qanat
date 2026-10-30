package proxy

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"qanat/internal/testkit/proxytest"
)

// trustServer makes ProbeHandshake accept srv's test certificate.
func trustServer(t *testing.T, srv *httptest.Server) {
	t.Cleanup(SetHandshakeRootsForTest(srv.Certificate()))
}

func probeCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestProbeHandshake_Direct(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	trustServer(t, srv)
	h := ProbeHandshake(probeCtx(t), nil, srv.URL)
	if !h.OK() || h.Connect == nil || h.TLS == nil || h.DNS != nil {
		t.Fatalf("direct to an IP: want connect+TLS and no DNS, got %+v", h)
	}
	if h.Total < *h.Connect+*h.TLS {
		t.Fatalf("total %v below its phases", h.Total)
	}
}

// Tower's rule: a failed certificate is a different fault from a dead path.
func TestProbeHandshake_FailureKinds(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	if h := ProbeHandshake(probeCtx(t), nil, srv.URL); h.Kind != "tls" {
		t.Fatalf("untrusted certificate: kind %q (%s)", h.Kind, h.Detail)
	}

	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	if h := ProbeHandshake(probeCtx(t), nil, "https://"+addr); h.Kind != "tcp" {
		t.Fatalf("closed port: kind %q (%s)", h.Kind, h.Detail)
	}

	// A listener that never answers the TLS hello runs into the deadline.
	silent, _ := net.Listen("tcp", "127.0.0.1:0")
	defer silent.Close()
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if h := ProbeHandshake(ctx, nil, "https://"+silent.Addr().String()); h.Kind != "timeout" || h.Connect == nil {
		t.Fatalf("silent server: want timeout after connect, got %+v", h)
	}
}

func TestProbeHandshake_HTTPProxy(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	trustServer(t, srv)
	p := proxytest.New(t, "u", "pw")
	host, port := p.HostPort()

	h := ProbeHandshake(probeCtx(t), &Spec{ID: "p", Type: "http", Host: host, Port: port, Username: "u", Password: "pw"}, srv.URL)
	if !h.OK() || h.TLS == nil || p.Hits() != 1 {
		t.Fatalf("through the proxy: %+v (hits %d)", h, p.Hits())
	}
	h = ProbeHandshake(probeCtx(t), &Spec{ID: "p", Type: "http", Host: host, Port: port, Username: "u", Password: "wrong"}, srv.URL)
	if h.Kind != "proxy_auth" {
		t.Fatalf("wrong password: kind %q (%s)", h.Kind, h.Detail)
	}
}

func TestProbeHandshake_SOCKS5(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	trustServer(t, srv)
	host, port := socksServer(t, "u", "pw")
	for _, typ := range []string{"socks5", "socks5h"} {
		h := ProbeHandshake(probeCtx(t), &Spec{ID: "s", Type: typ, Host: host, Port: port, Username: "u", Password: "pw"}, srv.URL)
		if !h.OK() || h.TLS == nil {
			t.Fatalf("%s: %+v", typ, h)
		}
	}
	h := ProbeHandshake(probeCtx(t), &Spec{ID: "s", Type: "socks5", Host: host, Port: port, Username: "u", Password: "bad"}, srv.URL)
	if h.Kind != "proxy_auth" {
		t.Fatalf("wrong password: kind %q (%s)", h.Kind, h.Detail)
	}
}

// socksServer is a minimal RFC 1928/1929 CONNECT server.
func socksServer(t *testing.T, user, pass string) (string, int) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				b := make([]byte, 262)
				io.ReadFull(c, b[:2])
				io.ReadFull(c, b[:b[1]])
				c.Write([]byte{5, 2})
				io.ReadFull(c, b[:2])
				u := make([]byte, b[1])
				io.ReadFull(c, u)
				io.ReadFull(c, b[:1])
				p := make([]byte, b[0])
				io.ReadFull(c, p)
				if string(u) != user || string(p) != pass {
					c.Write([]byte{1, 1})
					return
				}
				c.Write([]byte{1, 0})
				io.ReadFull(c, b[:4])
				var host string
				switch b[3] {
				case 1:
					io.ReadFull(c, b[:4])
					host = net.IP(b[:4]).String()
				case 3:
					io.ReadFull(c, b[:1])
					n := b[0]
					io.ReadFull(c, b[:n])
					host = string(b[:n])
				}
				io.ReadFull(c, b[:2])
				up, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(b[:2])))))
				if err != nil {
					c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				defer up.Close()
				c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				go io.Copy(up, c)
				io.Copy(c, up)
			}()
		}
	}()
	a := l.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}
