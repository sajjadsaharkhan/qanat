package antigravity

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Google's error body: {"error": {code, message, status, details}}.
type googleError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Details []struct {
			RetryDelay string `json:"retryDelay"`
			Metadata   struct {
				QuotaResetTimeStamp string `json:"quotaResetTimeStamp"`
			} `json:"metadata"`
		} `json:"details"`
	} `json:"error"`
}

// errorInfo is the Anthropic error type and message for a Google error.
func errorInfo(status int, body []byte) (typ, msg string) {
	var g googleError
	_ = json.Unmarshal(body, &g)
	if status == 0 {
		status = g.Error.Code
	}
	msg = strings.TrimSpace(g.Error.Message)
	if msg == "" {
		msg = strings.TrimSpace(string(body))
		if len(msg) > 500 {
			msg = msg[:500]
		}
	}
	switch {
	case status == http.StatusBadRequest:
		typ = "invalid_request_error"
	case status == http.StatusUnauthorized:
		typ = "authentication_error"
	case status == http.StatusForbidden:
		typ = "permission_error"
	case status == http.StatusNotFound:
		typ = "not_found_error"
	case status == http.StatusRequestEntityTooLarge:
		typ = "request_too_large"
	case status == http.StatusTooManyRequests || g.Error.Status == "RESOURCE_EXHAUSTED":
		typ = "rate_limit_error"
	case status == http.StatusServiceUnavailable:
		typ = "overloaded_error"
	default:
		typ = "api_error"
	}
	return typ, "Antigravity: " + msg
}

// ErrorBody is a Google error as an Anthropic error body.
func ErrorBody(status int, body []byte) []byte {
	typ, msg := errorInfo(status, body)
	b, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]string{"type": typ, "message": msg}})
	return b
}

// RetryAfter reads when a 429 lifts from Google's RetryInfo or quota reset
// time (9router's parseError); 0 when the body doesn't say.
func RetryAfter(body []byte, now time.Time) time.Duration {
	var g googleError
	if json.Unmarshal(body, &g) != nil {
		return 0
	}
	for _, d := range g.Error.Details {
		if d.RetryDelay != "" {
			if v, err := time.ParseDuration(d.RetryDelay); err == nil && v > 0 {
				return v
			}
		}
		if ts := d.Metadata.QuotaResetTimeStamp; ts != "" {
			if t, err := time.Parse(time.RFC3339, ts); err == nil && t.After(now) {
				return t.Sub(now)
			}
		}
	}
	return 0
}
