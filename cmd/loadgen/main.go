// Command loadgen measures the gateway's TTFT overhead under concurrency and
// enforces the CI latency gate (PRD REQ-PERF-05 and §2.3).
//
// It runs the same request against the mock upstream directly and through the
// gateway, then compares TTFT distributions:
//
//	overhead.pXX = gateway.pXX - direct.pXX
//
// With -metrics, the budget and the baseline apply to the gateway's own
// overhead histogram (qanat_gateway_overhead_seconds: client TTFT minus
// upstream TTFT, §15.1) over the gateway run instead. The client-side
// difference of two p99s is still reported, but each side's tail swings by
// tens of milliseconds with the upstream's replayed timing, so it can't
// resolve a 10 ms budget.
//
// Exit codes: 0 ok, 1 budget or regression exceeded, 2 usage/runtime error.
//
//	loadgen -gateway http://localhost:8080 -direct http://localhost:9090 \
//	  -key qnt_live_bench -n 500 -c 50 \
//	  -budget-p99 10ms -baseline bench/baseline.json -max-regression 0.10 \
//	  -out bench/result.json
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"
)

type dist struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50_ms"`
	P99 float64 `json:"p99_ms"`
	Max float64 `json:"max_ms"`
	Err int     `json:"errors"`
}

type result struct {
	Direct   dist `json:"direct"`
	Gateway  dist `json:"gateway"`
	Overhead struct {
		P50 float64 `json:"p50_ms"`
		P99 float64 `json:"p99_ms"`
	} `json:"overhead"`
	// Measured is the gateway's own view of its overhead (-metrics).
	Measured    *measured `json:"gateway_measured,omitempty"`
	Concurrency int       `json:"concurrency"`
	At          string    `json:"at"`
}

type measured struct {
	N               int     `json:"n"`
	WithinBudgetPct float64 `json:"within_budget_pct"`
	P99AtMost       float64 `json:"p99_at_most_ms"` // upper edge of the histogram bucket holding p99
}

const defaultBody = `{"model":"claude-sonnet-4-6","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"bench"}]}`

func main() {
	gw := flag.String("gateway", "", "gateway base URL")
	direct := flag.String("direct", "", "mock upstream base URL (baseline)")
	key := flag.String("key", "", "Qanat API key for the gateway")
	n := flag.Int("n", 300, "requests per target")
	c := flag.Int("c", 20, "concurrency")
	bodyFile := flag.String("body", "", "request body file (default: small streaming request)")
	metrics := flag.String("metrics", "", "gateway /metrics URL: gate on its own overhead histogram")
	out := flag.String("out", "", "write JSON result here")
	budget := flag.Duration("budget-p99", 0, "fail if overhead p99 exceeds this (0 = off)")
	baseline := flag.String("baseline", "", "previous result JSON to compare against")
	maxReg := flag.Float64("max-regression", 0.10, "allowed overhead p99 regression vs baseline")
	flag.Parse()
	if *gw == "" || *direct == "" {
		fmt.Fprintln(os.Stderr, "-gateway and -direct are required")
		os.Exit(2)
	}
	body := []byte(defaultBody)
	if *bodyFile != "" {
		b, err := os.ReadFile(*bodyFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		body = b
	}

	var res result
	res.Concurrency, res.At = *c, time.Now().UTC().Format(time.RFC3339)
	res.Direct = run(*direct, "", body, *n, *c)
	var before map[float64]float64
	if *metrics != "" {
		var err error
		if before, err = scrape(*metrics); err != nil {
			fmt.Fprintln(os.Stderr, "metrics:", err)
			os.Exit(2)
		}
	}
	res.Gateway = run(*gw, *key, body, *n, *c)
	if *metrics != "" {
		after, err := scrape(*metrics)
		if err != nil {
			fmt.Fprintln(os.Stderr, "metrics:", err)
			os.Exit(2)
		}
		res.Measured = measure(before, after, *budget)
	}
	res.Overhead.P50 = res.Gateway.P50 - res.Direct.P50
	res.Overhead.P99 = res.Gateway.P99 - res.Direct.P99

	js, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(js))
	if *out != "" {
		os.WriteFile(*out, js, 0o644)
	}

	fail := false
	if res.Gateway.Err > 0 || res.Direct.Err > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: request errors (direct=%d gateway=%d)\n", res.Direct.Err, res.Gateway.Err)
		fail = true
	}
	switch m := res.Measured; {
	case *budget <= 0:
	case m != nil && (m.N == 0 || m.WithinBudgetPct < 99):
		fmt.Fprintf(os.Stderr, "FAIL: gateway overhead within %.0fms for %.1f%% of %d requests; p99 needs 99%%\n", ms(*budget), m.WithinBudgetPct, m.N)
		fail = true
	case m == nil && res.Overhead.P99 > ms(*budget):
		fmt.Fprintf(os.Stderr, "FAIL: overhead p99 %.3fms exceeds budget %.3fms\n", res.Overhead.P99, ms(*budget))
		fail = true
	}
	if *baseline != "" {
		var base result
		if b, err := os.ReadFile(*baseline); err == nil && json.Unmarshal(b, &base) == nil && res.Measured != nil && base.Measured != nil {
			if limit := base.Measured.P99AtMost * (1 + *maxReg); res.Measured.P99AtMost > limit {
				fmt.Fprintf(os.Stderr, "FAIL: gateway overhead p99 ≤%.1fms regressed more than %.0f%% vs baseline ≤%.1fms\n",
					res.Measured.P99AtMost, *maxReg*100, base.Measured.P99AtMost)
				fail = true
			}
		} else if err == nil && base.Overhead.P99 > 0 {
			limit := base.Overhead.P99 * (1 + *maxReg)
			if res.Overhead.P99 > limit {
				fmt.Fprintf(os.Stderr, "FAIL: overhead p99 %.3fms regressed more than %.0f%% vs baseline %.3fms\n",
					res.Overhead.P99, *maxReg*100, base.Overhead.P99)
				fail = true
			}
		}
	}
	if fail {
		os.Exit(1)
	}
}

