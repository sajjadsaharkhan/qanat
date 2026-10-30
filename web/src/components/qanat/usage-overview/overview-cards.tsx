/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/usage/components/OverviewCards.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 */
"use client";

import type { Schemas } from "@/lib/api/client";

import { Card, fmt, fmtCost } from "./shared";

export function OverviewCards({ stats }: { stats: Schemas["UsageOverview"] }) {
  const cards = [
    { label: "Total Requests", value: fmt(stats.total_requests) },
    { label: "Total Input Tokens", value: fmt(stats.total_prompt_tokens) },
    { label: "Cached Tokens", value: fmt(stats.total_cached_tokens) },
    { label: "Output Tokens", value: fmt(stats.total_completion_tokens) },
  ];
  return (
    <div className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2 sm:gap-4 md:grid-cols-3 lg:grid-cols-5">
      {cards.map((c) => (
        <Card key={c.label} className="flex min-w-0 flex-col items-center gap-1 px-3 py-3 text-center sm:px-4">
          <span className="text-xs font-semibold uppercase text-muted-foreground sm:text-sm">{c.label}</span>
          <span className="w-full truncate text-lg font-bold tabular-nums xl:text-xl" title={c.value}>
            {c.value}
          </span>
        </Card>
      ))}
      <Card className="flex min-w-0 flex-col items-center gap-1 px-3 py-3 text-center sm:px-4">
        <span className="text-xs font-semibold uppercase text-muted-foreground sm:text-sm">Est. Cost</span>
        <span className="w-full truncate text-lg font-bold tabular-nums xl:text-xl" title={`~${fmtCost(stats.total_cost)}`}>
          ~{fmtCost(stats.total_cost)}
        </span>
        <span className="text-[10px] text-muted-foreground">From confirmed prices, not provider billing</span>
      </Card>
    </div>
  );
}
