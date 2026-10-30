// Package proxy manages outbound connections through admin-defined proxies
// (PRD §8): one warm transport per proxy, so a proxy adds no handshake cost
// to TTFT (REQ-PROXY-02), and edits swap the transport without touching
// in-flight streams (REQ-PROXY-04).
package proxy

import (
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"qanat/internal/core/secretbox"
)

// Types are the proxy schemes Qanat accepts. socks5h resolves DNS on the
// proxy side.
var Types = map[string]bool{"http": true, "https": true, "socks5": true, "socks5h": true}

// Spec is one proxy as the data plane needs it. A nil *Spec means direct.
type Spec struct {
	ID       string
	Type     string
	Host     string
	Port     int
	Username string
	Password string // plaintext; never logged
}

// URL is the proxy URL for http.Transport.Proxy, credentials included.
func (s *Spec) URL() *url.URL {
	u := &url.URL{Scheme: s.Type, Host: net.JoinHostPort(s.Host, strconv.Itoa(s.Port))}
	if s.Username != "" {
		u.User = url.UserPassword(s.Username, s.Password)
	}
	return u
}

// fingerprint changes whenever anything that affects the connection does.
func (s *Spec) fingerprint() [32]byte {
	return sha256.Sum256([]byte(s.Type + "\x00" + s.Host + "\x00" + strconv.Itoa(s.Port) + "\x00" + s.Username + "\x00" + s.Password))
}

// NewTransport is a warm upstream pool (§9: HTTP/2, keep-alive, TLS
// resumption) through spec, or direct when spec is nil. Compression is off:
// SSE must arrive as written.
func NewTransport(spec *Spec) *http.Transport {
	t := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Minute, // long thinking can delay headers
		DisableCompression:    true,
	}
	if spec != nil {
		t.Proxy = http.ProxyURL(spec.URL())
	}
	return t
}

// SealPassword and OpenPassword bind a proxy password to its proxy ID, so a
// sealed value can't be moved to another row.
func SealPassword(box *secretbox.Box, proxyID, password string) ([]byte, error) {
	if box == nil {
		return nil, ErrNoMasterKey
	}
	return box.Seal([]byte(password), []byte("proxy:"+proxyID))
}

func OpenPassword(box *secretbox.Box, proxyID string, sealed []byte) (string, error) {
	if len(sealed) == 0 {
		return "", nil
	}
	if box == nil {
		return "", ErrNoMasterKey
	}
	plain, err := box.Open(sealed, []byte("proxy:"+proxyID))
	return string(plain), err
}

// ErrNoMasterKey: proxy passwords need QANAT_MASTER_KEY.
var ErrNoMasterKey = fmt.Errorf("QANAT_MASTER_KEY is not set, so proxy passwords can't be stored or read")

// Pool hands out one client per proxy (plus one direct), rebuilding a
// proxy's transport when its settings change.
type Pool struct {
	direct *http.Client
	mu     sync.Mutex
	byID   map[string]entry
}

type entry struct {
	fp     [32]byte
	client *http.Client
}

func NewPool() *Pool {
	return &Pool{direct: &http.Client{Transport: NewTransport(nil)}, byID: map[string]entry{}}
}

// Client returns the client for spec (nil = direct).
func (p *Pool) Client(spec *Spec) *http.Client {
	if spec == nil {
		return p.direct
	}
	fp := spec.fingerprint()
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.byID[spec.ID]; ok {
		if e.fp == fp {
			return e.client
		}
		// Settings changed: new requests use a fresh transport; streams on the
		// old one finish undisturbed and its idle connections are dropped.
		e.client.CloseIdleConnections()
	}
	c := &http.Client{Transport: NewTransport(spec)}
	p.byID[spec.ID] = entry{fp, c}
	return c
}

// Retain drops transports for proxies no longer in use.
func (p *Pool) Retain(ids map[string]bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, e := range p.byID {
		if !ids[id] {
			e.client.CloseIdleConnections()
			delete(p.byID, id)
		}
	}
}
