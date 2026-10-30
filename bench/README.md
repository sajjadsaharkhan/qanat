# Benchmarks

`make bench` writes `result.json` here. To accept a new performance level,
commit it as `baseline.json` in a dedicated PR with the reason in the
description. CI fails when overhead p99 regresses more than 10% against
`baseline.json` (PRD REQ-PERF-05).
