package gateway

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Account balancing, after 9router's fallbackStrategy
// (src/sse/services/auth.js; MIT, see LICENSE-9router).
//
// sticky-balanced (the default): each user stays on one account, so the
// provider's prompt cache for their conversation stays warm. A user with no
// account yet, or whose account can't serve, goes to the least-loaded
// account: fewest users active in the last activeWindow, weighted by quota
// left. A user idle longer than cacheIdle (their cache has expired, so moving
// is free) moves when another account is clearly less loaded.
//
// round-robin: each user keeps an account for StickyLimit requests, then
// moves to the account no one has used for longest.
//
// Under both, accounts with less subscription quota left than
// MinHeadroomPct (as the control plane's limits poll last read it) are tried
// only after every other account. fill-first: the user's last good account,
// then pool order (the behavior before 0012). Failover is unchanged in every
// strategy: the next account in the order is tried on a 429, a 5xx or a
// refused connection.

// Routing is a provider's account-picking policy.
type Routing struct {
	Strategy       string // "sticky-balanced", "round-robin" or "fill-first"
	StickyLimit    int
	MinHeadroomPct float64
}

var defaultRouting = Routing{Strategy: "sticky-balanced", StickyLimit: 3, MinHeadroomPct: 5}

func (s *Snapshot) routingFor(provider string) Routing {
	if r, ok := s.routing[provider]; ok {
		return r
	}
	return defaultRouting
}

// HeadroomKey is the Redis hash the control plane publishes account headroom
// to: account ID → "<percent left>|<unix seconds read>".
const HeadroomKey = "qanat:account-headroom"

const (
	headroomEvery = 30 * time.Second
	// headroomMaxAge drops a read the poll hasn't renewed (the account
	// was disabled, or its usage API keeps failing): unknown, not low.
	headroomMaxAge = 30 * time.Minute
	rotationTTL    = time.Hour

	// cacheIdle is how long a provider keeps a prompt cache warm without a
	// hit (Anthropic: 5 minutes). Moving a user idle this long costs nothing.
	cacheIdle = 5 * time.Minute
	// activeWindow is how recently a user must have sent a request to count
	// toward an account's load.
	activeWindow = 15 * time.Minute
	// rebalanceMargin: an idle user moves only to an account at least this
	// much less loaded, so users don't flap between near-equal accounts.
	rebalanceMargin = 1.5
)

// rotation is a user's place in the round-robin plus when each account was
// last used. Redis holds it so every gateway replica rotates together; the
// in-memory form serves tests and a gateway started without Redis.
type rotation interface {
	// state returns the user's current account and how many requests in a
	// row it has served, and when each of ids was last used (0 = never).
	state(ctx context.Context, userProvider string, provider string, ids []string) (current string, n int, lastUsed []int64, err error)
	// used records that the account served the user's request.
	used(ctx context.Context, userProvider, provider, accountID string, now time.Time) error

	// pin returns the user's pinned account, when they last used it (unix
	// ms, 0 = never), and how many other users were active on each of ids
	// since now-activeWindow (sticky-balanced).
	pin(ctx context.Context, provider, userID string, ids []string, now time.Time) (current string, last int64, active []int, err error)
	// pinned records that the account served the user (sticky-balanced).
	pinned(ctx context.Context, provider, userID, accountID string, now time.Time) error
}

type balancer struct {
	rot rotation
	mu  sync.RWMutex
	// headroom is account ID → percent left, refreshed from Redis.
	headroom map[string]float64
}

func newBalancer() *balancer {
	return &balancer{rot: newMemRotation(), headroom: map[string]float64{}}
}

// balancer returns the gateway's balancer, made on first use for gateways
// built without New (tests).
func (g *Gateway) balancer() *balancer {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.bal == nil {
		g.bal = newBalancer()
	}
	return g.bal
}

// UseRedis makes the rotation shared across gateway replicas. Call it
// before serving.
func (g *Gateway) UseRedis(rdb *redis.Client) { g.balancer().rot = &redisRotation{rdb: rdb} }

