/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/usage/components/UsageChart.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat changes: data comes with the Overview payload; one primary series
 * color with a flat fill (design.md §3.4: no gradients).
 */
"use client";

import { useState } from "react";
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import type { Schemas } from "@/lib/api/client";

import { Card, fmtTokens, Segmented, tooltipStyle } from "./shared";

type ViewMode = "tokens" | "requests" | "cost";

const fmtCost = (n: number) => `$${(n || 0).toFixed(4)}`;
const fmtRequests = (n: number) => String(n || 0);

const VIEW_MODES: { value: ViewMode; label: string }[] = [
  { value: "tokens", label: "Tokens" },
  { value: "requests", label: "Requests" },
  { value: "cost", label: "Cost" },
];

const VIEW_CONFIG: Record<ViewMode, { formatter: (n: number) => string; label: string }> = {
  tokens: { formatter: fmtTokens, label: "Tokens" },
  requests: { formatter: fmtRequests, label: "Requests" },
  cost: { formatter: fmtCost, label: "Cost" },
};

const hourLabel = new Intl.DateTimeFormat("en-US", { hour: "2-digit", minute: "2-digit", hour12: false });
const dayLabel = new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", timeZone: "UTC" });

export function UsageChart({ chart, granularity }: { chart: Schemas["ChartBucket"][]; granularity: Schemas["UsageOverview"]["granularity"] }) {
  const [viewMode, setViewMode] = useState<ViewMode>("tokens");
  const cfg = VIEW_CONFIG[viewMode];
  const data = chart.map((b) => ({
    label: (granularity === "hour" ? hourLabel : dayLabel).format(new Date(b.start)),
    tokens: b.tokens,
    requests: b.requests,
    cost: b.cost,
  }));
  const hasData = data.some((d) => (d[viewMode] || 0) > 0);

  return (
    <Card className="flex min-w-0 flex-col gap-3 p-3 sm:p-4">
      <Segmented label="Chart measure" options={VIEW_MODES} value={viewMode} onChange={setViewMode} className="w-full sm:w-auto sm:self-start" />
      {!hasData ? (
        <div className="flex h-48 items-center justify-center text-sm text-muted-foreground">No data for this period</div>
      ) : (
        <ResponsiveContainer width="100%" height={220}>
          <AreaChart data={data} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
            <CartesianGrid strokeDasharray="3 3" strokeOpacity={0.1} />
            <XAxis
              dataKey="label"
              tick={{ fontSize: 10, fill: "currentColor", fillOpacity: 0.5 }}
              tickLine={false}
              axisLine={false}
              interval="preserveStartEnd"
            />
            <YAxis
              tick={{ fontSize: 10, fill: "currentColor", fillOpacity: 0.5 }}
              tickLine={false}
              axisLine={false}
              tickFormatter={cfg.formatter}
              width={50}
            />
            <Tooltip contentStyle={tooltipStyle} formatter={(value) => [cfg.formatter(Number(value)), cfg.label]} />
            <Area
              type="monotone"
              dataKey={viewMode}
              stroke="var(--chart-1)"
              strokeWidth={2}
              fill="var(--chart-1)"
              fillOpacity={0.12}
              dot={false}
              activeDot={{ r: 4 }}
            />
          </AreaChart>
        </ResponsiveContainer>
      )}
    </Card>
  );
}
