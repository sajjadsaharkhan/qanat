package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func tokenServer(t *testing.T, status int, resp string, seen *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if seen != nil {
			m := map[string]any{}
			json.Unmarshal(body, &m) // the Claude flow sends JSON
			*seen = m
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, resp)
	}))
}

func TestBeginClaudeAuthorizeURL(t *testing.T) {
	authURL, state, p, err := Begin("claude", "http://localhost:3000/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != "claude.ai" || u.Path != "/oauth/authorize" {
		t.Fatalf("authorize URL: %s", authURL)
	}
	q := u.Query()
	if q.Get("client_id") != claude.ClientID || q.Get("response_type") != "code" {
		t.Fatalf("client params: %v", q)
	}
	if q.Get("redirect_uri") != "http://localhost:3000/callback" || q.Get("code") != "true" {
		t.Fatalf("redirect/code params: %v", q)
	}
	if q.Get("scope") != claude.Scope || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("scope/PKCE params: %v", q)
	}
	if len(q.Get("code_challenge")) != 43 { // base64url of a 256-bit digest
		t.Fatalf("code_challenge length: %d", len(q.Get("code_challenge")))
	}
	if len(q.Get("state")) != 64 || q.Get("state") != state {
		t.Fatalf("state: %q vs %q", q.Get("state"), state)
	}
	if p == nil || p.Verifier == "" || p.RedirectURI != "http://localhost:3000/callback" {
		t.Fatalf("pending: %+v", p)
	}
}

func TestBeginUnknownProvider(t *testing.T) {
	if _, _, _, err := Begin("openrouter", "http://localhost:3000/callback"); err == nil {
		t.Fatal("openrouter has no flow; want an error")
	}
}

func TestExchangeClaude(t *testing.T) {
	var seen map[string]any
	srv := tokenServer(t, 200, `{"access_token":"sk-ant-oat01-x","refresh_token":"rt1","expires_in":3600,"scope":"user:inference"}`, &seen)
	defer srv.Close()
	orig := *claude
	claude.TokenURL = srv.URL
	t.Cleanup(func() { *claude = orig })

	_, _, p, err := Begin("claude", "http://localhost:3000/callback")
	if err != nil {
		t.Fatal(err)
	}
	toks, err := claude.Exchange(context.Background(), srv.Client(), "the-code", "the-state", "http://localhost:3000/callback", p)
	if err != nil {
		t.Fatal(err)
	}
	if toks.AccessToken != "sk-ant-oat01-x" || toks.RefreshToken != "rt1" || toks.ExpiresIn != 3600 {
		t.Fatalf("tokens: %+v", toks)
	}
	for _, k := range []string{"code", "state", "grant_type", "client_id", "redirect_uri", "code_verifier"} {
		if _, ok := seen[k]; !ok {
			t.Fatalf("exchange body missing %s: %v", k, seen)
		}
	}
	if seen["grant_type"] != "authorization_code" || seen["code"] != "the-code" || seen["state"] != "the-state" {
		t.Fatalf("exchange body: %v", seen)
	}
	if seen["client_id"] != claude.ClientID || seen["code_verifier"] != p.Verifier {
		t.Fatalf("exchange body: %v", seen)
	}
	if _, ok := seen["client_secret"]; ok {
		t.Fatal("the Claude flow must not send a client secret")
	}
}

func TestExchangeFragmentState(t *testing.T) {
	var seen map[string]any
	srv := tokenServer(t, 200, `{"access_token":"a","refresh_token":"r","expires_in":60}`, &seen)
	defer srv.Close()
	orig := *claude
	claude.TokenURL = srv.URL
	t.Cleanup(func() { *claude = orig })

	_, _, p, err := Begin("claude", "http://localhost:3000/callback")
	if err != nil {
		t.Fatal(err)
	}
	// Claude may redirect to …?code=XYZ%23STATE with no state query param.
	if _, err := claude.Exchange(context.Background(), srv.Client(), "abc#xyz", "", "http://localhost:3000/callback", p); err != nil {
		t.Fatal(err)
	}
	if seen["code"] != "abc" || seen["state"] != "xyz" {
		t.Fatalf("fragment split: %v", seen)
	}
}

func TestExchangeUnrecoverable(t *testing.T) {
	srv := tokenServer(t, 400, `{"error":"invalid_grant","error_description":"code already used"}`, nil)
	defer srv.Close()
	orig := *claude
	claude.TokenURL = srv.URL
	t.Cleanup(func() { *claude = orig })

	_, _, p, _ := Begin("claude", "http://localhost:3000/callback")
	_, err := claude.Exchange(context.Background(), srv.Client(), "c", "s", "http://localhost:3000/callback", p)
	var unrecoverable *UnrecoverableError
	if !errors.As(err, &unrecoverable) {
		t.Fatalf("invalid_grant must be unrecoverable, got: %v", err)
	}
}

func TestExchangeProfileEmail(t *testing.T) {
	srv := tokenServer(t, 200, `{"access_token":"a","refresh_token":"r","expires_in":60}`, nil)
	defer srv.Close()
	profile := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"account":{"uuid":"u1","email":"alice@example.com"}}`))
	}))
	defer profile.Close()
	orig := *claude
	claude.TokenURL = srv.URL
	claude.ProfileURL = profile.URL
	t.Cleanup(func() { *claude = orig })

	_, _, p, _ := Begin("claude", "http://localhost:3000/callback")
	toks, err := claude.Exchange(context.Background(), srv.Client(), "c", "s", "http://localhost:3000/callback", p)
	if err != nil {
		t.Fatal(err)
	}
	if toks.Email != "alice@example.com" {
		t.Fatalf("email: %q", toks.Email)
	}
}

func TestRefreshClaude(t *testing.T) {
	var seen map[string]any
	srv := tokenServer(t, 200, `{"access_token":"a2","refresh_token":"rt2","expires_in":3600}`, &seen)
	defer srv.Close()
	orig := *claude
	claude.TokenURL = srv.URL
	t.Cleanup(func() { *claude = orig })

	toks, err := claude.Refresh(context.Background(), srv.Client(), "rt1")
	if err != nil {
		t.Fatal(err)
	}
	if toks.AccessToken != "a2" || toks.RefreshToken != "rt2" {
		t.Fatalf("tokens: %+v", toks)
	}
	if seen["grant_type"] != "refresh_token" || seen["refresh_token"] != "rt1" || seen["client_id"] != claude.ClientID {
		t.Fatalf("refresh body: %v", seen)
	}
}
