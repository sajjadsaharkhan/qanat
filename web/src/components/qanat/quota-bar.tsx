"use client";

import { spring, useShouldReduceMotion, useTempoTransition } from "@animate-ui/react/utils/motion";
import { motion } from "motion/react";

import { quotaLevel, type QuotaLevel } from "@/lib/format";
import { cn } from "@/lib/utils";

// design.md §5: ink below 80%, amber 80–95%, red from 95%.
const fill: Record<QuotaLevel, string> = {
  ok: "bg-primary",
  warning: "bg-warning",
  critical: "bg-destructive",
  exhausted: "bg-destructive",
};

/**
 * A quota usage bar. The label names the window so status isn't color-only.
 * The fill grows in once and glides to new values; it moves by translateX so
 * its rounded end never stretches.
 */
export function QuotaBar({
  used,
  limit,
  label,
  size = "sm",
  className,
}: {
  used: number;
  limit: number;
  label: string;
  size?: "sm" | "md";
  className?: string;
}) {
  const reduce = useShouldReduceMotion();
  const glide = useTempoTransition(spring.progress);
  const level = quotaLevel(used, limit);
  const pct = limit > 0 ? Math.min(100, (used / limit) * 100) : 100;
  const offset = `${pct - 100}%`;
  return (
    <div
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(pct)}
      className={cn("w-full overflow-hidden rounded-full bg-muted", size === "md" ? "h-2.5" : "h-1.5", className)}
    >
      <motion.div
        className={cn("h-full w-full rounded-full transition-colors duration-200", fill[level])}
        initial={{ x: reduce ? offset : "-100%" }}
        animate={{ x: offset }}
        transition={glide}
      />
    </div>
  );
}
