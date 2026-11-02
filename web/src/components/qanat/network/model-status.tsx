/*
 * Model Status: every model used in the last 24 hours, one availability bar
 * per 30 minutes (after the status pages of AI gateways). The data is the
 * gateways' own attempt counters kept in Redis for 24 h, not Postgres.
 */
"use client";

import { Panel } from "@/components/qanat/network/charts";
import { SectionHeader, Tag } from "@/components/qanat/page";
import { Skeleton } from "@/components/ui/skeleton";
import { api, type Schemas } from "@/lib/api/client";
import { fmtMs } from "@/lib/network";
import { cn } from "@/lib/utils";

const POLL_MS = 60_000;
const timeFmt = new Intl.DateTimeFormat("en-US", { dateStyle: "short", timeStyle: "short" });

type Bucket = Schemas["ModelStatusBucket"];

// Green ≥ 99 % of counted attempts, amber ≥ 90 %, red below; grey: no traffic.
function tone(b: Bucket): "none" | "ok" | "warn" | "bad" {
  if (b.attempts === 0) return "none";
  const rate = (b.attempts - b.faults) / b.attempts;
  return rate >= 0.99 ? "ok" : rate >= 0.9 ? "warn" : "bad";
}

const toneClass = { none: "bg-muted", ok: "bg-success", warn: "bg-warning", bad: "bg-destructive" } as const;

const pct2 = (rate: number | null) => (rate == null ? "—" : `${(rate * 100).toFixed(2)}%`);

function Bar({ b }: { b: Bucket }) {
  const t = tone(b);
  const text = b.attempts === 0 ? "no requests" : `${b.attempts - b.faults}/${b.attempts} ok`;
  return <span title={`${timeFmt.format(new Date(b.t))} · ${text}`} className={cn("h-5 min-w-0 flex-1 rounded-[2px]", toneClass[t])} />;
}

function Metric({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <span>
      {label}: <span className="font-medium text-foreground tabular-nums">{children}</span>
    </span>
  );
}

function ModelRow({ m }: { m: Schemas["ModelStatus"] }) {
  const latest = [...m.buckets].reverse().find((b) => b.attempts > 0);
  const dot = latest ? toneClass[tone(latest)] : "bg-muted";
  return (
    <Panel className="flex flex-col gap-3 p-4">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span aria-hidden className={cn("size-2.5 rounded-full", dot)} />
        <span className="font-mono text-sm font-semibold">{m.model}</span>
        <Tag>
          <span className="font-mono">{m.prefix}/</span>
        </Tag>
        <div className="ms-auto flex flex-wrap gap-x-4 text-meta text-muted-foreground">
          <Metric label="Availability">{pct2(m.availability)}</Metric>
          <Metric label="TTFT P90">{fmtMs(m.ttft_p90_ms)}</Metric>
          <Metric label="Avg latency">{fmtMs(m.avg_latency_ms)}</Metric>
        </div>
      </div>
      <div role="img" aria-label={`${m.model}: ${m.attempts} requests in 24 hours, availability ${pct2(m.availability)}`} className="flex gap-[3px]">
        {m.buckets.map((b) => (
          <Bar key={b.t} b={b} />
        ))}
      </div>
    </Panel>
  );
}

export function ModelStatus() {
  const q = api.useQuery("get", "/api/network/models", {}, { refetchInterval: POLL_MS });
  const d = q.data;
  // A model is available unless its 24 h availability is in the red.
  const available = (d?.models ?? []).filter((m) => (m.availability ?? 1) >= 0.9).length;
  return (
    <section aria-label="Model status">
      <SectionHeader>Model status</SectionHeader>
      {q.isError ? (
        <p role="alert" className="text-destructive-text">
          Could not load model status. It will retry shortly.
        </p>
      ) : !d ? (
        <Skeleton className="h-40 w-full rounded-2xl" />
      ) : d.models.length === 0 ? (
        <Panel className="p-4 text-sm text-muted-foreground">No model has been used in the last 24 hours.</Panel>
      ) : (
        <div className="flex flex-col gap-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <Panel className="p-4">
              <p className="text-meta text-muted-foreground">Success rate (24h)</p>
              <p className="mt-1 text-2xl font-semibold tabular-nums">{pct2(d.success_rate)}</p>
            </Panel>
            <Panel className="p-4">
              <p className="text-meta text-muted-foreground">Available models</p>
              <p className="mt-1 text-2xl font-semibold tabular-nums">
                {available} / {d.models.length}
              </p>
            </Panel>
          </div>
          {d.models.map((m) => (
            <ModelRow key={`${m.provider}/${m.model}`} m={m} />
          ))}
          <p className="text-meta text-muted-foreground">
            Updated {timeFmt.format(new Date(d.generated_at))} · Window 24h, one bar per 30 minutes. Counts successes and upstream faults; rate limits and client aborts are left out.
          </p>
        </div>
      )}
    </section>
  );
}
