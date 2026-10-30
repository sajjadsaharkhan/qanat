package gateway

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics exposed on /metrics (PRD §15.1). Labels never include user IDs.
var (
	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qanat_requests_total", Help: "Requests by endpoint and response status.",
	}, []string{"endpoint", "status"})

	overhead = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "qanat_gateway_overhead_seconds",
		Help:    "Client TTFT minus upstream TTFT: time the gateway adds before the first byte.",
		Buckets: []float64{.0005, .001, .002, .005, .01, .02, .05, .1, .25},
	}, []string{"provider"})

	upstreamTTFT = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "qanat_upstream_ttft_seconds", Help: "Upstream time to first byte.",
		Buckets: prometheus.ExponentialBuckets(.05, 2, 10),
	}, []string{"provider"})

	activeStreams = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "qanat_active_streams", Help: "Responses currently being streamed.",
	})

	upstreamErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qanat_upstream_errors_total", Help: "Upstream failures by provider and reason.",
	}, []string{"provider", "account", "reason"})

	rejections = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qanat_rejections_total", Help: "Requests refused by the gateway, by reason.",
	}, []string{"reason"})

	redisErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "qanat_redis_errors_total", Help: "Redis calls that failed on the hot path (admission fails open).",
	})

	usageDropped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "qanat_usage_events_dropped_total", Help: "Usage events dropped because the in-memory buffer was full.",
	})

	usageBuffered = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "qanat_usage_buffer_depth", Help: "Usage events waiting to be written to Redis.",
	})

	tokensTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qanat_tokens_total", Help: "Tokens by provider and kind.",
	}, []string{"provider", "kind"})

	accountHealthy = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "qanat_account_healthy", Help: "1 if the upstream account is healthy.",
	}, []string{"provider", "account"})

	snapshotGeneration = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "qanat_config_generation", Help: "Config generation of the gateway's snapshot.",
	})

	// Declared now so dashboards and alerts can reference it; translators
	// (P1) increment it for every dropped capability hint (REQ-CAP-02).
	_ = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "qanat_translation_dropped_total", Help: "Request hints dropped on the translation path.",
	}, []string{"provider", "hint"})
)
