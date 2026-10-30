package control

import (
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"qanat/internal/core/oauth"
	"qanat/internal/core/secretbox"
	"qanat/internal/platform"
	"qanat/internal/store/storetest"
	"qanat/internal/testkit/itenv"
)

// newOAuthHarness is the control harness with a master key, plus the Claude
// flow pointed at a fake provider.
func newOAuthHarness(t *testing.T) *harness {
	t.Helper()
	pg := storetest.DB(t)
	rdb := platform.Redis(itenv.Redis(t))
	t.Cleanup(func() { rdb.Close() })
	box, err := secretbox.New(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	s := New(pg, rdb, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{RootPassword: rootPW, Box: box})
	if err := s.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &harness{t, s, srv}
}

// fakeClaude swaps the Claude flow's endpoints for a fake provider answering
// with a fixed token set and profile.
func fakeClaude(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			w.Write([]byte(`{"access_token":"sk-ant-oat01-test","refresh_token":"rt1","expires_in":3600,"scope":"user:inference"}`))
		case "/profile":
			w.Write([]byte(`{"account":{"uuid":"u1","email":"alice@example.com"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	f := oauth.Get("claude")
	orig := *f
	f.TokenURL = srv.URL + "/token"
	f.ProfileURL = srv.URL + "/profile"
	t.Cleanup(func() { *f = orig })
}

// signInOut runs one full sign-in against the fake provider and returns (id, merged, status).
func (h *harness) signInOut(c *http.Client, code string) (string, bool, int) {
	h.t.Helper()
	var auth struct {
		AuthURL     string `json:"auth_url"`
		State       string `json:"state"`
		RedirectURI string `json:"redirect_uri"`
	}
	st := h.do(c, "GET", "/api/admin/accounts/oauth/claude/authorize?redirect_uri=http%3A%2F%2Flocalhost%3A3000%2Fcallback", "", &auth)
	if st != 200 {
		h.t.Fatalf("authorize: %d", st)
	}
	var ex struct {
		ID     string `json:"id"`
		Merged bool   `json:"merged"`
	}
	st = h.do(c, "POST", "/api/admin/accounts/oauth/claude/exchange",
		`{"code":"`+code+`","state":"`+auth.State+`","redirect_uri":"http://localhost:3000/callback"}`, &ex)
	return ex.ID, ex.Merged, st
}

// REQ-OAUTH-01: the dashboard-initiated sign-in creates an OAuth account whose
// tokens are sealed, and a second sign-in with the same identity merges into
// the same account instead of adding a duplicate.
func TestOAuthSignInClaude(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	h := newOAuthHarness(t)
	fakeClaude(t)
	c := h.signInRoot()

	id1, merged, st := h.signInOut(c, "code-1")
	if st != 201 || merged || id1 == "" {
		t.Fatalf("first sign-in: status %d merged %v id %q", st, merged, id1)
	}

	var accounts []map[string]any
	h.do(c, "GET", "/api/admin/accounts", "", &accounts)
	if len(accounts) != 1 {
		t.Fatalf("want 1 account, got %d", len(accounts))
	}
	a := accounts[0]
	if a["auth_kind"] != "oauth" || a["email"] != "alice@example.com" || a["label"] != "alice@example.com" {
		t.Fatalf("account: %v", a)
	}
	if a["has_credentials"] != true {
		t.Fatalf("credentials not stored: %v", a)
	}

	id2, merged, st := h.signInOut(c, "code-2")
	if st != 200 || !merged || id2 != id1 {
		t.Fatalf("second sign-in: status %d merged %v id %q (was %q)", st, merged, id2, id1)
	}
	accounts = nil
	h.do(c, "GET", "/api/admin/accounts", "", &accounts)
	if len(accounts) != 1 {
		t.Fatalf("merge must not add an account, got %d", len(accounts))
	}
}

func TestOAuthSignInRejectedInputs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	h := newOAuthHarness(t)
	fakeClaude(t)
	c := h.signInRoot()

	if st := h.do(c, "GET", "/api/admin/accounts/oauth/claude/authorize?redirect_uri=https%3A%2F%2Fevil.example%2Fcallback", "", nil); st != 400 {
		t.Fatalf("non-loopback redirect: %d", st)
	}
	if st := h.do(c, "GET", "/api/admin/accounts/oauth/openrouter/authorize?redirect_uri=http%3A%2F%2Flocalhost%3A3000%2Fcallback", "", nil); st != 400 {
		t.Fatalf("provider without a flow: %d", st)
	}
	if st := h.do(c, "POST", "/api/admin/accounts/oauth/claude/exchange",
		`{"code":"x","state":"bogus","redirect_uri":"http://localhost:3000/callback"}`, nil); st != 400 {
		t.Fatalf("unknown state: %d", st)
	}

	// A pasted "OAuth credential" is refused; the flow is the only way in.
	if st := h.do(c, "POST", "/api/admin/accounts",
		`{"provider":"claude","auth_kind":"oauth","access_token":"sk-ant-oat01-paste"}`, nil); st != 400 {
		t.Fatalf("pasted oauth credential: %d", st)
	}
	if st := h.do(c, "POST", "/api/admin/accounts/validate",
		`{"provider":"claude","auth_kind":"oauth","access_token":"sk-ant-oat01-paste"}`, nil); st != 400 {
		t.Fatalf("pasted oauth validate: %d", st)
	}

	// And an OAuth account's stored tokens can't be overwritten via PATCH.
	id, _, _ := h.signInOut(c, "code-1")
	if st := h.do(c, "PATCH", "/api/admin/accounts/"+id, `{"access_token":"overwrite"}`, nil); st != 400 {
		t.Fatalf("PATCH oauth credential: %d", st)
	}
	var accounts []map[string]any
	h.do(c, "GET", "/api/admin/accounts", "", &accounts)
	if len(accounts) != 1 {
		t.Fatalf("want 1 account, got %d", len(accounts))
	}
	if !strings.Contains(accounts[0]["label"].(string), "@") {
		t.Fatalf("label should be the email: %v", accounts[0])
	}
}