// RunHeadroom reads the control plane's published account headroom until
// ctx ends.
func (g *Gateway) RunHeadroom(ctx context.Context, rdb *redis.Client) {
	t := time.NewTicker(headroomEvery)
	defer t.Stop()
	for {
		g.balancer().loadHeadroom(ctx, rdb)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (b *balancer) loadHeadroom(ctx context.Context, rdb *redis.Client) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	raw, err := rdb.HGetAll(ctx, HeadroomKey).Result()
	if err != nil {
		redisErrors.Inc()
		return // keep the last view
	}
	b.setHeadroom(parseHeadroom(raw, time.Now()))
}

func parseHeadroom(raw map[string]string, now time.Time) map[string]float64 {
	out := make(map[string]float64, len(raw))
	for id, v := range raw {
		pct, at, ok := strings.Cut(v, "|")
		if !ok {
			continue
		}
		left, err1 := strconv.ParseFloat(pct, 64)
		sec, err2 := strconv.ParseInt(at, 10, 64)
		if err1 != nil || err2 != nil || now.Sub(time.Unix(sec, 0)) > headroomMaxAge {
			continue
		}
		out[id] = left
	}
	return out
}

func (b *balancer) setHeadroom(h map[string]float64) {
	b.mu.Lock()
	b.headroom = h
	b.mu.Unlock()
}

func (b *balancer) left(id string) (float64, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	v, ok := b.headroom[id]
	return v, ok
}

// order arranges usable accounts (candidates, in fill-first order) for one
// request. Under round-robin the first is the pick and the rest are the
// failover order: the other healthy accounts by headroom, then the low ones.
func (g *Gateway) order(ctx context.Context, rt *route, provider string, accounts []*Account) []*Account {
	pol := rt.snap.routingFor(provider)
	if pol.Strategy == "fill-first" || len(accounts) < 2 {
		return accounts
	}
	type ranked struct {
		a     *Account
		left  float64
		known bool
	}
	bal := g.balancer()
	var ok, low []ranked
	for _, a := range accounts {
		left, known := bal.left(a.ID)
		r := ranked{a, left, known}
		if known && left < pol.MinHeadroomPct {
			low = append(low, r)
		} else {
			ok = append(ok, r)
		}
	}
	// Most quota left first; accounts that report none (API keys) sit in
	// the middle; ties keep pool order.
	byLeft := func(rs []ranked) {
		score := func(r ranked) float64 {
			if !r.known {
				return 50
			}
			return r.left
		}
		sort.SliceStable(rs, func(i, j int) bool { return score(rs[i]) > score(rs[j]) })
	}
	byLeft(ok)
	byLeft(low)
	pool := ok
	if len(pool) == 0 {
		pool = low // every account is low: rotate among them anyway
	}

	ids := make([]string, len(pool))
	for i, r := range pool {
		ids[i] = r.a.ID
	}
	var pick int
	if pol.Strategy == "round-robin" {
		pick = g.pickRoundRobin(ctx, bal, rt, provider, ids, pol)
	} else {
		weights := make([]float64, len(pool))
		for i, r := range pool {
			weights[i] = weight(r.left, r.known)
		}
		pick = g.pickBalanced(ctx, bal, rt, provider, ids, weights)
	}

	chosen := pool[pick].a
	out := make([]*Account, 0, len(accounts))
	out = append(out, chosen)
	for _, rs := range [][]ranked{ok, low} {
		for _, r := range rs {
			if r.a != chosen {
				out = append(out, r.a)
			}
		}
	}
	return out
}

func (g *Gateway) pickRoundRobin(ctx context.Context, bal *balancer, rt *route, provider string, ids []string, pol Routing) int {
	sctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	current, n, lastUsed, err := bal.rot.state(sctx, rt.user.ID+"|"+provider, provider, ids)
	cancel()
	if err != nil {
		redisErrors.Inc()
		g.log.Warn("round-robin state unavailable; routing by headroom", "err", err)
	}
	for i, id := range ids {
		if id == current && n < pol.StickyLimit {
			return i // keep the account for this user
		}
	}
	// The least recently used account, leaving the current one if there's
	// another; ties go to the most quota left (ids order).
	pick := -1
	for i, id := range ids {
		if id == current && len(ids) > 1 {
			continue
		}
		if pick < 0 || lastUsed[i] < lastUsed[pick] {
			pick = i
		}
	}
	return pick
}

// weight is how much load an account should take relative to the others:
// its share of quota left, floored so a nearly spent account still counts;
// an account that reports no quota (API keys) counts as half full.
func weight(left float64, known bool) float64 {
	if !known {
		return 0.5
	}
	return max(left, 10) / 100
}

// pickBalanced is sticky-balanced's choice among ids (sorted by headroom).
func (g *Gateway) pickBalanced(ctx context.Context, bal *balancer, rt *route, provider string, ids []string, weights []float64) int {
	now := time.Now()
	sctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	current, last, active, err := bal.rot.pin(sctx, provider, rt.user.ID, ids, now)
	cancel()
	if err != nil {
		redisErrors.Inc()
		g.log.Warn("sticky-balanced state unavailable; routing by headroom", "err", err)
	}
	// load is users per unit of quota left, counting this user as joining.
	load := func(i int) float64 { return float64(active[i]+1) / weights[i] }
	best := 0
	for i := range ids {
		if load(i) < load(best) {
			best = i // ties keep the most quota left
		}
	}
	for i, id := range ids {
		if id != current {
			continue
		}
		// Warm cache: stay. Cold: stay unless another account is clearly
		// less loaded.
		if now.Sub(time.UnixMilli(last)) < cacheIdle || load(i) <= rebalanceMargin*load(best) {
			return i
		}
		return best
	}
	return best // a new user, or their account can't serve now
}

// served records which account answered: the user's pin (sticky-balanced),
// their place in the round-robin, or fill-first's sticky binding.
func (g *Gateway) served(rt *route, provider, accountID string) {
	strategy := rt.snap.routingFor(provider).Strategy
	if strategy == "fill-first" {
		g.bindSticky(rt, provider, accountID)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var err error
		if strategy == "round-robin" {
			err = g.balancer().rot.used(ctx, rt.user.ID+"|"+provider, provider, accountID, time.Now())
		} else {
			err = g.balancer().rot.pinned(ctx, provider, rt.user.ID, accountID, time.Now())
		}
		if err != nil {
			redisErrors.Inc()
		}
	}()
}

// --- rotation state ------------------------------------------------------------

type memRotation struct {
	mu   sync.Mutex
	cur  map[string]memPlace
	last map[string]int64 // provider|account → unix ms
	// sticky-balanced: provider|user → pin, provider|account → user → unix ms
	pins    map[string]memPin
	actives map[string]map[string]int64
}

type memPin struct {
	account string
	last    int64
}

type memPlace struct {
	account string
	n       int
}

func newMemRotation() *memRotation {
	return &memRotation{cur: map[string]memPlace{}, last: map[string]int64{}, pins: map[string]memPin{}, actives: map[string]map[string]int64{}}
}

func (m *memRotation) state(_ context.Context, up, provider string, ids []string) (string, int, []int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	last := make([]int64, len(ids))
	for i, id := range ids {
		last[i] = m.last[provider+"|"+id]
	}
	p := m.cur[up]
	return p.account, p.n, last, nil
}

func (m *memRotation) used(_ context.Context, up, provider, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.cur[up]
	if p.account == id {
		p.n++
	} else {
		p = memPlace{id, 1}
	}
	m.cur[up] = p
	m.last[provider+"|"+id] = now.UnixMilli()
	return nil
}

func (m *memRotation) pin(_ context.Context, provider, user string, ids []string, now time.Time) (string, int64, []int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	since := now.Add(-activeWindow).UnixMilli()
	active := make([]int, len(ids))
	for i, id := range ids {
		for u, t := range m.actives[provider+"|"+id] {
			if u != user && t > since {
				active[i]++
			}
		}
	}
	p := m.pins[provider+"|"+user]
	return p.account, p.last, active, nil
}

func (m *memRotation) pinned(_ context.Context, provider, user, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := provider + "|" + user
	if prev := m.pins[k].account; prev != "" && prev != id {
		delete(m.actives[provider+"|"+prev], user)
	}
	m.pins[k] = memPin{id, now.UnixMilli()}
	set := m.actives[provider+"|"+id]
	if set == nil {
		set = map[string]int64{}
		m.actives[provider+"|"+id] = set
	}
	set[user] = now.UnixMilli()
	return nil
}

type redisRotation struct{ rdb *redis.Client }

func rrUserKey(up string) string       { return "qanat:rr:user:" + up }
func rrLastKey(provider string) string { return "qanat:rr:last:" + provider }

func (r *redisRotation) state(ctx context.Context, up, provider string, ids []string) (string, int, []int64, error) {
	pipe := r.rdb.Pipeline()
	place := pipe.HMGet(ctx, rrUserKey(up), "account", "n")
	scores := pipe.ZMScore(ctx, rrLastKey(provider), ids...)
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return "", 0, make([]int64, len(ids)), err
	}
	var current string
	var n int
	if v := place.Val(); len(v) == 2 {
		current, _ = v[0].(string)
		if s, ok := v[1].(string); ok {
			n, _ = strconv.Atoi(s)
		}
	}
	last := make([]int64, len(ids))
	for i, f := range scores.Val() {
		if i < len(last) {
			last[i] = int64(f) // a missing member scores 0: never used
		}
	}
	return current, n, last, nil
}

