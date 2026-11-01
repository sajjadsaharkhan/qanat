package control

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"qanat/internal/core/oauth"
)

// fakeCodex points the Codex flow at a fake OpenAI whose id_token names the
// email and ChatGPT account.
func fakeCodex(t *testing.T) {
	t.Helper()
	enc := base64.RawURLEncoding.EncodeToString
	idToken := enc([]byte(`{"alg":"none"}`)) + "." +
		enc([]byte(`{"email":"carol@example.com","https://api.openai.com/auth":{"chatgpt_account_id":"acc-42"}}`)) + ".sig"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.URL.Path != "/token" || r.PostForm.Get("redirect_uri") != oauth.CodexRedirectURI {
			http.Error(w, `{"error":"invalid_request"}`, 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"cx-at","refresh_token":"cx-rt","id_token":"`+idToken+`","expires_in":3600}`)
	}))
	t.Cleanup(srv.Close)
	f := oauth.Get("codex")
	orig := *f
	f.TokenURL = srv.URL + "/token"
	t.Cleanup(func() { *f = orig })
}

// Codex's client only accepts the CLI's loopback redirect: authorize answers
// with it whatever the dashboard asked for, and the exchange of the pasted
// code creates an account bound to the id_token's ChatGPT account.
func TestOAuthSignInCodex(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	h := newOAuthHarness(t)
	fakeCodex(t)
	c := h.signInRoot()

	var auth struct {
		AuthURL     string `json:"auth_url"`
		State       string `json:"state"`
		RedirectURI string `json:"redirect_uri"`
	}
	if st := h.do(c, "GET", "/api/admin/accounts/oauth/codex/authorize?redirect_uri=http%3A%2F%2Flocalhost%3A3001%2Fcallback", "", &auth); st != 200 {
		t.Fatalf("authorize: %d", st)
	}
	if auth.RedirectURI != oauth.CodexRedirectURI {
		t.Fatalf("redirect_uri: %q", auth.RedirectURI)
	}
	var ex struct {
		ID string `json:"id"`
	}
	if st := h.do(c, "POST", "/api/admin/accounts/oauth/codex/exchange",
		`{"code":"ac_1","state":"`+auth.State+`","redirect_uri":"`+auth.RedirectURI+`"}`, &ex); st != 201 {
		t.Fatalf("exchange: %d", st)
	}
	var accounts []map[string]any
	h.do(c, "GET", "/api/admin/accounts", "", &accounts)
	if len(accounts) != 1 || accounts[0]["provider"] != "codex" || accounts[0]["auth_kind"] != "oauth" || accounts[0]["email"] != "carol@example.com" {
		t.Fatalf("accounts: %v", accounts)
	}
	creds := h.sealedCreds(ex.ID)
	if creds.AccessToken != "cx-at" || creds.RefreshToken != "cx-rt" || creds.AccountID != "acc-42" {
		t.Fatalf("sealed credentials: %+v", creds)
	}
}

// The Codex models list carries each model's instructions and outgrew the
// old 512 KB read cap, which cut the JSON off ("unexpected end of JSON
// input"). A list past that size syncs whole; an empty answer fails without
// wiping the stored catalog.
func TestSyncCodexCatalog(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	h := newOAuthHarness(t)
	fakeCodex(t)
	c := h.signInRoot()
	var auth struct {
		State       string `json:"state"`
		RedirectURI string `json:"redirect_uri"`
	}
	h.do(c, "GET", "/api/admin/accounts/oauth/codex/authorize?redirect_uri=http%3A%2F%2Flocalhost%3A3001%2Fcallback", "", &auth)
	if st := h.do(c, "POST", "/api/admin/accounts/oauth/codex/exchange",
		`{"code":"ac_1","state":"`+auth.State+`","redirect_uri":"`+auth.RedirectURI+`"}`, nil); st != 201 {
		t.Fatalf("exchange: %d", st)
	}

	pad := strings.Repeat("x", 600<<10)
	var body atomic.Pointer[string]
	full := `{"models":[{"slug":"gpt-6.1-sol","base_instructions":"` + pad + `"},{"slug":"gpt-6-luna"}]}`
	body.Store(&full)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer cx-at" {
			http.Error(w, "unexpected request", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, *body.Load())
	}))
	t.Cleanup(upstream.Close)
	orig := oauth.CodexAPIURL
	oauth.CodexAPIURL = upstream.URL
	t.Cleanup(func() { oauth.CodexAPIURL = orig })

	var synced struct {
		Models []string `json:"models"`
	}
	if st := h.do(c, "POST", "/api/admin/providers/codex/models", "", &synced); st != 200 {
		t.Fatalf("sync: %d", st)
	}
	if strings.Join(synced.Models, ",") != "gpt-6.1-sol,gpt-6-luna" {
		t.Fatalf("synced models: %v", synced.Models)
	}

	empty := ""
	body.Store(&empty)
	var failed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if st := h.do(c, "POST", "/api/admin/providers/codex/models", "", &failed); st != 502 {
		t.Fatalf("empty sync: %d", st)
	}
	if !strings.Contains(failed.Error.Message, "empty model list") {
		t.Fatalf("empty sync message: %q", failed.Error.Message)
	}
	var stored struct {
		Models []string `json:"models"`
	}
	h.do(c, "GET", "/api/admin/providers/codex/models", "", &stored)
	if len(stored.Models) != 2 {
		t.Fatalf("stored catalog after failed sync: %v", stored.Models)
	}
}
