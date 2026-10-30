package quota

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"qanat/internal/testkit/itenv"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: itenv.Redis(t)})
	t.Cleanup(func() { rdb.Close() })
	b := make([]byte, 6)
	rand.Read(b)
	return NewStore(rdb), "u-" + hex.EncodeToString(b) // unique user per test
}

var utc = Anchors{Location: time.UTC, Weekday: time.Saturday, MonthDay: 1}

// PRD §10.4: admit while every window has remaining > 0; overdraft is
// bounded to the request in flight.
func TestStore_AdmitThenSettle(t *testing.T) {
	s, u := testStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	p := Policy{Anchors: utc, Windows: []Window{{Kind: Daily, LimitMicro: ToMicro(1)}, {Kind: Rolling5h, LimitMicro: ToMicro(5)}}}

	d, err := s.Admit(ctx, u, "r1", p, now)
	if err != nil || !d.Admitted || d.RemainingMicro != ToMicro(1) {
		t.Fatalf("first: %+v %v", d, err)
	}
	// Overdraft: one request costs more than what was left.
	if err := s.Charge(ctx, u, p.Windows, utc, ToMicro(1.20), now); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Admit(ctx, u, "r2", p, now.Add(time.Minute))
	if d.Admitted || d.Reason != ReasonQuota || d.Window.Kind != Daily {
		t.Fatalf("second: %+v", d)
	}
	if want := 11*time.Hour + 59*time.Minute; d.RetryAfter != want {
		t.Fatalf("retry-after %v, want %v (until 00:00 UTC)", d.RetryAfter, want)
	}
	// The next day the daily window is fresh; the rolling one has aged out too.
	if d, _ = s.Admit(ctx, u, "r3", p, now.Add(24*time.Hour)); !d.Admitted {
		t.Fatalf("next day: %+v", d)
	}
}

func TestStore_RollingWindowFreesBucketByBucket(t *testing.T) {
	s, u := testStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 12, 2, 0, 0, time.UTC)
	p := Policy{Anchors: utc, Windows: []Window{{Kind: Rolling5h, LimitMicro: ToMicro(2)}}}
	s.Charge(ctx, u, p.Windows, utc, ToMicro(2), now)

	d, _ := s.Admit(ctx, u, "r", p, now.Add(time.Hour))
	if d.Admitted {
		t.Fatal("rolling window should be exhausted")
	}
	// The 12:00 bucket leaves the window at 17:00.
	if want := time.Date(2026, 10, 30, 17, 0, 0, 0, time.UTC); !d.ResetAt.Equal(want) {
		t.Fatalf("frees at %v, want %v", d.ResetAt, want)
	}
	if d, _ = s.Admit(ctx, u, "r", p, now.Add(5*time.Hour)); !d.Admitted {
		t.Fatalf("after 5h: %+v", d)
	}
}

// §10.2: per-model windows only bind the scoped model.
func TestStore_ScopedWindowIsSeparate(t *testing.T) {
	s, u := testStore(t)
	ctx := context.Background()
	now := time.Now()
	opus := Window{Kind: Daily, Scope: "opus", LimitMicro: ToMicro(1)}
	all := Window{Kind: Daily, LimitMicro: ToMicro(100)}
	s.Charge(ctx, u, []Window{all, opus}, utc, ToMicro(1), now)

	if d, _ := s.Admit(ctx, u, "a", Policy{Anchors: utc, Windows: []Window{all, opus}}, now); d.Admitted {
		t.Fatal("opus should be exhausted")
	}
	if d, _ := s.Admit(ctx, u, "b", Policy{Anchors: utc, Windows: []Window{all}}, now); !d.Admitted {
		t.Fatalf("other models stay open: %+v", d)
	}
}

func TestStore_RateLimits(t *testing.T) {
	s, u := testStore(t)
	ctx := context.Background()
	now := time.Now()
	p := Policy{Anchors: utc, Windows: []Window{{Kind: Daily, LimitMicro: ToMicro(100)}}, RPM: 3, MaxConcurrent: 2}

	for i, id := range []string{"a", "b"} {
		if d, _ := s.Admit(ctx, u, id, p, now); !d.Admitted {
			t.Fatalf("request %d: %+v", i, d)
		}
	}
	if d, _ := s.Admit(ctx, u, "c", p, now); d.Admitted || d.Reason != ReasonConcurrency {
		t.Fatalf("third concurrent: %+v", d)
	}
	s.Release(ctx, u, "a")
	if d, _ := s.Admit(ctx, u, "c", p, now); !d.Admitted {
		t.Fatalf("after release: %+v", d)
	}
	s.Release(ctx, u, "b")
	s.Release(ctx, u, "c")
	// A refused request must not consume RPM: a, b, c were admitted (3/3).
	if d, _ := s.Admit(ctx, u, "d", p, now); d.Admitted || d.Reason != ReasonRPM {
		t.Fatalf("fourth in a minute: %+v", d)
	}
}

func TestStore_ZeroQuota(t *testing.T) {
	s, u := testStore(t)
	d, err := s.Admit(context.Background(), u, "r", Policy{Anchors: utc}, time.Now())
	if err != nil || d.Admitted || d.Reason != ReasonZeroQuota || d.RetryAfter <= 0 {
		t.Fatalf("%+v %v", d, err)
	}
}
