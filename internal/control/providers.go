package control

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"qanat/internal/core/oauth"
	"qanat/internal/core/provider"
	"qanat/internal/proxy"
	"qanat/internal/store/db"
)

// GET /api/admin/providers/{provider}/models — the provider's real model
// catalog, fetched with a stored account's own credential. The Models page
// otherwise only shows what admins have priced; this answers "what can this
// provider actually serve right now".
func (s *Server) providerModels(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	providerID := r.PathValue("provider")
	if !provider.Known(providerID) {
		fail(w, http.StatusBadRequest, "invalid_provider", `Provider must be "claude", "codex" or "antigravity".`)
		return
	}
	// First usable account with a credential, in pool order.
	rows, err := s.q.ListAccounts(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	var pick db.ListAccountsRow
	found := false
	for _, a := range rows {
		if a.Provider == providerID && a.HasCredentials && a.DisabledAt == nil && a.State != "disconnected" {
			pick = a
			found = true
			break
		}
	}
	if !found {
		fail(w, http.StatusConflict, "no_account",
			"No enabled account for this provider. Add and enable one, then load its catalog.")
		return
	}
	admin, err := s.q.GetAccountAdmin(r.Context(), pick.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if s.opts.Box == nil {
		fail(w, http.StatusConflict, "credentials_unreadable", "QANAT_MASTER_KEY isn't set on the server, so stored credentials can't be read.")
		return
	}
	plain, err := s.opts.Box.Open(admin.Credentials, []byte(admin.ID))
	if err != nil {
		fail(w, http.StatusConflict, "credentials_unreadable", "The stored credential can't be read with the current master key. Save the credential again.")
		return
	}
	var creds struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(plain, &creds); err != nil || creds.AccessToken == "" {
		fail(w, http.StatusConflict, "credentials_unreadable", "The stored credential can't be read with the current master key. Save the credential again.")
		return
	}

	base := deref(admin.BaseUrl)
	if base == "" {
		base = testUpstreamBase[providerID]
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	req, err := modelsProbe(ctx, providerID, admin.AuthKind, creds.AccessToken, base)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	client := &http.Client{}
	if spec := s.accountProxySpec(r.Context(), admin); spec != nil {
		client.Transport = proxy.NewTransport(spec)
	}
	start := s.now()
	resp, err := client.Do(req)
	if err != nil {
		fail(w, http.StatusBadGateway, "provider_unreachable", cleanUpstreamError(err))
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fail(w, http.StatusBadGateway, "provider_refused", upstreamErrorMessage(resp.StatusCode, body))
		return
	}
	models, err := parseModelCatalog(providerID, body)
	if err != nil {
		fail(w, http.StatusBadGateway, "provider_unparseable", "The provider's model list couldn't be read: "+err.Error())
		return
	}
	s.audit(r.Context(), nil, "provider.models", "provider", providerID,
		nil, map[string]any{"count": len(models), "latency_ms": time.Since(start).Milliseconds()})
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// modelsProbe builds the provider's own models-list request for a credential.
func modelsProbe(ctx context.Context, providerID, authKind, token, base string) (*http.Request, error) {
	base = strings.TrimSuffix(base, "/")
	var req *http.Request
	var err error
	switch providerID {
	case "antigravity":
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1beta/models", nil)
		if err == nil {
			req.Header.Set("X-Goog-Api-Key", token)
		}
	default: // claude and codex both expose GET /v1/models
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
		if err == nil {
			switch {
			case providerID == "claude" && authKind == "api_key":
				req.Header.Set("X-Api-Key", token)
			case providerID == "claude":
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("anthropic-beta", oauth.OAuthBeta)
			default:
				req.Header.Set("Authorization", "Bearer "+token)
			}
			if providerID == "claude" {
				req.Header.Set("anthropic-version", "2023-06-01")
			}
		}
	}
	return req, err
}

// parseModelCatalog reads the provider's model IDs out of its list response:
// Anthropic and OpenAI both use {"data":[{"id":…}]}, Google uses
// {"models":[{"name":"models/…"}]}.
func parseModelCatalog(providerID string, body []byte) ([]string, error) {
	var out []string
	if providerID == "antigravity" {
		var wire struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &wire); err != nil {
			return nil, err
		}
		for _, m := range wire.Models {
			out = append(out, strings.TrimPrefix(m.Name, "models/"))
		}
		return out, nil
	}
	var wire struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, err
	}
	for _, m := range wire.Data {
		out = append(out, m.ID)
	}
	return out, nil
}
