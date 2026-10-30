package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/sync/singleflight"

	"qanat/internal/core/oauth"
	"qanat/internal/core/providerlimits"
	"qanat/internal/proxy"
	"qanat/internal/store/db"
)

// Provider limits, like 9router's /api/usage/[connectionId]: each account's
// remaining subscription quota, read from the provider's own usage endpoint
// through the account's proxy. A read is cached in Redis for limitsFresh;
// the Refresh button (?force=1) skips the cache. A rate-limited usage
// endpoint is left alone for limitsCooldown, and the last good read is served
// meanwhile.

const (
	limitsFresh = 5 * time.Minute
	// limitsKeep is how long the last good read stays available as a
	// fallback when the provider can't be reached.
	limitsKeep     = 24 * time.Hour
	limitsCooldown = 3 * time.Minute
)

// limitsFlight shares one upstream call between concurrent reads of an
// account (several tabs, a bulk refresh).
var limitsFlight singleflight.Group

type accountLimitsResponse struct {
	AccountID string `json:"account_id"`
	Supported bool   `json:"supported"`
	providerlimits.Snapshot
	FetchedAt *time.Time `json:"fetched_at"`
	// Stale means the windows are the last good read, not a fresh one.
	Stale bool   `json:"stale"`
	Error string `json:"error,omitempty"`
}

type limitsEntry struct {
	Snapshot  providerlimits.Snapshot `json:"snapshot"`
	FetchedAt time.Time               `json:"fetched_at"`
}

// HeadroomKey is the Redis hash the gateway reads account headroom from.
const HeadroomKey = "qanat:account-headroom"

func limitsKey(id string) string         { return "qanat:provider-limits:" + id }
func limitsCooldownKey(id string) string { return "qanat:provider-limits-cooldown:" + id }

