package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"

	"qanat/internal/core/provider"
	"qanat/internal/proxy"
	"qanat/internal/store/db"
)

// --- proxies (PRD §8) ------------------------------------------------------------

type proxyResponse struct {
	ID           string    `json:"id"`
	Label        string    `json:"label"`
	Type         string    `json:"type"`
	Host         string    `json:"host"`
	Port         int32     `json:"port"`
	Username     *string   `json:"username"`
	HasPassword  bool      `json:"has_password"`
	Enabled      bool      `json:"enabled"`
	IsDefault    bool      `json:"is_default"`
	AccountCount int64     `json:"account_count"`
	CreatedAt    time.Time `json:"created_at"`
	// Health is the gateway's latest check per upstream origin
	// (REQ-PROXY-03); empty when no account routes through the proxy.
	Health []proxy.CheckResult `json:"health"`
}

func (s *Server) listProxies(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	rows, err := s.q.ListProxies(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]proxyResponse, 0, len(rows))
	for _, p := range rows {
		out = append(out, proxyResponse{p.ID, p.Label, p.Type, p.Host, p.Port, p.Username, p.HasPassword, p.Enabled, p.IsDefault, p.AccountCount, p.CreatedAt, []proxy.CheckResult{}})
	}
	s.addProxyHealth(r.Context(), out)
	writeJSON(w, http.StatusOK, out)
}

// addProxyHealth reads what the gateways left in Redis. Health is
// informational here, so a Redis error leaves it empty.
func (s *Server) addProxyHealth(ctx context.Context, out []proxyResponse) {
	if s.rdb == nil || len(out) == 0 {
		return
	}
	pipe := s.rdb.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(out))
	for i, p := range out {
		cmds[i] = pipe.HGetAll(ctx, proxy.HealthKey(p.ID))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		s.log.Warn("proxy health unavailable", "err", err)
		return
	}
	for i, c := range cmds {
		for _, v := range c.Val() {
			var r proxy.CheckResult
			if json.Unmarshal([]byte(v), &r) == nil {
				out[i].Health = append(out[i].Health, r)
			}
		}
		slices.SortFunc(out[i].Health, func(a, b proxy.CheckResult) int { return strings.Compare(a.Origin, b.Origin) })
	}
}

type proxyRequest struct {
	Label    string  `json:"label"`
	Type     string  `json:"type"`
	Host     string  `json:"host"`
	Port     int32   `json:"port"`
	Username *string `json:"username"`
	// Password is sealed with the master key and never returned. On update,
	// null keeps the stored one and "" removes it.
	Password  *string `json:"password"`
	Enabled   *bool   `json:"enabled"`
	IsDefault bool    `json:"is_default"`
}

// validate normalizes req and reports the first problem as an API error.
func (req *proxyRequest) validate(w http.ResponseWriter) bool {
	req.Label = strings.TrimSpace(req.Label)
	req.Host = strings.TrimSpace(req.Host)
	req.Type = strings.ToLower(strings.TrimSpace(req.Type))
	if !proxy.Types[req.Type] {
		fail(w, http.StatusBadRequest, "invalid_proxy", `Type must be "http", "https", "socks5" or "socks5h".`)
		return false
	}
	if req.Host == "" || strings.ContainsAny(req.Host, "/@ []") || strings.Contains(req.Host, ":") && net.ParseIP(req.Host) == nil {
		fail(w, http.StatusBadRequest, "invalid_proxy", "Give the proxy host on its own, like proxy.example.com or 10.0.0.5 (no scheme or port).")
		return false
	}
	if req.Port < 1 || req.Port > 65535 {
		fail(w, http.StatusBadRequest, "invalid_proxy", "The port must be between 1 and 65535.")
		return false
	}
	if req.Username != nil && strings.TrimSpace(*req.Username) == "" {
		req.Username = nil
	}
	if req.Label == "" {
		req.Label = fmt.Sprintf("%s:%d", req.Host, req.Port)
	}
	return true
}

func (s *Server) sealProxyPassword(w http.ResponseWriter, r *http.Request, id, password string) ([]byte, bool) {
	sealed, err := proxy.SealPassword(s.opts.Box, id, password)
	if errors.Is(err, proxy.ErrNoMasterKey) {
		fail(w, http.StatusBadRequest, "no_master_key", "Proxy passwords can't be stored until QANAT_MASTER_KEY is set on the server.")
		return nil, false
	}
	if err != nil {
		s.internal(w, r, err)
		return nil, false
	}
	return sealed, true
}

