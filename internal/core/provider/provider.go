// Package provider is the registry of upstream providers and their model-name
// prefixes, after 9router's open-sse/providers/registry (MIT): a client may
// write "cc/claude-opus-4-7" to pin the Claude pool, "cx/gpt-5.5" for Codex
// and "ag/gemini-3-pro" for Antigravity (REQ-MAP-04).
package provider

import "strings"

// Provider is one upstream provider.
type Provider struct {
	ID     string // stored in accounts, mappings and usage
	Prefix string // model-name prefix, without the slash
	Name   string
	// Families are model-name prefixes the provider serves, used only to list
	// prefixed names for discovery; routing accepts any name after the prefix.
	Families []string
}

// All lists providers in display order.
var All = []Provider{
	{ID: "claude", Prefix: "cc", Name: "Claude Code", Families: []string{"claude-"}},
	{ID: "codex", Prefix: "cx", Name: "OpenAI Codex", Families: []string{"gpt-", "codex-", "o1", "o3", "o4"}},
	{ID: "antigravity", Prefix: "ag", Name: "Antigravity", Families: []string{"gemini-", "claude-"}},
}

var byPrefix, byID = func() (map[string]Provider, map[string]Provider) {
	p, id := map[string]Provider{}, map[string]Provider{}
	for _, v := range All {
		p[v.Prefix], id[v.ID] = v, v
	}
	return p, id
}()

// Known reports whether id names a provider.
func Known(id string) bool { _, ok := byID[id]; return ok }

// Get returns the provider with id.
func Get(id string) (Provider, bool) { p, ok := byID[id]; return p, ok }

// Split takes a known prefix off name: "cc/claude-opus-4-7" → ("claude",
// "claude-opus-4-7", true). Names without a known prefix return ok=false.
func Split(name string) (id, model string, ok bool) {
	pre, rest, found := strings.Cut(name, "/")
	if !found || rest == "" {
		return "", "", false
	}
	p, ok := byPrefix[pre]
	if !ok {
		return "", "", false
	}
	return p.ID, rest, true
}

// Bare drops a known prefix, leaving other names as they are.
func Bare(name string) string {
	if _, m, ok := Split(name); ok {
		return m
	}
	return name
}

// Serves reports whether model belongs to a family the provider serves.
func (p Provider) Serves(model string) bool {
	for _, f := range p.Families {
		if strings.HasPrefix(model, f) {
			return true
		}
	}
	return false
}
