package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"

	"qanat/internal/core/quota"
	"qanat/internal/core/usage"
	"qanat/internal/store/db"
)

// --- usage Overview (the 9router usage layout) -------------------------------------
//
// One payload per page load: totals, grouped rows for the tables and bar
// charts, chart buckets, and the live bits (recent requests, in-flight count,
// last error). Scoped to one user, or to everyone for admins.

// usagePeriods are the Overview's period buttons.
var usagePeriods = map[string]bool{"today": true, "24h": true, "7d": true, "30d": true, "60d": true, "all": true}

// messagesEndpoint is the only endpoint that records usage (§4.2:
// count_tokens is not charged).
const messagesEndpoint = "/v1/messages"

type usageGroup struct {
	Model            string    `json:"model"`
	Provider         string    `json:"provider"`
	Requests         int64     `json:"requests"`
	PromptTokens     int64     `json:"prompt_tokens"` // input + cache read + cache write
	CachedTokens     int64     `json:"cached_tokens"` // cache reads, a subset of prompt tokens
	CompletionTokens int64     `json:"completion_tokens"`
	Cost             float64   `json:"cost"`
	LastUsed         time.Time `json:"last_used"`

	AccountID   string `json:"account_id,omitempty"`
	AccountName string `json:"account_name,omitempty"`
	KeyID       string `json:"key_id,omitempty"`
	KeyName     string `json:"key_name,omitempty"`
	KeyPrefix   string `json:"key_prefix,omitempty"`
	UserEmail   string `json:"user_email,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
}

func (g *usageGroup) add(r groupRow) {
	g.Requests += r.Requests
	g.PromptTokens += r.PromptTokens
	g.CachedTokens += r.CachedTokens
	g.CompletionTokens += r.CompletionTokens
	g.Cost += r.CostUsd
	if r.lastUsed.After(g.LastUsed) {
		g.LastUsed = r.lastUsed
	}
}

type chartBucket struct {
	Start    time.Time `json:"start"`
	Tokens   int64     `json:"tokens"`
	Cost     float64   `json:"cost"`
	Requests int64     `json:"requests"`
}

type recentUsage struct {
	Timestamp        time.Time `json:"timestamp"`
	Model            string    `json:"model"`
	Provider         string    `json:"provider"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	CachedTokens     int64     `json:"cached_tokens"`
	Status           string    `json:"status"` // "ok" or "error"
}

type usageOverview struct {
	Period                string        `json:"period"`
	Granularity           string        `json:"granularity"` // "hour" or "day" chart buckets
	TotalRequests         int64         `json:"total_requests"`
	TotalPromptTokens     int64         `json:"total_prompt_tokens"`
	TotalCachedTokens     int64         `json:"total_cached_tokens"`
	TotalCompletionTokens int64         `json:"total_completion_tokens"`
	TotalCost             float64       `json:"total_cost"`
	ByProvider            []usageGroup  `json:"by_provider"`
	ByModel               []usageGroup  `json:"by_model"`
	ByAccount             []usageGroup  `json:"by_account"`
	ByAPIKey              []usageGroup  `json:"by_api_key"`
	ByEndpoint            []usageGroup  `json:"by_endpoint"`
	Chart                 []chartBucket `json:"chart"`
	RecentRequests        []recentUsage `json:"recent_requests"`
	// ActiveCount is requests streaming right now (concurrency slots). The
	// gateway doesn't track them per provider (no hot-path writes, §9).
	ActiveCount   int64  `json:"active_count"`
	LastProvider  string `json:"last_provider"`
	ErrorProvider string `json:"error_provider"` // provider of an error in the last 10 s
	// Providers are the topology nodes: providers with pool accounts or usage.
	Providers []string `json:"providers"`
}

// groupRow is a grouped usage row from either the hourly or daily table.
type groupRow struct {
	db.UsageGroupsDailyRow
	lastUsed time.Time
}

