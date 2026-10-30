package quota

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Window is one applicable quota window with its limit in micro-USD.
type Window struct {
	Kind       Kind
	Scope      string // "" = all models
	LimitMicro int64
}

// Policy is what admission checks for one request (PRD §10.2).
type Policy struct {
	Windows       []Window
	RPM           int // 0 = unlimited
	MaxConcurrent int // 0 = unlimited
	Anchors       Anchors
}

// Reason names why a request was refused.
type Reason string

const (
	ReasonZeroQuota   Reason = "zero_quota"
	ReasonQuota       Reason = "quota"
	ReasonRPM         Reason = "rpm"
	ReasonConcurrency Reason = "concurrency"
)

// Decision is the admission result.
type Decision struct {
	Admitted bool
	// RemainingMicro is the least headroom across windows (for
	// x-qanat-quota-remaining-usd), when admitted.
	RemainingMicro int64
	Reason         Reason
	Window         Window // the exhausted window, for ReasonQuota
	ResetAt        time.Time
	RetryAfter     time.Duration
}

// Store is the CounterStore of PRD §10.6: hot counters in Redis.
type Store struct{ rdb *redis.Client }

// NewStore wraps a Redis client.
func NewStore(rdb *redis.Client) *Store { return &Store{rdb: rdb} }

// staleInFlight drops concurrency slots a crashed gateway never released.
const staleInFlight = 15 * time.Minute

func concKey(userID string) string { return "qanat:conc:" + userID }
func rpmKey(userID string, now time.Time) string {
	return "qanat:rpm:" + userID + ":" + strconv.FormatInt(now.Unix()/60, 10)
}

// admitScript checks every window, then RPM, then concurrency, atomically.
// KEYS: conc, rpm, then each window's counter keys.
// ARGV: now_ms, req_id, max_conc, rpm, stale_ms, n_windows, then per window
// (n_keys, limit).
var admitScript = redis.NewScript(`
local now, req, maxc, rpm, stale, nw = tonumber(ARGV[1]), ARGV[2], tonumber(ARGV[3]), tonumber(ARGV[4]), tonumber(ARGV[5]), tonumber(ARGV[6])
local k, a = 3, 7
local minrem = nil
for w = 1, nw do
  local nkeys, limit = tonumber(ARGV[a]), tonumber(ARGV[a + 1])
  a = a + 2
  local used = 0
  for i = 1, nkeys do
    used = used + (tonumber(redis.call('GET', KEYS[k])) or 0)
    k = k + 1
  end
  local rem = limit - used
  if rem <= 0 then return {0, 'quota', w} end
  if minrem == nil or rem < minrem then minrem = rem end
end
if rpm > 0 then
  local c = redis.call('INCR', KEYS[2])
  if c == 1 then redis.call('PEXPIRE', KEYS[2], 61000) end
  if c > rpm then redis.call('DECR', KEYS[2]) return {0, 'rpm', 0} end
end
if maxc > 0 then
  redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - stale)
  if redis.call('ZCARD', KEYS[1]) >= maxc then
    if rpm > 0 then redis.call('DECR', KEYS[2]) end
    return {0, 'concurrency', 0}
  end
  redis.call('ZADD', KEYS[1], now, req)
  redis.call('PEXPIRE', KEYS[1], stale)
end
return {1, minrem or 0, 0}
`)

// windowKeys returns the counter keys a window sums over.
func windowKeys(userID string, w Window, a Anchors, now time.Time) []string {
	if w.Kind == Rolling5h {
		return BucketKeys(userID, w.Scope, now)
	}
	start, _ := Bounds(w.Kind, a, now)
	return []string{FixedKey(userID, w.Kind, w.Scope, start)}
}

