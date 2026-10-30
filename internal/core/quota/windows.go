// Package quota holds the window math and counter layout shared by the
// gateway (which writes counters) and the control plane (which reads them for
// the dashboard). PRD §10.2–10.4.
package quota

import (
	"fmt"
	"strconv"
	"time"
)

// Kind is a quota window kind (plan_quotas.window_kind).
type Kind string

const (
	Rolling5h Kind = "rolling_5h"
	Daily     Kind = "daily"
	Weekly    Kind = "weekly"
	Monthly   Kind = "monthly"
)

// Kinds in display order.
var Kinds = []Kind{Rolling5h, Daily, Weekly, Monthly}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	switch k {
	case Rolling5h, Daily, Weekly, Monthly:
		return true
	}
	return false
}

// Anchors define when fixed windows reset (PRD §10.3). Defaults: 00:00
// Asia/Tehran, Saturday, the 1st.
type Anchors struct {
	Location *time.Location
	Hour     int          // daily reset hour, 0–23
	Minute   int          // daily reset minute, 0–59
	Weekday  time.Weekday // weekly reset day
	MonthDay int          // monthly reset day, 1–28
}

// DefaultAnchors returns the PRD defaults.
func DefaultAnchors() Anchors {
	loc, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		loc = time.UTC
	}
	return Anchors{Location: loc, Weekday: time.Saturday, MonthDay: 1}
}

// RollingSpan and BucketSpan define the rolling window: 5 hours counted in
// 5-minute buckets (PRD §10.3).
const (
	RollingSpan = 5 * time.Hour
	BucketSpan  = 5 * time.Minute
)

// Bounds returns the window containing now. For fixed windows, start is the
// last reset at or before now and reset is the next one. For the rolling
// window, start is the oldest bucket still counted and reset is zero: when
// capacity frees depends on usage, not on the calendar.
func Bounds(k Kind, a Anchors, now time.Time) (start, reset time.Time) {
	loc := a.Location
	if loc == nil {
		loc = time.UTC
	}
	t := now.In(loc)
	at := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, a.Hour, a.Minute, 0, 0, loc)
	}
	switch k {
	case Rolling5h:
		end := now.Truncate(BucketSpan)
		return end.Add(-RollingSpan + BucketSpan), time.Time{}
	case Daily:
		start = at(t.Year(), t.Month(), t.Day())
		if start.After(t) {
			start = at(t.Year(), t.Month(), t.Day()-1)
		}
		return start, at(start.Year(), start.Month(), start.Day()+1)
	case Weekly:
		back := (int(t.Weekday()) - int(a.Weekday) + 7) % 7
		start = at(t.Year(), t.Month(), t.Day()-back)
		if start.After(t) {
			start = at(start.Year(), start.Month(), start.Day()-7)
		}
		return start, at(start.Year(), start.Month(), start.Day()+7)
	case Monthly:
		day := min(max(a.MonthDay, 1), 28)
		start = at(t.Year(), t.Month(), day)
		if start.After(t) {
			start = at(t.Year(), t.Month()-1, day)
		}
		return start, at(start.Year(), start.Month()+1, day)
	}
	panic(fmt.Sprintf("quota: unknown window kind %q", k))
}

// Counters hold micro-USD (USD × 1e6) as Redis integers, so INCRBY stays exact.
const MicroUSD = 1_000_000

// ToMicro converts USD to integer micro-USD, rounding to nearest.
func ToMicro(usd float64) int64 { return int64(usd*MicroUSD + 0.5) }

// FromMicro converts micro-USD back to USD.
func FromMicro(m int64) float64 { return float64(m) / MicroUSD }

// FixedKey is the Redis key of a fixed-window counter. The window start is
// part of the key, so a reset is simply a new key; the old one expires by TTL
// (PRD §10.3: no cron). scope is "" for all models or one Qanat name.
func FixedKey(userID string, k Kind, scope string, start time.Time) string {
	return "qanat:q:" + userID + ":" + string(k) + ":" + scope + ":" + strconv.FormatInt(start.Unix(), 10)
}

// BucketKeys returns the keys of every 5-minute bucket in the rolling window
// containing now, oldest first.
func BucketKeys(userID, scope string, now time.Time) []string {
	start, _ := Bounds(Rolling5h, Anchors{}, now)
	n := int(RollingSpan / BucketSpan)
	keys := make([]string, n)
	for i := range n {
		keys[i] = "qanat:q5:" + userID + ":" + scope + ":" + strconv.FormatInt(start.Add(time.Duration(i)*BucketSpan).Unix(), 10)
	}
	return keys
}