func (s *Server) accountLimits(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	a, err := s.q.GetAccountAdmin(r.Context(), r.PathValue("id"))
	if isBadUUID(err) || notFound(err) {
		fail(w, http.StatusNotFound, "not_found", "That account doesn't exist.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	res := accountLimitsResponse{AccountID: a.ID, Snapshot: providerlimits.Snapshot{Windows: []providerlimits.Window{}}}
	if !providerlimits.Supported(a.Provider, a.AuthKind) {
		writeJSON(w, http.StatusOK, res)
		return
	}
	res.Supported = true
	if len(a.Credentials) == 0 {
		res.Error = "This account has no credential yet."
		writeJSON(w, http.StatusOK, res)
		return
	}
	if s.opts.Box == nil {
		fail(w, http.StatusConflict, "credentials_unreadable", "QANAT_MASTER_KEY isn't set on the server, so stored credentials can't be read.")
		return
	}

	maxAge := limitsFresh
	if r.URL.Query().Get("force") == "1" {
		maxAge = 0
	}
	entry, stale, msg := s.loadLimits(r.Context(), a, maxAge)
	writeJSON(w, http.StatusOK, limitsResponse(res, entry, stale, msg))
}

// loadLimits returns the account's limits: the cached read when it's younger
// than maxAge, else a fresh one from the provider (stored, and its headroom
// published for the gateway). When the provider can't be read it returns the
// last good read with stale set and msg saying why.
func (s *Server) loadLimits(ctx context.Context, a db.GetAccountAdminRow, maxAge time.Duration) (entry *limitsEntry, stale bool, msg string) {
	cached := s.cachedLimits(ctx, a.ID)
	if cached != nil && maxAge > 0 && s.now().Sub(cached.FetchedAt) < maxAge {
		return cached, false, ""
	}
	if ttl, err := s.rdb.TTL(ctx, limitsCooldownKey(a.ID)).Result(); err == nil && ttl > 0 {
		return cached, true, "The provider is rate limiting its usage API; try again in " + ttl.Round(time.Second).String() + "."
	}

	// The shared call outlives a reader who hangs up.
	v, err, _ := limitsFlight.Do(a.ID, func() (any, error) {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		return s.fetchLimits(fctx, a)
	})
	if err != nil {
		msg := cleanUpstreamError(err)
		switch {
		case errors.Is(err, providerlimits.ErrRateLimited):
			_ = s.rdb.Set(ctx, limitsCooldownKey(a.ID), "1", limitsCooldown).Err()
			msg = "The provider is rate limiting its usage API; try again in a few minutes."
		case errors.Is(err, providerlimits.ErrUnauthorized):
			msg = "The provider rejected the token. Test the account, or sign in again."
		}
		return cached, true, msg
	}
	entry = v.(*limitsEntry)
	if b, err := json.Marshal(entry); err == nil {
		_ = s.rdb.Set(ctx, limitsKey(a.ID), b, limitsKeep).Err()
	}
	s.publishHeadroom(ctx, a.ID, entry)
	return entry, false, ""
}

// publishHeadroom tells the gateway how much quota the account has left
// (provider_routing's min_headroom_pct): "<percent left>|<unix seconds>" in
// one Redis hash. An account that reports no windows is removed.
func (s *Server) publishHeadroom(ctx context.Context, id string, e *limitsEntry) {
	left, ok := e.Snapshot.Headroom()
	if !ok {
		_ = s.rdb.HDel(ctx, HeadroomKey, id).Err()
		return
	}
	_ = s.rdb.HSet(ctx, HeadroomKey, id, strconv.FormatFloat(left, 'f', 2, 64)+"|"+strconv.FormatInt(e.FetchedAt.Unix(), 10)).Err()
}

// fetchLimits reads the account's limits upstream. An OAuth token the
// provider rejects is refreshed once and the read retried.
func (s *Server) fetchLimits(ctx context.Context, a db.GetAccountAdminRow) (*limitsEntry, error) {
	creds, err := s.openCredentials(a)
	if err != nil {
		return nil, err
	}
	hc := &http.Client{Timeout: 20 * time.Second}
	if spec := s.accountProxySpec(ctx, a); spec != nil {
		hc.Transport = proxy.NewTransport(spec)
	}
	snap, err := providerlimits.Fetch(ctx, hc, a.Provider, creds.AccessToken, deref(a.BaseUrl), creds.Upstream())
	if errors.Is(err, providerlimits.ErrUnauthorized) && a.AuthKind == "oauth" {
		s.refreshAccount(ctx, a.ID, true)
		if fresh, err2 := s.q.GetAccountAdmin(ctx, a.ID); err2 == nil {
			if creds2, err2 := s.openCredentials(fresh); err2 == nil && creds2.AccessToken != creds.AccessToken {
				snap, err = providerlimits.Fetch(ctx, hc, a.Provider, creds2.AccessToken, deref(a.BaseUrl), creds2.Upstream())
			}
		}
	}
	if err != nil {
		return nil, err
	}
	return &limitsEntry{Snapshot: *snap, FetchedAt: s.now()}, nil
}

func (s *Server) openCredentials(a db.GetAccountAdminRow) (*oauth.Credentials, error) {
	plain, err := s.opts.Box.Open(a.Credentials, []byte(a.ID))
	if err != nil {
		return nil, errors.New("the stored credential can't be read with the current master key")
	}
	var c oauth.Credentials
	if err := json.Unmarshal(plain, &c); err != nil || c.AccessToken == "" {
		return nil, errors.New("the stored credential can't be read with the current master key")
	}
	return &c, nil
}

func (s *Server) cachedLimits(ctx context.Context, id string) *limitsEntry {
	b, err := s.rdb.Get(ctx, limitsKey(id)).Bytes()
	if err != nil {
		return nil
	}
	var e limitsEntry
	if json.Unmarshal(b, &e) != nil {
		return nil
	}
	return &e
}

func limitsResponse(res accountLimitsResponse, e *limitsEntry, stale bool, msg string) accountLimitsResponse {
	if e != nil {
		res.Snapshot = e.Snapshot
		if res.Windows == nil {
			res.Windows = []providerlimits.Window{}
		}
		res.FetchedAt = &e.FetchedAt
		res.Stale = stale
	}
	res.Error = msg
	return res
}
