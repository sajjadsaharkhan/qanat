# Runbook: local Claude Code client telemetry

**Audience:** an AI agent with a shell. Run the steps in order. Every step has
a command, a **Check** that must pass before you continue, and an **If it
fails** action. Every step can be re-run safely.

**Goal:** collect Claude Code's OpenTelemetry events and metrics on this
machine, so client-side API errors can be told apart from Qanat/upstream
errors.

**Scope:** local, single user. No auth, no TLS, 14-day retention. Never expose
the ports to a network.

```
Claude Code ──OTLP──▶ collector :4317 (gRPC) / :4318 (HTTP)
                         ├─ logs/events ─▶ VictoriaLogs    :9428
                         └─ metrics     ─▶ VictoriaMetrics :8428
```

## Rules for the agent

- Ask the user before Step 4. It changes `~/.claude/settings.json`.
- Don't change the image versions, ports or the directory unless the user asks.
- Read data with the `curl` commands in Step 6. The web UIs are for humans.
- Stop and report if a check still fails after its "If it fails" action.

---

## Step 1: Check prerequisites

```sh
docker version --format '{{.Server.Version}}' && docker compose version --short && command -v curl python3 claude
```

**Check:** prints a Docker server version, a Compose version (2.x or later),
and three paths.
**If it fails:** report what's missing to the user and stop. Don't install
Docker yourself.

```sh
python3 -c 'import socket
for p in (4317, 4318, 8428, 9428):
    if socket.socket().connect_ex(("127.0.0.1", p)) == 0: print("port", p, "BUSY")
print("ports-checked")'
```

**Check:** prints only `ports-checked`.
**If it fails:** if a port is busy, run `docker ps --format '{{.Names}} {{.Ports}}'`.
If the busy port belongs to an earlier run of this stack (container names
start with `claude-telemetry-`), continue. Otherwise report the conflict and
stop.

## Step 2: Write the stack files

```sh
mkdir -p ~/claude-telemetry && cd ~/claude-telemetry

cat > compose.yml <<'EOF'
name: claude-telemetry
services:
  collector:
    image: otel/opentelemetry-collector-contrib:0.165.0
    command: ["--config=/etc/otelcol/config.yaml"]
    volumes: ["./otel-collector.yaml:/etc/otelcol/config.yaml:ro"]
    ports: ["127.0.0.1:4317:4317", "127.0.0.1:4318:4318"]
    depends_on: [victorialogs, victoriametrics]
    restart: unless-stopped
  victorialogs:
    image: victoriametrics/victoria-logs:v1.53.0
    command: ["-storageDataPath=/data", "-retentionPeriod=14d"]
    volumes: ["vl-data:/data"]
    ports: ["127.0.0.1:9428:9428"]
    restart: unless-stopped
  victoriametrics:
    image: victoriametrics/victoria-metrics:v1.155.0
    command: ["-storageDataPath=/data", "-retentionPeriod=14d", "-opentelemetry.usePrometheusNaming"]
    volumes: ["vm-data:/data"]
    ports: ["127.0.0.1:8428:8428"]
    restart: unless-stopped
volumes:
  vl-data:
  vm-data:
EOF

cat > otel-collector.yaml <<'EOF'
receivers:
  otlp:
    protocols:
      grpc: { endpoint: 0.0.0.0:4317 }
      http: { endpoint: 0.0.0.0:4318 }
processors:
  deltatocumulative: {}   # Claude Code sends delta counters; VictoriaMetrics stores cumulative
  batch: {}
exporters:
  otlphttp/logs:
    logs_endpoint: http://victorialogs:9428/insert/opentelemetry/v1/logs
    headers: { VL-Stream-Fields: service.name }
  otlphttp/metrics:
    metrics_endpoint: http://victoriametrics:8428/opentelemetry/v1/metrics
service:
  pipelines:
    logs:    { receivers: [otlp], processors: [batch], exporters: [otlphttp/logs] }
    metrics: { receivers: [otlp], processors: [deltatocumulative, batch], exporters: [otlphttp/metrics] }
EOF

docker compose config --quiet && echo config-ok
```

