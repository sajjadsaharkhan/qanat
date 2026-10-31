# Benchmarks

`make bench` writes `result.json` here. To accept a new performance level,
commit it as `baseline.json` in a dedicated PR with the reason in the
description. CI fails when overhead p99 regresses more than 10% against
`baseline.json` (PRD REQ-PERF-05).

The gate reads the gateway's own overhead histogram from the test gateway's
`/metrics` (`gateway_measured` in the result). The client-side `overhead`
fields are the difference of two independently measured p99s and swing by
tens of milliseconds between identical runs, so they're reported, not gated.
