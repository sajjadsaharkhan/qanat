package quota

import (
	"strconv"
	"testing"
	"time"
)

func tehran(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// PRD §10.3 defaults: daily 00:00 Asia/Tehran, weekly Saturday, monthly 1st.
func TestBounds_DefaultsInTehran(t *testing.T) {
	loc := tehran(t)
	a := DefaultAnchors()
	// Thursday 2026-10-29 21:00 UTC = Friday 00:30 Tehran (UTC+3:30).
	now := time.Date(2026, 10, 29, 21, 0, 0, 0, time.UTC)

	cases := []struct {
		k            Kind
		start, reset time.Time
	}{
		{Daily, time.Date(2026, 10, 30, 0, 0, 0, 0, loc), time.Date(2026, 10, 31, 0, 0, 0, 0, loc)},
		{Weekly, time.Date(2026, 10, 24, 0, 0, 0, 0, loc), time.Date(2026, 10, 31, 0, 0, 0, 0, loc)}, // Saturdays
		{Monthly, time.Date(2026, 10, 1, 0, 0, 0, 0, loc), time.Date(2026, 11, 1, 0, 0, 0, 0, loc)},
	}
	for _, c := range cases {
		start, reset := Bounds(c.k, a, now)
		if !start.Equal(c.start) || !reset.Equal(c.reset) {
			t.Errorf("%s: got [%v, %v), want [%v, %v)", c.k, start, reset, c.start, c.reset)
		}
	}
}

func TestBounds_BeforeAnchorBelongsToPreviousWindow(t *testing.T) {
	a := Anchors{Location: time.UTC, Hour: 9, Weekday: time.Monday, MonthDay: 15}
	now := time.Date(2026, 3, 15, 8, 59, 0, 0, time.UTC) // Sunday, before 09:00, on the monthly anchor day

	if s, r := Bounds(Daily, a, now); !s.Equal(time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)) || !r.Equal(time.Date(2026, 3, 15, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("daily: [%v, %v)", s, r)
	}
	if s, _ := Bounds(Weekly, a, now); !s.Equal(time.Date(2026, 3, 9, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("weekly start %v", s)
	}
	if s, r := Bounds(Monthly, a, now); !s.Equal(time.Date(2026, 2, 15, 9, 0, 0, 0, time.UTC)) || !r.Equal(time.Date(2026, 3, 15, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("monthly: [%v, %v)", s, r)
	}
	// Exactly at the anchor starts a new window.
	if s, _ := Bounds(Daily, a, time.Date(2026, 3, 15, 9, 0, 0, 0, time.UTC)); !s.Equal(time.Date(2026, 3, 15, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("at anchor: %v", s)
	}
}

// A DST zone: the window is a calendar day, not 24 hours.
func TestBounds_DailyAcrossDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	a := Anchors{Location: ny}
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, ny) // spring forward day
	s, r := Bounds(Daily, a, now)
	if r.Sub(s) != 23*time.Hour {
		t.Fatalf("DST day length %v", r.Sub(s))
	}
}

func TestBucketKeys_CoverFiveHours(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 7, 0, 0, time.UTC)
	keys := BucketKeys("u1", "", now)
	if len(keys) != 60 {
		t.Fatalf("%d buckets", len(keys))
	}
	if want := "qanat:q5:u1::" + strconv.FormatInt(time.Date(2026, 10, 30, 12, 5, 0, 0, time.UTC).Unix(), 10); keys[59] != want { // 12:05 bucket
		t.Fatalf("last key %s, want %s", keys[59], want)
	}
}

func TestMicroRoundTrip(t *testing.T) {
	if got := FromMicro(ToMicro(0.0105)); got != 0.0105 {
		t.Fatalf("%v", got)
	}
}
