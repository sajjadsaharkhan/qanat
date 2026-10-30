// Command demoseed fills a local stack with demo data so every dashboard page
// has something to show, and removes it again (-clean). It is for the local
// compose stack only (`make local-demo`, `make local-demo-clean`).
//
// Every demo row has an ID starting with de000000-, so cleaning removes
// exactly what seeding added and nothing the owner created. The signed-in
// user view (Overview quotas, Keys, Requests) is seeded for the Keycloak test
// user alice@qanat.local, if she has signed in once; root is never touched.
//
// Demo upstream accounts point at http://demo-upstream.invalid: the gateway
// can't connect, so it moves on to the next account and no request ever
// reaches a real provider.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"qanat/internal/config"
	"qanat/internal/core/apikey"
	"qanat/internal/core/quota"
	"qanat/internal/core/secretbox"
	"qanat/internal/core/usage"
	"qanat/internal/platform"
	"qanat/internal/proxy"
)

const demoUser = "alice@qanat.local"

// id builds a demo UUID: de000000-0000-4000-8000-<kind><n>.
func id(kind byte, n int) string { return fmt.Sprintf("de000000-0000-4000-8000-%c%011d", kind, n) }

var clean = flag.Bool("clean", false, "remove the demo data instead of adding it")

func main() {
	flag.Parse()
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	pg, err := platform.Postgres(ctx, cfg.PostgresDSN)
	if err != nil {
		log.Fatal(err)
	}
	defer pg.Close()
	rdb := platform.Redis(cfg.RedisAddr)
	defer rdb.Close()

	var alice string
	if err := pg.QueryRow(ctx, `SELECT id FROM users WHERE lower(email) = $1`, demoUser).Scan(&alice); err != nil && err != pgx.ErrNoRows {
		log.Fatal(err)
	}

	if err := removeDemo(ctx, pg, rdb, alice); err != nil {
		log.Fatal("clean: ", err)
	}
	if *clean {
		fmt.Println("Demo data removed.")
		return
	}

	var box *secretbox.Box
	if cfg.MasterKey != "" {
		if box, err = secretbox.New(cfg.MasterKey); err != nil {
			log.Fatal(err)
		}
	}
	s := &seeder{ctx: ctx, rdb: rdb, box: box, alice: alice, rnd: rand.New(rand.NewPCG(7, 11)), now: time.Now()}
	if err := pgx.BeginFunc(ctx, pg, s.postgres); err != nil {
		log.Fatal("seed: ", err)
	}
	if err := s.redis(); err != nil {
		log.Fatal("seed redis: ", err)
	}
	fmt.Println("Demo data added: 3 Plans, 8 users, 5 accounts, 3 proxies, 5 model mappings, 4 prices, 14 days of usage, audit entries.")
	if alice == "" {
		fmt.Println("alice@qanat.local hasn't signed in yet, so the personal pages (Overview, Keys, Requests) got no demo data.")
		fmt.Println("Sign in once as alice/alice with Keycloak, then run this again.")
	} else {
		fmt.Println("Personal pages: sign in with Keycloak as alice/alice. Admin pages: any admin.")
	}
}