**Check:** prints `config-ok`.
**If it fails:** the heredocs were mangled. Delete `~/claude-telemetry` and
redo this step exactly as written.

## Step 3: Start the stack and smoke-test it

```sh
cd ~/claude-telemetry && docker compose up -d
for i in $(seq 1 30); do docker compose logs collector 2>&1 | grep -q "Everything is ready" && break; sleep 2; done
docker compose ps --format '{{.Service}} {{.State}}'
curl -sf localhost:9428/health && curl -sf localhost:8428/health && echo
```

**Check:** all three services are `running`, and the last line is `OKOK` (one `OK` from each health call).
**If it fails:** run `docker compose logs --tail 50 <service>`, report the
error and stop.

Send a test event and read it back:

```sh
curl -s -o /dev/null -w 'otlp:%{http_code}\n' -X POST localhost:4318/v1/logs -H 'content-type: application/json' \
  -d '{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"runbook-smoke"}}]},"scopeLogs":[{"logRecords":[{"body":{"stringValue":"smoke"},"attributes":[{"key":"event.name","value":{"stringValue":"smoke_test"}}]}]}]}]}'
sleep 3
curl -s localhost:9428/select/logsql/query --data-urlencode 'query=event.name:smoke_test' | head -c 200; echo
```

**Check:** prints `otlp:200`, then a JSON line containing `"smoke"`.
**If it fails:** run `docker compose logs --tail 50 collector` and look for
exporter errors. Report them and stop.

## Step 4: Configure Claude Code (ask the user first)

This merges telemetry variables into the `env` block of
`~/.claude/settings.json` and keeps every other setting. It backs the file up
first.

```sh
python3 - <<'EOF'
import json, pathlib, shutil
p = pathlib.Path.home() / ".claude" / "settings.json"
p.parent.mkdir(exist_ok=True)
data = json.loads(p.read_text()) if p.exists() and p.read_text().strip() else {}
if p.exists(): shutil.copy(p, p.with_suffix(".json.bak"))
data.setdefault("env", {}).update({
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "OTEL_LOGS_EXPORTER": "otlp",
    "OTEL_METRICS_EXPORTER": "otlp",
    "OTEL_EXPORTER_OTLP_PROTOCOL": "grpc",
    "OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4317",
    "OTEL_METRIC_EXPORT_INTERVAL": "10000",
    "OTEL_LOGS_EXPORT_INTERVAL": "5000",
})
p.write_text(json.dumps(data, indent=2) + "\n")
print("settings-ok", sorted(k for k in data["env"] if k.startswith(("OTEL_", "CLAUDE_CODE_ENABLE"))))
EOF
```

**Check:** prints `settings-ok` and the seven variable names.
**If it fails:** the file isn't valid JSON. Restore it with
`cp ~/.claude/settings.json.bak ~/.claude/settings.json` if a backup exists,
report the problem and stop.

Notes:
- Telemetry starts with the *next* Claude Code session. Already-running sessions don't export.
- For every user in an org, the same `env` block belongs in Claude Code's *managed* settings. A project's `.claude/settings.json` can't set the `OTEL_EXPORTER_*` variables.
- Prompt text is not exported unless `OTEL_LOG_USER_PROMPTS=1` is set. Don't set it unless the user asks.

## Step 5: Verify end to end

This makes one small model call, which costs a fraction of a cent.

```sh
cd /tmp && claude -p "reply ok" --model haiku; sleep 8
curl -s localhost:9428/select/logsql/query --data-urlencode 'query=_time:5m event.name:api_request | fields _time, model, status_code, duration_ms, ttft_ms' ; echo
curl -s localhost:8428/api/v1/label/__name__/values | python3 -c 'import sys,json; print([n for n in json.load(sys.stdin)["data"] if n.startswith("claude_code")])'
```

