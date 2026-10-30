package gateway

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"qanat/internal/platform"
	"qanat/internal/proxy"
	"qanat/internal/testkit/itenv"
)

func rrSetup(routing map[string]Routing, ids ...string) (*Gateway, *route, []*Account) {
	var accts []*Account
	for _, id := range ids {
		accts = append(accts, &Account{ID: id, Provider: "claude", State: "healthy", Proxies: []*proxy.Spec{nil}})
	}
	s := &Snapshot{Gen: 1, accounts: map[string][]*Account{"claude": accts}, sticky: map[string]string{}, routing: routing}
	g := newTestGateway()
	g.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	rt := &route{caller: &caller{snap: s, user: &User{ID: "u1"}, key: &Key{}, plan: &Plan{}}}
	return g, rt, accts
}

func ids(as []*Account) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.ID
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func asUser(rt *route, id string) *route {
	r := *rt
	c := *rt.caller
	c.user = &User{ID: id}
	r.caller = &c
	return &r
}

// sticky-balanced (the default, no routing row): 10 users over 3 equal
// accounts spread 4/3/3, and each then stays on its account.
func TestSticky_SpreadsAndStays(t *testing.T) {
	g, rt, accts := rrSetup(nil, "a", "b", "c")
	ctx := t.Context()
	now := time.Now()
	home := map[string]string{}
	for u := range 10 {
		user := "u" + strconv.Itoa(u)
		pick := g.order(ctx, asUser(rt, user), "claude", accts)[0].ID
		g.balancer().rot.pinned(ctx, "claude", user, pick, now)
		home[user] = pick
	}
	count := map[string]int{}
	for _, a := range home {
		count[a]++
	}
	if count["a"]+count["b"]+count["c"] != 10 || max(count["a"], count["b"], count["c"]) != 4 || min(count["a"], count["b"], count["c"]) != 3 {
		t.Fatalf("spread %v", count)
	}
	for user, want := range home {
		for range 5 {
			if got := g.order(ctx, asUser(rt, user), "claude", accts)[0].ID; got != want {
				t.Fatalf("%s moved %s → %s with a warm cache", user, want, got)
			}
		}
	}
}

// More quota left takes proportionally more users.
func TestSticky_WeightsByHeadroom(t *testing.T) {
	g, rt, accts := rrSetup(nil, "big", "small")
	g.balancer().setHeadroom(map[string]float64{"big": 90, "small": 30})
	ctx := t.Context()
	count := map[string]int{}
	for u := range 8 {
		user := "u" + strconv.Itoa(u)
		pick := g.order(ctx, asUser(rt, user), "claude", accts)[0].ID
		g.balancer().rot.pinned(ctx, "claude", user, pick, time.Now())
		count[pick]++
	}
	if count["big"] != 6 || count["small"] != 2 {
		t.Fatalf("want 6/2 for 90%%/30%% left, got %v", count)
	}
}

// An idle user (cold cache) moves to a clearly less-loaded account; a warm
// one stays; a near-equal account isn't worth a move.
func TestSticky_RebalancesOnlyWhenCold(t *testing.T) {
	g, rt, accts := rrSetup(nil, "a", "b")
	ctx := t.Context()
	rot := g.balancer().rot
	now := time.Now()
	for _, u := range []string{"x1", "x2", "x3", "x4"} { // a is crowded
		rot.pinned(ctx, "claude", u, "a", now)
	}
	rot.pinned(ctx, "claude", "me", "a", now.Add(-time.Minute))
	if got := g.order(ctx, asUser(rt, "me"), "claude", accts)[0].ID; got != "a" {
		t.Fatalf("warm cache must stay: %s", got)
	}
	rot.pinned(ctx, "claude", "me", "a", now.Add(-10*time.Minute))
	if got := g.order(ctx, asUser(rt, "me"), "claude", accts)[0].ID; got != "b" {
		t.Fatalf("cold and crowded should move: %s", got)
	}

	g2, rt2, accts2 := rrSetup(nil, "a", "b")
	rot2 := g2.balancer().rot
	rot2.pinned(ctx, "claude", "y1", "b", now) // a: me only, b: one other
	rot2.pinned(ctx, "claude", "me", "a", now.Add(-10*time.Minute))
	if got := g2.order(ctx, asUser(rt2, "me"), "claude", accts2)[0].ID; got != "a" {
		t.Fatalf("no move between near-equal accounts: %s", got)
	}
}

