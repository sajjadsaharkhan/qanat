package oauth

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// fakeJWT is an unsigned JWT carrying claims; codexIdentity reads only the
// payload.
func fakeJWT(claims string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(claims)) + ".sig"
}

func TestBeginCodexUsesTheCLIRedirect(t *testing.T) {
	authURL, state, p, err := Begin("codex", "http://localhost:3001/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	if u.Host != "auth.openai.com" || u.Path != "/oauth/authorize" {
		t.Fatalf("authorize URL: %s", authURL)
	}
	q := u.Query()
	if q.Get("redirect_uri") != CodexRedirectURI || p.RedirectURI != CodexRedirectURI {
		t.Fatalf("redirect: %q / pending %q", q.Get("redirect_uri"), p.RedirectURI)
	}
	if q.Get("client_id") != codex.ClientID || q.Get("scope") != "openid profile email offline_access" {
		t.Fatalf("client/scope: %v", q)
	}
	if q.Get("code_challenge_method") != "S256" || p.Verifier == "" {
		t.Fatalf("PKCE: %v", q)
	}
	if q.Get("originator") != "codex_cli_rs" || q.Get("codex_cli_simplified_flow") != "true" || q.Get("id_token_add_organizations") != "true" {
		t.Fatalf("extra params: %v", q)
	}
	if q.Get("state") != state {
		t.Fatalf("state: %v", q)
	}
}

func TestExchangeCodex(t *testing.T) {
	idToken := fakeJWT(`{"email":"dev@example.com","https://api.openai.com/auth":{"chatgpt_account_id":"acc-123","chatgpt_plan_type":"plus"}}`)
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(b))
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("content type %q", r.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"at1","refresh_token":"rt1","id_token":"`+idToken+`","expires_in":3600}`)
	}))
	defer srv.Close()
	orig := *codex
	codex.TokenURL = srv.URL
	t.Cleanup(func() { *codex = orig })

	_, state, p, err := Begin("codex", "")
	if err != nil {
		t.Fatal(err)
	}
	toks, err := codex.Exchange(context.Background(), srv.Client(), "the-code", state, p.RedirectURI, p)
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("grant_type") != "authorization_code" || form.Get("code") != "the-code" || form.Get("code_verifier") != p.Verifier ||
		form.Get("redirect_uri") != CodexRedirectURI || form.Get("client_id") != codex.ClientID || form.Has("client_secret") {
		t.Fatalf("exchange form: %v", form)
	}
	if toks.Email != "dev@example.com" || toks.AccountID != "acc-123" || toks.RefreshToken != "rt1" {
		t.Fatalf("tokens: %+v", toks)
	}
	c := CredentialsFrom(toks)
	if c.AccountID != "acc-123" || c.Upstream() != "acc-123" {
		t.Fatalf("credentials: %+v", c)
	}
	// A refresh that rotates only the access token keeps the account binding.
	if r := c.Refreshed(&Tokens{AccessToken: "at2"}); r.AccountID != "acc-123" || r.RefreshToken != "rt1" {
		t.Fatalf("refreshed: %+v", r)
	}
}

func TestRefreshCodexSendsScope(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(b))
		io.WriteString(w, `{"access_token":"at2","refresh_token":"rt2","expires_in":3600}`)
	}))
	defer srv.Close()
	orig := *codex
	codex.TokenURL = srv.URL
	t.Cleanup(func() { *codex = orig })

	toks, err := codex.Refresh(context.Background(), srv.Client(), "rt1")
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "rt1" || form.Get("client_id") != codex.ClientID || form.Get("scope") != codex.Scope {
		t.Fatalf("refresh form: %v", form)
	}
	if toks.AccessToken != "at2" || toks.RefreshToken != "rt2" {
		t.Fatalf("tokens: %+v", toks)
	}
}

func TestCodexIdentityFallsBackToAccessToken(t *testing.T) {
	toks := &Tokens{AccessToken: fakeJWT(`{"https://api.openai.com/profile":{"email":"p@example.com"},"https://api.openai.com/auth":{"chatgpt_account_id":"acc-9"}}`)}
	codexIdentity(context.Background(), nil, toks)
	if toks.Email != "p@example.com" || toks.AccountID != "acc-9" {
		t.Fatalf("identity: %+v", toks)
	}
	toks = &Tokens{AccessToken: "opaque"}
	codexIdentity(context.Background(), nil, toks)
	if toks.Email != "" || toks.AccountID != "" {
		t.Fatalf("opaque token: %+v", toks)
	}
}

func TestCodexModelsRequestAndParse(t *testing.T) {
	req, err := CodexModelsRequest(context.Background(), "at1", "acc-1")
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://chatgpt.com/backend-api/codex/models?client_version="+CodexCLIVersion {
		t.Fatalf("url: %s", req.URL)
	}
	h := req.Header
	if h.Get("Authorization") != "Bearer at1" || h.Get("ChatGPT-Account-ID") != "acc-1" || h.Get("originator") != "codex_cli_rs" || h.Get("User-Agent") != CodexUserAgent {
		t.Fatalf("headers: %v", h)
	}
	got, err := ParseCodexModels([]byte(`{"models":[{"slug":"gpt-5.6-sol","visibility":"list"},{"slug":"internal-x","visibility":"hide"},{"slug":"gpt-6-luna"}]}`))
	if err != nil || len(got) != 2 || got[0] != "gpt-5.6-sol" || got[1] != "gpt-6-luna" {
		t.Fatalf("parse: %v %v", got, err)
	}
	if _, err := ParseCodexModels([]byte(`{"other":1}`)); err == nil {
		t.Fatal("unknown shape: want an error")
	}
}
