package latency

import (
	"testing"
	"time"
)

func TestSummarizeAndBudget(t *testing.T) {
	var ds []time.Duration
	for i := 1; i <= 100; i++ {
		ds = append(ds, time.Duration(i)*time.Millisecond)
	}
	r := Summarize(ds)
	if r.P50 != 50*time.Millisecond || r.P99 != 99*time.Millisecond || r.Max != 100*time.Millisecond {
		t.Fatalf("bad report: %v", r)
	}
	if err := (Budget{P99: 100 * time.Millisecond}).Check(r); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := (Budget{P50: 10 * time.Millisecond}).Check(r); err == nil {
		t.Fatal("expected budget violation")
	}
}

func TestPerChunkAlignment(t *testing.T) {
	base := time.Now()
	written := []time.Time{base, base.Add(10 * time.Millisecond)}
	arr := []Arrival{{At: base.Add(time.Millisecond)}, {At: base.Add(12 * time.Millisecond)}}
	got, err := PerChunk(written, arr)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != time.Millisecond || got[1] != 2*time.Millisecond {
		t.Fatalf("got %v", got)
	}
	if _, err := PerChunk(written, arr[:1]); err == nil {
		t.Fatal("expected error for missing events")
	}
}
