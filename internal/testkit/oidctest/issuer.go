// Package oidctest is an in-process OIDC issuer that stands in for Keycloak.
//
// It serves Keycloak-shaped endpoints under /realms/<realm>/..., signs RS256
// tokens with a per-test key, and supports an "auto-login" authorization flow:
// the next /auth request immediately redirects back with a code for whichever
// user the test selected via LoginAs. That lets dashboard end-to-end tests
// (Playwright) and control-plane tests exercise the real OIDC code path
// without a Keycloak container.
//
// PKCE code_challenge values are accepted but not verified; the issuer is for
// tests only and must never be shipped.
package oidctest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Claims describes a test user. Groups maps to Keycloak's "groups" claim,
// which Qanat uses for the entry gate (REQ-AUTH-02: group qanat-users).
type Claims struct {
	Subject  string
	Email    string
	Name     string
	Groups   []string
	Audience string
	TTL      time.Duration
}

// Issuer is a running fake identity provider.
type Issuer struct {
	Realm    string
	ClientID string

	baseURL string
	key     *rsa.PrivateKey
	kid     string

	mu    sync.Mutex
	next  *Claims
	codes map[string]Claims
}

func newIssuer() (*Issuer, *http.ServeMux, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	i := &Issuer{Realm: "qanat", ClientID: "qanat-dashboard", key: key, kid: "test-key-1", codes: map[string]Claims{}}
	mux := http.NewServeMux()
	base := "/realms/" + i.Realm
	mux.HandleFunc(base+"/.well-known/openid-configuration", i.discovery)
	mux.HandleFunc(base+"/protocol/openid-connect/certs", i.jwks)
	mux.HandleFunc(base+"/protocol/openid-connect/auth", i.auth)
	mux.HandleFunc(base+"/protocol/openid-connect/token", i.token)
	mux.HandleFunc("POST /test/login-as", i.loginAsHTTP)
	return i, mux, nil
}

// New starts an in-process issuer for realm "qanat" and client
// "qanat-dashboard", stopped automatically when the test ends.
func New(t testing.TB) *Issuer {
	t.Helper()
	i, mux, err := newIssuer()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	i.baseURL = srv.URL
	return i
}

// Serve runs a standalone issuer on addr (for Playwright and the compose
// stack, via cmd/oidcmock). publicURL is the URL browsers use to reach it.
// It blocks until the server stops.
func Serve(addr, publicURL string) error {
	i, mux, err := newIssuer()
	if err != nil {
		return err
	}
	i.baseURL = strings.TrimRight(publicURL, "/")
	return http.ListenAndServe(addr, mux)
}

// IssuerURL is the value to configure as the OIDC issuer.
func (i *Issuer) IssuerURL() string { return i.baseURL + "/realms/" + i.Realm }

// loginAsHTTP lets an out-of-process test runner (Playwright) choose the next
// user: POST /test/login-as {"subject":..,"email":..,"name":..,"groups":[..]}
func (i *Issuer) loginAsHTTP(w http.ResponseWriter, r *http.Request) {
	var c struct {
		Subject string   `json:"subject"`
		Email   string   `json:"email"`
		Name    string   `json:"name"`
		Groups  []string `json:"groups"`
	}
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil || c.Subject == "" {
		http.Error(w, `want {"subject":..,"email":..,"groups":[..]}`, http.StatusBadRequest)
		return
	}
	i.LoginAs(Claims{Subject: c.Subject, Email: c.Email, Name: c.Name, Groups: c.Groups})
	w.WriteHeader(http.StatusNoContent)
}

// LoginAs makes the next authorization request log in as c.
func (i *Issuer) LoginAs(c Claims) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.next = &c
}

// Token mints a signed access token for c.
func (i *Issuer) Token(c Claims) string {
	if c.TTL == 0 {
		c.TTL = time.Hour
	}
	if c.Audience == "" {
		c.Audience = i.ClientID
	}
	now := time.Now()
	payload := map[string]any{
		"iss":                i.IssuerURL(),
		"sub":                c.Subject,
		"aud":                c.Audience,
		"azp":                i.ClientID,
		"iat":                now.Unix(),
		"exp":                now.Add(c.TTL).Unix(),
		"email":              c.Email,
		"email_verified":     true,
		"name":               c.Name,
		"preferred_username": c.Email,
		"groups":             c.Groups,
	}
	return i.sign(payload)
}

func (i *Issuer) sign(payload map[string]any) string {
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": i.kid})
	p, _ := json.Marshal(payload)
	signing := b64(h) + "." + b64(p)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, i.key, crypto.SHA256, sum[:])
	if err != nil {
		panic(err)
	}
	return signing + "." + b64(sig)
}

// Verify checks a token's signature, issuer and expiry and returns its claims.
// It exists so the issuer can self-test; Qanat code must use a real OIDC library.
func (i *Issuer) Verify(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("oidctest: malformed token")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&i.key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		return nil, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, err
	}
	if claims["iss"] != i.IssuerURL() {
		return nil, fmt.Errorf("oidctest: wrong issuer %v", claims["iss"])
	}
	if exp, _ := claims["exp"].(float64); int64(exp) < time.Now().Unix() {
		return nil, errors.New("oidctest: token expired")
	}
	return claims, nil
}

func (i *Issuer) discovery(w http.ResponseWriter, _ *http.Request) {
	base := i.IssuerURL() + "/protocol/openid-connect"
	writeJSON(w, map[string]any{
		"issuer":                                i.IssuerURL(),
		"authorization_endpoint":                base + "/auth",
		"token_endpoint":                        base + "/token",
		"jwks_uri":                              base + "/certs",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{"S256"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
	})
}

func (i *Issuer) jwks(w http.ResponseWriter, _ *http.Request) {
	pub := i.key.PublicKey
	writeJSON(w, map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": i.kid,
		"n": b64(pub.N.Bytes()),
		"e": b64(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

func (i *Issuer) auth(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || redirect.String() == "" {
		http.Error(w, "missing redirect_uri", http.StatusBadRequest)
		return
	}
	i.mu.Lock()
	user := i.next
	i.next = nil
	if user == nil {
		i.mu.Unlock()
		http.Error(w, "oidctest: call LoginAs before starting a login", http.StatusConflict)
		return
	}
	code := randomString()
	i.codes[code] = *user
	i.mu.Unlock()

	rq := redirect.Query()
	rq.Set("code", code)
	rq.Set("state", q.Get("state"))
	redirect.RawQuery = rq.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (i *Issuer) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	code := r.PostForm.Get("code")
	i.mu.Lock()
	user, ok := i.codes[code]
	delete(i.codes, code)
	i.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": "invalid_grant"})
		return
	}
	tok := i.Token(user)
	writeJSON(w, map[string]any{
		"access_token": tok,
		"id_token":     tok,
		"token_type":   "Bearer",
		"expires_in":   3600,
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func randomString() string {
	b := make([]byte, 16)
	rand.Read(b)
	return b64(b)
}
