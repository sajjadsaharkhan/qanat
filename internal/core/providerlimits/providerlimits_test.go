package providerlimits

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func byKey(s *Snapshot) map[string]Window {
	m := map[string]Window{}
	for _, w := range s.Windows {
		m[w.Key] = w
	}
	return m
}

func TestParseClaude(t *testing.T) {
	s, err := ParseClaude([]byte(`{
		"five_hour": {"utilization": 87, "resets_at": "2026-10-31T15:00:00Z"},
		"seven_day": {"utilization": 40.5, "resets_at": 1793462400},
		"seven_day_opus": {"utilization": 100, "resets_at": null},
		"seven_day_oauth_apps": null,
		"limits": [
			{"kind": "weekly_scoped", "percent": 12, "resets_at": "2026-11-02T00:00:00Z", "scope": {"model": {"display_name": "Fable"}}},
			{"kind": "weekly_scoped", "percent": 50, "scope": {"model": {"display_name": "Opus"}}},
			{"kind": "other", "percent": 99}
		],
		"cedar_ember": {"eligible": true, "grants": [
			{"id": "a", "resets_left": 2}, {"id": "b", "resets_left": "1"}, {"id": "c", "resets_left": 3, "paused": true}
		]}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	w := byKey(s)
	if got := w["session"]; got.UsedPercent != 87 || got.Group != GroupSession || got.ResetsAt == nil || !got.ResetsAt.Equal(time.Date(2026, 10, 31, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("session: %+v", got)
	}
	if got := w["weekly"]; got.UsedPercent != 40.5 || got.ResetsAt == nil || got.ResetsAt.Unix() != 1793462400 {
		t.Fatalf("weekly: %+v", got)
	}
	if got := w["weekly_opus"]; got.UsedPercent != 100 || got.Group != GroupModel || got.ResetsAt != nil || got.Label != "Weekly Opus (7d)" {
		t.Fatalf("opus (the seven_day_ key wins over limits[]): %+v", got)
	}
	if got := w["weekly_fable"]; got.UsedPercent != 12 {
		t.Fatalf("fable: %+v", got)
	}
	if _, ok := w["weekly_oauth_apps"]; ok {
		t.Fatal("a null window must not become a row")
	}
	if len(s.Windows) != 4 {
		t.Fatalf("windows: %+v", s.Windows)
	}
	if s.ResetCredits == nil || *s.ResetCredits != 3 {
		t.Fatalf("reset credits (paused grants don't count): %v", s.ResetCredits)
	}
	if s.LimitReached {
		t.Fatal("a model window at 100% doesn't block the account")
	}
}

func TestParseCodex(t *testing.T) {
	s, err := ParseCodex([]byte(`{
		"plan_type": "plus",
		"rate_limit": {"limit_reached": true,
			"primary_window": {"used_percent": 100, "reset_at": 1793462400},
			"secondary_window": {"used_percent": 22}},
		"additional_rate_limits": [
			{"limit_name": "code_review", "rate_limit": {"primary_window": {"used_percent": 5}}},
			{"limit_name": "GPT-5.3-Codex-Spark", "rate_limit": {"secondary_window": {"used_percent": 70}}}
		],
		"rate_limit_reset_credits": {"available_count": 2}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	w := byKey(s)
	if s.Plan != "plus" || !s.LimitReached || s.ResetCredits == nil || *s.ResetCredits != 2 {
		t.Fatalf("snapshot: %+v", s)
	}
	if w["session"].UsedPercent != 100 || w["session"].ResetsAt == nil || w["weekly"].UsedPercent != 22 {
		t.Fatalf("normal windows: %+v", s.Windows)
	}
	if w["review_session"].UsedPercent != 5 || w["review_session"].Group != GroupModel {
		t.Fatalf("review: %+v", s.Windows)
	}
	if w["spark_weekly"].UsedPercent != 70 || w["spark_weekly"].Label != "Spark Weekly (7d)" {
		t.Fatalf("spark: %+v", s.Windows)
	}
}

func TestParseAntigravity(t *testing.T) {
	models := []byte(`{"models": {
		"gemini-3.1-pro-high": {"displayName": "Gemini 3.1 Pro", "quotaInfo": {"remainingFraction": 0, "resetTime": "2026-10-31T18:00:00Z"}},
		"gemini-3.8-flash-low": {"quotaInfo": {}},
		"claude-opus-5-5": {"displayName": "Claude Opus 5.5", "quotaInfo": {"remainingFraction": 0.25}},
		"chat_internal": {"isInternal": true, "quotaInfo": {"remainingFraction": 1}},
		"no-quota": {}
	}}`)
	free, err := ParseAntigravityModels(models, false)
	if err != nil || len(free.Windows) != 0 {
		t.Fatalf("free tier shows no per-model windows: %+v %v", free, err)
	}
	s, err := ParseAntigravityModels(models, true)
	if err != nil {
		t.Fatal(err)
	}
	w := byKey(s)
	if len(s.Windows) != 3 || w["claude-opus-5-5"].UsedPercent != 75 || w["gemini-3.8-flash-low"].UsedPercent != 100 {
		t.Fatalf("models: %+v", s.Windows)
	}

	MergeAntigravitySummary(s, []byte(`{"quotaSummary": {"groups": [
		{"displayName": "Gemini models", "buckets": [
			{"window": "5h", "remainingFraction": 0.6, "resetTime": "2026-10-31T16:00:00Z"},
			{"window": "weekly", "remainingFraction": 0.9}
		]},
		{"displayName": "Claude and GPT", "buckets": [
			{"bucketId": "five hour", "disabled": true},
			{"window": "weekly", "disabled": true, "remainingFraction": 0.5}
		]}
	]}}`))
	w = byKey(s)
	// Every Gemini model is exhausted, so the family's 5h window is too.
	if g := w["gemini_session"]; g.UsedPercent != 100 || g.ResetsAt == nil || g.ResetsAt.Hour() != 18 {
		t.Fatalf("gemini session: %+v", g)
	}
	if g := w["gemini_weekly"]; g.UsedPercent != 10 || g.Group != GroupWeekly {
		t.Fatalf("gemini weekly: %+v", g)
	}
	if c := w["claude_gpt_session"]; c.UsedPercent != 100 {
		t.Fatalf("a disabled 5h bucket shows as exhausted: %+v", c)
	}
	if _, ok := w["claude_gpt_weekly"]; ok {
		t.Fatal("a disabled weekly bucket is skipped")
	}
	if s.Windows[0].Key != "gemini_session" {
		t.Fatalf("summary windows come first: %+v", s.Windows)
	}
}

func TestFetchStatuses(t *testing.T) {
	status := http.StatusUnauthorized
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oauth/usage" || r.URL.Query().Get("cedar_ember") != "1" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("request: %s %v", r.URL, r.Header)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"five_hour": {"utilization": 3}}`))
	}))
	defer srv.Close()

	if _, err := Fetch(t.Context(), srv.Client(), "claude", "tok", srv.URL, ""); err != ErrUnauthorized {
		t.Fatalf("401: %v", err)
	}
	status = http.StatusTooManyRequests
	if _, err := Fetch(t.Context(), srv.Client(), "claude", "tok", srv.URL, ""); err != ErrRateLimited {
		t.Fatalf("429: %v", err)
	}
	status = http.StatusOK
	s, err := Fetch(t.Context(), srv.Client(), "claude", "tok", srv.URL, "")
	if err != nil || len(s.Windows) != 1 || s.Windows[0].UsedPercent != 3 {
		t.Fatalf("200: %+v %v", s, err)
	}
}

func TestSupported(t *testing.T) {
	cases := []struct {
		provider, kind string
		want           bool
	}{
		{"claude", "oauth", true}, {"claude", "token", true}, {"claude", "api_key", false},
		{"codex", "oauth", true}, {"codex", "api_key", false},
		{"antigravity", "oauth", true}, {"antigravity", "api_key", false},
	}
	for _, c := range cases {
		if got := Supported(c.provider, c.kind); got != c.want {
			t.Errorf("%s/%s = %v", c.provider, c.kind, got)
		}
	}
}
