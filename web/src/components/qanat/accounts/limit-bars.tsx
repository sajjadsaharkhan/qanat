/*
 * An account's provider limits, after 9router's ProviderLimitCard and
 * QuotaProgressBar (https://github.com/decolua/9router,
 * src/app/(dashboard)/dashboard/usage/components/ProviderLimits).
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat change: bars fill with what's left and color by what's used
 * (design.md §5, 80/95%); reset grants are counted, not spent;
 * per-model windows fold under the headline ones.
 */
"use client";

import { SlidingNumber } from "@animate-ui/react/primitives/texts/sliding-number";
import { blur, spring, transition, useMotionTokens, useShouldReduceMotion, useTempoTransition } from "@animate-ui/react/utils/motion";
import { ChevronDown, CircleDashed, OctagonX, RefreshCw, TicketCheck, TriangleAlert } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { useEffect, useState } from "react";

import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/qanat/accounts/animated";
import { headlineWindows, type AccountLimits, type LimitWindow } from "@/components/qanat/accounts/limits";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { formatDuration, quotaLevel } from "@/lib/format";
import { cn } from "@/lib/utils";

/** useNow ticks every `ms` so countdowns stay current. */
export function useNow(ms = 30_000) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(t);
  }, [ms]);
  return now;
}

const leftText: Record<string, string> = {
  ok: "text-foreground",
  warning: "text-warning-text",
  critical: "text-destructive-text",
  exhausted: "text-destructive-text",
};

/** Percent rolls to its new value; screen readers read it once. */
function Percent({ value }: { value: number }) {
  const { spring } = useMotionTokens();
  const n = Math.round(value);
  return (
    <span className="inline-flex tabular-nums" aria-label={`${n}%`}>
      <SlidingNumber number={n} initiallyStable transition={spring.number} aria-hidden />
      <span aria-hidden>%</span>
    </span>
  );
}

export function ResetIn({ at, now, prefix = "resets in" }: { at: string | Date | null | undefined; now: number; prefix?: string }) {
  if (!at) return null;
  const ms = new Date(at).getTime() - now;
  if (ms <= 0) return <span>{prefix === "resets in" ? "resetting" : ""}</span>;
  return (
    <span data-volatile>
      {prefix} {formatDuration(ms)}
    </span>
  );
}

const leftFill: Record<string, string> = {
  ok: "bg-primary",
  warning: "bg-warning",
  critical: "bg-destructive",
  exhausted: "bg-destructive",
};

/**
 * LeftBar fills with what's left (like 9router's limit bars), colored by
 * what's used per design.md §5. The fill grows in once and glides by
 * translateX, so its rounded end never stretches.
 */
export function LeftBar({ used, label, className }: { used: number; label: string; className?: string }) {
  const reduce = useShouldReduceMotion();
  const glide = useTempoTransition(spring.progress);
  const left = Math.max(0, Math.min(100, 100 - used));
  const offset = `${left - 100}%`;
  return (
    <div
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(left)}
      className={cn("h-1.5 w-full overflow-hidden rounded-full bg-muted", className)}
    >
      <motion.div
        className={cn("h-full w-full rounded-full transition-colors duration-200", leftFill[quotaLevel(used, 100)])}
        initial={{ x: reduce ? offset : "-100%" }}
        animate={{ x: offset }}
        transition={glide}
      />
    </div>
  );
}

const resetFormat = new Intl.DateTimeFormat(undefined, {
  weekday: "short",
  month: "short",
  day: "numeric",
  hour: "numeric",
  minute: "2-digit",
});

/** "Resets at Sat, Nov 1, 3:00 AM (in 2h 17m)", or why there's no time. */
export function resetAtText(at: string | null | undefined, now: number) {
  if (!at) return "The provider reported no reset time for this limit.";
  const d = new Date(at);
  const ms = d.getTime() - now;
  return ms > 0 ? `Resets at ${resetFormat.format(d)} (in ${formatDuration(ms)})` : `Reset at ${resetFormat.format(d)}`;
}

/**
 * One window: label and what's left on top, the bar, then when it resets.
 * Hovering or focusing it shows the exact reset time.
 */
export function LimitBar({ w, now, dense = false }: { w: LimitWindow; now: number; dense?: boolean }) {
  const level = quotaLevel(w.used_percent, 100);
  const left = Math.max(0, 100 - w.used_percent);
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <div tabIndex={0} className="min-w-0 rounded-sm outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50">
          <div className="flex items-baseline justify-between gap-2 text-xs">
            <span className="truncate text-muted-foreground">{w.label}</span>
            <span className={cn("inline-flex shrink-0 items-center gap-1 font-medium", leftText[level])}>
              {level === "exhausted" && <OctagonX aria-hidden className="size-3" />}
              <Percent value={left} />
              <span className="font-normal text-muted-foreground">left</span>
            </span>
          </div>
          <LeftBar used={w.used_percent} label={`${w.label}: ${Math.round(left)}% left`} className="mt-1" />
          {!dense && w.resets_at && (
            <p className="mt-0.5 text-[11px] text-muted-foreground">
              <ResetIn at={w.resets_at} now={now} />
            </p>
          )}
        </div>
      </TooltipTrigger>
      <TooltipContent>
        <span className="font-medium">{w.label}</span> · {Math.round(w.used_percent)}% used
        <br />
        <span data-volatile>{resetAtText(w.resets_at, now)}</span>
      </TooltipContent>
    </Tooltip>
  );
}

