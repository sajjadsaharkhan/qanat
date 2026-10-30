package control

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
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
