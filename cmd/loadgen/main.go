// Command loadgen measures the gateway's TTFT overhead under concurrency and
// enforces the CI latency gate (PRD REQ-PERF-05 and §2.3).
//
// It runs the same request against the mock upstream directly and through the
// gateway, then compares TTFT distributions:
//
//	overhead.pXX = gateway.pXX - direct.pXX
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
	"net/http"
	"os"
	"sort"
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
	Concurrency int    `json:"concurrency"`
	At          string `json:"at"`
}

const defaultBody = `{"model":"claude-sonnet-4-6","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"bench"}]}`

func main() {
	gw := flag.String("gateway", "", "gateway base URL")
	direct := flag.String("direct", "", "mock upstream base URL (baseline)")
	key := flag.String("key", "", "Qanat API key for the gateway")
	n := flag.Int("n", 300, "requests per target")
	c := flag.Int("c", 20, "concurrency")
	bodyFile := flag.String("body", "", "request body file (default: small streaming request)")
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
	res.Gateway = run(*gw, *key, body, *n, *c)
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
	if *budget > 0 && res.Overhead.P99 > ms(*budget) {
		fmt.Fprintf(os.Stderr, "FAIL: overhead p99 %.3fms exceeds budget %.3fms\n", res.Overhead.P99, ms(*budget))
		fail = true
	}
	if *baseline != "" {
		var base result
		if b, err := os.ReadFile(*baseline); err == nil && json.Unmarshal(b, &base) == nil && base.Overhead.P99 > 0 {
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
