package control

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"qanat/internal/core/oauth"
	"qanat/internal/core/provider"
	"qanat/internal/proxy"
	"qanat/internal/store/db"
)

// GET /api/admin/providers/{provider}/models — the provider's stored catalog
// (migration 0009). POST …/models/sync refreshes it from the provider itself
// with a stored account's own credential and replaces the stored rows.
func (s *Server) providerModels(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	providerID := r.PathValue("provider")
	if !provider.Known(providerID) {
		fail(w, http.StatusBadRequest, "invalid_provider", `Provider must be "claude", "codex" or "antigravity".`)
		return
	}
	models, err := s.q.ListProviderModels(r.Context(), providerID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (s *Server) syncProviderModels(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	providerID := r.PathValue("provider")
	if !provider.Known(providerID) {
		fail(w, http.StatusBadRequest, "invalid_provider", `Provider must be "claude", "codex" or "antigravity".`)
		return
	}
	models, errMsg, code := s.fetchProviderCatalog(r, providerID)
	if errMsg != "" {
		fail(w, code, "provider_catalog_failed", errMsg)
		return
	}
	err := pgx.BeginFunc(r.Context(), s.pg, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.ReplaceProviderModels(r.Context(), providerID); err != nil {
			return err
		}
		for _, m := range models {
			if err := q.InsertProviderModel(r.Context(), db.InsertProviderModelParams{Provider: providerID, Model: m}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r.Context(), nil, "provider.models.sync", "provider", providerID, nil, map[string]any{"count": len(models)})
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// fetchProviderCatalog asks the provider, through the first usable account's
// own credential and proxy. A non-empty returned message is a user-facing
// failure with its status.
func (s *Server) fetchProviderCatalog(r *http.Request, providerID string) (models []string, errMsg string, status int) {
	// First usable account with a credential, in pool order.
	rows, err := s.q.ListAccounts(r.Context())
	if err != nil {
		return nil, "", http.StatusInternalServerError
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
		return nil, "No enabled account for this provider. Add and enable one, then sync its catalog.", http.StatusConflict
	}
	admin, err := s.q.GetAccountAdmin(r.Context(), pick.ID)
	if err != nil {
		return nil, "", http.StatusInternalServerError
	}
	if s.opts.Box == nil {
		return nil, "QANAT_MASTER_KEY isn't set on the server, so stored credentials can't be read.", http.StatusConflict
	}
	plain, err := s.opts.Box.Open(admin.Credentials, []byte(admin.ID))
	if err != nil {
		return nil, "The stored credential can't be read with the current master key. Save the credential again.", http.StatusConflict
	}
	var creds struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(plain, &creds); err != nil || creds.AccessToken == "" {
		return nil, "The stored credential can't be read with the current master key. Save the credential again.", http.StatusConflict
	}

	base := deref(admin.BaseUrl)
	if base == "" {
		base = testUpstreamBase[providerID]
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	req, err := modelsProbe(ctx, providerID, admin.AuthKind, creds.AccessToken, base)
	if err != nil {
		return nil, "", http.StatusInternalServerError
	}
	client := &http.Client{}
	if spec := s.accountProxySpec(r.Context(), admin); spec != nil {
		client.Transport = proxy.NewTransport(spec)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, cleanUpstreamError(err), http.StatusBadGateway
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, upstreamErrorMessage(resp.StatusCode, body), http.StatusBadGateway
	}
	models, err = parseModelCatalog(providerID, body)
	if err != nil {
		return nil, "The provider's model list couldn't be read: " + err.Error(), http.StatusBadGateway
	}
	return models, "", http.StatusOK
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
