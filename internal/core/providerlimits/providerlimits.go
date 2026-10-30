// Package providerlimits reads an upstream account's own subscription
// limits — how much of its 5-hour, weekly and per-model windows is left —
// from the provider's usage endpoints.
//
// Ported from 9router (https://github.com/decolua/9router),
// open-sse/services/usage/{claude,codex,google,antigravity-weekly}.js.
// MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
// Qanat change: one normalized Snapshot for every provider (used percent per
// window); reset grants are counted, never spent.
package providerlimits

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"qanat/internal/core/oauth"
)

// Endpoints; tests point them at a fake server.
var (
	ClaudeURL         = "https://api.anthropic.com"
	CodexURL          = "https://chatgpt.com/backend-api"
	AntigravityAPIURL = oauth.AntigravityAPIURL
)

// Window groups, in display order.
const (
	GroupSession = "session"
	GroupWeekly  = "weekly"
	GroupModel   = "model"
)

// Window is one limit window. UsedPercent is 0–100.
type Window struct {
	Key         string     `json:"key"`
	Label       string     `json:"label"`
	Group       string     `json:"group"`
	UsedPercent float64    `json:"used_percent"`
	ResetsAt    *time.Time `json:"resets_at"`
}

// Snapshot is what an account's provider reports about its limits.
type Snapshot struct {
	Plan         string   `json:"plan,omitempty"`
	Windows      []Window `json:"windows"`
	ResetCredits *int     `json:"reset_credits,omitempty"`
	LimitReached bool     `json:"limit_reached"`
}

var (
	// ErrUnauthorized means the token was rejected; an OAuth account may
	// refresh and retry.
	ErrUnauthorized = errors.New("the provider rejected the token")
	// ErrRateLimited means the usage endpoint itself is rate limited (chat
	// with the same token still works).
	ErrRateLimited = errors.New("the provider's usage endpoint is rate limited")
)

// Supported reports whether the provider publishes limits for this kind of
// credential: subscription tokens do, plain API keys don't.
func Supported(providerID, authKind string) bool {
	if authKind == "api_key" {
		return false
	}
	switch providerID {
	case "claude", "codex":
		return true
	case "antigravity":
		return authKind == "oauth"
	}
	return false
}

// Fetch reads the account's limits. base overrides Claude's API base (the
// account's base_url); upstream is Antigravity's Cloud Code project or
// Codex's ChatGPT account.
func Fetch(ctx context.Context, hc *http.Client, providerID, token, base, upstream string) (*Snapshot, error) {
	switch providerID {
	case "claude":
		if base == "" {
			base = ClaudeURL
		}
		return fetchClaude(ctx, hc, token, base)
	case "codex":
		return fetchCodex(ctx, hc, token, upstream)
	case "antigravity":
		return fetchAntigravity(ctx, hc, token, upstream)
	}
	return nil, fmt.Errorf("no usage API for %q", providerID)
}

// --- Claude ------------------------------------------------------------------

func fetchClaude(ctx context.Context, hc *http.Client, token, base string) (*Snapshot, error) {
	// cedar_ember=1 adds the "limit reset" grant block, as Claude Code sends.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/api/oauth/usage?cedar_ember=1", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", oauth.OAuthBeta)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("User-Agent", oauth.ClaudeUserAgent)
	body, err := do(hc, req)
	if err != nil {
		return nil, err
	}
	return ParseClaude(body)
}

type claudeWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    any      `json:"resets_at"`
}