// LoadScripts caches the balancing scripts in Redis (see quota.LoadScripts).
func LoadScripts(ctx context.Context, rdb *redis.Client) error {
	for _, sc := range []*redis.Script{usedScript, pinnedScript} {
		if err := sc.Load(ctx, rdb).Err(); err != nil {
			return err
		}
	}
	return nil
}

// usedScript bumps the user's run on the same account or starts a new one,
// and stamps the account's last use — atomically, so replicas agree.
var usedScript = redis.NewScript(`
local cur = redis.call('HGET', KEYS[1], 'account')
if cur == ARGV[1] then
  redis.call('HINCRBY', KEYS[1], 'n', 1)
else
  redis.call('HSET', KEYS[1], 'account', ARGV[1], 'n', 1)
end
redis.call('PEXPIRE', KEYS[1], ARGV[3])
redis.call('ZADD', KEYS[2], ARGV[2], ARGV[1])
return 1`)

func (r *redisRotation) used(ctx context.Context, up, provider, id string, now time.Time) error {
	return usedScript.Run(ctx, r.rdb, []string{rrUserKey(up), rrLastKey(provider)},
		id, now.UnixMilli(), rotationTTL.Milliseconds()).Err()
}

// --- sticky-balanced in Redis ----------------------------------------------------