// A pinned account that runs low (or can't serve) loses the user at once.
func TestSticky_LeavesLowAccount(t *testing.T) {
	g, rt, accts := rrSetup(nil, "a", "b")
	ctx := t.Context()
	g.balancer().rot.pinned(ctx, "claude", "me", "a", time.Now())
	g.balancer().setHeadroom(map[string]float64{"a": 2, "b": 50})
	order := ids(g.order(ctx, asUser(rt, "me"), "claude", accts))
	if !eq(order, []string{"b", "a"}) {
		t.Fatalf("low account must go last even when pinned: %v", order)
	}
	// Not a candidate at all (cooling down): the user lands elsewhere.
	if got := g.order(ctx, asUser(rt, "me"), "claude", accts[1:])[0].ID; got != "b" {
		t.Fatalf("unavailable pin: %s", got)
	}
}

// round-robin: a user keeps an account for StickyLimit requests, then moves
// to the account used longest ago; the rest follow as failover order.
func TestOrder_RoundRobinRotates(t *testing.T) {
	g, rt, accts := rrSetup(map[string]Routing{"claude": {Strategy: "round-robin", StickyLimit: 2, MinHeadroomPct: 5}}, "a", "b", "c")
	ctx := t.Context()
	var picks []string
	now := time.Unix(1000, 0)
	for i := range 7 {
		order := g.order(ctx, rt, "claude", accts)
		if len(order) != 3 {
			t.Fatalf("every account stays in the failover order: %v", ids(order))
		}
		picks = append(picks, order[0].ID)
		// what served() records, synchronously
		g.balancer().rot.used(ctx, "u1|claude", "claude", order[0].ID, now.Add(time.Duration(i)*time.Second))
	}
	if want := []string{"a", "a", "b", "b", "c", "c", "a"}; !eq(picks, want) {
		t.Fatalf("picks %v, want %v", picks, want)
	}
}

// Another user's traffic counts toward "least recently used", so users
// spread over the pool instead of all starting on the first account.
func TestOrder_UsersSpread(t *testing.T) {
	g, rt, accts := rrSetup(map[string]Routing{"claude": {Strategy: "round-robin", StickyLimit: 3, MinHeadroomPct: 5}}, "a", "b")
	ctx := t.Context()
	g.balancer().rot.used(ctx, "u0|claude", "claude", "a", time.Unix(1000, 0))
	if got := g.order(ctx, rt, "claude", accts)[0].ID; got != "b" {
		t.Fatalf("a new user should start on the idle account, got %s", got)
	}
}

// Accounts under MinHeadroomPct go last, both as pick and as failover; the
// most quota left breaks ties; unknown headroom (API keys) ranks mid.
func TestOrder_HeadroomAware(t *testing.T) {
	g, rt, accts := rrSetup(map[string]Routing{"claude": {Strategy: "round-robin", StickyLimit: 3, MinHeadroomPct: 10}}, "low", "mid", "high", "key")
	g.balancer().setHeadroom(map[string]float64{"low": 4, "mid": 40, "high": 90})
	order := g.order(t.Context(), rt, "claude", accts)
	if want := []string{"high", "key", "mid", "low"}; !eq(ids(order), want) {
		t.Fatalf("order %v, want %v", ids(order), want)
	}

	// The user's current account is kept while it has headroom…
	g.balancer().rot.used(t.Context(), "u1|claude", "claude", "mid", time.Unix(1000, 0))
	if got := g.order(t.Context(), rt, "claude", accts)[0].ID; got != "mid" {
		t.Fatalf("sticky pick: %s", got)
	}
	// …and dropped as soon as it runs low.
	g.balancer().setHeadroom(map[string]float64{"low": 4, "mid": 2, "high": 90})
	if got := g.order(t.Context(), rt, "claude", accts)[0].ID; got == "mid" || got == "low" {
		t.Fatalf("a low account must not be picked while others have headroom: %s", got)
	}

	// Every account low: still rotate among them rather than refuse.
	g.balancer().setHeadroom(map[string]float64{"low": 1, "mid": 2, "high": 3, "key": 0})
	if got := g.order(t.Context(), rt, "claude", accts); len(got) != 4 {
		t.Fatalf("all low: %v", ids(got))
	}
}

