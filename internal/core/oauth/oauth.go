// Package oauth implements the provider sign-in flows (PRD §6.6,
// REQ-OAUTH-01), ported from 9router's src/lib/oauth/providers (MIT). Each
// flow is the official client's own authorization-code flow: same public
// client ID, same endpoints, PKCE where the client uses it. Tokens returned
// here are sealed by the caller before storage.
package oauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Flow is one provider's sign-in flow. Endpoint fields are vars so tests can
// point a flow at a fake provider.
type Flow struct {
	ID           string
	AuthorizeURL string
	TokenURL     string
	ClientID     string
	ClientSecret string // only flows that need it (Antigravity)
	Scope        string
	PKCE         bool
	// CallbackPath is the redirect path the dashboard page listens on.
	CallbackPath string
	// AuthorizeExtra are appended to the authorize URL verbatim.
	AuthorizeExtra url.Values
	// ProfileURL, when set, is fetched after a successful exchange for
	// optional identity metadata (the account email). Tests redirect it.
	ProfileURL string
	// PostExchange runs after a successful token exchange; failures only
	// cost optional metadata (e.g. the account email).
	PostExchange func(ctx context.Context, hc *http.Client, t *Tokens)

	tokensEncoding  string // "json" or "form" for the exchange body
	refreshEncoding string
	refreshBody     func(clientID, refreshToken string) ([]byte, error)
}

// Tokens is what a flow yields. Email is "" when the flow doesn't provide one.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	ExpiresIn    int64 // seconds until AccessToken expires
	Scope        string
	Email        string
}

// UnrecoverableError marks failures only a fresh sign-in fixes
// (invalid_grant and friends); anything else may be transient.
type UnrecoverableError struct{ Err error }

func (e *UnrecoverableError) Error() string { return e.Err.Error() }
func (e *UnrecoverableError) Unwrap() error { return e.Err }

// ClaudeUserAgent is the official CLI's UA, sent on requests that must look
// like the client the OAuth token was issued to (9router's CLAUDE_CLI_VERSION).
const ClaudeUserAgent = "claude-cli/2.1.280 (external, sdk-cli)"

// OAuthBeta is the Anthropic beta an OAuth access token requires upstream.
const OAuthBeta = "oauth-2025-04-20"

// claude is the Claude Code CLI flow (9router registry/claude.js): PKCE with
// the CLI's public client ID, JSON token bodies, no client secret anywhere.
var claude = &Flow{
	ID:              "claude",
	AuthorizeURL:    "https://claude.ai/oauth/authorize",
	TokenURL:        "https://api.anthropic.com/v1/oauth/token",
	ClientID:        "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
	Scope:           "org:create_api_key user:profile user:inference",
	PKCE:            true,
	CallbackPath:    "/callback",
	tokensEncoding:  "json",
	refreshEncoding: "application/json",
	refreshBody: func(clientID, refreshToken string) ([]byte, error) {
		return json.Marshal(map[string]string{
			"grant_type": "refresh_token", "refresh_token": refreshToken, "client_id": clientID,
		})
	},
	ProfileURL: "https://api.anthropic.com/api/oauth/profile",
}

func init() { claude.PostExchange = fetchClaudeProfile }

// flows maps provider ID to its flow; providers without an entry answer the
// sign-in endpoints with a clear error until their milestone lands.
var flows = map[string]*Flow{"claude": claude}

// HasFlow reports whether a provider's sign-in flow is implemented.
func HasFlow(providerID string) bool { _, ok := flows[providerID]; return ok }

// Get returns the flow for providerID, or nil when there is none yet.
func Get(providerID string) *Flow { return flows[providerID] }

// FlowNames lists the providers with a sign-in flow.
func FlowNames() []string {
	out := make([]string, 0, len(flows))
	for id := range flows {
		out = append(out, id)
	}
	return out
}

// Pending is the per-sign-in server state an exchange later consumes.
type Pending struct {
	Provider    string
	RedirectURI string
	Verifier    string // PKCE, when the flow uses it
}

