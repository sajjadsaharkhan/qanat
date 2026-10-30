package proxy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ProviderHosts are the endpoints the Test button checks (REQ-PROXY-05).
var ProviderHosts = []Target{
	{Provider: "claude", URL: "https://api.anthropic.com/"},
	{Provider: "codex", URL: "https://chatgpt.com/"},
	{Provider: "antigravity", URL: "https://cloudcode-pa.googleapis.com/"},
}

// Target is one host to probe through the proxy.
type Target struct {
	Provider string
	URL      string
}

// Result is the Test button's report.
type Result struct {
	EgressIP       string        `json:"egress_ip"`
	EgressError    string        `json:"egress_error,omitempty"`
	Location       *Location     `json:"location,omitempty"`
	Providers      []ProbeResult `json:"providers"`
	ProxyReachable bool          `json:"proxy_reachable"`
}

// Location is where the egress IP is, as the IP lookup service reports it.
// Geo-IP data is approximate.
type Location struct {
	City     string `json:"city,omitempty"`
	Region   string `json:"region,omitempty"`
	Country  string `json:"country,omitempty"` // ISO 3166-1 alpha-2, e.g. "SE"
	Org      string `json:"org,omitempty"`     // network operator, e.g. "AS215960 Example GmbH"
	Timezone string `json:"timezone,omitempty"`
}

// ProbeResult is one provider host reached through the proxy.
type ProbeResult struct {
	Provider   string `json:"provider"`
	Host       string `json:"host"`
	OK         bool   `json:"ok"`
	LatencyMS  int64  `json:"latency_ms"`
	TLSVersion string `json:"tls_version,omitempty"`
	Status     int    `json:"status,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Test reaches each target and the egress-IP echo through spec, in
// parallel, on throwaway connections so the numbers include connect and TLS.
func Test(ctx context.Context, spec *Spec, ipEcho string, targets []Target) Result {
	tr := NewTransport(spec)
	tr.DisableKeepAlives = true
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	res := Result{Providers: make([]ProbeResult, len(targets))}
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Go(func() { res.Providers[i] = probe(ctx, client, t) })
	}
	if ipEcho != "" {
		wg.Go(func() {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ipEcho, nil)
			resp, err := client.Do(req)
			if err != nil {
				res.EgressError = clean(err)
				return
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			if resp.StatusCode == http.StatusProxyAuthRequired {
				res.EgressError = "the proxy rejected the username or password (407)"
				return
			}
			if resp.StatusCode != http.StatusOK {
				res.EgressError = "IP echo service answered " + resp.Status
				return
			}
			res.EgressIP, res.Location = parseEcho(b)
		})
	}
	wg.Wait()
	for _, p := range res.Providers {
		res.ProxyReachable = res.ProxyReachable || p.OK
	}
	res.ProxyReachable = res.ProxyReachable || res.EgressIP != ""
	return res
}

func probe(ctx context.Context, client *http.Client, t Target) ProbeResult {
	r := ProbeResult{Provider: t.Provider, Host: strings.TrimSuffix(strings.TrimPrefix(t.URL, "https://"), "/")}
	var tlsState *tls.ConnectionState
	trace := &httptrace.ClientTrace{TLSHandshakeDone: func(s tls.ConnectionState, err error) {
		if err == nil {
			tlsState = &s
		}
	}}
	req, _ := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodHead, t.URL, nil)
	start := time.Now()
	resp, err := client.Do(req)
	r.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		r.Error = clean(err)
		return r
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusProxyAuthRequired {
		r.Error = "the proxy rejected the username or password (407)"
		return r
	}
	// Any HTTP answer proves the proxy reached the host over TLS.
	r.OK, r.Status = true, resp.StatusCode
	if tlsState != nil {
		r.TLSVersion = tls.VersionName(tlsState.Version)
	}
	return r
}

// parseEcho reads an IP lookup answer: ipinfo.io-style JSON (ip, city,
// region, country, org, timezone) or a bare IP in plain text.
func parseEcho(b []byte) (string, *Location) {
	var v struct {
		IP string `json:"ip"`
		Location
	}
	if json.Unmarshal(b, &v) != nil {
		return strings.TrimSpace(string(b)), nil
	}
	if v.Location == (Location{}) {
		return v.IP, nil
	}
	return v.IP, &v.Location
}

// clean drops the request URL Go prepends, leaving what went wrong
// ("proxyconnect tcp: … connection refused").
func clean(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}