func pinKey(provider, user string) string       { return "qanat:sb:pin:" + provider + ":" + user }
func activeKey(provider, account string) string { return "qanat:sb:active:" + provider + ":" + account }

func (r *redisRotation) pin(ctx context.Context, provider, user string, ids []string, now time.Time) (string, int64, []int, error) {
	sinceMS := now.Add(-activeWindow).UnixMilli()
	since := strconv.FormatInt(sinceMS, 10)
	pipe := r.rdb.Pipeline()
	place := pipe.HMGet(ctx, pinKey(provider, user), "account", "t")
	counts := make([]*redis.IntCmd, len(ids))
	mine := make([]*redis.FloatCmd, len(ids))
	for i, id := range ids {
		counts[i] = pipe.ZCount(ctx, activeKey(provider, id), "("+since, "+inf")
		mine[i] = pipe.ZScore(ctx, activeKey(provider, id), user)
	}
	active := make([]int, len(ids))
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return "", 0, active, err
	}
	var current string
	var last int64
	if v := place.Val(); len(v) == 2 {
		current, _ = v[0].(string)
		if s, ok := v[1].(string); ok {
			last, _ = strconv.ParseInt(s, 10, 64)
		}
	}
	for i := range ids {
		active[i] = int(counts[i].Val())
		// Don't count the user against their own account.
		if t, err := mine[i].Result(); err == nil && int64(t) > sinceMS {
			active[i] = max(0, active[i]-1)
		}
	}
	return current, last, active, nil
}

// pinnedScript moves the user's pin and their place in the accounts' active
// sets atomically, and trims entries older than the active window.
var pinnedScript = redis.NewScript(`
local prev = redis.call('HGET', KEYS[1], 'account')
if prev and prev ~= ARGV[1] then
  redis.call('ZREM', ARGV[5] .. prev, ARGV[2])
end
redis.call('HSET', KEYS[1], 'account', ARGV[1], 't', ARGV[3])
redis.call('PEXPIRE', KEYS[1], ARGV[6])
redis.call('ZADD', KEYS[2], ARGV[3], ARGV[2])
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', '(' .. ARGV[4])
redis.call('PEXPIRE', KEYS[2], ARGV[6])
return 1`)

func (r *redisRotation) pinned(ctx context.Context, provider, user, id string, now time.Time) error {
	return pinnedScript.Run(ctx, r.rdb, []string{pinKey(provider, user), activeKey(provider, id)},
		id, user, now.UnixMilli(), now.Add(-activeWindow).UnixMilli(), activeKey(provider, ""), rotationTTL.Milliseconds()).Err()
}
