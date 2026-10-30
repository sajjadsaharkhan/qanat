package control

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"qanat/internal/core/oauth"
	"qanat/internal/store/db"
)

// fakeAntigravity points the Antigravity flow at a fake Google: sign-in
// tokens expire in expiresIn seconds, every refresh issues "ya29.refreshed-N",
// and refresh token "dead" is revoked.
func fakeAntigravity(t *testing.T, expiresIn string) *atomic.Int32 {
	t.Helper()
	var refreshes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			r.ParseForm()
			switch {
			case r.PostForm.Get("grant_type") == "authorization_code":
				io.WriteString(w, `{"access_token":"ya29.signin","refresh_token":"1//rt","expires_in":`+expiresIn+`}`)
			case r.PostForm.Get("refresh_token") == "dead":
				w.WriteHeader(400)
				io.WriteString(w, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`)
			default:
				n := refreshes.Add(1)
				io.WriteString(w, `{"access_token":"ya29.refreshed-`+string(rune('0'+n))+`","expires_in":3599}`)
			}
		case "/userinfo":
			io.WriteString(w, `{"email":"bob@gmail.com"}`)
		case "/v1internal:loadCodeAssist":
			io.WriteString(w, `{"cloudaicompanionProject":"proj-7","allowedTiers":[{"id":"standard-tier","isDefault":true}]}`)
		case "/v1internal:onboardUser":
			io.WriteString(w, `{"done":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	f := oauth.Get("antigravity")
	orig, origCC := *f, oauth.CloudCodeURL
	f.TokenURL = srv.URL + "/token"
	f.ProfileURL = srv.URL + "/userinfo"
	oauth.CloudCodeURL = srv.URL
	t.Cleanup(func() { *f, oauth.CloudCodeURL = orig, origCC })
	return &refreshes
}

func (h *harness) signInAntigravity(c *http.Client) string {
	h.t.Helper()
	var auth struct {
		State string `json:"state"`
	}
	if st := h.do(c, "GET", "/api/admin/accounts/oauth/antigravity/authorize?redirect_uri=http%3A%2F%2Flocalhost%3A3000%2Fcallback", "", &auth); st != 200 {
		h.t.Fatalf("authorize: %d", st)
	}
	var ex struct {
		ID string `json:"id"`
	}
	if st := h.do(c, "POST", "/api/admin/accounts/oauth/antigravity/exchange",
		`{"code":"4/abc","state":"`+auth.State+`","redirect_uri":"http://localhost:3000/callback"}`, &ex); st != 201 {
		h.t.Fatalf("exchange: %d", st)
	}
	return ex.ID
}

// sealedCreds opens an account's stored credentials.
func (h *harness) sealedCreds(id string) oauth.Credentials {
	h.t.Helper()
	a, err := h.s.q.GetAccountAdmin(h.t.Context(), id)
	if err != nil {
		h.t.Fatal(err)
	}
	plain, err := h.s.opts.Box.Open(a.Credentials, []byte(id))
	if err != nil {
		h.t.Fatal(err)
	}
	var c oauth.Credentials
	if err := json.Unmarshal(plain, &c); err != nil {
		h.t.Fatal(err)
	}
	return c
}

// Antigravity signs in with Google: the account carries the Google email and
// its Cloud Code project, sealed with the tokens.
func TestOAuthSignInAntigravity(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	h := newOAuthHarness(t)
	fakeAntigravity(t, "3599")
	c := h.signInRoot()

	id := h.signInAntigravity(c)
	var accounts []map[string]any
	h.do(c, "GET", "/api/admin/accounts", "", &accounts)
	if len(accounts) != 1 || accounts[0]["provider"] != "antigravity" || accounts[0]["auth_kind"] != "oauth" ||
		accounts[0]["email"] != "bob@gmail.com" {
		t.Fatalf("accounts: %v", accounts)
	}
	creds := h.sealedCreds(id)
	if creds.AccessToken != "ya29.signin" || creds.RefreshToken != "1//rt" || creds.ProjectID != "proj-7" {
		t.Fatalf("sealed credentials: %+v", creds)
	}
}

// REQ-OAUTH-03: a token about to expire is refreshed by the sweep; the
// refresh token and project survive; a fresh token is left alone; a revoked
// refresh token disconnects the account.
func TestOAuthRefreshSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	h := newOAuthHarness(t)
	refreshes := fakeAntigravity(t, "60") // inside oauth.RefreshLead
	c := h.signInRoot()
	id := h.signInAntigravity(c)

	h.s.refreshDue(t.Context())
	if n := refreshes.Load(); n != 1 {
		t.Fatalf("refreshes after first sweep: %d", n)
	}
	creds := h.sealedCreds(id)
	if creds.AccessToken != "ya29.refreshed-1" || creds.RefreshToken != "1//rt" || creds.ProjectID != "proj-7" {
		t.Fatalf("refreshed credentials: %+v", creds)
	}
	a, err := h.s.q.GetAccountAdmin(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if a.ExpiresAt == nil || time.Until(*a.ExpiresAt) < 50*time.Minute || a.LastRefreshAt == nil {
		t.Fatalf("expiry/refresh time: %v %v", a.ExpiresAt, a.LastRefreshAt)
	}

	h.s.refreshDue(t.Context()) // now valid for an hour: nothing to do
	if n := refreshes.Load(); n != 1 {
		t.Fatalf("a fresh token was refreshed again: %d", n)
	}

	// A 401 nudge refreshes even a fresh token, but not twice in a row.
	h.s.q.SetOAuthRefreshFailed(t.Context(), db.SetOAuthRefreshFailedParams{ID: id, LastRefreshAt: ptr(time.Now().Add(-time.Minute))})
	h.s.refreshAccount(t.Context(), id, true)
	h.s.refreshAccount(t.Context(), id, true)
	if n := refreshes.Load(); n != 2 {
		t.Fatalf("forced refreshes: %d, want exactly one more", n)
	}

	// Revoked: the account leaves the pool with a reason.
	creds = h.sealedCreds(id)
	creds.RefreshToken = "dead"
	b, _ := json.Marshal(creds)
	sealed, _ := h.s.opts.Box.Seal(b, []byte(id))
	if _, err := h.s.q.SetOAuthRefreshed(t.Context(), db.SetOAuthRefreshedParams{
		ID: id, Credentials: sealed, ExpiresAt: ptr(time.Now().Add(time.Minute)), LastRefreshAt: ptr(time.Now().Add(-time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}
	h.s.refreshDue(t.Context())
	var accounts []map[string]any
	h.do(c, "GET", "/api/admin/accounts", "", &accounts)
	if accounts[0]["state"] != "disconnected" || accounts[0]["last_refresh_error"] == nil {
		t.Fatalf("revoked account: %v", accounts[0])
	}
}