// ParseClaude reads /api/oauth/usage. utilization is the percent used.
func ParseClaude(body []byte) (*Snapshot, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	s := &Snapshot{Windows: []Window{}}
	window := func(key string) (claudeWindow, bool) {
		var w claudeWindow
		if json.Unmarshal(raw[key], &w) != nil || w.Utilization == nil {
			return w, false
		}
		return w, true
	}
	if w, ok := window("five_hour"); ok {
		s.Windows = append(s.Windows, Window{Key: "session", Label: "Session (5h)", Group: GroupSession, UsedPercent: clamp(*w.Utilization), ResetsAt: parseReset(w.ResetsAt)})
	}
	if w, ok := window("seven_day"); ok {
		s.Windows = append(s.Windows, Window{Key: "weekly", Label: "Weekly (7d)", Group: GroupWeekly, UsedPercent: clamp(*w.Utilization), ResetsAt: parseReset(w.ResetsAt)})
	}
	// Model-specific weekly windows: seven_day_opus, seven_day_sonnet, …
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	seen := map[string]bool{}
	for _, k := range keys {
		name, ok := strings.CutPrefix(k, "seven_day_")
		if !ok {
			continue
		}
		if w, ok := window(k); ok {
			seen[name] = true
			s.Windows = append(s.Windows, Window{Key: "weekly_" + name, Label: "Weekly " + titleCase(name) + " (7d)", Group: GroupModel, UsedPercent: clamp(*w.Utilization), ResetsAt: parseReset(w.ResetsAt)})
		}
	}
	// Model-scoped weekly limits arrive in limits[]; no entry means no such window.
	var limits []struct {
		Kind     string   `json:"kind"`
		Percent  *float64 `json:"percent"`
		ResetsAt any      `json:"resets_at"`
		Scope    struct {
			Model struct {
				DisplayName string `json:"display_name"`
			} `json:"model"`
		} `json:"scope"`
	}
	if json.Unmarshal(raw["limits"], &limits) == nil {
		for _, l := range limits {
			name := strings.ToLower(strings.TrimSpace(l.Scope.Model.DisplayName))
			if l.Kind != "weekly_scoped" || name == "" || l.Percent == nil || seen[name] {
				continue
			}
			seen[name] = true
			s.Windows = append(s.Windows, Window{Key: "weekly_" + name, Label: "Weekly " + titleCase(name) + " (7d)", Group: GroupModel, UsedPercent: clamp(*l.Percent), ResetsAt: parseReset(l.ResetsAt)})
		}
	}
	var grants struct {
		Eligible bool `json:"eligible"`
		Grants   []struct {
			ID         string `json:"id"`
			Paused     bool   `json:"paused"`
			ResetsLeft any    `json:"resets_left"`
		} `json:"grants"`
	}
	if json.Unmarshal(raw["cedar_ember"], &grants) == nil && grants.Eligible {
		n := 0
		for _, g := range grants.Grants {
			if left := int(number(g.ResetsLeft)); g.ID != "" && !g.Paused && left > 0 {
				n += left
			}
		}
		s.ResetCredits = &n
	}
	for _, w := range s.Windows {
		if w.Group != GroupModel && w.UsedPercent >= 100 {
			s.LimitReached = true
		}
	}
	return s, nil
}

// --- Codex -------------------------------------------------------------------

func fetchCodex(ctx context.Context, hc *http.Client, token, accountID string) (*Snapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(CodexURL, "/")+"/wham/usage", nil)
	if err != nil {
		return nil, err
	}
	oauth.SetCodexHeaders(req.Header, token, accountID)
	req.Header.Set("Accept", "application/json")
	body, err := do(hc, req)
	if err != nil {
		return nil, err
	}
	return ParseCodex(body)
}

