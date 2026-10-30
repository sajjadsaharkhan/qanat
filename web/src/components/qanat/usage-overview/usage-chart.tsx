/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/usage/components/UsageChart.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat changes: data comes with the Overview payload; one primary series
 * color with a soft vertical fade (an owner-approved exception to design.md
 * §3.4 for this page), full-contrast axes.
 */
"use client";

import { useId, useState } from "react";
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import type { Schemas } from "@/lib/api/client";
import { formatUsd } from "@/lib/format";

import { axisTick, Card, CardHeading, fmtTokens, gridStroke, Segmented, tooltipLabelStyle, tooltipStyle } from "./shared";

type ViewMode = "tokens" | "requests" | "cost";

const fmtCost = (n: number) => formatUsd(n || 0);
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

const hourLabel = new Intl.DateTimeFormat("en-US", {
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
});
const dayLabel = new Intl.DateTimeFormat("en-US", {
  month: "short",
  day: "numeric",
  timeZone: "UTC",
});

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
  const fillId = `usage-fill-${useId().replace(/:/g, "")}`;

  return (
    <Card className="flex min-w-0 flex-col gap-4 p-4 sm:p-5">
      <CardHeading title="Activity" hint={granularity === "hour" ? "Per hour" : "Per day"}>
        <Segmented label="Chart measure" options={VIEW_MODES} value={viewMode} onChange={setViewMode} className="w-full sm:w-auto" />
      </CardHeading>
      {!hasData ? (
        <div className="flex h-[260px] items-center justify-center text-sm text-muted-foreground">No data for this period</div>
      ) : (
        <ResponsiveContainer width="100%" height={260}>
          <AreaChart data={data} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
            <defs>
              <linearGradient id={fillId} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="var(--chart-1)" stopOpacity={0.35} />
                <stop offset="100%" stopColor="var(--chart-1)" stopOpacity={0.02} />
              </linearGradient>
            </defs>
            <CartesianGrid stroke={gridStroke} strokeDasharray="3 3" vertical={false} />
            <XAxis dataKey="label" tick={axisTick} tickLine={false} axisLine={false} interval="preserveStartEnd" />
            <YAxis tick={axisTick} tickLine={false} axisLine={false} tickFormatter={cfg.formatter} width={50} />
            <Tooltip
              contentStyle={tooltipStyle}
              labelStyle={tooltipLabelStyle}
              cursor={{
                stroke: "var(--muted-foreground)",
                strokeDasharray: "3 3",
              }}
              formatter={(value) => [cfg.formatter(Number(value)), cfg.label]}
            />
            <Area
              type="monotone"
              dataKey={viewMode}
              stroke="var(--chart-1)"
              strokeWidth={2.5}
              fill={`url(#${fillId})`}
              dot={false}
              activeDot={{
                r: 5,
                strokeWidth: 2,
                stroke: "var(--card)",
                fill: "var(--chart-1)",
              }}
              animationDuration={700}
            />
          </AreaChart>
        </ResponsiveContainer>
      )}
    </Card>
  );
}
