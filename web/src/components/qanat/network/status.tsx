"use client";

import { CircleCheck, CircleDashed, OctagonX, TriangleAlert, Unplug, type LucideIcon } from "lucide-react";

import type { Schemas } from "@/lib/api/client";
import type { Tone } from "@/lib/network";
import { cn } from "@/lib/utils";

import { Swap } from "./motion";

/** Each state has its own shape, so it reads without color (design.md §5). */
const icons: Record<Schemas["NetworkState"], LucideIcon> = {
  healthy: CircleCheck,
  degraded: TriangleAlert,
  unstable: OctagonX,
  unreachable: OctagonX,
  offline: OctagonX,
  unavailable: Unplug,
  checking: CircleDashed,
  no_accounts: CircleDashed,
};

export const toneText: Record<Tone, string> = {
  ok: "text-foreground",
  warning: "text-warning-text",
  destructive: "text-destructive-text",
  neutral: "text-muted-foreground",
};

/** Pills and tiles: tinted only for warning and broken states (design.md §3). */
const tonePill: Record<Tone, string> = {
  ok: "bg-brand-soft text-brand-text ring-brand-text/20",
  warning: "bg-warning/15 text-warning-text ring-warning/40",
  destructive: "bg-destructive/12 text-destructive-text ring-destructive/40",
  neutral: "bg-muted text-muted-foreground ring-foreground/10",
};

/** toneEdge: a card's top edge, colored for the states that need attention. */
export const toneEdge: Record<Tone, string> = {
  ok: "before:bg-transparent",
  warning: "before:bg-warning",
  destructive: "before:bg-destructive",
  neutral: "before:bg-transparent",
};

export function StatusIcon({ state, className }: { state: Schemas["NetworkState"]; className?: string }) {
  const Icon = icons[state];
  return <Icon aria-hidden className={cn("size-4 shrink-0", className)} />;
}

/** StatusLabel: icon + words, tinted only for warning and broken states. */
export function StatusLabel({ state, tone, children }: { state: Schemas["NetworkState"]; tone: Tone; children: React.ReactNode }) {
  return (
    <span className={cn("inline-flex items-center gap-1.5 font-semibold", toneText[tone])}>
      <StatusIcon state={state} />
      {children}
    </span>
  );
}

/** StatusPill: the verdict as a pill; it cross-fades when the state changes. */
export function StatusPill({ state, tone, children }: { state: Schemas["NetworkState"]; tone: Tone; children: React.ReactNode }) {
  return (
    <Swap id={`${state}-${String(children)}`}>
      <span className={cn("inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-sm font-semibold ring-1 ring-inset", tonePill[tone])}>
        <StatusIcon state={state} className="size-3.5" />
        {children}
      </span>
    </Swap>
  );
}

/** StatusTile: the hero's large icon square (Tower's verdict tile). */
export function StatusTile({ state, tone }: { state: Schemas["NetworkState"]; tone: Tone }) {
  const Icon = icons[state];
  return (
    <Swap id={state}>
      <span aria-hidden className={cn("grid size-14 shrink-0 place-items-center rounded-2xl ring-1 ring-inset", tonePill[tone])}>
        <Icon className="size-7" />
      </span>
    </Swap>
  );
}
