package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Codex signs in with ChatGPT as the Codex CLI does (9router registry/codex.js
// and lib/oauth/providers/codex.js, MIT): the CLI's public client with PKCE,
// form token bodies, and the CLI's fixed loopback redirect. That redirect
// can't reach the dashboard, so the admin pastes the address it lands on.

// CodexCLIVersion is the Codex CLI version the backend sees in the identity
// headers (9router's CODEX_CLI_VERSION).
const CodexCLIVersion = "0.159.0"

// CodexOriginator names the official client to ChatGPT's backend.
const CodexOriginator = "codex_cli_rs"

// CodexUserAgent is the Codex CLI's UA.
const CodexUserAgent = CodexOriginator + "/" + CodexCLIVersion

// CodexAPIURL serves the Codex CLI's Responses endpoint and its model list.
// A var so tests can point it at a fake.
var CodexAPIURL = "https://chatgpt.com/backend-api/codex"

// CodexRedirectURI is the only redirect the Codex CLI's client accepts.
const CodexRedirectURI = "http://localhost:1455/auth/callback"

var codex = &Flow{
	ID:           "codex",
	AuthorizeURL: "https://auth.openai.com/oauth/authorize",
	TokenURL:     "https://auth.openai.com/oauth/token",
	ClientID:     "app_EMoamEEZ73f0CkXaXp7hrann",
	Scope:        "openid profile email offline_access",
	PKCE:         true,
	CallbackPath: "/auth/callback",
	RedirectURI:  CodexRedirectURI,
	AuthorizeExtra: url.Values{
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {CodexOriginator},
	},
	tokensEncoding:  "form",
	refreshEncoding: "application/x-www-form-urlencoded",
	refreshBody: func(f *Flow, refreshToken string) ([]byte, error) {
		return []byte(url.Values{
			"grant_type": {"refresh_token"}, "refresh_token": {refreshToken},
			"client_id": {f.ClientID}, "scope": {f.Scope},
		}.Encode()), nil
	},
}

// codexIdentity reads the email and the ChatGPT account (workspace) out of
// the id_token, as 9router's extractCodexAccountInfo does. The token comes
// straight from OpenAI's token endpoint over TLS, so its claims are read
// without verifying the signature.
func codexIdentity(_ context.Context, _ *http.Client, t *Tokens) {
	claims := jwtClaims(t.IDToken)
	if claims == nil {
		claims = jwtClaims(t.AccessToken)
	}
	if claims == nil {
		return
	}
	var c struct {
		Email     string `json:"email"`
		AccountID string `json:"account_id"`
		Auth      struct {
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
		Profile struct {
			Email string `json:"email"`
		} `json:"https://api.openai.com/profile"`
	}
	if json.Unmarshal(claims, &c) != nil {
		return
	}
	t.Email = c.Email
	if t.Email == "" {
		t.Email = c.Profile.Email
	}
	t.AccountID = c.Auth.AccountID
	if t.AccountID == "" {
		t.AccountID = c.AccountID
	}
}

// jwtClaims is a JWT's decoded payload, or nil.
func jwtClaims(token string) []byte {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	return b
}

// SetCodexHeaders adds the Codex CLI's identity headers and the account
// binding: ChatGPT-Account-ID keeps a token from landing on another
// workspace of the same login.
func SetCodexHeaders(h http.Header, token, accountID string) {
	h.Set("Authorization", "Bearer "+token)
	h.Set("originator", CodexOriginator)
	h.Set("User-Agent", CodexUserAgent)
	h.Set("version", CodexCLIVersion)
	if accountID != "" {
		h.Set("ChatGPT-Account-ID", accountID)
	}
}

// CodexModelsRequest lists the models the signed-in account may use (the
// Models page's catalog sync and the account Test button).
func CodexModelsRequest(ctx context.Context, token, accountID string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CodexAPIURL+"/models?client_version="+CodexCLIVersion, nil)
	if err != nil {
		return nil, err
	}
	SetCodexHeaders(req.Header, token, accountID)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// ParseCodexModels reads model IDs from the Codex models list:
// {"models":[{"slug":…}]}, or the OpenAI {"data":[{"id":…}]} shape. Models
// the CLI hides from its picker are skipped.
func ParseCodexModels(body []byte) ([]string, error) {
	type model struct {
		ID         string `json:"id"`
		Slug       string `json:"slug"`
		Visibility string `json:"visibility"`
	}
	var wire struct {
		Models []model `json:"models"`
		Data   []model `json:"data"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, err
	}
	var out []string
	for _, m := range append(wire.Models, wire.Data...) {
		id := m.Slug
		if id == "" {
			id = m.ID
		}
		if id != "" && m.Visibility != "hide" {
			out = append(out, id)
		}
	}
	if out == nil && wire.Models == nil && wire.Data == nil {
		return nil, fmt.Errorf("unexpected models shape")
	}
	return out, nil
}
