package control

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"qanat/internal/store/db"
)

var providers = map[string]bool{"claude": true, "codex": true, "antigravity": true}

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
}

func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	rows, err := s.q.ListAccounts(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]accountResponse, 0, len(rows))
	for _, a := range rows {
		out = append(out, accountResponse{a.ID, a.Provider, a.Label, a.BaseUrl, a.HasCredentials, a.State, a.CooldownUntil, a.CreatedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

type createAccountRequest struct {
	Provider string  `json:"provider"`
	Label    string  `json:"label"`
	BaseURL  *string `json:"base_url"`
	// AccessToken is sealed with the master key before storage and never
	// returned. OAuth connect and refresh arrive in M5 (§6.6).
	AccessToken *string `json:"access_token"`
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
	if !providers[req.Provider] {
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
	if err := s.q.CreateAccount(r.Context(), db.CreateAccountParams{ID: id, Provider: req.Provider, Label: req.Label, BaseUrl: req.BaseURL, Credentials: sealed}); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r.Context(), &actor.ID, "account.create", "account", id, nil,
		map[string]any{"provider": req.Provider, "label": req.Label, "base_url": req.BaseURL, "has_credentials": sealed != nil})
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
		if !providers[m.Provider] {
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

// --- prices (REQ-PRICE-01…03) ----------------------------------------------------

type priceRequest struct {
	UpstreamModel string  `json:"upstream_model"`
	PIn           float64 `json:"p_in"`
	POut          float64 `json:"p_out"`
	PCacheRead    float64 `json:"p_cache_read"`
	PCacheWrite   float64 `json:"p_cache_write"`
}

type priceResponse struct {
	Version       int64      `json:"version"`
	UpstreamModel string     `json:"upstream_model"`
	PIn           float64    `json:"p_in"`
	POut          float64    `json:"p_out"`
	PCacheRead    float64    `json:"p_cache_read"`
	PCacheWrite   float64    `json:"p_cache_write"`
	EffectiveFrom time.Time  `json:"effective_from"`
	ConfirmedAt   *time.Time `json:"confirmed_at"`
}

func (s *Server) listPrices(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	rows, err := s.q.ListPrices(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]priceResponse, 0, len(rows))
	for _, p := range rows {
		out = append(out, priceResponse{p.ID, p.UpstreamModel, p.PIn, p.POut, p.PCacheRead, p.PCacheWrite, p.EffectiveFrom, p.ConfirmedAt})
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
	req.UpstreamModel = strings.TrimSpace(req.UpstreamModel)
	if req.UpstreamModel == "" || req.PIn < 0 || req.POut < 0 || req.PCacheRead < 0 || req.PCacheWrite < 0 {
		fail(w, http.StatusBadRequest, "invalid_price", "Give the upstream model name and non-negative prices per 1M tokens.")
		return
	}
	v, err := s.q.InsertConfirmedPrice(r.Context(), db.InsertConfirmedPriceParams{
		UpstreamModel: req.UpstreamModel, PIn: req.PIn, POut: req.POut, PCacheRead: req.PCacheRead, PCacheWrite: req.PCacheWrite, ConfirmedBy: &actor.ID,
	})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r.Context(), &actor.ID, "price.set", "price", req.UpstreamModel, nil, req)
	writeJSON(w, http.StatusCreated, map[string]int64{"version": v})
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
