// Package latency measures what the gateway adds on top of the upstream.
//
// The mock upstream records when it wrote each chunk; the client side records
// when each event arrived. The difference, chunk by chunk, is the gateway's
// overhead (plus loopback network time, which is constant and tiny in tests).
// This is the measurement behind the PRD success metrics in §2.3 and the CI
// benchmark gate in REQ-PERF-05.
package latency

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"qanat/internal/testkit/sse"
)

// Arrival is one event and when the client received it.
type Arrival struct {
	At    time.Time
	Event sse.Event
}

// Read consumes an SSE body, timestamping each event on arrival.
func Read(body io.Reader) ([]Arrival, error) {
	rd := sse.NewReader(body)
	var out []Arrival
	for {
		ev, err := rd.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, Arrival{At: time.Now(), Event: ev})
	}
}

// PerChunk returns arrival[i] - written[i] for each aligned pair. Use it on
// the passthrough path, where each upstream chunk is exactly one event. For
// translated paths, pass the arrivals of the first event produced per chunk.
func PerChunk(written []time.Time, arrivals []Arrival) ([]time.Duration, error) {
	if len(arrivals) < len(written) {
		return nil, fmt.Errorf("latency: %d chunks written but only %d events arrived", len(written), len(arrivals))
	}
	out := make([]time.Duration, len(written))
	for i := range written {
		out[i] = arrivals[i].At.Sub(written[i])
	}
	return out, nil
}

// Report summarizes a sample of durations.
type Report struct {
	N             int
	P50, P99, Max time.Duration
}

func (r Report) String() string {
	return fmt.Sprintf("n=%d p50=%v p99=%v max=%v", r.N, r.P50, r.P99, r.Max)
}

// Summarize computes percentiles (nearest-rank).
func Summarize(ds []time.Duration) Report {
	if len(ds) == 0 {
		return Report{}
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return Report{N: len(s), P50: Percentile(s, 50), P99: Percentile(s, 99), Max: s[len(s)-1]}
}

// Percentile returns the p-th percentile of an already sorted slice.
func Percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(p/100*float64(len(sorted))+0.999999) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// Budget is a latency target from the PRD.
type Budget struct {
	P50, P99 time.Duration
}

// Check returns an error describing every exceeded percentile.
func (b Budget) Check(r Report) error {
	var errs []error
	if b.P50 > 0 && r.P50 > b.P50 {
		errs = append(errs, fmt.Errorf("p50 %v exceeds budget %v", r.P50, b.P50))
	}
	if b.P99 > 0 && r.P99 > b.P99 {
		errs = append(errs, fmt.Errorf("p99 %v exceeds budget %v", r.P99, b.P99))
	}
	return errors.Join(errs...)
}

// PRD §2.3 budgets.
var (
	PassthroughTTFT = Budget{P50: 2 * time.Millisecond, P99: 10 * time.Millisecond}
	TranslatedTTFT  = Budget{P50: 5 * time.Millisecond, P99: 15 * time.Millisecond}
	PerChunkBudget  = Budget{P99: 1 * time.Millisecond}
)
