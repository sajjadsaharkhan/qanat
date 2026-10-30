package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"log/slog"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"qanat/internal/core/provider"
	"qanat/internal/core/quota"
	"qanat/internal/core/secretbox"
	"qanat/internal/proxy"
	"qanat/internal/store/db"
)

// Snapshot is the gateway's in-memory view of configuration (§11.6). It is
// immutable once built; a reload builds a new one and swaps the pointer, so
// the hot path never takes a lock or touches the database.
type Snapshot struct {
	Gen      int64
	LoadedAt time.Time

	keys     map[[32]byte]*Key
	users    map[string]*User
	plans    map[string]*Plan
	grants   map[string]int64 // user → active top-up, micro-USD
	exact    map[string]Target
	globs    []globRule
	combos   []*Combo
	prices   map[string]Price
	accounts map[string][]*Account // provider → accounts in registration order
	// providerModels is each provider's stored catalog (migration 0009),
	// synced from the provider itself; /v1/models lists these prefixed.
	providerModels map[string][]string
	sticky         map[string]string // user|provider → account ID
	proxyIDs       map[string]bool   // proxies some account routes through
}

// Key is a personal API key (hash lookup only).
type Key struct {
	ID, UserID string
	Allowed    map[string]bool // optional per-key restriction; nil = Plan decides
	ExpiresAt  *time.Time
}

// User is what admission needs about a person.
type User struct{ ID, PlanID string }

// Plan is a Plan in hot-path form (REQ-PLAN-02).
type Plan struct {
	Anchors       quota.Anchors
	Quotas        []quota.Window
	RPM           int
	MaxConcurrent int
	Models        map[string]bool // nil = all models
}

// Member is one concrete upstream target.
type Member struct {
	Provider string
	Model    string // "" keeps the incoming name
}

// Target is where a Qanat name routes: one model or a Combo (REQ-MAP-01).
type Target struct{ Members []Member }

// Combo is an ordered fallback list (REQ-COMBO-01).
type Combo struct {
	Name    string
	Members []Member
}

type globRule struct {
	pattern string
	target  Target
}

// Price is a confirmed price version per 1M tokens (§10.1).
type Price struct {
	Version                int64
	In, Out, CRead, CWrite float64
}

// CostMicro is (in·p_in + out·p_out + cr·p_cr + cw·p_cw) / 1e6 USD, in
// micro-USD: the 1e6 cancels.
func (p Price) CostMicro(in, out, cr, cw int64) int64 {
	return int64(float64(in)*p.In + float64(out)*p.Out + float64(cr)*p.CRead + float64(cw)*p.CWrite + 0.5)
}

// Account is a pool account (REQ-POOL-01/02).
type Account struct {
	ID, Provider, BaseURL string
	Origin                string // scheme://host of the upstream, for proxy health
	Token                 string // decrypted access token; never logged
	Project               string // Antigravity's Cloud Code project, from sign-in
	State                 string
	// AuthKind is how Token authenticates upstream: "token" sends
	// Authorization: Bearer (e.g. a token from `claude setup-token`), "api_key"
	// sends x-api-key. Antigravity is always an OAuth bearer token.
	AuthKind      string
	Disabled      bool // disabled by an admin; sits out until re-enabled
	CooldownUntil time.Time
	// Proxies are the outbound routes to try, in order (§8.2): one entry for
	// a proxy, a sticky group or direct (a nil entry); a failover group's
	// enabled members by priority. Empty when every candidate is disabled or
	// unreadable: the account sits out rather than leave from another source
	// IP (REQ-PROXY-01).
	Proxies []*proxy.Spec
}

// Lookup resolves a presented key.
func (s *Snapshot) Lookup(presented string) (*Key, *User, *Plan, bool) {
	k := s.keys[sha256.Sum256([]byte(presented))]
	if k == nil {
		return nil, nil, nil, false
	}
	u := s.users[k.UserID]
	if u == nil {
		return nil, nil, nil, false
	}
	p := s.plans[u.PlanID]
	if p == nil {
		return nil, nil, nil, false
	}
	return k, u, p, true
}

