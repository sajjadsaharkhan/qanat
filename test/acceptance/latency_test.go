package acceptance

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"qanat/internal/testkit/latency"
	"qanat/internal/testkit/mockupstream"
)

// PRD §2.3: gateway TTFT overhead on the passthrough path.
//
// overhead = (client-observed TTFT) - (upstream-side TTFT)
//
// The upstream answers after a fixed 20 ms, so anything above that on the
// client side is added by the gateway (plus loopback, which is microseconds).
// Timing tests are skipped under -short and enforce the PRD budget only when
// QANAT_LATENCY_STRICT=1 (dedicated benchmark runners); elsewhere they log the
// report and fail only on gross regressions (10x budget), so shared CI
// runners do not flake.
func TestLatency_PassthroughTTFTOverhead(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	e := setup(t)
	const runs = 60
	sc := mockupstream.NewScript(
		mockupstream.Step{Delay: 20 * time.Millisecond, Data: []byte(evMessageStart)},
		mockupstream.Step{Data: []byte(evMessageStop)},
	)
	e.up.SetDefault(msgPath, sc)

	var overheads []time.Duration
	for i := 0; i < runs; i++ {
		start := time.Now()
		resp := e.post(t, context.Background(), quirkyBody, nil)
		arrivals, err := latency.Read(resp.Body)
		must(t, err)
		rec := e.up.Last()
		<-rec.Done
		if len(arrivals) == 0 || len(rec.WrittenAt) == 0 {
			t.Fatal("no events")
		}
		clientTTFT := arrivals[0].At.Sub(start)
		upstreamTTFT := rec.WrittenAt[0].Sub(rec.ArrivedAt)
		if i >= 5 { // discard warm-up (connection pool, JIT caches)
			overheads = append(overheads, clientTTFT-upstreamTTFT)
		}
		io.Copy(io.Discard, resp.Body)
	}

	r := latency.Summarize(overheads)
	t.Logf("passthrough TTFT overhead: %v (budget p50<%v p99<%v)", r, latency.PassthroughTTFT.P50, latency.PassthroughTTFT.P99)
	budget := latency.PassthroughTTFT
	if os.Getenv("QANAT_LATENCY_STRICT") != "1" {
		budget = latency.Budget{P50: budget.P50 * 10, P99: budget.P99 * 10}
	}
	if err := budget.Check(r); err != nil {
		t.Fatal(err)
	}
}
