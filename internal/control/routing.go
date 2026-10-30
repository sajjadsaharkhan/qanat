package control

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"qanat/internal/core/oauth"
	"qanat/internal/core/pricing"
	"qanat/internal/core/provider"
	"qanat/internal/proxy"
	"qanat/internal/store/db"
)

// --- accounts (REQ-POOL-01) ----------------------------------------------------

type accountResponse struct {
	ID             string     `json:"id"`
	Provider       string     `json:"provider"`
	Label          string     `json:"label"`
	BaseURL        *string    `json:"base_url"`
	HasCredentials bool       `json:"has_credentials"`
	State          string     `json:"state"`
	CooldownUntil  *time.Time `json:"cooldown_until"`
	CreatedAt      time.Time  `json:"created_at"`
	AuthKind       string     `json:"auth_kind"`
	ExpiresAt      *time.Time `json:"expires_at"`
	DisabledAt     *time.Time `json:"disabled_at"`
	LastCheckAt    *time.Time `json:"last_check_at"`
	LastCheckOk    *bool      `json:"last_check_ok"`
	LastCheckError *string    `json:"last_check_error"`
	Email          *string    `json:"email"` // OAuth identity; null for key/token accounts
	LastRefreshAt  *time.Time `json:"last_refresh_at"`
	LastRefreshErr *string    `json:"last_refresh_error"`
	Position       int64      `json:"position"`
	Proxy          string     `json:"proxy"` // "inherit", "direct", a proxy ID or "group:<id>" (§8.2)
}

