package control

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"qanat/internal/core/oauth"
	"qanat/internal/core/provider"
	"qanat/internal/store/db"
)

// Provider OAuth sign-in (PRD §6.6, REQ-OAUTH-01), ported from 9router's
// /api/oauth/[provider]/[action] (MIT): the dashboard asks for an authorize
// URL, the provider redirects to the dashboard's /callback page (or, for
// Codex, to the CLI's loopback port, whose address the admin pastes), and the
// code+state go to the exchange, which creates — or re-signs-in — the
// account with its tokens sealed.

// oauthHTTPClient reaches the providers' token endpoints. Tests override the
// flows' URLs, not this client.
var oauthHTTPClient = &http.Client{Timeout: 30 * time.Second}

type oauthPendingEntry struct {
	pending   *oauth.Pending
	userID    string
	createdAt time.Time
}

type oauthExchangeResult struct {
	ID     string `json:"id"`
	Merged bool   `json:"merged"`
}

type oauthAuthorizeResponse struct {
	AuthURL     string `json:"auth_url"`
	State       string `json:"state"`
	RedirectURI string `json:"redirect_uri"`
}

// oauthAuthorize answers the Add-account dialog with the provider's authorize
// URL and keeps the matching pending sign-in server-side, so the PKCE
// verifier never crosses the browser.
func (s *Server) oauthAuthorize(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	providerID := r.PathValue("provider")
	if oauth.Get(providerID) == nil {
		fail(w, http.StatusBadRequest, "invalid_provider", "There is no sign-in flow for this provider yet.")
		return
	}
	redirectURI := r.URL.Query().Get("redirect_uri")
	if !validRedirectURI(oauth.Get(providerID), redirectURI) {
		fail(w, http.StatusBadRequest, "invalid_redirect_uri",
			"The redirect URL must be http(s) on localhost with the path /callback.")
		return
	}
	authURL, state, pending, err := oauth.Begin(providerID, redirectURI)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.oauthMu.Lock()
	now := s.now()
	for k, v := range s.oauthPending {
		if now.Sub(v.createdAt) > oauth.PendingTTL {
			delete(s.oauthPending, k)
		}
	}
	s.oauthPending[state] = oauthPendingEntry{pending: pending, userID: actor.ID, createdAt: now}
	s.oauthMu.Unlock()
	writeJSON(w, http.StatusOK, oauthAuthorizeResponse{AuthURL: authURL, State: state, RedirectURI: pending.RedirectURI})
}

type oauthExchangeRequest struct {
	// Code is the authorization code, possibly still carrying "#state" the
	// way Claude's redirect does.
	Code        string `json:"code"`
	State       string `json:"state"`
	RedirectURI string `json:"redirect_uri"`
}

// oauthExchange finishes a sign-in: it swaps the code for tokens, seals them,
// and creates the account — or refreshes the existing one when the same
// identity already has an account (9router's connection merge).
func (s *Server) oauthExchange(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	providerID := r.PathValue("provider")
	f := oauth.Get(providerID)
	if f == nil {
		fail(w, http.StatusBadRequest, "invalid_provider", "There is no sign-in flow for this provider yet.")
		return
	}
	var req oauthExchangeRequest
	if !decode(w, r, &req) {
		return
	}
	if req.State == "" { // Claude may deliver the state as "#fragment" on the code
		if i := strings.IndexByte(req.Code, '#'); i >= 0 {
			req.State = req.Code[i+1:]
		}
	}
	s.oauthMu.Lock()
	entry, ok := s.oauthPending[req.State]
	if ok {
		delete(s.oauthPending, req.State)
	}
	s.oauthMu.Unlock()
	if !ok || entry.pending.Provider != providerID || entry.pending.RedirectURI != req.RedirectURI {
		fail(w, http.StatusBadRequest, "unknown_state",
			"This sign-in can't be matched to a started one. Open the sign-in again.")
		return
	}

	tokens, err := f.Exchange(r.Context(), oauthHTTPClient, req.Code, req.State, req.RedirectURI, entry.pending)
	if err != nil {
		fail(w, http.StatusBadGateway, "oauth_exchange_failed",
			"The provider refused the sign-in: "+err.Error())
		return
	}
	if s.opts.Box == nil {
		fail(w, http.StatusConflict, "no_master_key", "Credentials can't be stored until QANAT_MASTER_KEY is set on the server.")
		return
	}

	var email *string
	if tokens.Email != "" {
		email = &tokens.Email
	}
	var expiresAt *time.Time
	if tokens.ExpiresIn > 0 {
		t := s.now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
		expiresAt = &t
	}
	// Same identity already has an account → refresh it (9router's merge);
	// otherwise add a new one at the bottom of the pool.
	if email != nil {
		rows, err := s.q.FindOAuthAccount(r.Context(), db.FindOAuthAccountParams{Provider: providerID, Email: email})
		if err != nil {
			s.internal(w, r, err)
			return
		}
		if len(rows) > 0 {
			existingID := rows[0]
			sealed, err := s.opts.Box.Seal(oauthCredentialsJSON(tokens), []byte(existingID))
			if err != nil {
				s.internal(w, r, err)
				return
			}
			if _, err := s.q.MergeOAuthAccount(r.Context(), db.MergeOAuthAccountParams{ID: existingID, Credentials: sealed, ExpiresAt: expiresAt, Email: email}); err != nil {
				s.internal(w, r, err)
				return
			}
			s.audit(r.Context(), &actor.ID, "account.oauth", "account", existingID, nil,
				map[string]any{"provider": providerID, "merged": true, "email": deref(email)})
			writeJSON(w, http.StatusOK, oauthExchangeResult{ID: existingID, Merged: true})
			return
		}
	}

	id := newUUID()
	sealed, err := s.opts.Box.Seal(oauthCredentialsJSON(tokens), []byte(id))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	label := deref(email)
	if label == "" {
		p, _ := provider.Get(providerID)
		label = p.Name + " OAuth"
	}
	if err := s.q.CreateAccount(r.Context(), db.CreateAccountParams{
		ID: id, Provider: providerID, Label: label, Credentials: sealed, AuthKind: "oauth",
		ExpiresAt: expiresAt, Email: email,
	}); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r.Context(), &actor.ID, "account.oauth", "account", id, nil,
		map[string]any{"provider": providerID, "merged": false, "email": deref(email)})
	writeJSON(w, http.StatusCreated, oauthExchangeResult{ID: id, Merged: false})
}

// oauthCredentialsJSON is the sealed plaintext for a new OAuth account.
func oauthCredentialsJSON(t *oauth.Tokens) []byte {
	b, _ := json.Marshal(oauth.CredentialsFrom(t))
	return b
}

// validRedirectURI holds redirects to the dashboard's own callback page on a
// loopback address — the shape the official clients use. A flow with a fixed
// redirect (Codex) ignores the dashboard's.
func validRedirectURI(f *oauth.Flow, raw string) bool {
	if f.RedirectURI != "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Path != f.CallbackPath {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
