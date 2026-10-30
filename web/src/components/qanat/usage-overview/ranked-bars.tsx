/*
 * A ranked list of bars: name, value and share, each bar scaled to the
 * leader. Replaces the provider and top-model bar charts on the usage pages
 * so long names read in full and every value is printed, not hovered.
 * Bars grow in on mount and glide when the data or measure changes.
 */
"use client";

import { spring, useShouldReduceMotion, useTempoTransition } from "@animate-ui/react/utils/motion";
import { motion } from "motion/react";
import type { ReactNode } from "react";

const share = new Intl.NumberFormat("en-US", {
  style: "percent",
  maximumFractionDigits: 0,
});

export type RankedItem = {
  key: string;
  label: ReactNode;
  title?: string;
  value: number;
  color: string;
};

export function RankedBars({ items, format, empty }: { items: RankedItem[]; format: (n: number) => string; empty: string }) {
  const reduce = useShouldReduceMotion();
  const grow = useTempoTransition(spring.progress);
  if (!items.length) return <div className="flex h-44 items-center justify-center text-sm text-muted-foreground">{empty}</div>;
  const top = Math.max(...items.map((i) => i.value)) || 1;
  const total = items.reduce((s, i) => s + i.value, 0) || 1;

  return (
    <ol className="flex flex-col gap-3.5">
      {items.map((it, i) => {
        // translateX, not width: the fill keeps its rounded end while it grows
        const offset = `${(it.value / top) * 100 - 100}%`;
        return (
          <li key={it.key} className="flex flex-col gap-1.5">
            <div className="flex items-baseline justify-between gap-3 text-sm">
              <span className="flex min-w-0 items-center gap-2 font-medium" title={it.title}>
                {it.label}
              </span>
              <span className="shrink-0 tabular-nums">
                <span className="font-semibold">{format(it.value)}</span>
                <span className="ms-2 inline-block w-9 text-end text-meta text-muted-foreground">{share.format(it.value / total)}</span>
              </span>
            </div>
            <div className="h-2 overflow-hidden rounded-full bg-muted" aria-hidden>
              <motion.div
                className="h-full w-full rounded-full"
                style={{ backgroundColor: it.color }}
                initial={{ x: reduce ? offset : "-100%" }}
                animate={{ x: offset }}
                transition={{ ...grow, delay: reduce ? 0 : i * 0.04 }}
              />
            </div>
          </li>
        );
      })}
    </ol>
  );
}