// Begin starts a sign-in: it returns the URL to open, the state to key the
// pending attempt by, and the pending to keep until the exchange.
func Begin(providerID, redirectURI string) (authURL, state string, p *Pending, err error) {
	f, ok := flows[providerID]
	if !ok {
		return "", "", nil, fmt.Errorf("no sign-in flow for provider %q", providerID)
	}
	state, err = randomHex(32)
	if err != nil {
		return "", "", nil, err
	}
	q := url.Values{}
	if f.PKCE {
		verifier, err := randomVerifier()
		if err != nil {
			return "", "", nil, err
		}
		sum := sha256.Sum256([]byte(verifier))
		q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
		q.Set("code_challenge_method", "S256")
		p = &Pending{Provider: providerID, RedirectURI: redirectURI, Verifier: verifier}
	} else {
		p = &Pending{Provider: providerID, RedirectURI: redirectURI}
	}
	for k, vs := range f.AuthorizeExtra {
		q[k] = vs
	}
	q.Set("client_id", f.ClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	if f.Scope != "" {
		q.Set("scope", f.Scope)
	}
	return f.AuthorizeURL + "?" + q.Encode() + "&code=true", state, p, nil
}

// Exchange swaps the authorization code for tokens. The code may carry the
// state as a "#fragment" (Claude does); the fragment wins when state is empty.
func (f *Flow) Exchange(ctx context.Context, hc *http.Client, code, state, redirectURI string, p *Pending) (*Tokens, error) {
	if i := strings.IndexByte(code, '#'); i >= 0 {
		if state == "" {
			state = code[i+1:]
		}
		code = code[:i]
	}
	var body []byte
	var ct string
	if f.tokensEncoding == "json" {
		b, err := json.Marshal(map[string]string{
			"code": code, "state": state, "grant_type": "authorization_code",
			"client_id": f.ClientID, "redirect_uri": redirectURI, "code_verifier": p.Verifier,
		})
		if err != nil {
			return nil, err
		}
		body, ct = b, "application/json"
	} else {
		form := url.Values{
			"grant_type": {"authorization_code"}, "client_id": {f.ClientID},
			"code": {code}, "redirect_uri": {redirectURI},
		}
		if p.Verifier != "" {
			form.Set("code_verifier", p.Verifier)
		}
		if f.ClientSecret != "" {
			form.Set("client_secret", f.ClientSecret)
		}
		body, ct = []byte(form.Encode()), "application/x-www-form-urlencoded"
	}
	t, err := f.postToken(ctx, hc, body, ct)
	if err != nil {
		return nil, err
	}
	if f.PostExchange != nil {
		f.PostExchange(ctx, hc, t)
	}
	return t, nil
}

// Refresh trades a refresh token for fresh tokens (OA3 uses this on 401 and
// on the background sweep). The provider may rotate the refresh token; the
// caller must persist the returned one.
func (f *Flow) Refresh(ctx context.Context, hc *http.Client, refreshToken string) (*Tokens, error) {
	body, err := f.refreshBody(f.ClientID, refreshToken)
	if err != nil {
		return nil, err
	}
	return f.postToken(ctx, hc, body, f.refreshEncoding)
}

func (f *Flow) postToken(ctx context.Context, hc *http.Client, body []byte, contentType string) (*Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.TokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("token endpoint answered %d: %s", resp.StatusCode, tokenErrorText(raw))
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && isUnrecoverable(e.Error) {
			return nil, &UnrecoverableError{Err: err}
		}
		return nil, err
	}
	var wire struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		IDToken          string `json:"id_token"`
		ExpiresIn        int64  `json:"expires_in"`
		Scope            string `json:"scope"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("token endpoint sent malformed JSON: %w", err)
	}
	if wire.Error != "" {
		err := fmt.Errorf("token endpoint refused: %s", strings.TrimSpace(wire.Error+" "+wire.ErrorDescription))
		if isUnrecoverable(wire.Error) {
			return nil, &UnrecoverableError{Err: err}
		}
		return nil, err
	}
	if wire.AccessToken == "" {
		return nil, fmt.Errorf("token endpoint answered without an access token")
	}
	return &Tokens{
		AccessToken: wire.AccessToken, RefreshToken: wire.RefreshToken, IDToken: wire.IDToken,
		ExpiresIn: wire.ExpiresIn, Scope: wire.Scope,
	}, nil
}

func isUnrecoverable(oauthErr string) bool {
	for _, s := range []string{"invalid_grant", "invalid_client", "unauthorized_client"} {
		if strings.Contains(oauthErr, s) {
			return true
		}
	}
	return false
}

func tokenErrorText(raw []byte) string {
	var e struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if json.Unmarshal(raw, &e) == nil && (e.Error != "" || e.ErrorDescription != "") {
		return strings.TrimSpace(e.Error + " " + e.ErrorDescription)
	}
	if len(raw) == 0 {
		return "empty body"
	}
	if len(raw) > 200 {
		raw = raw[:200]
	}
	return string(raw)
}

// fetchClaudeProfile pulls the account email from the OAuth profile endpoint
// Claude Code itself uses. Failure only costs dedup-on-next-sign-in.
func fetchClaudeProfile(ctx context.Context, hc *http.Client, t *Tokens) {
	f := claude
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.ProfileURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+t.AccessToken)
	resp, err := hc.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
	if err != nil {
		return
	}
	var p struct {
		Email   string `json:"email"`
		Account struct {
			Email string `json:"email"`
		} `json:"account"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	t.Email = p.Email
	if t.Email == "" {
		t.Email = p.Account.Email
	}
}

func randomHex(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func randomVerifier() (string, error) {
	b := make([]byte, 32) // 256 bits, like the official CLI's PKCE verifier
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// PendingTTL bounds a sign-in attempt (9router's OAUTH_TIMEOUT is 5 min; the
// extra headroom covers slow provider consent screens).
const PendingTTL = 10 * time.Minute
