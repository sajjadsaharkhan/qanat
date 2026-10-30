package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Handshake is one timed path check to an upstream origin, after Tower's API
// probe (github.com/ghhrmnzdh/tower, towerd.py _api_probe): DNS, TCP connect
// (or the proxy tunnel) and TLS, then the connection is closed. No HTTP
// request is sent, so it costs nothing and needs no credentials.
type Handshake struct {
	// Phases that ran; nil when skipped (an IP literal, or DNS the proxy
	// does) or never reached.
	DNS, Connect, TLS *time.Duration
	Total             time.Duration
	// Kind is why it failed: "dns", "tcp", "proxy", "proxy_auth", "tls" or
	// "timeout"; "" when it succeeded.
	Kind   string
	Detail string
}

// OK reports whether the handshake completed.
func (h Handshake) OK() bool { return h.Kind == "" }

// handshakeTLS is the client config; tests swap in their own roots.
var handshakeTLS = func(host string) *tls.Config {
	return &tls.Config{ServerName: host, NextProtos: []string{"h2", "http/1.1"}}
}

// SetHandshakeRootsForTest makes ProbeHandshake trust cert instead of the
// system roots until the returned func runs. For tests only.
func SetHandshakeRootsForTest(cert *x509.Certificate) func() {
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	prev := handshakeTLS
	handshakeTLS = func(host string) *tls.Config { return &tls.Config{ServerName: host, RootCAs: pool} }
	return func() { handshakeTLS = prev }
}

// ProbeHandshake checks the path to origin (scheme://host[:port]) through
// spec, or direct when spec is nil, within ctx's deadline. The certificate is
// verified: a portal or a man in the middle fails as "tls".
func ProbeHandshake(ctx context.Context, spec *Spec, origin string) Handshake {
	start := time.Now()
	var h Handshake
	fail := func(kind string, err error) Handshake {
		h.Total = time.Since(start)
		if ctx.Err() != nil || isTimeout(err) {
			kind = "timeout"
		}
		h.Kind, h.Detail = kind, clean(err)
		return h
	}
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return fail("dns", fmt.Errorf("bad origin %q", origin))
	}
	host, port := u.Hostname(), u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	phase := func(t0 time.Time) *time.Duration { d := time.Since(t0); return &d }

	// DNS runs here unless the proxy resolves the name (http(s) CONNECT and
	// socks5h send the hostname).
	addr := host
	if net.ParseIP(host) == nil && (spec == nil || spec.Type == "socks5") {
		t0 := time.Now()
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			if err == nil {
				err = errors.New("no addresses")
			}
			return fail("dns", err)
		}
		h.DNS = phase(t0)
		addr = ips[0].IP.String()
	}

	var d net.Dialer
	var conn net.Conn
	t0 := time.Now()
	if spec == nil {
		if conn, err = d.DialContext(ctx, "tcp", net.JoinHostPort(addr, port)); err != nil {
			return fail("tcp", err)
		}
	} else {
		if conn, err = d.DialContext(ctx, "tcp", net.JoinHostPort(spec.Host, strconv.Itoa(spec.Port))); err != nil {
			return fail("proxy", err)
		}
		if dl, ok := ctx.Deadline(); ok {
			conn.SetDeadline(dl)
		}
		if spec.Type == "https" {
			tc := tls.Client(conn, &tls.Config{ServerName: spec.Host})
			if err = tc.HandshakeContext(ctx); err != nil {
				conn.Close()
				return fail("proxy", err)
			}
			conn = tc
		}
		var kind string
		switch spec.Type {
		case "socks5", "socks5h":
			kind, err = socksConnect(conn, spec, addr, port)
		default:
			kind, err = httpConnect(conn, spec, net.JoinHostPort(host, port))
		}
		if err != nil {
			conn.Close()
			return fail(kind, err)
		}
	}
	defer conn.Close()
	h.Connect = phase(t0)

	if u.Scheme == "https" {
		t0 = time.Now()
		if err := tls.Client(conn, handshakeTLS(host)).HandshakeContext(ctx); err != nil {
			return fail("tls", err)
		}
		h.TLS = phase(t0)
	}
	h.Total = time.Since(start)
	return h
}

// httpConnect opens a CONNECT tunnel to target on an http(s) proxy.
func httpConnect(conn net.Conn, spec *Spec, target string) (string, error) {
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: http.Header{}}
	if spec.Username != "" {
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(spec.Username+":"+spec.Password)))
	}
	if err := req.Write(conn); err != nil {
		return "proxy", err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return "proxy", err
	}
	// The body is the tunnel itself: closing it would wait for EOF.
	switch {
	case resp.StatusCode == http.StatusProxyAuthRequired:
		return "proxy_auth", errors.New("the proxy rejected the username or password (407)")
	case resp.StatusCode != http.StatusOK:
		return "proxy", fmt.Errorf("the proxy answered CONNECT with %s", resp.Status)
	}
	return "", nil
}

// socksConnect runs the RFC 1928 handshake (RFC 1929 credentials). host is
// an IP for socks5 and the name for socks5h.
func socksConnect(conn net.Conn, spec *Spec, host, port string) (string, error) {
	methods := []byte{0x00}
	if spec.Username != "" {
		methods = []byte{0x02}
	}
	if _, err := conn.Write(append([]byte{5, byte(len(methods))}, methods...)); err != nil {
		return "proxy", err
	}
	var rep [2]byte
	if _, err := io.ReadFull(conn, rep[:]); err != nil {
		return "proxy", err
	}
	if rep[0] != 5 || rep[1] == 0xff {
		return "proxy_auth", errors.New("the SOCKS proxy accepts none of the offered sign-in methods")
	}
	if rep[1] == 0x02 {
		u, p := spec.Username, spec.Password
		msg := append(append(append([]byte{1, byte(len(u))}, u...), byte(len(p))), p...)
		if _, err := conn.Write(msg); err != nil {
			return "proxy", err
		}
		if _, err := io.ReadFull(conn, rep[:]); err != nil {
			return "proxy", err
		}
		if rep[1] != 0 {
			return "proxy_auth", errors.New("the SOCKS proxy rejected the username or password")
		}
	}
	pn, err := strconv.Atoi(port)
	if err != nil {
		return "proxy", err
	}
	req := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip == nil {
		req = append(append(req, 3, byte(len(host))), host...)
	} else if v4 := ip.To4(); v4 != nil {
		req = append(append(req, 1), v4...)
	} else {
		req = append(append(req, 4), ip.To16()...)
	}
	req = binary.BigEndian.AppendUint16(req, uint16(pn))
	if _, err := conn.Write(req); err != nil {
		return "proxy", err
	}
	var head [4]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return "proxy", err
	}
	if head[1] != 0 {
		return "proxy", fmt.Errorf("the SOCKS proxy couldn't reach the host (reply %d)", head[1])
	}
	skip := 0
	switch head[3] {
	case 1:
		skip = 4
	case 4:
		skip = 16
	case 3:
		var n [1]byte
		if _, err := io.ReadFull(conn, n[:]); err != nil {
			return "proxy", err
		}
		skip = int(n[0])
	}
	if _, err := io.CopyN(io.Discard, conn, int64(skip+2)); err != nil {
		return "proxy", err
	}
	return "", nil
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout()
}

// TCPPing is the time to open a TCP connection to addr (≈ one round trip),
// Tower's internet reference probe.
func TCPPing(ctx context.Context, addr string) (time.Duration, error) {
	t0 := time.Now()
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return 0, err
	}
	c.Close()
	return time.Since(t0), nil
}