func run(base, key string, body []byte, n, c int) dist {
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: c, DisableCompression: true}}
	jobs := make(chan struct{})
	var (
		mu   sync.Mutex
		ttfs []time.Duration
		errs int
		wg   sync.WaitGroup
	)
	for i := 0; i < c; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				d, err := once(client, base, key, body)
				mu.Lock()
				if err != nil {
					errs++
				} else {
					ttfs = append(ttfs, d)
				}
				mu.Unlock()
			}
		}()
	}
	for i := 0; i < n; i++ {
		jobs <- struct{}{}
	}
	close(jobs)
	wg.Wait()

	sort.Slice(ttfs, func(i, j int) bool { return ttfs[i] < ttfs[j] })
	d := dist{N: len(ttfs), Err: errs}
	if len(ttfs) > 0 {
		d.P50, d.P99, d.Max = ms(pct(ttfs, 50)), ms(pct(ttfs, 99)), ms(ttfs[len(ttfs)-1])
	}
	return d
}

// once returns time to the first response body byte.
func once(client *http.Client, base, key string, body []byte) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		io.Copy(io.Discard, resp.Body)
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	one := make([]byte, 1)
	if _, err := io.ReadFull(resp.Body, one); err != nil {
		return 0, err
	}
	ttfb := time.Since(start)
	io.Copy(io.Discard, resp.Body)
	return ttfb, nil
}

func pct(sorted []time.Duration, p float64) time.Duration {
	i := int(p/100*float64(len(sorted))+0.999999) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

var bucketLine = regexp.MustCompile(`^qanat_gateway_overhead_seconds_bucket\{.*le="([^"]+)".*\} (\S+)$`)

// scrape reads the gateway's overhead histogram, summed over providers:
// cumulative count per bucket upper edge, in seconds.
func scrape(url string) (map[float64]float64, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	out := map[float64]float64{}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		m := bucketLine.FindSubmatch(line)
		if m == nil {
			continue
		}
		le, err1 := strconv.ParseFloat(string(m[1]), 64) // "+Inf" parses as +Inf
		v, err2 := strconv.ParseFloat(string(m[2]), 64)
		if err1 == nil && err2 == nil {
			out[le] += v
		}
	}
	return out, nil
}

// measure turns two scrapes into the share of requests within budget and
// the bucket edge p99 falls under. A budget between edges counts only the
// edge below it, so the check never passes on rounding.
func measure(before, after map[float64]float64, budget time.Duration) *measured {
	edges := make([]float64, 0, len(after))
	for le := range after {
		edges = append(edges, le)
	}
	sort.Float64s(edges)
	n := after[math.Inf(1)] - before[math.Inf(1)]
	m := &measured{N: int(n)}
	if n <= 0 {
		return m
	}
	m.P99AtMost = math.Inf(1)
	for _, le := range edges {
		share := (after[le] - before[le]) / n
		if le <= budget.Seconds() {
			m.WithinBudgetPct = 100 * share
		}
		if share >= 0.99 && math.IsInf(m.P99AtMost, 1) {
			m.P99AtMost = le * 1000
		}
	}
	return m
}
