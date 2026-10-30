/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/usage/components/OverviewCards.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat changes: left-aligned KPI tiles with an icon, a compact headline that
 * counts up, and the exact figure or a ratio underneath; cost is the filled tile.
 */
"use client";

import { ArrowDownToLine, ArrowUpFromLine, Coins, DatabaseZap, Send, type LucideIcon } from "lucide-react";

import type { Schemas } from "@/lib/api/client";
import { cn } from "@/lib/utils";

import { CountUp, Reveal } from "./motion";
import { Card, fmt, fmtCost, fmtTokens } from "./shared";

const pct = new Intl.NumberFormat("en-US", {
  style: "percent",
  maximumFractionDigits: 0,
});
const whole = (n: number) => fmt(Math.round(n));

type Tile = {
  label: string;
  icon: LucideIcon;
  value: number;
  format: (n: number) => string;
  meta: string;
  title?: string;
};

export function OverviewCards({ stats }: { stats: Schemas["UsageOverview"] }) {
  const req = stats.total_requests;
  const perReq = (n: number) => (req ? `${fmtTokens(Math.round(n / req))} per request` : "No requests yet");
  const tiles: Tile[] = [
    {
      label: "Requests",
      icon: Send,
      value: req,
      format: whole,
      meta: stats.active_count ? `${stats.active_count} streaming now` : "None streaming now",
    },
    {
      label: "Input tokens",
      icon: ArrowDownToLine,
      value: stats.total_prompt_tokens,
      format: fmtTokens,
      meta: perReq(stats.total_prompt_tokens),
      title: fmt(stats.total_prompt_tokens),
    },
    {
      label: "Cached tokens",
      icon: DatabaseZap,
      value: stats.total_cached_tokens,
      format: fmtTokens,
      meta: stats.total_prompt_tokens ? `${pct.format(stats.total_cached_tokens / stats.total_prompt_tokens)} of input` : "No input yet",
      title: fmt(stats.total_cached_tokens),
    },
    {
      label: "Output tokens",
      icon: ArrowUpFromLine,
      value: stats.total_completion_tokens,
      format: fmtTokens,
      meta: perReq(stats.total_completion_tokens),
      title: fmt(stats.total_completion_tokens),
    },
  ];

  return (
    <div className="grid min-w-0 grid-cols-2 gap-3 lg:grid-cols-5">
      {tiles.map((t, i) => (
        <Reveal key={t.label} index={i} className="min-w-0">
          <KpiTile {...t} />
        </Reveal>
      ))}
      <Reveal index={tiles.length} className="min-w-0 col-span-2 lg:col-span-1">
        <KpiTile filled label="Est. cost" icon={Coins} value={stats.total_cost} format={(n) => `~${fmtCost(n)}`} meta="Confirmed prices, not a bill" />
      </Reveal>
    </div>
  );
}

function KpiTile({ label, icon: Icon, value, format, meta, title, filled }: Tile & { filled?: boolean }) {
  return (
    <Card className={cn("flex h-full min-w-0 flex-col gap-3 p-4", filled && "border-transparent bg-primary text-primary-foreground")}>
      <div className="flex items-center gap-2">
        <span
          aria-hidden
          className={cn("grid size-7 shrink-0 place-items-center rounded-md", filled ? "bg-primary-foreground/15" : "bg-brand-soft text-brand-text")}
        >
          <Icon className="size-4" />
        </span>
        <span className={cn("truncate text-sm font-medium", filled ? "text-primary-foreground" : "text-foreground")}>{label}</span>
      </div>
      <div className="min-w-0">
        <CountUp value={value} format={format} className="block truncate text-2xl font-semibold sm:text-[28px] sm:leading-8 tracking-tight" />
        <p
          className={cn("mt-1 truncate text-meta", filled ? "text-primary-foreground/85" : "text-muted-foreground")}
          title={title ? `${title} exactly · ${meta}` : meta}
        >
          {meta}
        </p>
      </div>
    </Card>
  );
}