// overviewRange resolves a period to its first bucket and granularity. tz
// sets where "today" starts; days are UTC like the daily aggregates.
func (s *Server) overviewRange(ctx context.Context, period string, tz *time.Location, userID *string) (since time.Time, hourly bool, buckets int, err error) {
	now := s.now()
	switch period {
	case "today":
		local := now.In(tz)
		start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, tz)
		return start.UTC().Truncate(time.Hour), true, 24, nil
	case "24h":
		return now.UTC().Truncate(time.Hour).Add(-23 * time.Hour), true, 24, nil
	}
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	days := map[string]int{"7d": 7, "30d": 30, "60d": 60}[period]
	if period == "all" {
		first, err := s.q.UsageFirstDay(ctx, userID)
		if err != nil {
			return time.Time{}, false, 0, err
		}
		first = first.UTC()
		start := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, time.UTC)
		days = int(today.Sub(start)/(24*time.Hour)) + 1
	}
	return today.AddDate(0, 0, -(days - 1)), false, days, nil
}

func (s *Server) buildOverview(ctx context.Context, userID *string, period string, tz *time.Location) (usageOverview, error) {
	out := usageOverview{Period: period, Granularity: "day", ByProvider: []usageGroup{}, ByModel: []usageGroup{}, ByAccount: []usageGroup{},
		ByAPIKey: []usageGroup{}, ByEndpoint: []usageGroup{}, Chart: []chartBucket{}, RecentRequests: []recentUsage{}, Providers: []string{}}
	since, hourly, nBuckets, err := s.overviewRange(ctx, period, tz, userID)
	if err != nil {
		return out, err
	}
	now := s.now()
	span := 24 * time.Hour
	if hourly {
		out.Granularity, span = "hour", time.Hour
	}
	lastOf := func(bucket time.Time, width time.Duration) time.Time {
		if end := bucket.Add(width); end.Before(now) {
			return end
		}
		return now
	}

	// Grouped rows, with a precise "last used" from the 5-minute buckets.
	var rows []groupRow
	if hourly {
		rs, err := s.q.UsageGroupsHourly(ctx, db.UsageGroupsHourlyParams{UserID: userID, Since: since})
		if err != nil {
			return out, err
		}
		for _, r := range rs {
			rows = append(rows, groupRow{db.UsageGroupsDailyRow(r), lastOf(r.LastBucket, span)})
		}
	} else {
		rs, err := s.q.UsageGroupsDaily(ctx, db.UsageGroupsDailyParams{UserID: userID, Since: since})
		if err != nil {
			return out, err
		}
		for _, r := range rs {
			rows = append(rows, groupRow{r, lastOf(r.LastBucket, span)})
		}
	}
	recent5m := since
	if weekAgo := now.Add(-7 * 24 * time.Hour); recent5m.Before(weekAgo) {
		recent5m = weekAgo
	}
	precise, err := s.q.UsageLastUsed5m(ctx, db.UsageLastUsed5mParams{UserID: userID, Since: recent5m})
	if err != nil {
		return out, err
	}
	key := func(provider, model, keyID, accountID string) string {
		return provider + "\x00" + model + "\x00" + keyID + "\x00" + accountID
	}
	exact := map[string]time.Time{}
	for _, p := range precise {
		exact[key(p.Provider, p.UpstreamModel, p.KeyID, p.AccountID)] = lastOf(p.LastBucket, quota.BucketSpan)
	}

	type groups struct {
		m     map[string]*usageGroup
		order []string
	}
	newGroups := func() *groups { return &groups{m: map[string]*usageGroup{}} }
	get := func(g *groups, k string, init usageGroup) *usageGroup {
		if v, ok := g.m[k]; ok {
			return v
		}
		v := init
		g.m[k] = &v
		g.order = append(g.order, k)
		return &v
	}
	byProvider, byModel, byAccount, byKey, byEndpoint := newGroups(), newGroups(), newGroups(), newGroups(), newGroups()
	seenProvider := map[string]bool{}
	for _, r := range rows {
		if t, ok := exact[key(r.Provider, r.UpstreamModel, r.KeyID, r.AccountID)]; ok {
			r.lastUsed = t
		}
		out.TotalRequests += r.Requests
		out.TotalPromptTokens += r.PromptTokens
		out.TotalCachedTokens += r.CachedTokens
		out.TotalCompletionTokens += r.CompletionTokens
		out.TotalCost += r.CostUsd
		seenProvider[r.Provider] = true

		base := usageGroup{Model: r.UpstreamModel, Provider: r.Provider}
		get(byProvider, r.Provider, usageGroup{Provider: r.Provider}).add(r)
		get(byModel, r.Provider+"|"+r.UpstreamModel, base).add(r)

		acct := base
		acct.AccountID, acct.AccountName = r.AccountID, r.AccountLabel
		if acct.AccountName == "" {
			acct.AccountName = "Deleted or no account"
			if r.AccountID != "" && len(r.AccountID) >= 8 {
				acct.AccountName = "Account " + r.AccountID[:8] + "…"
			}
		}
		get(byAccount, r.AccountID+"|"+r.Provider+"|"+r.UpstreamModel, acct).add(r)

		k := base
		k.KeyID, k.KeyName, k.KeyPrefix, k.UserEmail = r.KeyID, r.KeyName, r.KeyPrefix, r.UserEmail
		if k.KeyName == "" {
			k.KeyName = "Deleted key"
		}
		get(byKey, r.KeyID+"|"+r.Provider+"|"+r.UpstreamModel, k).add(r)

		ep := base
		ep.Endpoint = messagesEndpoint
		get(byEndpoint, messagesEndpoint+"|"+r.Provider+"|"+r.UpstreamModel, ep).add(r)
	}
	flat := func(g *groups) []usageGroup {
		res := make([]usageGroup, 0, len(g.order))
		for _, k := range g.order {
			res = append(res, *g.m[k])
		}
		sort.SliceStable(res, func(i, j int) bool { return res[i].Cost > res[j].Cost })
		return res
	}
	out.ByProvider, out.ByModel, out.ByAccount, out.ByAPIKey, out.ByEndpoint =
		flat(byProvider), flat(byModel), flat(byAccount), flat(byKey), flat(byEndpoint)

	// Chart buckets, zero-filled.
	out.Chart = make([]chartBucket, nBuckets)
	for i := range out.Chart {
		out.Chart[i].Start = since.Add(time.Duration(i) * span)
		if !hourly {
			out.Chart[i].Start = since.AddDate(0, 0, i)
		}
	}
	place := func(at time.Time, tokens int64, cost float64, requests int64) {
		i := int(at.UTC().Sub(since) / span)
		if i >= 0 && i < nBuckets {
			out.Chart[i].Tokens += tokens
			out.Chart[i].Cost += cost
			out.Chart[i].Requests += requests
		}
	}
	if hourly {
		cs, err := s.q.UsageChartHourly(ctx, db.UsageChartHourlyParams{UserID: userID, Since: since})
		if err != nil {
			return out, err
		}
		for _, c := range cs {
			place(c.BucketStart, c.Tokens, c.CostUsd, c.Requests)
		}
	} else {
		cs, err := s.q.UsageChartDaily(ctx, db.UsageChartDailyParams{UserID: userID, Since: since})
		if err != nil {
			return out, err
		}
		for _, c := range cs {
			place(c.BucketStart, c.Tokens, c.CostUsd, c.Requests)
		}
	}

	// Live: recent requests, in-flight count, last error, topology nodes.
	users := []string{}
	if userID != nil {
		users = append(users, *userID)
	} else {
		all, err := s.q.ListUsers(ctx)
		if err != nil {
			return out, err
		}
		for _, u := range all {
			users = append(users, u.ID)
		}
	}
	events, err := s.recentEvents(ctx, users)
	if err != nil {
		return out, err
	}
	out.RecentRequests = recentRows(events)
	if len(events) > 0 {
		out.LastProvider = events[0].Provider
	}
	for _, ev := range events {
		if now.Sub(ev.At) > 10*time.Second {
			break
		}
		if ev.Status >= 500 || (ev.Error != "" && !ev.Aborted) {
			out.ErrorProvider = ev.Provider
			break
		}
	}
	if out.ActiveCount, err = quota.NewStore(s.rdb).InFlight(ctx, users, now); err != nil {
		return out, err
	}
	pool, err := s.q.ProviderAccountsInPool(ctx)
	if err != nil {
		return out, err
	}
	for _, p := range pool {
		seenProvider[p] = true
	}
	for p := range seenProvider {
		if p != "" {
			out.Providers = append(out.Providers, p)
		}
	}
	sort.Strings(out.Providers)
	return out, nil
}

