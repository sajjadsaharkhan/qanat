package codex

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Codex error body: {"error": {type|code, message, resets_in_seconds,
// resets_at}}, or FastAPI's {"detail": "…"}.
type codexError struct {
	Error struct {
		Type            string `json:"type"`
		Code            string `json:"code"`
		Message         string `json:"message"`
		ResetsInSeconds int64  `json:"resets_in_seconds"`
		ResetsAt        int64  `json:"resets_at"`
	} `json:"error"`
	Detail any `json:"detail"`
}

// errorType is the Anthropic error type for a Codex error code or status.
func errorType(code string, status int) string {
	switch {
	case status == http.StatusBadRequest:
		return "invalid_request_error"
	case status == http.StatusUnauthorized:
		return "authentication_error"
	case status == http.StatusForbidden:
		return "permission_error"
	case status == http.StatusNotFound:
		return "not_found_error"
	case status == http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case status == http.StatusTooManyRequests || strings.Contains(code, "rate_limit") || strings.Contains(code, "usage_limit"):
		return "rate_limit_error"
	case status == http.StatusServiceUnavailable || strings.Contains(code, "overloaded") || strings.Contains(code, "capacity"):
		return "overloaded_error"
	}
	return "api_error"
}

// ErrorBody is a Codex error as an Anthropic error body, keeping Codex's
// message so the client sees why.
func ErrorBody(status int, body []byte) []byte {
	var e codexError
	_ = json.Unmarshal(body, &e)
	msg := strings.TrimSpace(e.Error.Message)
	if msg == "" {
		if d, ok := e.Detail.(string); ok {
			msg = d
		}
	}
	if msg == "" {
		msg = strings.TrimSpace(string(body))
		if len(msg) > 500 {
			msg = msg[:500]
		}
	}
	code := e.Error.Code + e.Error.Type
	b, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]string{
		"type": errorType(code, status), "message": "Codex: " + msg,
	}})
	return b
}

// RetryAfter reads when a 429 lifts (usage_limit_reached carries the reset);
// 0 when the body doesn't say.
func RetryAfter(body []byte, now time.Time) time.Duration {
	var e codexError
	if json.Unmarshal(body, &e) != nil {
		return 0
	}
	if s := e.Error.ResetsInSeconds; s > 0 {
		return time.Duration(s) * time.Second
	}
	if t := time.Unix(e.Error.ResetsAt, 0); e.Error.ResetsAt > 0 && t.After(now) {
		return t.Sub(now)
	}
	return 0
}