**Check:** Claude replies `ok`. At least one `api_request` line appears. The
metric list includes `claude_code_token_usage_tokens_total`.
**If it fails:**
- No `api_request` line: check that `~/.claude/settings.json` has the env block (Step 4), then wait 10s more and query again.
- Claude itself fails: that's an API or auth problem, not telemetry. Report the error text.
- No metrics but events are present: wait for one more export interval (10s), then query again.

## Step 6: Query recipes

Events are queried with LogsQL on `localhost:9428/select/logsql/query`. Each
result line is one JSON object. Adjust `_time:24h` to the window you need.

```sh
lq() { curl -s localhost:9428/select/logsql/query --data-urlencode "query=$1"; echo; }

# Every failed API call after retries
lq '_time:24h event.name:api_error | fields _time, model, status_code, error, attempt, duration_ms'

# Errors grouped by status and message
lq '_time:24h event.name:api_error | stats by (status_code, error) count() n'

# No HTTP answer at all (network, DNS, TLS, timeout): status_code is absent
lq '_time:24h event.name:api_error status_code:""'

# An HTTP error came back from Qanat or upstream
lq '_time:24h event.name:api_error -status_code:""'

# Calls that needed more than one attempt
lq '_time:24h event.name:api_retries_exhausted'

# Client-side latency per model (ms)
lq '_time:24h event.name:api_request | stats by (model) count() n, avg(ttft_ms) ttft_avg, quantile(0.95, duration_ms) p95'
```

Metrics are queried with PromQL on `localhost:8428/api/v1/query`:

```sh
pq() { curl -s localhost:8428/api/v1/query --data-urlencode "query=$1"; echo; }
pq 'sum by (model) (claude_code_cost_usage_USD_total)'
pq 'sum by (model, type) (claude_code_token_usage_tokens_total)'
```

`api_error` fields:

| Field | Meaning |
|---|---|
| `status_code` | HTTP status. **Absent** when no HTTP response arrived. |
| `error` | Error message (Qanat's message when Qanat answered). |
| `attempt` | Attempts made, the first request included. The event is sent after retries run out. |
| `duration_ms`, `model`, `request_id` | `request_id` is present only when a server answered. |

`api_request` also has `ttft_ms` (client-measured time to first token),
`input_tokens`, `output_tokens`, `cache_read_tokens` and `cost_usd`.

## Step 7: Classify a failure

Apply the first rule that matches each `api_error`:

| Evidence | Conclusion | Next action |
|---|---|---|
| `status_code` absent | Client ↔ Qanat connectivity (network, DNS, TLS, edge proxy) | Check the client network. In Qanat's nginx `qanat_edge` access log, the request is missing or shows `499` with empty `us` |
| `status_code` 502 or 504 | Qanat edge is up, the gateway is down or slow | Check the gateway container and its restarts |
| 403 and `error` contains "no confirmed price" | Qanat config: the model has no price | An admin prices the model on the Models page (`qanat_unpriced_requests_total` names it) |
| 403 and `error` contains "Plan doesn't include" | Qanat plan access | An admin adjusts the user's plan |
| 529 | Qanat had no usable upstream account | In Grafana, `qanat_rejections_total` shows `no_account` (all cooling down) or `pool_exhausted` (all failed) |
| 429 with a Qanat quota/rate message | The user's Qanat quota or rate limit | Expected. Report the reset time in `error` |
| Any other 4xx/5xx | The upstream provider answered with an error | Match it in `qanat_upstream_responses_total` by `model` and `status` to find the account |

## Step 8: Stop or remove (only when asked)

```sh
cd ~/claude-telemetry && docker compose down        # stop, keep data
cd ~/claude-telemetry && docker compose down -v     # stop, delete data
```

To turn client telemetry off, delete the seven keys from Step 4 in the `env`
block of `~/.claude/settings.json`, or restore `~/.claude/settings.json.bak`.