// recentEvents reads the newest entries of each user's recent-request ring,
// newest first across users.
func (s *Server) recentEvents(ctx context.Context, users []string) ([]usage.Event, error) {
	pipe := s.rdb.Pipeline()
	cmds := make([]*redis.StringSliceCmd, len(users))
	for i, id := range users {
		cmds[i] = pipe.LRange(ctx, usage.RecentKey(id), 0, 99)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, err
	}
	var events []usage.Event
	for _, c := range cmds {
		for _, raw := range c.Val() {
			var ev usage.Event
			if json.Unmarshal([]byte(raw), &ev) == nil {
				events = append(events, ev)
			}
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.After(events[j].At) })
	return events, nil
}

// recentRows is the Recent Requests card: 20 newest, without zero-token
// entries and per-minute duplicates (as 9router shows them).
func recentRows(events []usage.Event) []recentUsage {
	out := []recentUsage{}
	seen := map[string]bool{}
	for _, ev := range events {
		prompt := ev.InputTokens + ev.CacheReadTokens + ev.CacheWriteTokens
		if prompt == 0 && ev.OutputTokens == 0 {
			continue
		}
		model := ev.UpstreamModel
		if model == "" {
			model = ev.Model
		}
		dedupe := fmt.Sprintf("%s|%s|%d|%d|%s", model, ev.Provider, prompt, ev.OutputTokens, ev.At.UTC().Format("2006-01-02T15:04"))
		if seen[dedupe] {
			continue
		}
		seen[dedupe] = true
		status := "ok"
		if ev.Status >= 400 || ev.Aborted {
			status = "error"
		}
		out = append(out, recentUsage{ev.At, model, ev.Provider, prompt, ev.OutputTokens, ev.CacheReadTokens, status})
		if len(out) == 20 {
			break
		}
	}
	return out
}