func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	rows, err := s.q.ListAccounts(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]accountResponse, 0, len(rows))
	for _, a := range rows {
		var checkOk *bool
		if a.LastCheckOk.Valid {
			v := a.LastCheckOk.Bool
			checkOk = &v
		}
		out = append(out, accountResponse{
			ID: a.ID, Provider: a.Provider, Label: a.Label, BaseURL: a.BaseUrl, HasCredentials: a.HasCredentials,
			State: a.State, CooldownUntil: a.CooldownUntil, CreatedAt: a.CreatedAt,
			AuthKind: a.AuthKind, ExpiresAt: a.ExpiresAt, DisabledAt: a.DisabledAt,
			LastCheckAt: a.LastCheckAt, LastCheckOk: checkOk, LastCheckError: a.LastCheckError,
			Email: a.Email, LastRefreshAt: a.LastRefreshAt, LastRefreshErr: a.LastRefreshError,
			Position: a.Position.Int64, Proxy: proxyChoice{a.ProxyID, a.ProxyGroupID, a.ProxyDirect}.String(),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type createAccountRequest struct {
	Provider string  `json:"provider"`
	Label    string  `json:"label"`
	BaseURL  *string `json:"base_url"`
	// AccessToken is sealed with the master key before storage and never
	// returned. Onboarding stays on official credentials: an API key, or a
	// token from the provider's own tooling such as `claude setup-token`.
	AccessToken *string `json:"access_token"`
	// AuthKind is "token" (default; sent as a bearer token) or "api_key"
	// (sent as x-api-key). Codex and Antigravity only accept "api_key".
	AuthKind  string     `json:"auth_kind"`
	ExpiresAt *time.Time `json:"expires_at"` // optional; the credential's own expiry
	// Proxy is "inherit" (default), "direct", a proxy ID or "group:<id>" (§8.2).
	Proxy string `json:"proxy"`
}

// validAuthKind reports whether kind is accepted for provider, answering with
// the API error when not. "oauth" accounts exist only through the sign-in
// flow; createAccount and validateAccount reject pasted OAuth credentials.
func validAuthKind(w http.ResponseWriter, providerID, kind string) bool {
	if kind != "token" && kind != "api_key" && kind != "oauth" {
		fail(w, http.StatusBadRequest, "invalid_auth_kind", `Auth kind must be "oauth", "token" or "api_key".`)
		return false
	}
	if kind == "token" && providerID != "claude" {
		fail(w, http.StatusBadRequest, "invalid_auth_kind",
			`Codex and Antigravity accounts authenticate with an API key. A pasted OAuth-style token is only accepted for Claude, e.g. the token from "claude setup-token".`)
		return false
	}
	return true
}

// rejectPastedOAuth keeps the sign-in flow the only way to make an OAuth
// account: a pasted access token can't carry the refresh half.
func rejectPastedOAuth(w http.ResponseWriter, kind string) bool {
	if kind == "oauth" {
		fail(w, http.StatusBadRequest, "invalid_auth_kind",
			`OAuth accounts are created with the provider's own sign-in flow, not a pasted credential. Use "Sign in" in the Add-account dialog.`)
		return true
	}
	return false
}

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (s *Server) createAccount(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	var req createAccountRequest
	if !decode(w, r, &req) {
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	if !provider.Known(req.Provider) {
		fail(w, http.StatusBadRequest, "invalid_provider", `Provider must be "claude", "codex" or "antigravity".`)
		return
	}
	if req.Label == "" {
		req.Label = req.Provider + " account"
	}
	if req.BaseURL != nil {
		if u, err := url.Parse(*req.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			fail(w, http.StatusBadRequest, "invalid_base_url", "The base URL must look like https://host[:port].")
			return
		}
	}
	pc, ok := s.parseProxyChoice(w, r, req.Proxy)
	if !ok {
		return
	}
	if req.AuthKind == "" {
		req.AuthKind = "token"
	}
	if !validAuthKind(w, req.Provider, req.AuthKind) {
		return
	}
	if rejectPastedOAuth(w, req.AuthKind) {
		return
	}
	id := newUUID()
	var sealed []byte
	if req.AccessToken != nil && *req.AccessToken != "" {
		if s.opts.Box == nil {
			fail(w, http.StatusBadRequest, "no_master_key", "Credentials can't be stored until QANAT_MASTER_KEY is set on the server.")
			return
		}
		plain, _ := json.Marshal(map[string]string{"access_token": *req.AccessToken})
		var err error
		if sealed, err = s.opts.Box.Seal(plain, []byte(id)); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	if err := s.q.CreateAccount(r.Context(), db.CreateAccountParams{ID: id, Provider: req.Provider, Label: req.Label, BaseUrl: req.BaseURL, Credentials: sealed, AuthKind: req.AuthKind, ExpiresAt: req.ExpiresAt, ProxyID: pc.proxyID, ProxyGroupID: pc.groupID, ProxyDirect: pc.direct}); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r.Context(), &actor.ID, "account.create", "account", id, nil,
		map[string]any{"provider": req.Provider, "label": req.Label, "base_url": req.BaseURL, "auth_kind": req.AuthKind, "has_credentials": sealed != nil, "proxy": pc.String()})
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	n, err := s.q.DeleteAccount(r.Context(), r.PathValue("id"))
	if isBadUUID(err) || (err == nil && n == 0) {
		fail(w, http.StatusNotFound, "not_found", "That account doesn't exist.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r.Context(), &actor.ID, "account.delete", "account", r.PathValue("id"), nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

// updateAccount changes an account's fields; absent ones are kept. The
// credential is write-only: null keeps it, "" removes it, a value re-seals it.
type updateAccountRequest struct {
	Label       *string `json:"label"`
	BaseURL     *string `json:"base_url"` // null keeps, "" clears
	AuthKind    *string `json:"auth_kind"`
	AccessToken *string `json:"access_token"`
	ExpiresAt   *string `json:"expires_at"` // RFC 3339; null keeps, "" clears
	Disabled    *bool   `json:"disabled"`
	Proxy       *string `json:"proxy"`
}

func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	var req updateAccountRequest
	if !decode(w, r, &req) {
		return
	}
	cur, err := s.q.GetAccountAdmin(r.Context(), r.PathValue("id"))
	if isBadUUID(err) || notFound(err) {
		fail(w, http.StatusNotFound, "not_found", "That account doesn't exist.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}

	label := cur.Label
	if req.Label != nil {
		if strings.TrimSpace(*req.Label) == "" {
			fail(w, http.StatusBadRequest, "invalid_label", "The label can't be empty.")
			return
		}
		label = strings.TrimSpace(*req.Label)
	}
	baseURL := cur.BaseUrl
	if req.BaseURL != nil {
		if *req.BaseURL == "" {
			baseURL = nil
		} else {
			if u, err := url.Parse(*req.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				fail(w, http.StatusBadRequest, "invalid_base_url", "The base URL must look like https://host[:port].")
				return
			}
			baseURL = req.BaseURL
		}
	}
	authKind := cur.AuthKind
	if req.AuthKind != nil {
		if !validAuthKind(w, cur.Provider, *req.AuthKind) {
			return
		}
		if rejectPastedOAuth(w, *req.AuthKind) {
			return
		}
		authKind = *req.AuthKind
	}
	expires := cur.ExpiresAt
	if req.ExpiresAt != nil {
		if *req.ExpiresAt == "" {
			expires = nil
		} else {
			t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
			if err != nil {
				fail(w, http.StatusBadRequest, "invalid_expires_at", "Give the expiry as an ISO 8601 date-time, like 2027-01-31T00:00:00Z.")
				return
			}
			expires = &t
		}
	}
	credentials := cur.Credentials
	credChanged := false
	if req.AccessToken != nil {
		if cur.AuthKind == "oauth" {
			fail(w, http.StatusBadRequest, "invalid_auth_kind",
				"This is an OAuth account: its tokens are replaced by signing in again, not by pasting a credential.")
			return
		}
		switch {
		case *req.AccessToken == "":
			credentials = nil
		case s.opts.Box == nil:
			fail(w, http.StatusBadRequest, "no_master_key", "Credentials can't be stored until QANAT_MASTER_KEY is set on the server.")
			return
		default:
			plain, _ := json.Marshal(map[string]string{"access_token": *req.AccessToken})
			sealed, err := s.opts.Box.Seal(plain, []byte(cur.ID))
			if err != nil {
				s.internal(w, r, err)
				return
			}
			credentials = sealed
		}
		credChanged = true
	}
	disabledAt := cur.DisabledAt
	if req.Disabled != nil {
		if *req.Disabled {
			t := s.now()
			disabledAt = &t
		} else {
			disabledAt = nil
		}
	}

	if _, err := s.q.UpdateAccountCore(r.Context(), db.UpdateAccountCoreParams{ID: cur.ID, Label: label, BaseUrl: baseURL, AuthKind: authKind, ExpiresAt: expires, Credentials: credentials, DisabledAt: disabledAt}); err != nil {
		s.internal(w, r, err)
		return
	}
	proxyNote := proxyChoice{cur.ProxyID, cur.ProxyGroupID, cur.ProxyDirect}.String()
	if req.Proxy != nil {
		pc, ok := s.parseProxyChoice(w, r, *req.Proxy)
		if !ok {
			return
		}
		if _, err := s.q.SetAccountProxy(r.Context(), db.SetAccountProxyParams{ID: cur.ID, ProxyID: pc.proxyID, ProxyGroupID: pc.groupID, ProxyDirect: pc.direct}); err != nil {
			s.internal(w, r, err)
			return
		}
		proxyNote = pc.String()
	}
	s.audit(r.Context(), &actor.ID, "account.update", "account", cur.ID, nil,
		map[string]any{"label": label, "auth_kind": authKind, "disabled": disabledAt != nil, "proxy": proxyNote, "credentials_changed": credChanged})
	w.WriteHeader(http.StatusNoContent)
}

// moveAccount swaps an account with its provider neighbor, mirroring
// 9router's connection priority arrows; the gateway tries accounts in this
// order.
type moveAccountRequest struct {
	Direction string `json:"direction"` // "up" or "down"
}

func (s *Server) moveAccount(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	var req moveAccountRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Direction != "up" && req.Direction != "down" {
		fail(w, http.StatusBadRequest, "invalid_direction", `Direction must be "up" or "down".`)
		return
	}
	cur, err := s.q.GetAccountAdmin(r.Context(), r.PathValue("id"))
	if isBadUUID(err) || notFound(err) {
		fail(w, http.StatusNotFound, "not_found", "That account doesn't exist.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	rows, err := s.q.AccountPositions(r.Context(), cur.Provider)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	i := slices.IndexFunc(rows, func(row db.AccountPositionsRow) bool { return row.ID == cur.ID })
	j := i - 1
	if req.Direction == "down" {
		j = i + 1
	}
	if i < 0 || j < 0 || j >= len(rows) {
		fail(w, http.StatusConflict, "invalid_move", fmt.Sprintf("That account can't move %s; it's already at the %s of the list.", req.Direction, map[string]string{"up": "top", "down": "bottom"}[req.Direction]))
		return
	}
	err = pgx.BeginFunc(r.Context(), s.pg, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), "SET CONSTRAINTS provider_accounts_position DEFERRED"); err != nil {
			return err
		}
		q := s.q.WithTx(tx)
		if _, err := q.SetAccountPosition(r.Context(), db.SetAccountPositionParams{ID: rows[j].ID, Position: rows[i].Position}); err != nil {
			return err
		}
		_, err := q.SetAccountPosition(r.Context(), db.SetAccountPositionParams{ID: rows[i].ID, Position: rows[j].Position})
		return err
	})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r.Context(), &actor.ID, "account.move", "account", cur.ID, nil, map[string]string{"direction": req.Direction})
	w.WriteHeader(http.StatusNoContent)
}

// --- account test ---------------------------------------------------------------

// testUpstreamBase is used when the account has no base_url override. Codex
// and Antigravity don't forward yet (not-done G4/G5); the Test button still
// validates their stored API key against the official endpoint.
var testUpstreamBase = map[string]string{
	"claude":      "https://api.anthropic.com",
	"codex":       "https://api.openai.com",
	"antigravity": "https://generativelanguage.googleapis.com",
}

type accountTestResponse struct {
	OK        bool   `json:"ok"`
	Status    int    `json:"status"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

// validateAccount is the Check button in the Add dialog: it probes a
// credential against the provider's official endpoint without storing it.
type validateAccountRequest struct {
	Provider    string  `json:"provider"`
	AuthKind    string  `json:"auth_kind"`
	BaseURL     *string `json:"base_url"`
	AccessToken string  `json:"access_token"`
}

func (s *Server) validateAccount(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	var req validateAccountRequest
	if !decode(w, r, &req) {
		return
	}
	if !provider.Known(req.Provider) {
		fail(w, http.StatusBadRequest, "invalid_provider", `Provider must be "claude", "codex" or "antigravity".`)
		return
	}
	if req.AuthKind == "" {
		req.AuthKind = "token"
	}
	if !validAuthKind(w, req.Provider, req.AuthKind) {
		return
	}
	if rejectPastedOAuth(w, req.AuthKind) {
		return
	}
	if req.AccessToken == "" {
		fail(w, http.StatusBadRequest, "no_credentials", "Paste the credential to check first.")
		return
	}
	base := deref(req.BaseURL)
	if base == "" {
		base = testUpstreamBase[req.Provider]
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	req2, err := upstreamProbe(ctx, req.Provider, req.AuthKind, req.AccessToken, base)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	start := s.now()
	resp, err := http.DefaultClient.Do(req2)
	if err != nil {
		writeJSON(w, http.StatusOK, accountTestResponse{OK: false, LatencyMS: time.Since(start).Milliseconds(), Error: cleanUpstreamError(err)})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	res := accountTestResponse{Status: resp.StatusCode, LatencyMS: time.Since(start).Milliseconds()}
	res.OK = resp.StatusCode >= 200 && resp.StatusCode < 300 || resp.StatusCode == http.StatusTooManyRequests
	if !res.OK {
		res.Error = upstreamErrorMessage(resp.StatusCode, body)
	}
	writeJSON(w, http.StatusOK, res)
}

// upstreamProbe builds the official-endpoint request that decides whether a
// credential is accepted. Claude OAuth tokens are probed on the usage
// endpoint the CLI itself uses — a Messages call would depend on a model
// name that subscription auth may not serve (and costs tokens); API keys
// list the models. Codex and antigravity list their models.
func upstreamProbe(ctx context.Context, providerID, authKind, token, base string) (*http.Request, error) {
	switch providerID {
	case "claude":
		var (
			req *http.Request
			err error
		)
		if authKind == "api_key" {
			req, err = http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/v1/models", nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("X-Api-Key", token)
		} else {
			req, err = http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/api/oauth/usage", nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("anthropic-beta", oauth.OAuthBeta)
		}
		req.Header.Set("anthropic-version", "2023-06-01")
		return req, nil
	case "codex":
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/v1/models", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return req, nil
	default: // antigravity
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/v1beta/models", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Goog-Api-Key", token)
		return req, nil
	}
}

// testAccount checks the stored credential against its provider's official
// endpoint, through the account's own proxy (REQ-PROXY-01), and records the
// result on the account. A 429 counts as valid: the credential authenticated.
func (s *Server) testAccount(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	a, err := s.q.GetAccountAdmin(r.Context(), r.PathValue("id"))
	if isBadUUID(err) || notFound(err) {
		fail(w, http.StatusNotFound, "not_found", "That account doesn't exist.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if len(a.Credentials) == 0 {
		fail(w, http.StatusBadRequest, "no_credentials", "This account has no credential to test. Add one first.")
		return
	}
	if s.opts.Box == nil {
		fail(w, http.StatusConflict, "credentials_unreadable", "QANAT_MASTER_KEY isn't set on the server, so stored credentials can't be read.")
		return
	}
	plain, err := s.opts.Box.Open(a.Credentials, []byte(a.ID))
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
	if !validAuthKind(w, a.Provider, a.AuthKind) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	base := deref(a.BaseUrl)
	if base == "" {
		base = testUpstreamBase[a.Provider]
	}
	probe, err := upstreamProbe(ctx, a.Provider, a.AuthKind, creds.AccessToken, base)
	if err != nil {
		s.internal(w, r, err)
		return
	}

	client := &http.Client{}
	if spec := s.accountProxySpec(r.Context(), a); spec != nil {
		client.Transport = proxy.NewTransport(spec)
	}
	start := s.now()
	resp, err := client.Do(probe)
	if err != nil {
		res := accountTestResponse{OK: false, LatencyMS: time.Since(start).Milliseconds(), Error: cleanUpstreamError(err)}
		s.recordCheck(r, actor, a.ID, res)
		writeJSON(w, http.StatusOK, res)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	res := accountTestResponse{Status: resp.StatusCode, LatencyMS: time.Since(start).Milliseconds()}
	res.OK = resp.StatusCode >= 200 && resp.StatusCode < 300 || resp.StatusCode == http.StatusTooManyRequests
	if !res.OK {
		res.Error = upstreamErrorMessage(resp.StatusCode, body)
	}
	s.recordCheck(r, actor, a.ID, res)
	writeJSON(w, http.StatusOK, res)
}

// recordCheck persists the test result and writes the audit entry.
func (s *Server) recordCheck(r *http.Request, actor *db.GetUserRow, accountID string, res accountTestResponse) {
	errText := &res.Error
	if res.Error == "" {
		errText = nil
	}
	t := s.now()
	if err := s.q.SetAccountCheckResult(r.Context(), db.SetAccountCheckResultParams{ID: accountID, LastCheckAt: &t, LastCheckOk: pgtype.Bool{Bool: res.OK, Valid: true}, LastCheckError: errText}); err != nil {
		s.log.Warn("can't record account check", "account", accountID, "err", err)
	}
	s.audit(r.Context(), &actor.ID, "account.test", "account", accountID, nil,
		map[string]any{"ok": res.OK, "status": res.Status, "latency_ms": res.LatencyMS, "error": res.Error})
}

// upstreamErrorMessage pulls the provider's own error message out of the
// response body; the dashboard shows it verbatim.
func upstreamErrorMessage(status int, body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return fmt.Sprintf("%d: %s", status, e.Error.Message)
	}
	return fmt.Sprintf("%d: %s", status, http.StatusText(status))
}

func cleanUpstreamError(err error) string {
	msg := err.Error()
	if u, ok := err.(*url.Error); ok {
		msg = u.Err.Error() // drop the URL, which is redundant here
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}

// accountProxySpec resolves the account's §8.2 chain: its own setting, then
// its provider's, then the global default. nil means direct.
func (s *Server) accountProxySpec(ctx context.Context, a db.GetAccountAdminRow) *proxy.Spec {
	c := proxyChoice{a.ProxyID, a.ProxyGroupID, a.ProxyDirect}
	if c == (proxyChoice{}) {
		rows, err := s.q.ListProviderProxies(ctx)
		if err == nil {
			for _, row := range rows {
				if row.Provider == a.Provider {
					c = proxyChoice{row.ProxyID, row.ProxyGroupID, row.ProxyDirect}
					break
				}
			}
		}
	}
	if c == (proxyChoice{}) {
		if p, err := s.q.GetDefaultProxy(ctx); err == nil {
			return s.proxySpec(ctx, p.ID, p.Type, p.Host, int(p.Port), p.Username, p.Password)
		}
		return nil
	}
	switch {
	case c.direct:
		return nil
	case c.proxyID != nil:
		if p, err := s.q.GetProxy(ctx, *c.proxyID); err == nil {
			return s.proxySpec(ctx, p.ID, p.Type, p.Host, int(p.Port), p.Username, p.Password)
		}
	case c.groupID != nil:
		members, err := s.q.ListProxyGroupMembers(ctx)
		if err == nil {
			for _, m := range members {
				if m.GroupID != *c.groupID {
					continue
				}
				if p, err := s.q.GetProxy(ctx, m.ProxyID); err == nil && p.Enabled {
					return s.proxySpec(ctx, p.ID, p.Type, p.Host, int(p.Port), p.Username, p.Password)
				}
			}
		}
	}
	return nil
}

func (s *Server) proxySpec(ctx context.Context, id, typ, host string, port int, username *string, password []byte) *proxy.Spec {
	spec := &proxy.Spec{ID: id, Type: typ, Host: host, Port: port, Username: deref(username)}
	if password != nil {
		if pw, err := proxy.OpenPassword(s.opts.Box, id, password); err == nil {
			spec.Password = pw
		}
	}
	return spec
}

// --- mapping (REQ-MAP-01/02) -----------------------------------------------------

type mappingRule struct {
	Pattern       string  `json:"pattern"`
	Provider      string  `json:"provider"`
	UpstreamModel *string `json:"upstream_model"` // null keeps the incoming name
	Priority      int32   `json:"priority"`
}

func (s *Server) listMappings(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	rows, err := s.q.ListMappings(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]mappingRule, 0, len(rows))
	for _, m := range rows {
		if m.TargetKind != "model" {
			continue // Combos get their own endpoints with the admin console (P2)
		}
		out = append(out, mappingRule{m.Pattern, deref(m.Provider), m.UpstreamModel, m.Priority})
	}
	writeJSON(w, http.StatusOK, out)
}

// replaceMappings swaps the whole global mapping table atomically.
func (s *Server) replaceMappings(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	var rules []mappingRule
	if !decode(w, r, &rules) {
		return
	}
	seen := map[string]bool{}
	for i := range rules {
		m := &rules[i]
		m.Pattern = strings.TrimSpace(m.Pattern)
		if m.Pattern == "" || seen[m.Pattern] {
			fail(w, http.StatusBadRequest, "invalid_mapping", "Every rule needs a pattern, and each pattern can appear once.")
			return
		}
		seen[m.Pattern] = true
		if _, _, ok := provider.Split(m.Pattern); ok {
			fail(w, http.StatusBadRequest, "invalid_mapping", fmt.Sprintf("Rule %q: provider prefixes (cc/, cx/, ag/) route on their own; write the rule for the bare name.", m.Pattern))
			return
		}
		if !provider.Known(m.Provider) {
			fail(w, http.StatusBadRequest, "invalid_mapping", fmt.Sprintf("Rule %q: provider must be claude, codex or antigravity.", m.Pattern))
			return
		}
		if m.UpstreamModel != nil && strings.TrimSpace(*m.UpstreamModel) == "" {
			m.UpstreamModel = nil
		}
	}
	err := pgx.BeginFunc(r.Context(), s.pg, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.DeleteMappings(r.Context()); err != nil {
			return err
		}
		for _, m := range rules {
			glob := strings.ContainsAny(m.Pattern, "*?[")
			p := m.Provider
			if err := q.InsertMapping(r.Context(), db.InsertMappingParams{Pattern: m.Pattern, IsGlob: glob, Provider: &p, UpstreamModel: m.UpstreamModel, Priority: m.Priority}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r.Context(), &actor.ID, "mapping.replace", "mapping", "", nil, rules)
	w.WriteHeader(http.StatusNoContent)
}

// --- providers (REQ-MAP-04) -------------------------------------------------------

type providerInfo struct {
	ID       string   `json:"id"`
	Prefix   string   `json:"prefix"`
	Name     string   `json:"name"`
	Families []string `json:"families"`
}

// listProviders returns the fixed provider registry and its model prefixes.
func (s *Server) listProviders(w http.ResponseWriter, _ *http.Request, _ *db.GetUserRow) {
	out := make([]providerInfo, 0, len(provider.All))
	for _, p := range provider.All {
		out = append(out, providerInfo{p.ID, p.Prefix, p.Name, p.Families})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- prices (REQ-PRICE-01…03) ----------------------------------------------------

type priceRequest struct {
	ModelID     string  `json:"model_id"`
	PIn         float64 `json:"p_in"`
	POut        float64 `json:"p_out"`
	PCacheRead  float64 `json:"p_cache_read"`
	PCacheWrite float64 `json:"p_cache_write"`
}

type priceResponse struct {
	Version       int64      `json:"version"`
	ModelID       string     `json:"model_id"`
	PIn           float64    `json:"p_in"`
	POut          float64    `json:"p_out"`
	PCacheRead    float64    `json:"p_cache_read"`
	PCacheWrite   float64    `json:"p_cache_write"`
	EffectiveFrom time.Time  `json:"effective_from"`
	ConfirmedAt   *time.Time `json:"confirmed_at"`
	Source        string     `json:"source"`      // "admin" or "default"
	HasDefault    bool       `json:"has_default"` // deleting the admin price falls back to it
}

func (s *Server) listPrices(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	rows, err := s.q.ListPrices(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]priceResponse, 0, len(rows))
	for _, p := range rows {
		out = append(out, priceResponse{p.ID, p.ModelID, p.PIn, p.POut, p.PCacheRead, p.PCacheWrite, p.EffectiveFrom, p.ConfirmedAt, p.Source, p.HasDefault})
	}
	writeJSON(w, http.StatusOK, out)
}

// setPrice adds a new confirmed price version, effective now. Past usage
// keeps its old version (REQ-PRICE-01).
func (s *Server) setPrice(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	var req priceRequest
	if !decode(w, r, &req) {
		return
	}
	// Prices are keyed by the provider's model id, which the gateway
	// forwards; a prefix (cc/, cx/, ag/) only picks the provider.
	req.ModelID = provider.Bare(strings.TrimSpace(req.ModelID))
	if req.ModelID == "" || req.PIn < 0 || req.POut < 0 || req.PCacheRead < 0 || req.PCacheWrite < 0 {
		fail(w, http.StatusBadRequest, "invalid_price", "Give a model id and non-negative prices per 1M tokens.")
		return
	}
	ok, err := s.q.PriceableModel(r.Context(), req.ModelID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if !ok {
		fail(w, http.StatusBadRequest, "unknown_model",
			fmt.Sprintf("No provider lists the model %q. Sync the provider's catalog on the Models page, then pick the model from it.", req.ModelID))
		return
	}
	v, err := s.q.InsertConfirmedPrice(r.Context(), db.InsertConfirmedPriceParams{
		ModelID: req.ModelID, PIn: req.PIn, POut: req.POut, PCacheRead: req.PCacheRead, PCacheWrite: req.PCacheWrite, ConfirmedBy: &actor.ID,
	})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r.Context(), &actor.ID, "price.set", "price", req.ModelID, nil, req)
	writeJSON(w, http.StatusCreated, map[string]int64{"version": v})
}

// deletePrice removes a model's admin price, so the built-in default (if
// any) applies again; without one the model becomes unusable (D13).
func (s *Server) deletePrice(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	model := provider.Bare(r.PathValue("model"))
	n, err := s.q.DeleteAdminPrices(r.Context(), model)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if n == 0 {
		fail(w, http.StatusNotFound, "not_found", "That model has no admin price. Built-in prices can be overridden but not deleted.")
		return
	}
	s.audit(r.Context(), &actor.ID, "price.delete", "price", model, nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

// syncDefaultPrices writes a 'default' price version for every built-in
// price that is new or changed since the last sync, so editing the table
// and restarting is how defaults are updated (REQ-PRICE-01 still holds: each
// change is a new version). Replicas starting together serialize on a lock.
func (s *Server) syncDefaultPrices(ctx context.Context) error {
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('qanat.default_prices'))`); err != nil {
		return err
	}
	q := s.q.WithTx(tx)
	rows, err := q.LatestDefaultPrices(ctx)
	if err != nil {
		return err
	}
	have := make(map[string]pricing.Price, len(rows))
	for _, p := range rows {
		have[p.ModelID] = pricing.Price{In: p.PIn, Out: p.POut, CacheRead: p.PCacheRead, CacheWrite: p.PCacheWrite}
	}
	for model, p := range pricing.Defaults {
		if cur, ok := have[model]; ok && cur == p {
			continue
		}
		if err := q.InsertDefaultPrice(ctx, db.InsertDefaultPriceParams{
			ModelID: model, PIn: p.In, POut: p.Out, PCacheRead: p.CacheRead, PCacheWrite: p.CacheWrite,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// --- usage -----------------------------------------------------------------------

type usageTotals struct {
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	RequestCount     int64   `json:"request_count"`
}

func (s *Server) userUsage(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	t, err := s.q.UserUsageTotals(r.Context(), r.PathValue("id"))
	if isBadUUID(err) {
		fail(w, http.StatusNotFound, "not_found", "That user doesn't exist.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, usageTotals{t.InputTokens, t.OutputTokens, t.CacheReadTokens, t.CacheWriteTokens, t.CostUsd, t.RequestCount})
}