// ParseCodex reads ChatGPT's /wham/usage: the normal limit's primary
// (session) and secondary (weekly) windows, plus the review and spark limits.
func ParseCodex(body []byte) (*Snapshot, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	s := &Snapshot{Windows: []Window{}}
	var plan string
	if json.Unmarshal(raw["plan_type"], &plan) == nil {
		s.Plan = plan
	}
	byID := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw["rate_limits_by_limit_id"], &byID)
	var additional []map[string]json.RawMessage
	_ = json.Unmarshal(raw["additional_rate_limits"], &additional)
	find := func(direct []string, ids []string, match func(string) bool) json.RawMessage {
		for _, k := range direct {
			if v, ok := raw[k]; ok && string(v) != "null" {
				return v
			}
		}
		for _, k := range ids {
			if v, ok := byID[k]; ok {
				return v
			}
		}
		for _, e := range additional {
			for _, k := range []string{"limit_name", "metered_feature", "id"} {
				var id string
				if json.Unmarshal(e[k], &id) == nil && id != "" {
					if match(strings.ToLower(id)) {
						b, _ := json.Marshal(e)
						return b
					}
					break
				}
			}
		}
		return nil
	}
	normal := find([]string{"rate_limit", "rate_limits"}, []string{"codex"}, func(string) bool { return false })
	review := find([]string{"code_review_rate_limit", "review_rate_limit"}, []string{"code_review", "codex_review", "review"}, func(id string) bool { return strings.Contains(id, "review") })
	spark := find([]string{"spark_rate_limit", "gpt_5_3_codex_spark_rate_limit"}, []string{"gpt-5.3-codex-spark", "gpt_5_3_codex_spark", "spark"}, func(id string) bool { return strings.Contains(id, "spark") })

	if reached := appendCodex(s, normal, "", "", GroupSession, GroupWeekly); reached {
		s.LimitReached = true
	}
	appendCodex(s, review, "review_", "Review ", GroupModel, GroupModel)
	appendCodex(s, spark, "spark_", "Spark ", GroupModel, GroupModel)

	var credits struct {
		AvailableCount any `json:"available_count"`
	}
	if json.Unmarshal(raw["rate_limit_reset_credits"], &credits) == nil && credits.AvailableCount != nil {
		n := max(0, int(number(credits.AvailableCount)))
		s.ResetCredits = &n
	}
	return s, nil
}

type codexWindow struct {
	UsedPercent  any `json:"used_percent"`
	PercentUsed  any `json:"percent_used"`
	ResetAt      any `json:"reset_at"`
	ResetsAt     any `json:"resets_at"`
	ResetAtCamel any `json:"resetAt"`
}

// appendCodex adds a limit's session and weekly windows; it reports
// limit_reached.
func appendCodex(s *Snapshot, snapshot json.RawMessage, keyPrefix, labelPrefix, sessionGroup, weeklyGroup string) bool {
	if snapshot == nil {
		return false
	}
	var outer map[string]json.RawMessage
	if json.Unmarshal(snapshot, &outer) != nil {
		return false
	}
	body := outer
	if inner, ok := outer["rate_limit"]; ok {
		var m map[string]json.RawMessage
		if json.Unmarshal(inner, &m) == nil && m != nil {
			body = m
		}
	}
	pick := func(keys ...string) *codexWindow {
		for _, src := range []map[string]json.RawMessage{body, outer} {
			for _, k := range keys {
				var w codexWindow
				if v, ok := src[k]; ok && string(v) != "null" && json.Unmarshal(v, &w) == nil {
					return &w
				}
			}
		}
		return nil
	}
	add := func(w *codexWindow, key, label, group string) {
		used := number(w.UsedPercent)
		if w.UsedPercent == nil {
			used = number(w.PercentUsed)
		}
		reset := parseReset(firstNonNil(w.ResetAt, w.ResetsAt, w.ResetAtCamel))
		s.Windows = append(s.Windows, Window{Key: keyPrefix + key, Label: labelPrefix + label, Group: group, UsedPercent: clamp(used), ResetsAt: reset})
	}
	if w := pick("primary_window", "primary"); w != nil {
		add(w, "session", "Session (5h)", sessionGroup)
	}
	if w := pick("secondary_window", "secondary"); w != nil {
		add(w, "weekly", "Weekly (7d)", weeklyGroup)
	}
	var reached bool
	_ = json.Unmarshal(body["limit_reached"], &reached)
	return reached
}

// --- Antigravity -------------------------------------------------------------

