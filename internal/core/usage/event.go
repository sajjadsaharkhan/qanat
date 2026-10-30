// Package usage defines the usage event that flows from the gateway to the
// control plane's aggregation worker through Redis Streams (PRD §10.6, D17).
package usage

import "time"

const (
	// Stream is the Redis Stream of usage events.
	Stream = "qanat:usage"
	// Group is the single consumer group (the control worker).
	Group = "control"
	// RecentLen and RecentTTL bound each user's recent-request ring buffer.
	RecentLen = 200
	RecentTTL = 3 * 24 * time.Hour
)

// RecentKey is a user's recent-request ring buffer (a Redis list, newest first).
func RecentKey(userID string) string { return "qanat:recent:" + userID }

// Event is one finished request. It carries metadata only, never prompt or
// response content (PRD §15.2).
type Event struct {
	RequestID     string    `json:"request_id"`
	At            time.Time `json:"at"` // request start
	UserID        string    `json:"user_id"`
	KeyID         string    `json:"key_id"`
	ClientIP      string    `json:"client_ip,omitempty"`
	Model         string    `json:"model"` // Qanat name the client asked for
	Provider      string    `json:"provider"`
	UpstreamModel string    `json:"upstream_model"`
	AccountID     string    `json:"account_id,omitempty"`

	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	CostMicro        int64 `json:"cost_micro"`
	PriceVersion     int64 `json:"price_version"`

	Status     int    `json:"status"`
	Aborted    bool   `json:"aborted"` // cut mid-stream (REQ-QUOTA-01)
	StopReason string `json:"stop_reason,omitempty"`
	TTFTMillis int64  `json:"ttft_ms"`
	DurMillis  int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}
