package gateway

import (
	"slices"
	"testing"

	"qanat/internal/core/quota"
)

func testSnapshot() *Snapshot {
	return &Snapshot{
		exact: map[string]Target{
			"sonnet": {Members: []Member{{Provider: "claude", Model: "claude-sonnet-4-6"}}},
			"smart":  {Members: []Member{{Provider: "claude", Model: "claude-opus-4-7"}, {Provider: "codex", Model: "gpt-5.5"}}},
		},
		globs: []globRule{
			{"claude-haiku-*", Target{Members: []Member{{Provider: "claude", Model: "claude-haiku-4-5"}}}},
			{"claude-*", Target{Members: []Member{{Provider: "claude"}}}},
		},
		combos: []*Combo{{Name: "smart"}},
		prices: map[string]Price{"claude-sonnet-4-6": {}, "claude-opus-4-7": {}, "claude-unrouted": {}},
	}
}

// REQ-MAP-02/03/04: exact before glob, globs by priority, prefixes, claude-* default.
func TestResolve(t *testing.T) {
	s := testSnapshot()
	cases := map[string]Member{
		"sonnet":                {Provider: "claude", Model: "claude-sonnet-4-6"},
		"claude-haiku-4-5-2025": {Provider: "claude", Model: "claude-haiku-4-5"}, // first matching glob wins
		"claude-opus-4-7":       {Provider: "claude"},                            // keeps the name
		"cx/gpt-5.5":            {Provider: "codex", Model: "gpt-5.5"},
		"ag/gemini-3-pro":       {Provider: "antigravity", Model: "gemini-3-pro"},
		// The prefix pins the provider; mapping rules then apply to the bare name.
		"cc/sonnet":                {Provider: "claude", Model: "claude-sonnet-4-6"},
		"cc/claude-haiku-4-5-2025": {Provider: "claude", Model: "claude-haiku-4-5"},
		"cc/claude-opus-4-7":       {Provider: "claude", Model: "claude-opus-4-7"},
		"cx/smart":                 {Provider: "codex", Model: "gpt-5.5"},      // the Combo member on Codex
		"ag/sonnet":                {Provider: "antigravity", Model: "sonnet"}, // rule targets Claude: bare name kept
		"cx/claude-opus-4-7":       {Provider: "codex", Model: "claude-opus-4-7"},
	}
	for name, want := range cases {
		got, ok := s.Resolve(name)
		if !ok || got.Members[0] != want {
			t.Errorf("%s → %+v %v, want %+v", name, got, ok, want)
		}
	}
	if _, ok := s.Resolve("gpt-4"); ok {
		t.Error("an unknown name without prefix must not resolve")
	}
	if got, _ := s.Resolve("smart"); len(got.Members) != 2 {
		t.Errorf("combo members: %+v", got)
	}
	// A catch-all glob must not hijack prefixed names.
	s.globs = append(s.globs, globRule{"*", Target{Members: []Member{{Provider: "claude", Model: "claude-opus-4-7"}}}})
	if got, _ := s.Resolve("cx/gpt-5.5"); got.Members[0] != (Member{Provider: "codex", Model: "gpt-5.5"}) {
		t.Errorf("prefix after catch-all: %+v", got)
	}
	// Without any rule, claude-* still routes to Claude (REQ-MAP-03).
	if got, ok := (&Snapshot{}).Resolve("claude-sonnet-4-6"); !ok || got.Members[0].Provider != "claude" {
		t.Errorf("default claude-*: %+v %v", got, ok)
	}
}

// REQ-ACCESS-01/02: the Plan allowlist and a key restriction both apply.
func TestModelNames_FilteredByPlanAndKey(t *testing.T) {
	s := testSnapshot()
	all := s.ModelNames(&Plan{}, &Key{})
	slices.Sort(all)
	if want := []string{"claude-opus-4-7", "claude-sonnet-4-6", "claude-unrouted", "smart", "sonnet"}; !slices.Equal(all, want) {
		t.Fatalf("all: %v", all)
	}
	plan := &Plan{Models: set([]string{"sonnet", "smart"})}
	key := &Key{Allowed: set([]string{"sonnet"})}
	if got := s.ModelNames(plan, key); !slices.Equal(got, []string{"sonnet"}) {
		t.Fatalf("plan ∩ key: %v", got)
	}
}

// A prefixed name passes the allowlist when its bare name is listed.
func TestAllowed_Prefixed(t *testing.T) {
	plan := &Plan{Models: set([]string{"sonnet"})}
	if !Allowed(plan, &Key{}, "cc/sonnet") || Allowed(plan, &Key{}, "cc/opus") {
		t.Fatal("plan allowlist with prefix")
	}
	if Allowed(plan, &Key{Allowed: set([]string{"opus"})}, "cc/sonnet") {
		t.Fatal("key restriction still applies")
	}
}

// /v1/models lists prefixed names for providers with accounts, by family.
func TestModelNames_Prefixed(t *testing.T) {
	s := testSnapshot()
	s.accounts = map[string][]*Account{"claude": {{ID: "a"}}, "codex": {{ID: "b"}}}
	s.prices["gpt-5.5"] = Price{}
	got := s.ModelNames(&Plan{}, &Key{})
	for _, want := range []string{"cc/claude-sonnet-4-6", "cx/gpt-5.5"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	for _, not := range []string{"ag/claude-sonnet-4-6", "cc/gpt-5.5", "cx/claude-sonnet-4-6"} {
		if slices.Contains(got, not) {
			t.Errorf("unexpected %s", not)
		}
	}
}

// §10.2: per-model windows bind only that model; grants raise every limit.
func TestWindows(t *testing.T) {
	p := &Plan{Quotas: []quota.Window{
		{Kind: quota.Daily, LimitMicro: 10},
		{Kind: quota.Daily, Scope: "opus", LimitMicro: 5},
	}}
	if ws := windows(p, 0, "sonnet"); len(ws) != 1 {
		t.Fatalf("sonnet: %+v", ws)
	}
	if ws := windows(p, 0, "cc/opus"); len(ws) != 2 {
		t.Fatalf("prefixed name escapes the per-model window: %+v", ws)
	}
	ws := windows(p, 3, "opus")
	if len(ws) != 2 || ws[0].LimitMicro != 13 || ws[1].LimitMicro != 8 {
		t.Fatalf("opus with grant: %+v", ws)
	}
}

func TestCostMicro(t *testing.T) {
	p := Price{In: 3, Out: 15, CRead: 0.3, CWrite: 3.75}
	// (1000·3 + 500·15 + 2000·0.3 + 100·3.75) / 1e6 USD = 0.011475 USD
	if got := p.CostMicro(1000, 500, 2000, 100); got != 11475 {
		t.Fatalf("%d micro-USD", got)
	}
}
