/*
 * A provider's pool at a glance: connections, errors, and how much
 * subscription quota is left across its accounts. After 9router's
 * ProviderCard (https://github.com/decolua/9router,
 * src/app/(dashboard)/dashboard/providers/page.js).
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat change: the card rolls up the accounts' provider limits (mean left
 * per window, accounts at their limit) and selects the provider's tab.
 */
"use client";

import { useMotionTokens, useShouldReduceMotion } from "@animate-ui/react/utils/motion";
import { SlidingNumber } from "@animate-ui/react/primitives/texts/sliding-number";
import { motion } from "motion/react";

import { poolSummary, type Account, type AccountLimits } from "@/components/qanat/accounts/limits";
import { Tag } from "@/components/qanat/page";
import { LeftBar, ResetIn } from "@/components/qanat/accounts/limit-bars";
import { cn } from "@/lib/utils";

export function accountCounts(accounts: Account[]) {
  const active = accounts.filter((a) => !a.disabled_at);
  const errored = active.filter((a) => a.state === "disconnected" || a.last_check_ok === false || !!a.last_refresh_error);
  return { total: accounts.length, active: active.length - errored.length, errored: errored.length, disabled: accounts.length - active.length };
}

function Dot({ tone }: { tone: "success" | "destructive" | "muted" }) {
  return (
    <span aria-hidden className={cn("size-1.5 rounded-full", tone === "success" ? "bg-success" : tone === "destructive" ? "bg-destructive" : "bg-muted-foreground/50")} />
  );
}

export function PoolSummaryCard({
  name,
  prefix,
  logo,
  accounts,
  byId,
  now,
  onSelect,
  footer,
}: {
  name: string;
  prefix: string;
  logo: string;
  accounts: Account[];
  byId: Map<string, { data?: AccountLimits }>;
  now: number;
  onSelect?: () => void;
  /** A row under the card's body, outside its click target (the routing control). */
  footer?: React.ReactNode;
}) {
  const reduce = useShouldReduceMotion();
  const { spring, scale } = useMotionTokens();
  const c = accountCounts(accounts);
  const s = poolSummary(accounts, byId);
  const Comp = onSelect ? motion.button : motion.div;

  return (
    <div className="flex h-full min-w-0 flex-col overflow-hidden rounded-[10px] border bg-card">
      <Comp
        type={onSelect ? "button" : undefined}
        onClick={onSelect}
        whileTap={onSelect && !reduce ? { scale: scale.choice } : undefined}
        transition={spring.press}
        className={cn(
          "flex min-w-0 flex-1 flex-col gap-3 p-3.5 text-start",
          onSelect && "outline-none transition-colors hover:bg-accent/40 focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:ring-inset",
        )}
      >
        <div className="flex min-w-0 items-center gap-2.5">
          <span aria-hidden className="grid size-9 shrink-0 place-items-center rounded-lg bg-brand-soft">
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img src={logo} alt="" className="max-h-6 max-w-6 rounded object-contain" />
          </span>
          <div className="min-w-0 flex-1">
            <p className="flex items-center gap-1.5 font-semibold">
              <span className="truncate">{name}</span>
              <Tag tone="brand">
                <code className="font-mono">{prefix}/</code>
              </Tag>
            </p>
            <p className="mt-0.5 flex flex-wrap items-center gap-x-2.5 gap-y-0.5 text-xs text-muted-foreground">
              {c.total === 0 ? (
                <span>No connections</span>
              ) : (
                <>
                  <span className="inline-flex items-center gap-1">
                    <Dot tone="success" />
                    <span className="inline-flex tabular-nums">
                      <SlidingNumber number={c.active} initiallyStable transition={spring.number} />
                    </span>{" "}
                    active
                  </span>
                  {c.errored > 0 && (
                    <span className="inline-flex items-center gap-1 text-destructive-text">
                      <Dot tone="destructive" />
                      {c.errored} error{c.errored === 1 ? "" : "s"}
                    </span>
                  )}
                  {c.disabled > 0 && (
                    <span className="inline-flex items-center gap-1">
                      <Dot tone="muted" />
                      {c.disabled} disabled
                    </span>
                  )}
                </>
              )}
            </p>
          </div>
        </div>

        {s.windows.length > 0 ? (
          <div className="space-y-2">
            {s.windows.slice(0, 2).map((w) => (
              <div key={w.key}>
                <div className="flex items-baseline justify-between gap-2 text-xs">
                  <span className="text-muted-foreground">{w.label} · pool</span>
                  <span className="font-medium tabular-nums">
                    {Math.round(w.left)}% <span className="font-normal text-muted-foreground">left</span>
                  </span>
                </div>
                <LeftBar used={100 - w.left} label={`${name} ${w.label}: ${Math.round(w.left)}% left on average`} className="mt-1" />
              </div>
            ))}
            <p className="flex flex-wrap justify-between gap-x-2 text-[11px] text-muted-foreground">
              <span>
                {s.exhausted > 0 ? (
                  <span className="text-destructive-text">
                    {s.exhausted} of {s.known} at limit
                  </span>
                ) : (
                  `${s.known} account${s.known === 1 ? "" : "s"} reporting`
                )}
              </span>
              {s.windows[0]?.reset && <ResetIn at={s.windows[0].reset} now={now} prefix="next reset in" />}
            </p>
          </div>
        ) : (
          <p className="text-xs text-muted-foreground">{c.total === 0 ? "Add an account to start pooling." : "No account reports subscription limits yet."}</p>
        )}
      </Comp>
      {footer && <div className="flex min-w-0 items-center justify-between gap-2 border-t px-3.5 py-2">{footer}</div>}
    </div>
  );
}