/** A refresh button whose icon spins while the provider is asked. */
export function RefreshLimitsButton({ busy, onClick, label }: { busy: boolean; onClick: () => void; label: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button variant="ghost" size="icon" className="size-7" aria-label={label} onClick={onClick} disabled={busy}>
          <RefreshCw className={cn("size-3.5", busy && "animate-spin motion-reduce:animate-none")} />
        </Button>
      </TooltipTrigger>
      <TooltipContent>Ask the provider now</TooltipContent>
    </Tooltip>
  );
}

/** Swap cross-fades one state of the limits block into the next. */
function Swap({ id, children }: { id: string; children: React.ReactNode }) {
  const swap = useTempoTransition(transition.swap);
  return (
    <AnimatePresence mode="popLayout" initial={false}>
      <motion.div
        key={id}
        initial={{ opacity: 0, filter: blur.icon }}
        animate={{ opacity: 1, filter: "blur(0px)", transition: swap }}
        exit={{ opacity: 0, filter: blur.icon, transition: swap }}
      >
        {children}
      </motion.div>
    </AnimatePresence>
  );
}

export function LimitsMessage({ tone, children }: { tone: "muted" | "warning"; children: React.ReactNode }) {
  const Icon = tone === "warning" ? TriangleAlert : CircleDashed;
  return (
    <p className={cn("flex items-start gap-1.5 text-xs", tone === "warning" ? "text-warning-text" : "text-muted-foreground")}>
      <Icon aria-hidden className="mt-px size-3.5 shrink-0" />
      <span className="min-w-0">{children}</span>
    </p>
  );
}

/**
 * AccountLimitsView shows an account's windows. `compact` keeps to the
 * headline windows in one line of bars (list rows); otherwise model windows
 * fold under a disclosure (cards).
 */
export function AccountLimitsView({
  supported,
  limits,
  pending,
  compact = false,
  now,
}: {
  supported: boolean;
  limits: AccountLimits | undefined;
  pending: boolean;
  compact?: boolean;
  now: number;
}) {
  const [open, setOpen] = useState(false);
  if (!supported) {
    return <LimitsMessage tone="muted">API keys don&apos;t report subscription limits.</LimitsMessage>;
  }
  const headline = headlineWindows(limits);
  const models = limits?.windows.filter((w) => !headline.includes(w)) ?? [];
  const state = pending && !limits ? "pending" : headline.length === 0 ? "empty" : "data";

  return (
    <div aria-live="polite" className="min-w-0">
      <Swap id={state}>
        {state === "pending" ? (
          <div className={cn("grid gap-3", compact ? "grid-cols-2" : "grid-cols-1")}>
            {[0, 1].map((i) => (
              <div key={i} className="space-y-1.5">
                <div className="h-3 w-2/3 animate-pulse rounded bg-muted motion-reduce:animate-none" />
                <div className="h-1.5 animate-pulse rounded-full bg-muted motion-reduce:animate-none" />
              </div>
            ))}
          </div>
        ) : state === "empty" ? (
          <LimitsMessage tone={limits?.error ? "warning" : "muted"}>{limits?.error || "The provider reported no limit windows."}</LimitsMessage>
        ) : (
          <div className="space-y-2">
            <div className={cn("grid gap-x-4 gap-y-2.5", compact ? "grid-cols-2" : "grid-cols-1")}>
              {(compact ? headline.slice(0, 2) : headline).map((w) => (
                <LimitBar key={w.key} w={w} now={now} dense={compact} />
              ))}
            </div>
            {!compact && models.length > 0 && (
              <Collapsible open={open} onOpenChange={setOpen}>
                <CollapsibleTrigger className="group inline-flex items-center gap-1 rounded text-xs text-muted-foreground outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50">
                  <ChevronDown aria-hidden className="size-3.5 transition-transform duration-200 group-data-[state=open]:rotate-180 motion-reduce:transition-none" />
                  {models.length} model limit{models.length === 1 ? "" : "s"}
                </CollapsibleTrigger>
                <CollapsibleContent>
                  <div className="grid gap-2.5 pt-2.5">
                    {models.map((w) => (
                      <LimitBar key={w.key} w={w} now={now} />
                    ))}
                  </div>
                </CollapsibleContent>
              </Collapsible>
            )}
            {limits?.stale && limits.error && <LimitsMessage tone="warning">{limits.error}</LimitsMessage>}
          </div>
        )}
      </Swap>
    </div>
  );
}

/** "Plan · 2 resets · updated 3m ago" under a card's bars. */
export function LimitsMeta({ limits, now }: { limits: AccountLimits | undefined; now: number }) {
  if (!limits?.fetched_at && !limits?.plan) return null;
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[11px] text-muted-foreground">
      {limits.plan && <span className="font-medium text-foreground/80 capitalize">{limits.plan}</span>}
      {limits.reset_credits != null && limits.reset_credits > 0 && (
        <span className="inline-flex items-center gap-1" title="Limit-reset grants available on this account (spend them from the provider's own app)">
          <TicketCheck aria-hidden className="size-3" />
          {limits.reset_credits} reset{limits.reset_credits === 1 ? "" : "s"}
        </span>
      )}
      {limits.fetched_at && <span data-volatile>updated {formatDuration(now - new Date(limits.fetched_at).getTime())} ago</span>}
    </div>
  );
}