// fill-first keeps the order candidates built (sticky, then pool order).
func TestOrder_FillFirstUnchanged(t *testing.T) {
	g, rt, accts := rrSetup(map[string]Routing{"claude": {Strategy: "fill-first", StickyLimit: 3, MinHeadroomPct: 5}}, "a", "b", "c")
	g.balancer().setHeadroom(map[string]float64{"a": 0})
	if got := ids(g.order(t.Context(), rt, "claude", accts)); !eq(got, []string{"a", "b", "c"}) {
		t.Fatalf("fill-first reordered: %v", got)
	}
}

func TestParseHeadroom(t *testing.T) {
	now := time.Unix(10_000, 0)
	got := parseHeadroom(map[string]string{
		"fresh": "42.50|9900",
		"old":   "80|1000", // older than headroomMaxAge
		"bad":   "x|9900",
		"junk":  "nope",
	}, now)
	if len(got) != 1 || got["fresh"] != 42.5 {
		t.Fatalf("headroom %v", got)
	}
}

// The Redis rotation behaves like the in-memory one and is shared.
func TestRedisRotation(t *testing.T) {
	rdb := platform.Redis(itenv.Redis(t))
	t.Cleanup(func() { rdb.Close() })
	ctx := context.Background()
	up := "rr-test-user|claude"
	rdb.Del(ctx, rrUserKey(up), rrLastKey("rr-test"))
	t.Cleanup(func() { rdb.Del(ctx, rrUserKey(up), rrLastKey("rr-test")) })

	r := &redisRotation{rdb: rdb}
	for range 2 {
		if err := r.used(ctx, up, "rr-test", "a", time.UnixMilli(5000)); err != nil {
			t.Fatal(err)
		}
	}
	r.used(ctx, "other|claude", "rr-test", "b", time.UnixMilli(7000))
	cur, n, last, err := r.state(ctx, up, "rr-test", []string{"a", "b", "c"})
	if err != nil || cur != "a" || n != 2 || last[0] != 5000 || last[1] != 7000 || last[2] != 0 {
		t.Fatalf("state: %q %d %v %v", cur, n, last, err)
	}
	r.used(ctx, up, "rr-test", "c", time.UnixMilli(9000))
	if cur, n, _, _ := r.state(ctx, up, "rr-test", []string{"c"}); cur != "c" || n != 1 {
		t.Fatalf("switch resets the run: %q %d", cur, n)
	}
	if ttl := rdb.PTTL(ctx, rrUserKey(up)).Val(); ttl <= 0 || ttl > rotationTTL {
		t.Fatalf("user place must expire: %v", ttl)
	}

	// sticky-balanced state
	prov := "sb-test"
	keys := []string{pinKey(prov, "me"), pinKey(prov, "u2"), activeKey(prov, "a"), activeKey(prov, "b")}
	rdb.Del(ctx, keys...)
	t.Cleanup(func() { rdb.Del(ctx, keys...) })
	now := time.Now()
	r.pinned(ctx, prov, "me", "a", now)
	r.pinned(ctx, prov, "u2", "a", now)
	cur, lastMS, active, err := r.pin(ctx, prov, "me", []string{"a", "b"}, now)
	if err != nil || cur != "a" || lastMS != now.UnixMilli() || active[0] != 1 || active[1] != 0 {
		t.Fatalf("pin: %q %d %v %v", cur, lastMS, active, err)
	}
	r.pinned(ctx, prov, "me", "b", now) // moving removes me from a
	_, _, active, _ = r.pin(ctx, prov, "u2", []string{"a", "b"}, now)
	if active[0] != 0 || active[1] != 1 {
		t.Fatalf("after move: %v", active)
	}
	// Users idle past the active window don't count.
	_, _, active, _ = r.pin(ctx, prov, "x", []string{"a", "b"}, now.Add(activeWindow+time.Minute))
	if active[0] != 0 || active[1] != 0 {
		t.Fatalf("stale actives counted: %v", active)
	}
}