// proxyAudit is what the audit log keeps: everything but the password.
func proxyAudit(req proxyRequest, hasPassword bool) map[string]any {
	return map[string]any{"label": req.Label, "type": req.Type, "host": req.Host, "port": req.Port,
		"username": req.Username, "has_password": hasPassword, "enabled": req.Enabled == nil || *req.Enabled, "is_default": req.IsDefault}
}

func (s *Server) createProxy(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	var req proxyRequest
	if !decode(w, r, &req) || !req.validate(w) {
		return
	}
	id := newUUID()
	var sealed []byte
	if req.Password != nil && *req.Password != "" {
		var ok bool
		if sealed, ok = s.sealProxyPassword(w, r, id, *req.Password); !ok {
			return
		}
	}
	err := pgx.BeginFunc(r.Context(), s.pg, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if req.IsDefault {
			if err := q.ClearDefaultProxy(r.Context(), id); err != nil {
				return err
			}
		}
		return q.CreateProxy(r.Context(), db.CreateProxyParams{ID: id, Label: req.Label, Type: req.Type, Host: req.Host, Port: req.Port,
			Username: req.Username, Password: sealed, Enabled: req.Enabled == nil || *req.Enabled, IsDefault: req.IsDefault})
	})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r.Context(), &actor.ID, "proxy.create", "proxy", id, nil, proxyAudit(req, sealed != nil))
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) updateProxy(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	id := r.PathValue("id")
	var req proxyRequest
	if !decode(w, r, &req) || !req.validate(w) {
		return
	}
	cur, err := s.q.GetProxy(r.Context(), id)
	if isBadUUID(err) || notFound(err) {
		fail(w, http.StatusNotFound, "not_found", "That proxy doesn't exist.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	sealed := cur.Password
	if req.Password != nil {
		sealed = nil
		if *req.Password != "" {
			var ok bool
			if sealed, ok = s.sealProxyPassword(w, r, id, *req.Password); !ok {
				return
			}
		}
	}
	enabled := cur.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	err = pgx.BeginFunc(r.Context(), s.pg, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if req.IsDefault {
			if err := q.ClearDefaultProxy(r.Context(), id); err != nil {
				return err
			}
		}
		_, err := q.UpdateProxy(r.Context(), db.UpdateProxyParams{ID: id, Label: req.Label, Type: req.Type, Host: req.Host, Port: req.Port,
			Username: req.Username, Password: sealed, Enabled: enabled, IsDefault: req.IsDefault})
		return err
	})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	req.Enabled = &enabled
	before := proxyAudit(proxyRequest{Label: cur.Label, Type: cur.Type, Host: cur.Host, Port: cur.Port, Username: cur.Username,
		Enabled: &cur.Enabled, IsDefault: cur.IsDefault}, cur.Password != nil)
	s.audit(r.Context(), &actor.ID, "proxy.update", "proxy", id, before, proxyAudit(req, sealed != nil))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteProxy(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	id := r.PathValue("id")
	n, err := s.q.DeleteProxy(r.Context(), id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
		users, _ := s.q.ProxyAccountLabels(r.Context(), &id)
		groups, _ := s.q.ProxyGroupLabels(r.Context(), id)
		for i, g := range groups {
			groups[i] = "group " + g
		}
		providers, _ := s.q.ProviderProxyNames(r.Context(), &id)
		fail(w, http.StatusConflict, "proxy_in_use",
			fmt.Sprintf("This proxy is still in use (%s). Move them to another proxy first, so their source IP doesn't change unnoticed.", strings.Join(append(append(users, groups...), providers...), ", ")))
		return
	}
	if isBadUUID(err) || (err == nil && n == 0) {
		fail(w, http.StatusNotFound, "not_found", "That proxy doesn't exist.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r.Context(), &actor.ID, "proxy.delete", "proxy", id, nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

// testProxy is the Test button (REQ-PROXY-05): egress IP, and latency and TLS
// to each provider host, through the saved proxy.
func (s *Server) testProxy(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	id := r.PathValue("id")
	p, err := s.q.GetProxy(r.Context(), id)
	if isBadUUID(err) || notFound(err) {
		fail(w, http.StatusNotFound, "not_found", "That proxy doesn't exist.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	password, err := proxy.OpenPassword(s.opts.Box, p.ID, p.Password)
	if err != nil {
		fail(w, http.StatusConflict, "proxy_password_unreadable", "The stored proxy password can't be decrypted with the current master key. Set the password again.")
		return
	}
	spec := &proxy.Spec{ID: p.ID, Type: p.Type, Host: p.Host, Port: int(p.Port), Username: deref(p.Username), Password: password}
	targets := s.opts.ProxyProbeTargets
	if targets == nil {
		targets = proxy.ProviderHosts
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, proxy.Test(ctx, spec, s.opts.ProxyIPEchoURL, targets))
}

// --- proxy settings (§8.2) -------------------------------------------------------

// proxyChoice is one level of §8.2 resolution. In the API it is a string:
// "inherit" (fall through to the next level), "direct", a proxy ID, or
// "group:" + a group ID.
type proxyChoice struct {
	proxyID, groupID *string
	direct           bool
}

func (c proxyChoice) String() string {
	switch {
	case c.direct:
		return "direct"
	case c.proxyID != nil:
		return *c.proxyID
	case c.groupID != nil:
		return groupPrefix + *c.groupID
	default:
		return "inherit"
	}
}

const groupPrefix = "group:"

// parseProxyChoice is the inverse of proxyChoice.String; it checks that the
// proxy or group exists.
func (s *Server) parseProxyChoice(w http.ResponseWriter, r *http.Request, v string) (proxyChoice, bool) {
	switch v {
	case "", "inherit":
		return proxyChoice{}, true
	case "direct":
		return proxyChoice{direct: true}, true
	}
	var err error
	var c proxyChoice
	if gid, ok := strings.CutPrefix(v, groupPrefix); ok {
		_, err = s.q.GetProxyGroup(r.Context(), gid)
		c.groupID = &gid
	} else {
		_, err = s.q.GetProxy(r.Context(), v)
		c.proxyID = &v
	}
	if err != nil {
		if isBadUUID(err) || notFound(err) {
			fail(w, http.StatusBadRequest, "invalid_proxy", `Proxy must be "inherit", "direct", the ID of an existing proxy, or "group:" and the ID of an existing proxy group.`)
		} else {
			s.internal(w, r, err)
		}
		return proxyChoice{}, false
	}
	return c, true
}

// --- provider proxies (§8.2, level 2) --------------------------------------------

type providerProxyResponse struct {
	Provider string `json:"provider"`
	Proxy    string `json:"proxy"`
}

// listProviderProxies returns every provider, "inherit" where none is set.
func (s *Server) listProviderProxies(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	rows, err := s.q.ListProviderProxies(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	set := map[string]string{}
	for _, p := range rows {
		set[p.Provider] = proxyChoice{p.ProxyID, p.ProxyGroupID, p.ProxyDirect}.String()
	}
	out := []providerProxyResponse{}
	for _, p := range provider.All {
		v, ok := set[p.ID]
		if !ok {
			v = "inherit"
		}
		out = append(out, providerProxyResponse{p.ID, v})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) setProviderProxy(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	name := r.PathValue("provider")
	if !provider.Known(name) {
		fail(w, http.StatusNotFound, "not_found", `Provider must be "claude", "codex" or "antigravity".`)
		return
	}
	var req updateAccountRequest
	if !decode(w, r, &req) {
		return
	}
	proxyValue := ""
	if req.Proxy != nil {
		proxyValue = *req.Proxy
	}
	c, ok := s.parseProxyChoice(w, r, proxyValue)
	if !ok {
		return
	}
	var err error
	if c == (proxyChoice{}) {
		err = s.q.ClearProviderProxy(r.Context(), name)
	} else {
		err = s.q.SetProviderProxy(r.Context(), db.SetProviderProxyParams{Provider: name, ProxyID: c.proxyID, ProxyGroupID: c.groupID, ProxyDirect: c.direct})
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r.Context(), &actor.ID, "provider.proxy", "provider", name, nil, map[string]string{"proxy": c.String()})
	w.WriteHeader(http.StatusNoContent)
}

// --- proxy groups (§8.1) ----------------------------------------------------------

type proxyGroupResponse struct {
	ID           string    `json:"id"`
	Label        string    `json:"label"`
	Policy       string    `json:"policy"`
	Members      []string  `json:"members"` // proxy IDs, failover order
	AccountCount int64     `json:"account_count"`
	CreatedAt    time.Time `json:"created_at"`
}

func (s *Server) listProxyGroups(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	groups, err := s.q.ListProxyGroups(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	members, err := s.q.ListProxyGroupMembers(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	by := map[string][]string{}
	for _, m := range members {
		by[m.GroupID] = append(by[m.GroupID], m.ProxyID)
	}
	out := make([]proxyGroupResponse, 0, len(groups))
	for _, g := range groups {
		ms := by[g.ID]
		if ms == nil {
			ms = []string{}
		}
		out = append(out, proxyGroupResponse{g.ID, g.Label, g.Policy, ms, g.AccountCount, g.CreatedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

type proxyGroupRequest struct {
	Label   string   `json:"label"`
	Policy  string   `json:"policy"`
	Members []string `json:"members"`
}

func (req *proxyGroupRequest) validate(w http.ResponseWriter) bool {
	req.Label = strings.TrimSpace(req.Label)
	if req.Policy != "failover" && req.Policy != "sticky" {
		fail(w, http.StatusBadRequest, "invalid_proxy_group", `Policy must be "failover" or "sticky".`)
		return false
	}
	if req.Label == "" {
		fail(w, http.StatusBadRequest, "invalid_proxy_group", "Give the group a label.")
		return false
	}
	if len(req.Members) == 0 {
		fail(w, http.StatusBadRequest, "invalid_proxy_group", "A group needs at least one proxy.")
		return false
	}
	seen := map[string]bool{}
	for _, m := range req.Members {
		if seen[m] {
			fail(w, http.StatusBadRequest, "invalid_proxy_group", "Each proxy can appear in a group only once.")
			return false
		}
		seen[m] = true
	}
	return true
}

// saveGroupMembers replaces a group's members, in the order given.
func saveGroupMembers(ctx context.Context, q *db.Queries, id string, members []string) error {
	if err := q.ClearProxyGroupMembers(ctx, id); err != nil {
		return err
	}
	for i, m := range members {
		if err := q.AddProxyGroupMember(ctx, db.AddProxyGroupMemberParams{GroupID: id, ProxyID: m, Priority: int32(i)}); err != nil {
			return err
		}
	}
	return nil
}

// groupWriteError reports a member that isn't a proxy.
func (s *Server) groupWriteError(w http.ResponseWriter, r *http.Request, err error) {
	var pgErr *pgconn.PgError
	if isBadUUID(err) || errors.As(err, &pgErr) && pgErr.Code == "23503" {
		fail(w, http.StatusBadRequest, "invalid_proxy_group", "Every member must be the ID of an existing proxy.")
		return
	}
	s.internal(w, r, err)
}

func (s *Server) createProxyGroup(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	var req proxyGroupRequest
	if !decode(w, r, &req) || !req.validate(w) {
		return
	}
	id := newUUID()
	err := pgx.BeginFunc(r.Context(), s.pg, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.CreateProxyGroup(r.Context(), db.CreateProxyGroupParams{ID: id, Label: req.Label, Policy: req.Policy}); err != nil {
			return err
		}
		return saveGroupMembers(r.Context(), q, id, req.Members)
	})
	if err != nil {
		s.groupWriteError(w, r, err)
		return
	}
	s.audit(r.Context(), &actor.ID, "proxy_group.create", "proxy_group", id, nil, req)
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) updateProxyGroup(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	id := r.PathValue("id")
	var req proxyGroupRequest
	if !decode(w, r, &req) || !req.validate(w) {
		return
	}
	cur, err := s.q.GetProxyGroup(r.Context(), id)
	if isBadUUID(err) || notFound(err) {
		fail(w, http.StatusNotFound, "not_found", "That proxy group doesn't exist.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	err = pgx.BeginFunc(r.Context(), s.pg, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.UpdateProxyGroup(r.Context(), db.UpdateProxyGroupParams{ID: id, Label: req.Label, Policy: req.Policy}); err != nil {
			return err
		}
		return saveGroupMembers(r.Context(), q, id, req.Members)
	})
	if err != nil {
		s.groupWriteError(w, r, err)
		return
	}
	s.audit(r.Context(), &actor.ID, "proxy_group.update", "proxy_group", id, map[string]string{"label": cur.Label, "policy": cur.Policy}, req)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteProxyGroup(w http.ResponseWriter, r *http.Request, actor *db.GetUserRow) {
	id := r.PathValue("id")
	n, err := s.q.DeleteProxyGroup(r.Context(), id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		users, _ := s.q.ProxyGroupAccountLabels(r.Context(), &id)
		providers, _ := s.q.ProviderProxyGroupNames(r.Context(), &id)
		fail(w, http.StatusConflict, "proxy_group_in_use",
			fmt.Sprintf("This group is still in use (%s). Move them to another proxy first, so their source IP doesn't change unnoticed.", strings.Join(append(users, providers...), ", ")))
		return
	}
	if isBadUUID(err) || (err == nil && n == 0) {
		fail(w, http.StatusNotFound, "not_found", "That proxy group doesn't exist.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r.Context(), &actor.ID, "proxy_group.delete", "proxy_group", id, nil, nil)
	w.WriteHeader(http.StatusNoContent)
}
