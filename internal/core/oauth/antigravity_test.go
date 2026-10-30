package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGoogle answers the token, userinfo and Cloud Code endpoints. project is
// what loadCodeAssist reports; "" makes onboarding hand one out.
type fakeGoogle struct {
	*httptest.Server
	mu        sync.Mutex
	project   string
	tokenForm url.Values
	onboards  int
	loadAuth  string
	loadUA    string
}

func newFakeGoogle(t *testing.T, project string) *fakeGoogle {
	t.Helper()
	g := &fakeGoogle{project: project}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			r.ParseForm()
			g.tokenForm = r.PostForm
			if r.PostForm.Get("refresh_token") == "dead" {
				w.WriteHeader(400)
				io.WriteString(w, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`)
				return
			}
			io.WriteString(w, `{"access_token":"ya29.a","refresh_token":"1//rt","expires_in":3599,"scope":"openid"}`)
		case "/userinfo":
			io.WriteString(w, `{"email":"bob@gmail.com"}`)
		case "/v1internal:loadCodeAssist":
			g.loadAuth, g.loadUA = r.Header.Get("Authorization"), r.Header.Get("User-Agent")
			if g.project == "" {
				io.WriteString(w, `{"allowedTiers":[{"id":"free-tier","isDefault":true}]}`)
				return
			}
			io.WriteString(w, `{"cloudaicompanionProject":"`+g.project+`","allowedTiers":[{"id":"standard-tier","isDefault":true}]}`)
		case "/v1internal:onboardUser":
			g.onboards++
			var body struct {
				TierID string `json:"tierId"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if g.onboards < 2 {
				io.WriteString(w, `{"done":false}`)
				return
			}
			io.WriteString(w, `{"done":true,"response":{"cloudaicompanionProject":{"id":"proj-`+body.TierID+`"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(g.Close)
	orig, origCC, origWait := *antigravity, CloudCodeURL, onboardWait
	antigravity.TokenURL = g.URL + "/token"
	antigravity.ProfileURL = g.URL + "/userinfo"
	CloudCodeURL = g.URL
	onboardWait = time.Millisecond
	t.Cleanup(func() { *antigravity, CloudCodeURL, onboardWait = orig, origCC, origWait })
	return g
}

func TestBeginAntigravityAuthorizeURL(t *testing.T) {
	authURL, state, p, err := Begin("antigravity", "http://localhost:3000/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "accounts.google.com" || u.Path != "/o/oauth2/v2/auth" {
		t.Fatalf("authorize URL: %s", authURL)
	}
	q := u.Query()
	if q.Get("client_id") != antigravity.ClientID || q.Get("response_type") != "code" || q.Get("state") != state {
		t.Fatalf("client params: %v", q)
	}
	if q.Get("access_type") != "offline" || q.Get("prompt") != "consent" {
		t.Fatalf("offline/consent params: %v", q)
	}
	if !strings.Contains(q.Get("scope"), "auth/cloud-platform") || !strings.Contains(q.Get("scope"), "userinfo.email") {
		t.Fatalf("scope: %q", q.Get("scope"))
	}
	if q.Has("code") || q.Has("code_challenge") || q.Has("client_secret") {
		t.Fatalf("Google's flow has no code=true, PKCE or secret in the URL: %v", q)
	}
	if p.Verifier != "" {
		t.Fatal("no PKCE verifier expected")
	}
}

func TestExchangeAntigravityExistingProject(t *testing.T) {
	g := newFakeGoogle(t, "proj-123")
	_, _, p, _ := Begin("antigravity", "http://localhost:3000/callback")
	toks, err := antigravity.Exchange(context.Background(), g.Client(), "4/code", "s", "http://localhost:3000/callback", p)
	if err != nil {
		t.Fatal(err)
	}
	if toks.AccessToken != "ya29.a" || toks.RefreshToken != "1//rt" || toks.ExpiresIn != 3599 {
		t.Fatalf("tokens: %+v", toks)
	}
	if toks.Email != "bob@gmail.com" || toks.ProjectID != "proj-123" {
		t.Fatalf("setup: email %q project %q", toks.Email, toks.ProjectID)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	f := g.tokenForm
	if f.Get("grant_type") != "authorization_code" || f.Get("code") != "4/code" || f.Get("client_secret") != antigravity.ClientSecret {
		t.Fatalf("exchange form: %v", f)
	}
	if g.loadAuth != "Bearer ya29.a" || g.loadUA != AntigravityUserAgent {
		t.Fatalf("loadCodeAssist headers: %q %q", g.loadAuth, g.loadUA)
	}
}

func TestExchangeAntigravityOnboardsNewAccount(t *testing.T) {
	g := newFakeGoogle(t, "")
	_, _, p, _ := Begin("antigravity", "http://localhost:3000/callback")
	toks, err := antigravity.Exchange(context.Background(), g.Client(), "c", "s", "http://localhost:3000/callback", p)
	if err != nil {
		t.Fatal(err)
	}
	if toks.ProjectID != "proj-free-tier" {
		t.Fatalf("project from onboarding: %q", toks.ProjectID)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.onboards != 2 {
		t.Fatalf("onboardUser calls: %d, want polling until done", g.onboards)
	}
}

func TestRefreshAntigravity(t *testing.T) {
	g := newFakeGoogle(t, "p")
	toks, err := antigravity.Refresh(context.Background(), g.Client(), "1//old")
	if err != nil {
		t.Fatal(err)
	}
	if toks.AccessToken != "ya29.a" {
		t.Fatalf("tokens: %+v", toks)
	}
	g.mu.Lock()
	f := g.tokenForm
	g.mu.Unlock()
	if f.Get("grant_type") != "refresh_token" || f.Get("refresh_token") != "1//old" ||
		f.Get("client_id") != antigravity.ClientID || f.Get("client_secret") != antigravity.ClientSecret {
		t.Fatalf("refresh form: %v", f)
	}

	_, err = antigravity.Refresh(context.Background(), g.Client(), "dead")
	var unrecoverable *UnrecoverableError
	if !errors.As(err, &unrecoverable) {
		t.Fatalf("revoked refresh token must be unrecoverable, got %v", err)
	}
}

func TestCredentialsRefreshedKeepsUnrotated(t *testing.T) {
	c := Credentials{AccessToken: "old", RefreshToken: "rt", IDToken: "id", ProjectID: "proj"}
	got := c.Refreshed(&Tokens{AccessToken: "new"})
	if got != (Credentials{AccessToken: "new", RefreshToken: "rt", IDToken: "id", ProjectID: "proj"}) {
		t.Fatalf("refreshed: %+v", got)
	}
	got = c.Refreshed(&Tokens{AccessToken: "new", RefreshToken: "rt2"})
	if got.RefreshToken != "rt2" {
		t.Fatalf("rotated refresh token dropped: %+v", got)
	}
}

func TestParseAvailableModels(t *testing.T) {
	got, err := ParseAvailableModels([]byte(`{"models":{"gemini-3.1-pro-high":{"displayName":"x"},"claude-opus-5-5":{},"chat_internal":{"isInternal":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "claude-opus-5-5,gemini-3.1-pro-high" {
		t.Fatalf("object form: %v", got)
	}
	got, err = ParseAvailableModels([]byte(`{"models":[{"id":"a"},{"name":"models/b"}]}`))
	if err != nil || strings.Join(got, ",") != "a,b" {
		t.Fatalf("list form: %v %v", got, err)
	}
}
