package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"qanat/internal/core/oauth"
	"qanat/internal/proxy"
	"qanat/internal/store/db"
)

// OAuth token refresh (PRD REQ-OAUTH-03), like 9router's background token
// refresh: a sweep refreshes access tokens that expire within
// oauth.RefreshLead, and the gateway asks for an immediate refresh when an
// OAuth account answers 401 (NOTIFY qanat_oauth_refresh). Refresh runs through
// the account's own proxy and is serialized per account with a Redis lock, so
// control replicas never spend one refresh token twice.

const (
	refreshEvery = time.Minute
	// refreshLockTTL outlives one token-endpoint round trip.
	refreshLockTTL = time.Minute
	// refreshDebounce keeps a burst of 401s from refreshing an account
	// again right after a refresh.
	refreshDebounce = 30 * time.Second
)

// RunTokenRefresh sweeps until ctx ends and serves the gateway's refresh
// requests.
func (s *Server) RunTokenRefresh(ctx context.Context) {
	go s.listenRefreshRequests(ctx)
	t := time.NewTicker(refreshEvery)
	defer t.Stop()
	for {
		s.refreshDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// refreshDue refreshes every OAuth account whose token expires soon.
func (s *Server) refreshDue(ctx context.Context) {
	ids, err := s.q.DueOAuthAccounts(ctx, ptr(s.now().Add(oauth.RefreshLead)))
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("oauth refresh sweep failed", "err", err)
		}
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		s.refreshAccount(ctx, id, false)
	}
}

// listenRefreshRequests serves the gateway's NOTIFYs, reconnecting on error.
func (s *Server) listenRefreshRequests(ctx context.Context) {
	for ctx.Err() == nil {
		if err := s.listenRefreshOnce(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("oauth refresh listener lost; reconnecting", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (s *Server) listenRefreshOnce(ctx context.Context) error {
	conn, err := s.pg.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+oauth.RefreshChannel); err != nil {
		return err
	}
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		go s.refreshAccount(ctx, n.Payload, true)
	}
}

// refreshAccount refreshes one account's tokens. A sweep (force false) skips
// an account another replica already refreshed; a 401 (force true) refreshes
// unless that just happened.
func (s *Server) refreshAccount(ctx context.Context, id string, force bool) {
	if s.opts.Box == nil {
		return
	}
	lockKey := "qanat:oauth-refresh:" + id
	ok, err := s.rdb.SetNX(ctx, lockKey, "1", refreshLockTTL).Result()
	if err != nil || !ok {
		return // another replica holds it, or Redis is down: next sweep retries
	}
	defer s.rdb.Del(context.WithoutCancel(ctx), lockKey)

	a, err := s.q.GetAccountAdmin(ctx, id)
	if err != nil || a.AuthKind != "oauth" || len(a.Credentials) == 0 || a.DisabledAt != nil {
		return
	}
	now := s.now()
	if force {
		if a.LastRefreshAt != nil && now.Sub(*a.LastRefreshAt) < refreshDebounce {
			return
		}
	} else if a.ExpiresAt == nil || a.ExpiresAt.After(now.Add(oauth.RefreshLead)) || a.State == "disconnected" {
		return
	}
	f := oauth.Get(a.Provider)
	if f == nil {
		return
	}
	plain, err := s.opts.Box.Open(a.Credentials, []byte(a.ID))
	if err != nil {
		s.refreshFailed(ctx, a.ID, "The stored credential can't be read with the current master key.", false)
		return
	}
	var creds oauth.Credentials
	if err := json.Unmarshal(plain, &creds); err != nil {
		s.refreshFailed(ctx, a.ID, "The stored credential can't be read with the current master key.", false)
		return
	}
	if creds.RefreshToken == "" {
		s.refreshFailed(ctx, a.ID, "No refresh token is stored. Sign in again.", true)
		return
	}

	hc := &http.Client{Timeout: 30 * time.Second}
	if spec := s.accountProxySpec(ctx, a); spec != nil {
		hc.Transport = proxy.NewTransport(spec)
	}
	t, err := f.Refresh(ctx, hc, creds.RefreshToken)
	if err != nil {
		var unrecoverable *oauth.UnrecoverableError
		dead := errors.As(err, &unrecoverable)
		msg := cleanUpstreamError(err)
		if dead {
			msg = "The provider revoked this sign-in (" + msg + "). Sign in again."
		}
		s.refreshFailed(ctx, a.ID, msg, dead)
		s.log.Warn("oauth refresh failed", "account", a.ID, "provider", a.Provider, "permanent", dead, "err", err)
		return
	}
	b, _ := json.Marshal(creds.Refreshed(t))
	sealed, err := s.opts.Box.Seal(b, []byte(a.ID))
	if err != nil {
		s.log.Error("can't seal refreshed credentials", "account", a.ID, "err", err)
		return
	}
	var expiresAt *time.Time
	if t.ExpiresIn > 0 {
		expiresAt = ptr(now.Add(time.Duration(t.ExpiresIn) * time.Second))
	}
	if _, err := s.q.SetOAuthRefreshed(ctx, db.SetOAuthRefreshedParams{
		ID: a.ID, Credentials: sealed, ExpiresAt: expiresAt, LastRefreshAt: &now,
	}); err != nil {
		s.log.Error("can't store refreshed credentials", "account", a.ID, "err", err)
		return
	}
	s.log.Info("oauth token refreshed", "account", a.ID, "provider", a.Provider, "forced", force)
}

// refreshFailed records the outcome; disconnect takes the account out of the
// pool until someone signs in again.
func (s *Server) refreshFailed(ctx context.Context, id, msg string, disconnect bool) {
	if err := s.q.SetOAuthRefreshFailed(ctx, db.SetOAuthRefreshFailedParams{
		ID: id, LastRefreshAt: ptr(s.now()), LastRefreshError: &msg, Disconnect: disconnect,
	}); err != nil {
		s.log.Warn("can't record oauth refresh failure", "account", id, "err", err)
	}
}

func ptr[T any](v T) *T { return &v }