// removeDemo deletes every demo row, in foreign-key order, and alice's demo
// Redis state. Users moved onto a demo Plan go back to the default Plan.
func removeDemo(ctx context.Context, pg *pgxpool.Pool, rdb *redis.Client, alice string) error {
	const demo = `'de000000-%'`
	err := pgx.BeginFunc(ctx, pg, func(tx pgx.Tx) error {
		for _, q := range []string{
			`UPDATE users SET plan_id = (SELECT id FROM plans WHERE is_default) WHERE plan_id::text LIKE ` + demo,
			`DELETE FROM audit_log WHERE actor_id::text LIKE ` + demo + ` OR entity_id LIKE ` + demo,
			`DELETE FROM usage_5m WHERE key_id::text LIKE ` + demo,
			`DELETE FROM usage_hourly WHERE key_id::text LIKE ` + demo,
			`DELETE FROM usage_daily WHERE key_id::text LIKE ` + demo,
			`DELETE FROM api_keys WHERE id::text LIKE ` + demo,
			`DELETE FROM quota_grants WHERE id::text LIKE ` + demo,
			`DELETE FROM provider_accounts WHERE id::text LIKE ` + demo,
			`DELETE FROM proxies WHERE id::text LIKE ` + demo,
			`DELETE FROM model_mappings WHERE id::text LIKE ` + demo,
			`DELETE FROM model_prices WHERE confirmed_by::text LIKE ` + demo,
			`DELETE FROM users WHERE id::text LIKE ` + demo,
			`DELETE FROM plans WHERE id::text LIKE ` + demo,
		} {
			if _, err := tx.Exec(ctx, q); err != nil {
				return fmt.Errorf("%s: %w", q, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Quota counters of demo users.
	for _, pattern := range []string{"qanat:q:de000000-*", "qanat:q5:de000000-*"} {
		keys, err := rdb.Keys(ctx, pattern).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := rdb.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
	}
	if alice == "" {
		return nil
	}
	// Recent requests: keep alice's real entries, drop the demo ones.
	key := usage.RecentKey(alice)
	items, err := rdb.LRange(ctx, key, 0, -1).Result()
	if err != nil {
		return err
	}
	pipe := rdb.TxPipeline()
	pipe.Del(ctx, key)
	kept := 0
	for _, it := range items {
		if !strings.Contains(it, `"request_id":"qreq_demo_`) {
			pipe.RPush(ctx, key, it)
			kept++
		}
	}
	if kept > 0 {
		pipe.Expire(ctx, key, usage.RecentTTL)
	}
	// Quota counters: alice is a test user with no real traffic.
	for _, pattern := range []string{"qanat:q:" + alice + ":*", "qanat:q5:" + alice + ":*"} {
		keys, err := rdb.Keys(ctx, pattern).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			pipe.Del(ctx, keys...)
		}
	}
	_, err = pipe.Exec(ctx)
	return err
}

type seeder struct {
	ctx   context.Context
	tx    pgx.Tx
	rdb   *redis.Client
	box   *secretbox.Box
	alice string
	rnd   *rand.Rand
	now   time.Time

	prices map[string]price
	keys   map[string][]string // user → key IDs
	recent []usage.Event
}

type price struct {
	version                int64
	in, out, cRead, cWrite float64 // USD per 1M tokens
}

func (s *seeder) exec(sql string, args ...any) {
	if _, err := s.tx.Exec(s.ctx, sql, args...); err != nil {
		panic(fmt.Errorf("%s: %w", strings.Fields(sql)[0:3], err))
	}
}

type plan struct {
	id, name     string
	quotas       map[string]float64 // window kind → USD
	rpm, conc    int
	models       []string
	scopedSonnet float64 // optional per-model daily quota on "sonnet"
}

var plans = []plan{
	{id: id('a', 1), name: "Team", quotas: map[string]float64{"rolling_5h": 8, "daily": 20, "weekly": 80}, rpm: 60, conc: 4},
	{id: id('a', 2), name: "Power users", quotas: map[string]float64{"rolling_5h": 30, "daily": 75, "monthly": 1200}, rpm: 120, conc: 8, scopedSonnet: 50},
	{id: id('a', 3), name: "Interns", quotas: map[string]float64{"daily": 5, "weekly": 20}, rpm: 20, conc: 2, models: []string{"haiku", "sonnet"}},
}

type person struct {
	n                 int
	email, name, role string
	plan              int
	disabled          bool
	daysAgo           int
	activity          float64 // relative daily spend
}

var people = []person{
	{1, "sara.ahmadi@example.com", "Sara Ahmadi", "admin", 1, false, 40, 1.6},
	{2, "reza.karimi@example.com", "Reza Karimi", "user", 0, false, 38, 1.0},
	{3, "neda.hosseini@example.com", "Neda Hosseini", "user", 0, false, 31, 0.8},
	{4, "omid.rahimi@example.com", "Omid Rahimi", "user", 1, false, 27, 2.2},
	{5, "leila.moradi@example.com", "Leila Moradi", "user", 2, false, 12, 0.3},
	{6, "kian.jafari@example.com", "Kian Jafari", "user", 0, true, 25, 0.5},
	{7, "maryam.sadeghi@example.com", "Maryam Sadeghi", "user", 2, false, 9, 0.25},
	{8, "arash.tavakoli@example.com", "Arash Tavakoli", "user", 0, false, 18, 0.9},
}

// Models: Qanat name → provider and upstream model.
var models = []struct{ name, provider, upstream string }{
	{"sonnet", "claude", "claude-sonnet-4-6"},
	{"opus", "claude", "claude-opus-4-7"},
	{"haiku", "claude", "claude-haiku-4-5"},
	{"gpt", "codex", "gpt-5.5"},
}

func (s *seeder) postgres(tx pgx.Tx) (err error) {
	s.tx = tx
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	admin := id('b', 1) // Sara: actor for audit entries and price confirmations

	for _, p := range plans {
		s.exec(`INSERT INTO plans (id, name, created_at) VALUES ($1, $2, $3)`, p.id, p.name, s.now.AddDate(0, 0, -45))
		for kind, usd := range p.quotas {
			s.exec(`INSERT INTO plan_quotas (plan_id, window_kind, limit_usd) VALUES ($1, $2, $3)`, p.id, kind, usd)
		}
		if p.scopedSonnet > 0 {
			s.exec(`INSERT INTO plan_quotas (plan_id, window_kind, model_scope, limit_usd) VALUES ($1, 'daily', 'sonnet', $2)`, p.id, p.scopedSonnet)
		}
		s.exec(`INSERT INTO plan_limits (plan_id, rpm, max_concurrent) VALUES ($1, $2, $3)`, p.id, p.rpm, p.conc)
		for _, m := range p.models {
			s.exec(`INSERT INTO plan_models (plan_id, model_name) VALUES ($1, $2)`, p.id, m)
		}
	}

	for _, u := range people {
		var disabled *time.Time
		if u.disabled {
			t := s.now.AddDate(0, 0, -3)
			disabled = &t
		}
		s.exec(`INSERT INTO users (id, email, name, oidc_subject, role, plan_id, created_at, disabled_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			id('b', u.n), u.email, u.name, "demo-"+id('b', u.n), u.role, plans[u.plan].id, s.now.AddDate(0, 0, -u.daysAgo), disabled)
	}

	// Prices (per 1M tokens), confirmed by Sara.
	s.prices = map[string]price{}
	for _, p := range []struct {
		model                  string
		in, out, cRead, cWrite float64
	}{
		{"claude-sonnet-4-6", 3, 15, 0.3, 3.75},
		{"claude-opus-4-7", 5, 25, 0.5, 6.25},
		{"claude-haiku-4-5", 1, 5, 0.1, 1.25},
		{"gpt-5.5", 1.25, 10, 0.125, 0},
	} {
		var v int64
		if err := tx.QueryRow(s.ctx, `INSERT INTO model_prices (upstream_model, p_in, p_out, p_cache_read, p_cache_write, effective_from, confirmed_by, confirmed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $6) RETURNING id`, p.model, p.in, p.out, p.cRead, p.cWrite, s.now.AddDate(0, 0, -30), admin).Scan(&v); err != nil {
			return err
		}
		s.prices[p.model] = price{v, p.in, p.out, p.cRead, p.cWrite}
	}
	for i, m := range models {
		s.exec(`INSERT INTO model_mappings (id, pattern, is_glob, target_kind, provider, upstream_model, priority) VALUES ($1, $2, false, 'model', $3, $4, 0)`,
			id('c', i+1), m.name, m.provider, m.upstream)
	}
	s.exec(`INSERT INTO model_mappings (id, pattern, is_glob, target_kind, provider, upstream_model, priority) VALUES ($1, 'claude-*', true, 'model', 'claude', NULL, 100)`, id('c', 9))

	// Proxies. The Frankfurt password is sealed like the API does it.
	var sealed []byte
	if s.box != nil {
		if sealed, err = proxy.SealPassword(s.box, id('d', 1), "demo-password"); err != nil {
			return err
		}
	}
	s.exec(`INSERT INTO proxies (id, label, type, host, port, username, password, enabled, created_at) VALUES
		($1, 'Frankfurt egress', 'socks5h', 'fra.proxy.example.net', 1080, 'qanat-fra', $4, true, $5),
		($2, 'Amsterdam egress', 'http', 'ams.proxy.example.net', 3128, NULL, NULL, true, $5),
		($3, 'Office gateway', 'http', '10.20.0.5', 3128, NULL, NULL, false, $5)`,
		id('d', 1), id('d', 2), id('d', 3), sealed, s.now.AddDate(0, 0, -20))

	// Accounts. A sealed placeholder token, so they show "Configured".
	type acct struct {
		n               int
		provider, label string
		proxyID         *string
		direct          bool
		state           string
		cooldown        *time.Time
	}
	fra, ams, office := id('d', 1), id('d', 2), id('d', 3)
	cool := s.now.Add(12 * time.Minute)
	for _, a := range []acct{
		{1, "claude", "Claude Team 1", &fra, false, "healthy", nil},
		{2, "claude", "Claude Team 2", &ams, false, "cooldown", &cool},
		{3, "claude", "Claude Backup", nil, true, "disconnected", nil},
		{4, "codex", "Codex Pool", nil, false, "healthy", nil},
		{5, "antigravity", "Antigravity Lab", &office, false, "healthy", nil},
	} {
		aid := id('e', a.n)
		var creds []byte
		if s.box != nil {
			plain, _ := json.Marshal(map[string]string{"access_token": "demo-not-a-real-token"})
			if creds, err = s.box.Seal(plain, []byte(aid)); err != nil {
				return err
			}
		}
		s.exec(`INSERT INTO provider_accounts (id, provider, label, base_url, credentials, auth_kind, state, cooldown_until, position, proxy_id, proxy_direct, created_at)
			VALUES ($1, $2, $3, 'http://demo-upstream.invalid', $4, 'token', $5, $6, (SELECT coalesce(max(position), 0) + 1 FROM provider_accounts), $7, $8, $9)`,
			aid, a.provider, a.label, creds, a.state, a.cooldown, a.proxyID, a.direct, s.now.AddDate(0, 0, -18+a.n))
	}

	// Keys and usage.
	s.keys = map[string][]string{}
	keyN := 0
	addKey := func(user, name string, created, lastUsed time.Time, revoked, expires *time.Time) {
		keyN++
		kid := id('f', keyN)
		_, display, hash := apikey.New() // random; the full key is discarded, so it can never be used
		var last *time.Time
		var ip *string
		if !lastUsed.IsZero() {
			last = &lastUsed
			v := fmt.Sprintf("10.20.%d.%d", s.rnd.IntN(4), 10+s.rnd.IntN(200))
			ip = &v
		}
		s.exec(`INSERT INTO api_keys (id, user_id, name, hash, prefix, expires_at, last_used_at, last_ip, revoked_at, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::inet, $9, $10)`, kid, user, name, hash, display, expires, last, ip, revoked, created)
		if revoked == nil {
			s.keys[user] = append(s.keys[user], kid)
		}
	}
	for _, u := range people {
		uid := id('b', u.n)
		addKey(uid, "Laptop", s.now.AddDate(0, 0, -u.daysAgo+1), s.now.Add(-time.Duration(s.rnd.IntN(300))*time.Minute), nil, nil)
		if u.activity > 1 {
			addKey(uid, "CI runner", s.now.AddDate(0, 0, -10), s.now.Add(-time.Duration(s.rnd.IntN(90))*time.Minute), nil, nil)
		}
	}
	if s.alice != "" {
		revoked := s.now.AddDate(0, 0, -6)
		expires := s.now.AddDate(0, 0, 9)
		addKey(s.alice, "Work laptop", s.now.AddDate(0, 0, -20), s.now.Add(-7*time.Minute), nil, nil)
		addKey(s.alice, "Home desktop", s.now.AddDate(0, 0, -12), s.now.Add(-26*time.Hour), nil, &expires)
		addKey(s.alice, "Old laptop", s.now.AddDate(0, 0, -40), s.now.AddDate(0, 0, -7), &revoked, nil)
		s.exec(`UPDATE users SET plan_id = $2 WHERE id = $1`, s.alice, plans[0].id)
		s.exec(`INSERT INTO quota_grants (id, user_id, amount_usd, expires_at, granted_by) VALUES ($1, $2, 10, $3, $4)`,
			id('9', 1), s.alice, s.now.AddDate(0, 0, 5), admin)
	}

	for _, u := range people {
		s.usage(id('b', u.n), u.activity, plans[u.plan], u.disabled)
	}
	if s.alice != "" {
		s.usage(s.alice, 1.1, plans[0], false)
	}

	s.audit(admin)
	return nil
}

// usage writes 14 days of daily aggregates, 48 hours of hourly and 6 hours
// of 5-minute buckets for one user, and collects alice's recent requests.
func (s *seeder) usage(user string, activity float64, p plan, disabled bool) {
	keys := s.keys[user]
	if len(keys) == 0 {
		return
	}
	allowed := models
	if len(p.models) > 0 {
		allowed = nil
		for _, m := range models {
			for _, n := range p.models {
				if m.name == n {
					allowed = append(allowed, m)
				}
			}
		}
	}
	day := time.Date(s.now.Year(), s.now.Month(), s.now.Day(), 0, 0, 0, 0, time.UTC)
	for d := 13; d >= 0; d-- {
		if disabled && d < 3 {
			continue
		}
		bucket := day.AddDate(0, 0, -d)
		weekday := bucket.Weekday() == time.Friday // the Iranian weekend is quiet
		scale := activity * (0.6 + s.rnd.Float64()*0.8)
		if weekday {
			scale *= 0.25
		}
		parts := 1 + s.rnd.IntN(min(3, len(allowed))) // a few models a day, so charts stack
		for range parts {
			s.bucket("usage_daily", bucket, user, keys, allowed, scale*40/float64(parts), false)
		}
	}
	hour := s.now.Truncate(time.Hour)
	for h := 47; h >= 0; h-- {
		t := hour.Add(-time.Duration(h) * time.Hour)
		local := t.In(quota.DefaultAnchors().Location).Hour()
		if disabled || local < 8 || local > 22 {
			continue
		}
		s.bucket("usage_hourly", t, user, keys, allowed, activity*(1+s.rnd.Float64()*3), false)
	}
	five := s.now.Truncate(5 * time.Minute)
	for m := 71; m >= 0; m-- {
		if disabled || s.rnd.Float64() < 0.5 {
			continue
		}
		s.bucket("usage_5m", five.Add(-time.Duration(m)*5*time.Minute), user, keys, allowed, activity*(0.2+s.rnd.Float64()*0.5), s.rnd.Float64() < 0.05)
	}
	if user == s.alice {
		s.recentFor(user, keys, allowed)
	}
}

// bucket writes one aggregate row for a random model with requests requests.
func (s *seeder) bucket(table string, at time.Time, user string, keys []string, allowed []struct{ name, provider, upstream string }, requests float64, aborted bool) {
	n := int64(requests + 0.5)
	if n <= 0 {
		return
	}
	m := allowed[s.rnd.IntN(len(allowed))]
	pr := s.prices[m.upstream]
	in := n * int64(2_000+s.rnd.IntN(18_000))
	out := n * int64(300+s.rnd.IntN(2_500))
	cr := n * int64(s.rnd.IntN(60_000))
	cw := n * int64(s.rnd.IntN(4_000))
	cost := (float64(in)*pr.in + float64(out)*pr.out + float64(cr)*pr.cRead + float64(cw)*pr.cWrite) / 1e6
	// Demo accounts that served it: the two Claude Team accounts share Claude traffic.
	account := map[string]string{"claude": id('e', 1+s.rnd.IntN(2)), "codex": id('e', 4)}[m.provider]
	s.exec(`INSERT INTO `+table+` (bucket_start, user_id, key_id, provider, upstream_model, account_id, aborted, price_version,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, request_count)
		VALUES ($1, $2, $3, $4, $5, $14, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (bucket_start, user_id, key_id, provider, upstream_model, account_id, aborted, price_version) DO UPDATE SET
			input_tokens = `+table+`.input_tokens + EXCLUDED.input_tokens,
			output_tokens = `+table+`.output_tokens + EXCLUDED.output_tokens,
			cost_usd = `+table+`.cost_usd + EXCLUDED.cost_usd,
			request_count = `+table+`.request_count + EXCLUDED.request_count`,
		at, user, keys[s.rnd.IntN(len(keys))], m.provider, m.upstream, aborted, pr.version, in, out, cr, cw, cost, n, account)
}

// recentFor builds alice's Requests page: 40 requests over the last 30 hours,
// mostly successful, with a few of each failure the page can show.
func (s *seeder) recentFor(user string, keys []string, allowed []struct{ name, provider, upstream string }) {
	at := s.now.Add(-3 * time.Minute)
	for i := range 40 {
		m := allowed[s.rnd.IntN(len(allowed))]
		pr := s.prices[m.upstream]
		ev := usage.Event{
			RequestID: fmt.Sprintf("qreq_demo_%04d", i), At: at, UserID: user, KeyID: keys[s.rnd.IntN(len(keys))],
			ClientIP: "10.20.1.44", Model: m.name, Provider: m.provider, UpstreamModel: m.upstream, AccountID: id('e', 1),
			PriceVersion: pr.version, Status: 200, StopReason: "end_turn",
			TTFTMillis: int64(380 + s.rnd.IntN(1500)), DurMillis: int64(2_000 + s.rnd.IntN(45_000)),
		}
		if m.provider == "codex" {
			ev.AccountID = id('e', 4)
		}
		switch {
		case i == 4:
			ev.Status, ev.Error, ev.StopReason, ev.TTFTMillis, ev.DurMillis = 429, "Daily quota exhausted", "", 0, 3
		case i == 11:
			ev.Aborted, ev.Error, ev.StopReason = true, "client disconnected", ""
		case i == 23:
			ev.Status, ev.Error, ev.StopReason, ev.TTFTMillis, ev.DurMillis = 529, "No upstream account is available", "", 0, 41
		case s.rnd.Float64() < 0.25:
			ev.StopReason = "tool_use"
		}
		if ev.Status == 200 {
			ev.InputTokens = int64(3_000 + s.rnd.IntN(25_000))
			ev.OutputTokens = int64(200 + s.rnd.IntN(3_000))
			ev.CacheReadTokens = int64(s.rnd.IntN(80_000))
			ev.CacheWriteTokens = int64(s.rnd.IntN(5_000))
			ev.CostMicro = int64((float64(ev.InputTokens)*pr.in + float64(ev.OutputTokens)*pr.out +
				float64(ev.CacheReadTokens)*pr.cRead + float64(ev.CacheWriteTokens)*pr.cWrite) + 0.5)
		}
		s.recent = append(s.recent, ev)
		at = at.Add(-time.Duration(5+s.rnd.IntN(80)) * time.Minute)
	}
}

func (s *seeder) audit(admin string) {
	type entry struct {
		ago            time.Duration
		actor          string
		action, entity string
		entityID       string
		after          any
	}
	h := time.Hour
	entries := []entry{
		{45 * 24 * h, admin, "plan.create", "plan", plans[0].id, map[string]any{"name": "Team"}},
		{45 * 24 * h, admin, "plan.create", "plan", plans[1].id, map[string]any{"name": "Power users"}},
		{44 * 24 * h, admin, "plan.create", "plan", plans[2].id, map[string]any{"name": "Interns"}},
		{30 * 24 * h, admin, "price.set", "price", "claude-sonnet-4-6", map[string]any{"p_in": 3, "p_out": 15}},
		{30 * 24 * h, admin, "price.set", "price", "claude-opus-4-7", map[string]any{"p_in": 5, "p_out": 25}},
		{29 * 24 * h, admin, "mapping.replace", "mapping", id('c', 1), map[string]any{"rules": 5}},
		{20 * 24 * h, admin, "proxy.create", "proxy", id('d', 1), map[string]any{"label": "Frankfurt egress", "type": "socks5h"}},
		{20 * 24 * h, admin, "proxy.create", "proxy", id('d', 2), map[string]any{"label": "Amsterdam egress", "type": "http"}},
		{17 * 24 * h, admin, "account.create", "account", id('e', 1), map[string]any{"provider": "claude", "label": "Claude Team 1"}},
		{16 * 24 * h, admin, "account.create", "account", id('e', 2), map[string]any{"provider": "claude", "label": "Claude Team 2"}},
		{14 * 24 * h, admin, "account.create", "account", id('e', 4), map[string]any{"provider": "codex", "label": "Codex Pool"}},
		{12 * 24 * h, admin, "user.update", "user", id('b', 5), map[string]any{"plan": "Interns"}},
		{5 * 24 * h, admin, "account.update", "account", id('e', 3), map[string]any{"proxy": "direct"}},
		{3 * 24 * h, admin, "user.update", "user", id('b', 6), map[string]any{"disabled": true}},
		{2 * 24 * h, admin, "proxy.update", "proxy", id('d', 3), map[string]any{"enabled": false}},
		{29 * h, id('b', 2), "auth.login", "user", id('b', 2), map[string]any{"method": "oidc"}},
		{27 * h, admin, "auth.login", "user", admin, map[string]any{"method": "oidc"}},
		{26 * h, id('b', 4), "key.create", "key", id('f', 6), map[string]any{"name": "CI runner"}},
	}
	// The Audit log pages newest-first by id (it's append-only), so these
	// backdated rows get negative ids in time order: they sort below every
	// real entry and stay chronological. All are at least a day old.
	for i, e := range entries {
		after, _ := json.Marshal(e.after)
		s.exec(`INSERT INTO audit_log (id, actor_id, action, entity, entity_id, after, at) OVERRIDING SYSTEM VALUE
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			int64(i-len(entries)), e.actor, e.action, e.entity, e.entityID, after, s.now.Add(-e.ago))
	}
}

// quotaUse is how much of each Plan window a demo person has used right
// now, so Usage & quotas shows every state: quiet, 80%+, 95%+ and exhausted.
var quotaUse = map[int]float64{1: 0.41, 2: 0.86, 3: 0.33, 4: 0.97, 5: 1.0, 7: 0.12, 8: 0.58}

// redis writes quota counters for the demo people, and alice's recent
// requests and quota counters (Overview bars).
func (s *seeder) redis() error {
	q := quota.NewStore(s.rdb)
	a := quota.DefaultAnchors() // the demo Plans keep the default anchors
	for _, u := range people {
		use, ok := quotaUse[u.n]
		if !ok {
			continue
		}
		for kind, limit := range plans[u.plan].quotas {
			// Longer windows have absorbed more of the history, so they sit lower.
			ratio := use * map[string]float64{"rolling_5h": 0.7, "daily": 1, "weekly": 0.8, "monthly": 0.55}[kind]
			if err := q.Charge(s.ctx, id('b', u.n), []quota.Window{{Kind: quota.Kind(kind)}}, a, quota.ToMicro(limit*ratio), s.now); err != nil {
				return err
			}
		}
	}
	if s.alice == "" {
		return nil
	}
	key := usage.RecentKey(s.alice)
	pipe := s.rdb.Pipeline()
	for i := len(s.recent) - 1; i >= 0; i-- { // LPUSH oldest first: newest ends up at the head
		js, _ := json.Marshal(s.recent[i])
		pipe.LPush(s.ctx, key, js)
	}
	pipe.LTrim(s.ctx, key, 0, usage.RecentLen-1)
	pipe.Expire(s.ctx, key, usage.RecentTTL)
	if _, err := pipe.Exec(s.ctx); err != nil {
		return err
	}
	for kind, usd := range map[quota.Kind]float64{quota.Rolling5h: 3.1, quota.Daily: 12.4, quota.Weekly: 46.9} {
		if err := q.Charge(s.ctx, s.alice, []quota.Window{{Kind: kind}}, a, quota.ToMicro(usd), s.now); err != nil {
			return err
		}
	}
	return nil
}
