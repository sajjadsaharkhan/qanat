// Package acceptance is Qanat's black-box acceptance suite (Seam A).
//
// Every test drives the gateway over HTTP exactly like Claude Code does and
// asserts only externally observable behavior: bytes on the wire, status
// codes, error shapes, timing, and usage visible through the Admin API.
// Tests reference PRD requirement IDs in their names and comments.
//
// The suite is implementation-agnostic. A Driver adapts it to a running
// system: it seeds state (users, Plans, keys, mapping, upstream accounts,
// prices) and reads usage back. Drivers register themselves from a
// build-tagged file:
//
//	-tags reference   in-process reference gateway (validates the suite itself)
//	-tags qanat       the real Qanat stack from deploy/test/compose.test.yml
//
// Without a registered driver every test is skipped.
package acceptance

import (
	"context"
	"testing"
)

// Plan mirrors REQ-PLAN-02: USD quotas, rate limits and a model allowlist.
type Plan struct {
	// AllowedModels are Qanat names (aliases/Combos). Empty means all models.
	AllowedModels []string
	// DailyUSD is the daily quota. Zero means zero quota (REQ-PLAN-03).
	DailyUSD float64
	// RPM and MaxConcurrent are rate limits; zero means unlimited.
	RPM           int
	MaxConcurrent int
}

// MappingRule is one row of the global model mapping (REQ-MAP-01/02).
type MappingRule struct {
	Pattern  string // exact name or glob, e.g. "claude-*"
	Provider string // "claude", "codex", "antigravity"
	Model    string // upstream model; "" keeps the incoming name
}

// Price is per 1M tokens, in USD (PRD §10.1).
type Price struct {
	Input, Output, CacheRead, CacheWrite float64
}

// Usage is what the Admin API reports for a user.
type Usage struct {
	InputTokens, OutputTokens int64
	CostUSD                   float64
}

// Driver adapts the suite to a system under test.
type Driver interface {
	// GatewayURL is the base URL Claude Code would use as ANTHROPIC_BASE_URL.
	GatewayURL() string
	// Reset clears all state between tests.
	Reset(ctx context.Context) error
	// CreateUser provisions a user with role "user" and returns its ID.
	CreateUser(ctx context.Context, email string) (string, error)
	SetPlan(ctx context.Context, userID string, p Plan) error
	// CreateKey returns a new personal API key (shown once).
	CreateKey(ctx context.Context, userID string) (string, error)
	// AddUpstreamAccount registers a pool account whose API lives at baseURL.
	// Accounts are tried in registration order.
	AddUpstreamAccount(ctx context.Context, provider, baseURL string) (string, error)
	SetMapping(ctx context.Context, rules []MappingRule) error
	SetPrice(ctx context.Context, upstreamModel string, p Price) error
	// Usage returns the user's aggregated usage. It may lag (async pipeline);
	// tests poll it.
	Usage(ctx context.Context, userID string) (Usage, error)
}

var factory func(t testing.TB) Driver

// RegisterDriver is called from a build-tagged file's init.
func RegisterDriver(f func(t testing.TB) Driver) { factory = f }

// needDriver returns the registered driver or skips the test.
func needDriver(t testing.TB) Driver {
	t.Helper()
	if factory == nil {
		t.Skip("no acceptance driver registered; run with -tags reference or -tags qanat")
	}
	return factory(t)
}
