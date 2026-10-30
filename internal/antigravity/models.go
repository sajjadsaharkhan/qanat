// Package antigravity speaks Antigravity's Cloud Code API for the gateway: it
// turns an Anthropic Messages request into a v1internal:streamGenerateContent
// envelope and the Gemini-shaped answer back into Anthropic SSE, after
// 9router's open-sse antigravity executor and translators (MIT). Antigravity
// serves Gemini and Claude models through the same Gemini wire format.
//
// Not ported from 9router, by owner decision: tool cloaking (renamed tools,
// decoy tools) and system-prompt rewriting.
package antigravity

import "strings"

// aliases are Qanat names whose upstream id differs (9router's registry
// upstreamModelId, the "(level)" suffix becoming the level below).
var aliases = map[string]string{
	"gemini-3.8-flash":        "gemini-3.8-flash-medium",
	"gemini-3.7-flash-high":   "gemini-3.7-flash-tiered",
	"gemini-3.7-flash-medium": "gemini-3.7-flash-tiered",
	"gemini-3.7-flash-low":    "gemini-3.7-flash-tiered",
	"gemini-3.6-flash-high":   "gemini-3.6-flash-tiered",
	"gemini-3.6-flash-medium": "gemini-3.6-flash-tiered",
	"gemini-3.6-flash-low":    "gemini-3.6-flash-tiered",
	"gemini-3.1-pro-high":     "gemini-pro-agent",
	"claude-sonnet-5-5":       "claude-sonnet-5-5-high",
	"claude-opus-5-5":         "claude-opus-5-5-high",
}

// Upstream returns the id Antigravity knows model by and the thinking level
// its name implies ("" when it implies none): "gemini-3.8-flash-high" is
// itself at level high, "gemini-3.7-flash-low" is the tiered model at low.
func Upstream(model string) (id, level string) {
	id = model
	if a, ok := aliases[model]; ok {
		id = a
	}
	if level = levelSuffix(model); level == "" {
		level = levelSuffix(id)
	}
	return id, level
}

func levelSuffix(model string) string {
	for _, l := range []string{"minimal", "low", "medium", "high"} {
		if strings.HasSuffix(model, "-"+l) {
			return l
		}
	}
	return ""
}

// levelBudget is thinking tokens per effort level (9router's LEVEL_TO_BUDGET,
// max capped to what Gemini accepts).
var levelBudget = map[string]int{
	"minimal": 512, "low": 1024, "medium": 8192, "high": 24576, "xhigh": 32768, "max": 32768,
}

// maxOutput is Antigravity's output ceiling (9router's
// MAX_ANTIGRAVITY_OUTPUT_TOKENS).
const maxOutput = 64000

// outputFloor is the least maxOutputTokens that leaves room to answer after
// thinking with budget (9router's geminiBudgetOutputFloor); -1 is dynamic.
func outputFloor(budget int) int {
	switch {
	case budget < 0:
		return 32768
	case budget <= 1024:
		return 8192
	case budget <= 8192:
		return 16384
	case budget <= 24576:
		return 32768
	}
	return maxOutput
}

func isClaude(model string) bool  { return strings.Contains(model, "claude") }
func isGemini4(model string) bool { return strings.HasPrefix(model, "gemini-4") }