func fetchAntigravity(ctx context.Context, hc *http.Client, token, project string) (*Snapshot, error) {
	// loadCodeAssist names the tier (and the project when none is stored).
	req, err := oauth.LoadCodeAssistRequest(ctx, token)
	if err != nil {
		return nil, err
	}
	body, err := do(hc, req)
	if err != nil {
		return nil, err
	}
	var sub struct {
		Project     json.RawMessage `json:"cloudaicompanionProject"`
		CurrentTier struct {
			Name string `json:"name"`
		} `json:"currentTier"`
		PaidTier struct {
			ID string `json:"id"`
		} `json:"paidTier"`
	}
	_ = json.Unmarshal(body, &sub)
	if project == "" {
		project = projectID(sub.Project)
	}

	req, err = antigravityRequest(ctx, "/v1internal:fetchAvailableModels", token, project)
	if err != nil {
		return nil, err
	}
	models, err := do(hc, req)
	if err != nil {
		return nil, err
	}
	// Free-tier accounts only have the weekly limit; their per-model quota
	// info is misleading (9router).
	paid := sub.PaidTier.ID != "" && sub.PaidTier.ID != "free-tier"
	s, err := ParseAntigravityModels(models, paid)
	if err != nil {
		return nil, err
	}
	s.Plan = sub.CurrentTier.Name

	// The weekly and 5-hour summary is best effort: it never hides the
	// per-model windows.
	if req, err := antigravityRequest(ctx, "/v1internal:retrieveUserQuotaSummary", token, project); err == nil {
		if summary, err := do(hc, req); err == nil {
			MergeAntigravitySummary(s, summary)
		}
	}
	return s, nil
}

