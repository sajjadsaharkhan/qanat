/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/usage/components/TopModelsChart.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat change: each bar wears its model tier accent (design.md §3.3).
 */
"use client";

import { useMemo, useState } from "react";
import { Bar, BarChart, CartesianGrid, Cell, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import type { Schemas } from "@/lib/api/client";
import { modelTier, type ModelTier } from "@/lib/model-tier";

import { Card, fmtTokens, Segmented, tooltipStyle } from "./shared";

const tierColor: Record<ModelTier, string> = {
  fable: "var(--model-fable)",
  opus: "var(--model-opus)",
  sonnet: "var(--model-sonnet)",
  haiku: "var(--model-haiku)",
  gpt: "var(--model-gpt)",
  gemini: "var(--model-gemini)",
  other: "var(--muted-foreground)",
};

const truncate = (s: string, max = 22) => (s && s.length > max ? s.slice(0, max) + "…" : s || "");

type ViewMode = "tokens" | "requests";

export function TopModelsChart({ byModel }: { byModel: Schemas["UsageGroup"][] }) {
  const [viewMode, setViewMode] = useState<ViewMode>("tokens");

  const chartData = useMemo(() => {
    // byModel is per model and provider; merge by model name like 9router's rawModel.
    const merged = new Map<string, { name: string; full: string; tokens: number; requests: number }>();
    for (const d of byModel) {
      const m = merged.get(d.model) ?? { name: truncate(d.model || "Unknown"), full: d.model, tokens: 0, requests: 0 };
      m.tokens += d.prompt_tokens + d.completion_tokens;
      m.requests += d.requests;
      merged.set(d.model, m);
    }
    return [...merged.values()]
      .filter((d) => d[viewMode] > 0)
      .sort((a, b) => b[viewMode] - a[viewMode])
      .slice(0, 5);
  }, [byModel, viewMode]);

  const fmt = viewMode === "tokens" ? fmtTokens : String;
  const label = viewMode === "tokens" ? "Tokens" : "Requests";

  return (
    <Card className="flex min-w-0 flex-col gap-3 p-3 sm:p-4">
      <div className="flex items-center justify-between gap-2">
        <span className="text-sm font-semibold uppercase tracking-wide text-muted-foreground">Top Models</span>
        <Segmented
          label="Top models measure"
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
        <div className="flex h-44 items-center justify-center text-sm text-muted-foreground">No model usage yet</div>
      ) : (
        <ResponsiveContainer width="100%" height={180}>
          <BarChart data={chartData} layout="vertical" margin={{ top: 4, right: 40, left: 4, bottom: 4 }}>
            <CartesianGrid strokeDasharray="3 3" strokeOpacity={0.1} horizontal={false} />
            <XAxis type="number" tick={{ fontSize: 10, fill: "currentColor", fillOpacity: 0.5 }} tickLine={false} axisLine={false} tickFormatter={fmt} />
            <YAxis
              type="category"
              dataKey="name"
              tick={{ fontSize: 10, fill: "currentColor", fillOpacity: 0.7 }}
              tickLine={false}
              axisLine={false}
              width={124} // 9router uses 90; Qanat's model IDs are longer
            />
            <Tooltip contentStyle={tooltipStyle} cursor={{ fill: "var(--muted)", opacity: 0.5 }} formatter={(value) => [fmt(Number(value)), label]} />
            <Bar dataKey={viewMode} radius={[0, 4, 4, 0]}>
              {chartData.map((d) => (
                <Cell key={d.full} fill={tierColor[modelTier(d.full)]} />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      )}
    </Card>
  );
}