// Admit applies admit-then-settle step 1 (PRD §10.4): admit when every window
// has remaining > 0 and rate limits allow, taking a concurrency slot reqID.
func (s *Store) Admit(ctx context.Context, userID, reqID string, p Policy, now time.Time) (Decision, error) {
	if len(p.Windows) == 0 {
		return Decision{Reason: ReasonZeroQuota, RetryAfter: time.Hour}, nil
	}
	keys := []string{concKey(userID), rpmKey(userID, now)}
	args := []any{now.UnixMilli(), reqID, p.MaxConcurrent, p.RPM, staleInFlight.Milliseconds(), len(p.Windows)}
	for _, w := range p.Windows {
		wk := windowKeys(userID, w, p.Anchors, now)
		keys = append(keys, wk...)
		args = append(args, len(wk), w.LimitMicro)
	}
	res, err := admitScript.Run(ctx, s.rdb, keys, args...).Slice()
	if err != nil {
		return Decision{}, err
	}
	if res[0].(int64) == 1 {
		return Decision{Admitted: true, RemainingMicro: res[1].(int64)}, nil
	}
	d := Decision{Reason: Reason(res[1].(string))}
	switch d.Reason {
	case ReasonQuota:
		d.Window = p.Windows[res[2].(int64)-1]
		d.ResetAt, d.RetryAfter = s.freesAt(ctx, userID, d.Window, p.Anchors, now)
	case ReasonRPM:
		d.RetryAfter = time.Duration(60-now.Unix()%60) * time.Second
	case ReasonConcurrency:
		d.RetryAfter = 2 * time.Second
	}
	return d, nil
}

// freesAt is when an exhausted window has capacity again: the reset for a
// fixed window; for the rolling window, when its oldest spend ages out.
func (s *Store) freesAt(ctx context.Context, userID string, w Window, a Anchors, now time.Time) (time.Time, time.Duration) {
	if w.Kind != Rolling5h {
		_, reset := Bounds(w.Kind, a, now)
		return reset, reset.Sub(now)
	}
	keys := BucketKeys(userID, w.Scope, now)
	vals, err := s.rdb.MGet(ctx, keys...).Result()
	start, _ := Bounds(Rolling5h, a, now)
	if err == nil {
		for i, v := range vals {
			if v != nil {
				at := start.Add(time.Duration(i+1)*BucketSpan + RollingSpan - BucketSpan)
				return at, at.Sub(now)
			}
		}
	}
	return now.Add(BucketSpan), BucketSpan
}

// Charge adds micro-USD to every window in ws (admit-then-settle steps 2–3).
func (s *Store) Charge(ctx context.Context, userID string, ws []Window, a Anchors, micro int64, now time.Time) error {
	if micro <= 0 || len(ws) == 0 {
		return nil
	}
	pipe := s.rdb.Pipeline()
	for _, w := range ws {
		if w.Kind == Rolling5h {
			start, _ := Bounds(Rolling5h, a, now)
			bucket := start.Add(RollingSpan - BucketSpan) // the current bucket
			key := "qanat:q5:" + userID + ":" + w.Scope + ":" + strconv.FormatInt(bucket.Unix(), 10)
			pipe.IncrBy(ctx, key, micro)
			pipe.Expire(ctx, key, RollingSpan+BucketSpan)
			continue
		}
		start, reset := Bounds(w.Kind, a, now)
		key := FixedKey(userID, w.Kind, w.Scope, start)
		pipe.IncrBy(ctx, key, micro)
		pipe.ExpireAt(ctx, key, reset.Add(time.Hour))
	}
	_, err := pipe.Exec(ctx)
	return err
}

// InFlight counts the requests these users have streaming right now: their
// concurrency slots, ignoring stale ones a crashed gateway never released.
func (s *Store) InFlight(ctx context.Context, userIDs []string, now time.Time) (int64, error) {
	pipe := s.rdb.Pipeline()
	min := strconv.FormatInt(now.Add(-staleInFlight).UnixMilli(), 10)
	cmds := make([]*redis.IntCmd, len(userIDs))
	for i, id := range userIDs {
		cmds[i] = pipe.ZCount(ctx, concKey(id), min, "+inf")
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return 0, err
	}
	var n int64
	for _, c := range cmds {
		n += c.Val()
	}
	return n, nil
}

// Release frees the concurrency slot taken by Admit.
func (s *Store) Release(ctx context.Context, userID, reqID string) error {
	return s.rdb.ZRem(ctx, concKey(userID), reqID).Err()
}