// Resolve maps an incoming model name through the global mapping (REQ-MAP-01…04).
// A provider prefix comes first and pins the provider: "cc/x" goes to Claude
// with the bare name x, after any mapping rule for x that targets Claude (so
// "cc/opus" follows an "opus" alias). Unprefixed names take the exact rule,
// then globs by priority, then the claude-* default.
func (s *Snapshot) Resolve(name string) (Target, bool) {
	if id, bare, ok := provider.Split(name); ok {
		m := Member{Provider: id, Model: bare}
		if t, ok := s.match(bare); ok {
			for _, tm := range t.Members {
				if tm.Provider == id && tm.Model != "" {
					m.Model = tm.Model
					break
				}
			}
		}
		return Target{Members: []Member{m}}, true
	}
	if t, ok := s.match(name); ok {
		return t, true
	}
	if strings.HasPrefix(name, "claude-") {
		return Target{Members: []Member{{Provider: "claude"}}}, true
	}
	return Target{}, false
}

func (s *Snapshot) match(name string) (Target, bool) {
	if t, ok := s.exact[name]; ok {
		return t, true
	}
	for _, g := range s.globs {
		if ok, _ := path.Match(g.pattern, name); ok {
			return g.target, true
		}
	}
	return Target{}, false
}

// serves reports whether the pool holds an account for the provider that can
// take traffic: not admin-disabled, not disconnected. Cooldowns don't count —
// they clear on their own. Disabled accounts stay in the snapshot so
// re-enabling is instant, but their provider's names stop being advertised.
func (s *Snapshot) serves(providerID string) bool {
	for _, a := range s.accounts[providerID] {
		if !a.Disabled && a.State != "disconnected" {
			return true
		}
	}
	return false
}

// Allowed reports whether a Plan and key allow a Qanat name (REQ-ACCESS-01).
// A prefixed name is allowed when either it or its bare name is, so a Plan
// that lists "sonnet" also admits "cc/sonnet".
func Allowed(p *Plan, k *Key, name string) bool {
	in := func(set map[string]bool) bool {
		return set == nil || set[name] || set[provider.Bare(name)]
	}
	return in(p.Models) && in(k.Allowed)
}

// ModelNames lists the Qanat names a person may use, for /v1/models: exact
// mapping names, Combos, and priced models that route somewhere.
func (s *Snapshot) ModelNames(p *Plan, k *Key) []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if !seen[n] && Allowed(p, k, n) {
			seen[n] = true
			out = append(out, n)
		}
	}
	for n := range s.exact {
		add(n)
	}
	for _, c := range s.combos {
		add(c.Name)
	}
	for m := range s.prices {
		// Prefixed names for each provider with a usable account that serves
		// the model's family, as 9router lists "cc/…" and "cx/…" (REQ-MAP-04).
		// Priced models are never advertised bare — bare names are reserved
		// for what an admin configured in the mapping (exact rules below).
		for _, pv := range provider.All {
			if s.serves(pv.ID) && pv.Serves(m) {
				add(pv.Prefix + "/" + m)
			}
		}
	}
	// Stored provider catalogs, prefixed: what the provider itself says it
	// serves, when it has a usable account.
	for _, pv := range provider.All {
		if !s.serves(pv.ID) {
			continue
		}
		for _, m := range s.providerModels[pv.ID] {
			add(pv.Prefix + "/" + m)
		}
	}
	return out
}

// --- loading -----------------------------------------------------------------

// Load builds a snapshot in one read-only, repeatable-read transaction so it
// reflects a single point in time (and one generation).
func Load(ctx context.Context, pool *pgxpool.Pool, box *secretbox.Box, log *slog.Logger) (*Snapshot, error) {
	var s *Snapshot
	err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		s, err = build(ctx, db.New(tx), box, log)
		return err
	})
	return s, err
}

