/*
 * The Network page's charts. The path chart is Tower's dual-series latency
 * chart (github.com/ghhrmnzdh/tower, Dashboard.swift LatencyChart): the
 * provider handshake over the gateway's internet baseline, on one ms axis.
 * Colors are the primary ramp only (design.md §3.4); status tints never
 * appear in a chart.
 */
"use client";

import { memo } from "react";
import { Bar, BarChart, CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import { tooltipStyle } from "@/components/qanat/usage-overview/shared";
import type { Schemas } from "@/lib/api/client";
import { fmtMs } from "@/lib/network";
import { cn } from "@/lib/utils";

const clock = new Intl.DateTimeFormat("en-US", { hour: "2-digit", minute: "2-digit", hour12: false });
const clockSec = new Intl.DateTimeFormat("en-US", { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });

// Axis text and grid stay recessive but readable on both themes.
const axis = { fontSize: 11, fill: "currentColor", fillOpacity: 0.72 };
const grid = { strokeDasharray: "3 4", stroke: "currentColor", strokeOpacity: 0.16 };
const legendStyle = { fontSize: 12, paddingTop: 6 };

/** Panel: the page's surface, with enough edge to stand off the background. */
export function Panel({ className, children }: { className?: string; children: React.ReactNode }) {
  return <div className={cn("rounded-2xl border border-foreground/12 bg-card text-card-foreground shadow-sm", className)}>{children}</div>;
}

function ChartCard({
  title,
  hint,
  value,
  empty,
  children,
}: {
  title: string;
  hint?: string;
  value?: string;
  empty: boolean;
  children: React.ReactElement;
}) {
  return (
    <Panel className="flex min-w-0 flex-col gap-3 p-4">
      <div className="flex items-start justify-between gap-2">
        <div>
          <h3 className="text-sm font-semibold">{title}</h3>
          {hint && <p className="text-meta text-muted-foreground">{hint}</p>}
        </div>
        {value && <span className="text-lg font-semibold tabular-nums">{value}</span>}
      </div>
      {empty ? (
        <div className="flex h-44 items-center justify-center rounded-xl border border-dashed border-foreground/15 text-sm text-muted-foreground">
          Collecting samples…
        </div>
      ) : (
        <div className="text-foreground">
          <ResponsiveContainer width="100%" height={180}>
            {children}
          </ResponsiveContainer>
        </div>
      )}
    </Panel>
  );
}

/** Sparkline: a provider card's TTFT trend, no axes. */
export const Sparkline = memo(function Sparkline({ history }: { history: Schemas["NetworkBucket"][] }) {
  const data = history.slice(-60).map((b) => ({ t: b.t, v: b.ttft_p50_ms ?? b.handshake_ms ?? null }));
  if (!data.some((d) => d.v != null)) return <div className="h-10" aria-hidden />;
  return (
    <div className="h-10 text-foreground" aria-hidden>
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={data} margin={{ top: 4, right: 2, left: 2, bottom: 4 }}>
          <Line type="monotone" dataKey="v" stroke="var(--chart-1)" strokeWidth={1.75} dot={false} isAnimationActive={false} connectNulls />
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
});

const last = <T,>(xs: T[], pick: (x: T) => number | null | undefined) => {
  for (let i = xs.length - 1; i >= 0; i--) {
    const v = pick(xs[i]!);
    if (v != null) return v;
  }
  return undefined;
};

function minutes(points: { t: string }[]) {
  const first = points[0];
  const last = points[points.length - 1];
  if (!first || !last || first === last) return "";
  const m = Math.round((Date.parse(last.t) - Date.parse(first.t)) / 60000);
  return `last ${Math.max(m, 1)} min`;
}

const msTooltip = (value: unknown, name: unknown) => [fmtMs(value == null ? null : Number(value)), String(name)] as [string, string];

/** PathChart: handshake to the provider vs the gateway's internet baseline. */
export const PathChart = memo(function PathChart({ history, internet }: { history: Schemas["NetworkBucket"][]; internet: Schemas["NetworkPoint"][] }) {
  const inet = new Map(internet.map((p) => [p.t, p.ms]));
  const data = history.map((b) => ({ t: b.t, handshake: b.handshake_ms ?? null, internet: inet.get(b.t) ?? null }));
  const empty = !data.some((d) => d.handshake != null || d.internet != null);
  return (
    <ChartCard title="Path latency" hint={`Handshake vs our internet · ${minutes(history) || "live"}`} value={fmtMs(last(history, (b) => b.handshake_ms))} empty={empty}>
      <LineChart data={data} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
        <CartesianGrid {...grid} />
        <XAxis dataKey="t" tick={axis} tickLine={false} axisLine={false} interval="preserveStartEnd" tickFormatter={(t) => clock.format(new Date(t))} minTickGap={40} />
        <YAxis tick={axis} tickLine={false} axisLine={false} tickFormatter={(v) => fmtMs(Number(v))} width={52} />
        <Tooltip contentStyle={tooltipStyle} labelFormatter={(t) => clockSec.format(new Date(String(t)))} formatter={msTooltip} />
        <Legend wrapperStyle={legendStyle} iconType="plainline" />
        <Line name="Provider handshake" type="monotone" dataKey="handshake" stroke="var(--chart-1)" strokeWidth={2} dot={false} activeDot={{ r: 4 }} connectNulls={false} isAnimationActive={false} />
        <Line name="Our internet" type="monotone" dataKey="internet" stroke="var(--chart-2)" strokeWidth={1.75} strokeDasharray="4 3" dot={false} activeDot={{ r: 4 }} connectNulls={false} isAnimationActive={false} />
      </LineChart>
    </ChartCard>
  );
});

/** TTFTChart: upstream time to first byte, p50 and p95 per 10 s. */
export const TTFTChart = memo(function TTFTChart({ history }: { history: Schemas["NetworkBucket"][] }) {
  const data = history.map((b) => ({ t: b.t, p50: b.ttft_p50_ms ?? null, p95: b.ttft_p95_ms ?? null }));
  const empty = !data.some((d) => d.p50 != null);
  return (
    <ChartCard title="Time to first token" hint={`p50 and p95 · ${minutes(history) || "live"}`} value={fmtMs(last(history, (b) => b.ttft_p50_ms))} empty={empty}>
      <LineChart data={data} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
        <CartesianGrid {...grid} />
        <XAxis dataKey="t" tick={axis} tickLine={false} axisLine={false} interval="preserveStartEnd" tickFormatter={(t) => clock.format(new Date(t))} minTickGap={40} />
        <YAxis tick={axis} tickLine={false} axisLine={false} tickFormatter={(v) => fmtMs(Number(v))} width={52} />
        <Tooltip contentStyle={tooltipStyle} labelFormatter={(t) => clockSec.format(new Date(String(t)))} formatter={msTooltip} />
        <Legend wrapperStyle={legendStyle} iconType="plainline" />
        <Line name="p50" type="monotone" dataKey="p50" stroke="var(--chart-1)" strokeWidth={2} dot={false} activeDot={{ r: 4 }} isAnimationActive={false} />
        <Line name="p95" type="monotone" dataKey="p95" stroke="var(--chart-2)" strokeWidth={1.75} strokeDasharray="4 3" dot={false} activeDot={{ r: 4 }} isAnimationActive={false} />
      </LineChart>
    </ChartCard>
  );
});

/** TrafficChart: attempts per 10 s, failed ones stacked on top. */
export const TrafficChart = memo(function TrafficChart({ history }: { history: Schemas["NetworkBucket"][] }) {
  const data = history.map((b) => ({ t: b.t, ok: b.requests - b.errors, errors: b.errors, rejected: b.rejected }));
  const empty = !data.some((d) => d.ok + d.errors + d.rejected > 0);
  return (
    <ChartCard title="Traffic" hint="Attempts per 10 s" value={`${data.reduce((n, d) => n + d.ok + d.errors, 0)} total`} empty={empty}>
      <BarChart data={data} margin={{ top: 4, right: 8, left: 0, bottom: 0 }} barCategoryGap={1}>
        <CartesianGrid {...grid} vertical={false} />
        <XAxis dataKey="t" tick={axis} tickLine={false} axisLine={false} interval="preserveStartEnd" tickFormatter={(t) => clock.format(new Date(t))} minTickGap={40} />
        <YAxis tick={axis} tickLine={false} axisLine={false} allowDecimals={false} width={32} />
        <Tooltip contentStyle={tooltipStyle} labelFormatter={(t) => clockSec.format(new Date(String(t)))} cursor={{ fillOpacity: 0.06 }} />
        <Legend wrapperStyle={legendStyle} iconType="square" />
        <Bar name="Succeeded" dataKey="ok" stackId="a" fill="var(--chart-1)" isAnimationActive={false} />
        <Bar name="Failed" dataKey="errors" stackId="a" fill="var(--chart-4)" stroke="var(--chart-1)" strokeWidth={1} isAnimationActive={false} />
        <Bar name="No account free" dataKey="rejected" stackId="a" fill="var(--chart-3)" isAnimationActive={false} radius={[3, 3, 0, 0]} />
      </BarChart>
    </ChartCard>
  );
});
