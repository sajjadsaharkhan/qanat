/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/usage/components/ProviderBarChart.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat change: bars use the forest ramp (design.md §3.4) instead of a rainbow.
 */
"use client";

import { useMemo, useState } from "react";
import { Bar, BarChart, CartesianGrid, Cell, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import type { Schemas } from "@/lib/api/client";

import { Card, fmtTokens, providerName, Segmented, tooltipStyle } from "./shared";

const COLORS = ["var(--chart-1)", "var(--chart-2)", "var(--chart-3)", "var(--chart-4)", "var(--chart-5)"];

type ViewMode = "tokens" | "requests";

export function ProviderBarChart({ byProvider }: { byProvider: Schemas["UsageGroup"][] }) {
  const [viewMode, setViewMode] = useState<ViewMode>("tokens");

  const chartData = useMemo(
    () =>
      byProvider
        .map((d) => ({ name: providerName(d.provider), tokens: d.prompt_tokens + d.completion_tokens, requests: d.requests }))
        .filter((d) => d[viewMode] > 0)
        .sort((a, b) => b[viewMode] - a[viewMode]),
    [byProvider, viewMode],
  );

  const fmt = viewMode === "tokens" ? fmtTokens : String;
  const label = viewMode === "tokens" ? "Tokens" : "Requests";

  return (
    <Card className="flex min-w-0 flex-col gap-3 p-3 sm:p-4">
      <div className="flex items-center justify-between gap-2">
        <span className="text-sm font-semibold uppercase tracking-wide text-muted-foreground">By Provider</span>
        <Segmented
          label="Provider chart measure"
          size="xs"
          options={[
            { value: "tokens", label: "Tokens" },
            { value: "requests", label: "Requests" },
          ]}
          value={viewMode}
          onChange={setViewMode}
          className="grid-cols-2"
        />
      </div>
      {!chartData.length ? (
        <div className="flex h-44 items-center justify-center text-sm text-muted-foreground">No provider usage yet</div>
      ) : (
        <ResponsiveContainer width="100%" height={180}>
          <BarChart data={chartData} margin={{ top: 4, right: 8, left: 0, bottom: 4 }}>
            <CartesianGrid strokeDasharray="3 3" strokeOpacity={0.1} vertical={false} />
            <XAxis
              dataKey="name"
              tick={{ fontSize: 10, fill: "currentColor", fillOpacity: 0.6 }}
              tickLine={false}
              axisLine={false}
              interval={0}
              tickFormatter={(v: string) => (v.length > 10 ? v.slice(0, 10) + "…" : v)}
            />
            <YAxis tick={{ fontSize: 10, fill: "currentColor", fillOpacity: 0.5 }} tickLine={false} axisLine={false} tickFormatter={fmt} width={44} />
            <Tooltip contentStyle={tooltipStyle} cursor={{ fill: "var(--muted)", opacity: 0.5 }} formatter={(value) => [fmt(Number(value)), label]} />
            <Bar dataKey={viewMode} radius={[4, 4, 0, 0]}>
              {chartData.map((_, i) => (
                <Cell key={i} fill={COLORS[i % COLORS.length]} />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      )}
    </Card>
  );
}