func antigravityRequest(ctx context.Context, path, token, project string) (*http.Request, error) {
	payload := map[string]any{}
	if project != "" {
		payload["project"] = project
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(AntigravityAPIURL, "/")+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", oauth.AntigravityUserAgent)
	req.Header.Set("X-Client-Name", "antigravity")
	req.Header.Set("X-Client-Version", "2.24.0")
	return req, nil
}

// ParseAntigravityModels reads per-model quota from fetchAvailableModels.
// Only paid tiers carry meaningful per-model windows.
func ParseAntigravityModels(body []byte, paid bool) (*Snapshot, error) {
	var wire struct {
		Models map[string]struct {
			DisplayName string `json:"displayName"`
			IsInternal  bool   `json:"isInternal"`
			QuotaInfo   *struct {
				RemainingFraction *float64 `json:"remainingFraction"`
				ResetTime         any      `json:"resetTime"`
			} `json:"quotaInfo"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, err
	}
	s := &Snapshot{Windows: []Window{}}
	if !paid {
		return s, nil
	}
	ids := make([]string, 0, len(wire.Models))
	for id := range wire.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		m := wire.Models[id]
		if m.IsInternal || m.QuotaInfo == nil {
			continue
		}
		remaining := 0.0
		if m.QuotaInfo.RemainingFraction != nil {
			remaining = *m.QuotaInfo.RemainingFraction
		}
		label := m.DisplayName
		if label == "" {
			label = id
		}
		s.Windows = append(s.Windows, Window{Key: id, Label: label, Group: GroupModel, UsedPercent: clamp(100 - remaining*100), ResetsAt: parseReset(m.QuotaInfo.ResetTime)})
	}
	return s, nil
}

// MergeAntigravitySummary adds retrieveUserQuotaSummary's weekly and 5-hour
// windows per model family. A family whose models are all exhausted shows
// its 5-hour window as exhausted too, until the latest model reset.
func MergeAntigravitySummary(s *Snapshot, body []byte) {
	var data struct {
		Groups       []agGroup `json:"groups"`
		QuotaSummary struct {
			Groups []agGroup `json:"groups"`
		} `json:"quotaSummary"`
	}
	if json.Unmarshal(body, &data) != nil {
		return
	}
	groups := data.Groups
	if groups == nil {
		groups = data.QuotaSummary.Groups
	}
	families := []struct {
		match       func(string) bool
		key, label  string
		modelPrefix string
	}{
		{func(n string) bool { return strings.Contains(n, "gemini") }, "gemini", "Gemini", "gemini-"},
		{func(n string) bool { return strings.Contains(n, "claude") || strings.Contains(n, "gpt") }, "claude_gpt", "Claude & GPT", "claude-"},
	}
	var summary []Window
	for _, f := range families {
		var weekly, session *Window
		for _, g := range groups {
			if !f.match(strings.ToLower(g.DisplayName)) {
				continue
			}
			for _, b := range g.Buckets {
				win := strings.ToLower(b.Window)
				text := strings.ToLower(b.BucketID + " " + b.DisplayName)
				isWeekly := win == "weekly" || strings.Contains(text, "weekly")
				isSession := win == "5h" || win == "daily" || strings.Contains(text, "five hour") || strings.Contains(text, "5h") || strings.Contains(text, "daily")
				if !isWeekly && !isSession || b.Disabled && isWeekly {
					continue
				}
				remaining := 0.0
				if !b.Disabled {
					if b.RemainingFraction == nil {
						continue
					}
					remaining = *b.RemainingFraction
				}
				w := Window{UsedPercent: clamp(100 - remaining*100), ResetsAt: parseReset(b.ResetTime)}
				if isWeekly && weekly == nil {
					w.Key, w.Label, w.Group = f.key+"_weekly", f.label+" weekly", GroupWeekly
					weekly = &w
				} else if !isWeekly && session == nil {
					w.Key, w.Label, w.Group = f.key+"_session", f.label+" (5h)", GroupSession
					session = &w
				}
			}
		}
		if session != nil {
			exhausted, found := true, false
			var latest *time.Time
			for _, m := range s.Windows {
				if m.Group != GroupModel || !strings.HasPrefix(m.Key, f.modelPrefix) || strings.Contains(m.Key, "image") {
					continue
				}
				found = true
				if m.UsedPercent < 100 {
					exhausted = false
				}
				if m.ResetsAt != nil && (latest == nil || m.ResetsAt.After(*latest)) {
					latest = m.ResetsAt
				}
			}
			if found && exhausted && session.UsedPercent < 100 {
				session.UsedPercent = 100
				if latest != nil {
					session.ResetsAt = latest
				}
			}
			summary = append(summary, *session)
		}
		if weekly != nil {
			summary = append(summary, *weekly)
		}
	}
	s.Windows = append(summary, s.Windows...)
	for _, w := range summary {
		if w.UsedPercent >= 100 && w.Group == GroupWeekly {
			s.LimitReached = true
		}
	}
}

type agGroup struct {
	DisplayName string `json:"displayName"`
	Buckets     []struct {
		BucketID          string   `json:"bucketId"`
		DisplayName       string   `json:"displayName"`
		Window            string   `json:"window"`
		Disabled          bool     `json:"disabled"`
		RemainingFraction *float64 `json:"remainingFraction"`
		ResetTime         any      `json:"resetTime"`
	} `json:"buckets"`
}

// --- helpers -----------------------------------------------------------------

func do(hc *http.Client, req *http.Request) ([]byte, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, ErrRateLimited
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("the usage API answered %d", resp.StatusCode)
	}
	return body, nil
}

// parseReset accepts Unix seconds or milliseconds (number or digit string)
// and RFC 3339 times.
func parseReset(v any) *time.Time {
	var t time.Time
	switch x := v.(type) {
	case float64:
		t = unix(x)
	case string:
		if x == "" {
			return nil
		}
		if n, err := strconv.ParseFloat(x, 64); err == nil {
			t = unix(n)
		} else if p, err := time.Parse(time.RFC3339Nano, x); err == nil {
			t = p
		} else {
			return nil
		}
	default:
		return nil
	}
	t = t.UTC()
	return &t
}

func unix(n float64) time.Time {
	if n < 1e12 {
		return time.Unix(int64(n), 0)
	}
	return time.UnixMilli(int64(n))
}

func number(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		if n, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
			return n
		}
	}
	return 0
}

func firstNonNil(vs ...any) any {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}

// clamp bounds a percent to 0–100, rounded to hundredths.
func clamp(p float64) float64 { return math.Round(min(100, max(0, p))*100) / 100 }

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func projectID(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var o struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &o) == nil {
		return strings.TrimSpace(o.ID)
	}
	return ""
}