func parseOverviewQuery(w http.ResponseWriter, r *http.Request) (string, *time.Location, bool) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "today"
	}
	if !usagePeriods[period] {
		fail(w, http.StatusBadRequest, "invalid_period", "period must be today, 24h, 7d, 30d, 60d or all.")
		return "", nil, false
	}
	tz := time.UTC
	if name := r.URL.Query().Get("tz"); name != "" {
		loc, err := time.LoadLocation(name)
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid_timezone", "tz must be an IANA time zone such as Asia/Tehran.")
			return "", nil, false
		}
		tz = loc
	}
	return period, tz, true
}

func (s *Server) myUsageOverview(w http.ResponseWriter, r *http.Request, u *db.GetUserRow) {
	period, tz, ok := parseOverviewQuery(w, r)
	if !ok {
		return
	}
	out, err := s.buildOverview(r.Context(), &u.ID, period, tz)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// adminUsageOverview is everyone's Overview, or one person's with ?user_id=.
func (s *Server) adminUsageOverview(w http.ResponseWriter, r *http.Request, _ *db.GetUserRow) {
	period, tz, ok := parseOverviewQuery(w, r)
	if !ok {
		return
	}
	var userID *string
	if id := r.URL.Query().Get("user_id"); id != "" {
		if _, err := s.q.GetUser(r.Context(), id); err != nil {
			if isBadUUID(err) || notFound(err) {
				fail(w, http.StatusNotFound, "not_found", "That user doesn't exist.")
			} else {
				s.internal(w, r, err)
			}
			return
		}
		userID = &id
	}
	out, err := s.buildOverview(r.Context(), userID, period, tz)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
