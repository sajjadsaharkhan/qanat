package control

import (
	"context"
	"sync"
	"time"

	"qanat/internal/core/providerlimits"
)

// The limits poll keeps every subscription account's headroom fresh for
// quota-aware routing, whether or not anyone has the Accounts page open.
// One control replica polls at a time (a Redis lock); reads go through
// loadLimits, so the cache, the shared in-flight call and the back-off after
// a rate-limited usage API all apply.

const (
	limitsPollEvery = 5 * time.Minute
	// limitsPollAge refreshes a read a little younger than the poll period,
	// so a tick never finds last tick's read still "fresh".
	limitsPollAge     = limitsPollEvery - 30*time.Second
	limitsPollLockTTL = limitsPollEvery - 10*time.Second
	limitsPollWorkers = 4
)

// RunLimitsPoll polls until ctx ends.
func (s *Server) RunLimitsPoll(ctx context.Context) {
	if s.opts.Box == nil {
		return // stored credentials can't be read
	}
	first := time.NewTimer(30 * time.Second) // let the server settle first
	defer first.Stop()
	select {
	case <-ctx.Done():
		return
	case <-first.C:
	}
	t := time.NewTicker(limitsPollEvery)
	defer t.Stop()
	for {
		s.pollLimits(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) pollLimits(ctx context.Context) {
	ok, err := s.rdb.SetNX(ctx, "qanat:limits-poll", "1", limitsPollLockTTL).Result()
	if err != nil || !ok {
		return // another replica polls this round, or Redis is down
	}
	accounts, err := s.q.ListAccounts(ctx)
	if err != nil {
		s.log.Warn("limits poll: can't list accounts", "err", err)
		return
	}
	ids := make(chan string)
	var wg sync.WaitGroup
	for range limitsPollWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range ids {
				a, err := s.q.GetAccountAdmin(ctx, id)
				if err != nil {
					continue
				}
				if _, stale, msg := s.loadLimits(ctx, a, limitsPollAge); stale {
					s.log.Debug("limits poll: kept last read", "account", id, "reason", msg)
				}
			}
		}()
	}
	for _, a := range accounts {
		if a.DisabledAt != nil || !a.HasCredentials || a.State == "disconnected" || !providerlimits.Supported(a.Provider, a.AuthKind) {
			continue
		}
		select {
		case ids <- a.ID:
		case <-ctx.Done():
		}
	}
	close(ids)
	wg.Wait()
}