func build(ctx context.Context, q *db.Queries, box *secretbox.Box, log *slog.Logger) (*Snapshot, error) {
	s := &Snapshot{
		LoadedAt: time.Now(), keys: map[[32]byte]*Key{}, users: map[string]*User{}, plans: map[string]*Plan{},
		grants: map[string]int64{}, exact: map[string]Target{}, prices: map[string]Price{},
		accounts: map[string][]*Account{}, providerModels: map[string][]string{}, sticky: map[string]string{}, proxyIDs: map[string]bool{},
	}
	var err error
	if s.Gen, err = q.SnapshotGeneration(ctx); err != nil {
		return nil, err
	}

	keys, err := q.SnapshotKeys(ctx)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		var h [32]byte
		copy(h[:], k.Hash)
		key := &Key{ID: k.ID, UserID: k.UserID, ExpiresAt: k.ExpiresAt}
		if k.AllowedModels != nil {
			key.Allowed = set(k.AllowedModels)
		}
		s.keys[h] = key
	}

	users, err := q.SnapshotUsers(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		s.users[u.ID] = &User{ID: u.ID, PlanID: u.PlanID}
	}

	plans, err := q.SnapshotPlans(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range plans {
		loc, err := time.LoadLocation(p.Timezone)
		if err != nil {
			loc = time.UTC
		}
		mins := int(p.DailyAnchor.Microseconds / int64(time.Minute/time.Microsecond))
		s.plans[p.ID] = &Plan{Anchors: quota.Anchors{Location: loc, Hour: mins / 60, Minute: mins % 60,
			Weekday: time.Weekday(p.WeeklyAnchor), MonthDay: int(p.MonthlyAnchor)}}
	}
	quotas, err := q.SnapshotPlanQuotas(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range quotas {
		if p := s.plans[r.PlanID]; p != nil {
			w := quota.Window{Kind: quota.Kind(r.WindowKind), LimitMicro: quota.ToMicro(r.LimitUsd)}
			if r.ModelScope != nil {
				w.Scope = *r.ModelScope
			}
			p.Quotas = append(p.Quotas, w)
		}
	}
	limits, err := q.SnapshotPlanLimits(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range limits {
		if p := s.plans[r.PlanID]; p != nil {
			p.RPM, p.MaxConcurrent = int(r.Rpm), int(r.MaxConcurrent)
		}
	}
	models, err := q.SnapshotPlanModels(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range models {
		if p := s.plans[r.PlanID]; p != nil {
			if p.Models == nil {
				p.Models = map[string]bool{}
			}
			p.Models[r.ModelName] = true
		}
	}

	grants, err := q.SnapshotGrants(ctx)
	if err != nil {
		return nil, err
	}
	for _, g := range grants {
		s.grants[g.UserID] = quota.ToMicro(g.AmountUsd)
	}

	combos, err := q.SnapshotCombos(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]*Combo{}
	for _, c := range combos {
		cb := byID[c.ID]
		if cb == nil {
			cb = &Combo{Name: c.Name}
			byID[c.ID] = cb
			s.combos = append(s.combos, cb)
		}
		cb.Members = append(cb.Members, Member{Provider: c.Provider, Model: c.UpstreamModel})
	}
	for _, cb := range s.combos {
		s.exact[cb.Name] = Target{Members: cb.Members}
	}

	mappings, err := q.SnapshotMappings(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range mappings {
		var t Target
		if m.TargetKind == "combo" {
			cb := byID[deref(m.ComboID)]
			if cb == nil {
				continue
			}
			t = Target{Members: cb.Members}
		} else {
			t = Target{Members: []Member{{Provider: deref(m.Provider), Model: deref(m.UpstreamModel)}}}
		}
		if m.IsGlob {
			s.globs = append(s.globs, globRule{m.Pattern, t}) // already ordered by priority
		} else {
			s.exact[m.Pattern] = t
		}
	}

	prices, err := q.SnapshotPrices(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range prices {
		s.prices[p.ModelID] = Price{Version: p.ID, In: p.PIn, Out: p.POut, CRead: p.PCacheRead, CWrite: p.PCacheWrite}
	}

	proxies, err := q.SnapshotProxies(ctx)
	if err != nil {
		return nil, err
	}
	specs := map[string]*proxy.Spec{} // usable proxies only
	defaultID := ""
	for _, p := range proxies {
		if p.IsDefault {
			defaultID = p.ID
		}
		if !p.Enabled {
			continue
		}
		password, err := proxy.OpenPassword(box, p.ID, p.Password)
		if err != nil {
			log.Error("proxy password unreadable; its accounts sit out", "proxy", p.ID, "err", err)
			continue
		}
		specs[p.ID] = &proxy.Spec{ID: p.ID, Type: p.Type, Host: p.Host, Port: int(p.Port), Username: deref(p.Username), Password: password}
	}
	groups, err := q.SnapshotProxyGroups(ctx)
	if err != nil {
		return nil, err
	}
	policy := map[string]string{}
	members := map[string][]*proxy.Spec{} // group → usable members by priority
	for _, m := range groups {
		policy[m.ID] = m.Policy
		if sp := specs[m.ProxyID]; sp != nil {
			members[m.ID] = append(members[m.ID], sp)
		}
	}
	providerSetting := map[string]proxySetting{}
	pps, err := q.SnapshotProviderProxies(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range pps {
		providerSetting[p.Provider] = proxySetting{deref(p.ProxyID), deref(p.ProxyGroupID), p.ProxyDirect}
	}
	defaultSetting := proxySetting{proxyID: defaultID, direct: defaultID == ""}

	// routes resolves a setting to the proxies an account tries, in order.
	routes := func(accountID string, ps proxySetting) []*proxy.Spec {
		switch {
		case ps.direct:
			return []*proxy.Spec{nil}
		case ps.proxyID != "":
			if sp := specs[ps.proxyID]; sp != nil {
				return []*proxy.Spec{sp}
			}
			return nil
		}
		ms := members[ps.groupID]
		if len(ms) == 0 {
			return nil
		}
		if policy[ps.groupID] == "sticky" {
			return []*proxy.Spec{proxy.Pick(accountID, ms)}
		}
		return ms
	}

	accounts, err := q.SnapshotAccounts(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range accounts {
		acct := &Account{ID: a.ID, Provider: a.Provider, BaseURL: deref(a.BaseUrl), State: a.State, AuthKind: a.AuthKind, Disabled: a.DisabledAt != nil}
		acct.Origin = proxy.Origin(acct.baseURL())
		// §8.2: the account's own setting, else its provider's, else the
		// global default proxy, else direct.
		ps := proxySetting{deref(a.ProxyID), deref(a.ProxyGroupID), a.ProxyDirect}
		if ps == (proxySetting{}) {
			var ok bool
			if ps, ok = providerSetting[a.Provider]; !ok {
				ps = defaultSetting
			}
		}
		acct.Proxies = routes(a.ID, ps)
		for _, sp := range acct.Proxies {
			if sp != nil {
				s.proxyIDs[sp.ID] = true
			}
		}
		if a.CooldownUntil != nil {
			acct.CooldownUntil = *a.CooldownUntil
		}
		if len(a.Credentials) > 0 {
			acct.Token, acct.Project, err = openToken(box, a.ID, a.Credentials)
			if err != nil {
				log.Error("account credentials unreadable; skipping account", "account", a.ID, "err", err)
				continue
			}
		}
		s.accounts[a.Provider] = append(s.accounts[a.Provider], acct)
	}

	sticky, err := q.SnapshotStickyBindings(ctx)
	if err != nil {
		return nil, err
	}
	for _, b := range sticky {
		s.sticky[b.UserID+"|"+b.Provider] = b.AccountID
	}

	catalog, err := q.SnapshotProviderModels(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range catalog {
		s.providerModels[m.Provider] = append(s.providerModels[m.Provider], m.Model)
	}
	return s, nil
}

// proxySetting is one level of §8.2: a proxy, a group, or direct. All zero
// means "inherit from the next level".
type proxySetting struct {
	proxyID, groupID string
	direct           bool
}

// Credentials is the sealed JSON stored in provider_accounts.credentials.
type Credentials struct {
	AccessToken string `json:"access_token"`
	ProjectID   string `json:"project_id,omitempty"` // Antigravity only
}

func openToken(box *secretbox.Box, accountID string, sealed []byte) (token, project string, err error) {
	if box == nil {
		return "", "", errNoMasterKey
	}
	plain, err := box.Open(sealed, []byte(accountID))
	if err != nil {
		return "", "", err
	}
	var c Credentials
	if err := json.Unmarshal(plain, &c); err != nil {
		return "", "", err
	}
	return c.AccessToken, c.ProjectID, nil
}

type gwError string

func (e gwError) Error() string { return string(e) }

const errNoMasterKey = gwError("QANAT_MASTER_KEY is not set, so stored credentials can't be decrypted")

func set(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// --- watching ----------------------------------------------------------------

// Store holds the current snapshot and keeps it fresh.
type Store struct {
	cur  atomic.Pointer[Snapshot]
	pool *pgxpool.Pool
	box  *secretbox.Box
	log  *slog.Logger
}

// NewStore creates an empty store; Run loads and watches.
func NewStore(pool *pgxpool.Pool, box *secretbox.Box, log *slog.Logger) *Store {
	return &Store{pool: pool, box: box, log: log}
}

// Current returns the latest snapshot, or nil before the first load.
func (st *Store) Current() *Snapshot { return st.cur.Load() }

func (st *Store) reload(ctx context.Context) {
	s, err := Load(ctx, st.pool, st.box, st.log)
	if err != nil {
		st.log.Error("snapshot load failed; keeping the previous one", "err", err)
		return
	}
	st.cur.Store(s)
	snapshotGeneration.Set(float64(s.Gen))
	for provider, accts := range s.accounts {
		for _, a := range accts {
			v := 0.0
			if a.State == "healthy" {
				v = 1
			}
			accountHealthy.WithLabelValues(provider, a.ID).Set(v)
		}
	}
}

// refreshEvery reloads even without notifications, so expiring keys, grants
// and future-dated prices take effect, and a missed NOTIFY heals.
const refreshEvery = 30 * time.Second

// Run keeps the snapshot current until ctx ends: LISTEN qanat_config and
// reload on every notification, debounced (§11.6).
func (st *Store) Run(ctx context.Context) {
	for ctx.Err() == nil {
		st.reload(ctx)
		if err := st.listen(ctx); err != nil && ctx.Err() == nil {
			st.log.Warn("config listener lost; reconnecting", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}
}

func (st *Store) listen(ctx context.Context) error {
	conn, err := st.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN qanat_config"); err != nil {
		return err
	}
	st.reload(ctx) // catch changes made before LISTEN took effect
	for {
		wctx, cancel := context.WithTimeout(ctx, refreshEvery)
		_, err := conn.Conn().WaitForNotification(wctx)
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && wctx.Err() == nil {
			conn.Conn().Close(context.Background()) // don't return a broken conn to the pool
			return err
		}
		// Debounce bursts (a Plan save touches several tables).
		drain, cancel2 := context.WithTimeout(ctx, 20*time.Millisecond)
		for {
			if _, err := conn.Conn().WaitForNotification(drain); err != nil {
				break
			}
		}
		cancel2()
		st.reload(ctx)
	}
}
