/*
 * Ported from 9router (https://github.com/decolua/9router): the formatters and
 * segmented buttons used across src/app/(dashboard)/dashboard/usage.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat changes: the segmented control's active surface glides (Animate UI);
 * chart axes and tooltips use full-strength tokens for contrast.
 */
"use client";

import { spring, useTempoTransition } from "@animate-ui/react/utils/motion";
import { motion } from "motion/react";
import { useId, type ReactNode } from "react";

import { formatUsd } from "@/lib/format";
import { cn } from "@/lib/utils";

export const fmt = (n?: number) => new Intl.NumberFormat().format(n || 0);
export const fmtCost = (n?: number) => formatUsd(n || 0);

export const fmtTokens = (n: number) => {
  if (n >= 1000000) return `${(n / 1000000).toFixed(1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}K`;
  return String(n || 0);
};

export function fmtTime(iso?: string | null) {
  if (!iso) return "Never";
  const diffMins = Math.floor((Date.now() - new Date(iso).getTime()) / 60000);
  if (diffMins < 1) return "Just now";
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffMins < 1440) return `${Math.floor(diffMins / 60)}h ago`;
  return new Date(iso).toLocaleDateString();
}

/** 9router's card: surface, subtle border, 14px radius. */
export function Card({ className, children, style }: { className?: string; children: ReactNode; style?: React.CSSProperties }) {
  return (
    <div className={cn("rounded-[14px] border bg-card text-card-foreground", className)} style={style}>
      {children}
    </div>
  );
}

/** 9router's segmented control: a bordered strip of buttons; the active
 * surface glides between options on Animate UI's selection spring. */
export function Segmented<T extends string>({
  options,
  value,
  onChange,
  size = "md",
  disabled,
  className,
  label,
}: {
  options: { value: T; label: string }[];
  value: T;
  onChange: (v: T) => void;
  size?: "sm" | "md" | "xs";
  disabled?: boolean;
  className?: string;
  label: string;
}) {
  const id = useId();
  const glide = useTempoTransition(spring.selection);
  return (
    <div
      role="group"
      aria-label={label}
      className={cn("grid items-center gap-1 rounded-lg border bg-muted p-1 sm:flex", className)}
      style={{
        gridTemplateColumns: `repeat(${options.length}, minmax(0, 1fr))`,
      }}
    >
      {options.map((o) => {
        const on = value === o.value;
        return (
          <button
            key={o.value}
            type="button"
            aria-pressed={on}
            disabled={disabled}
            onClick={() => onChange(o.value)}
            className={cn(
              "relative rounded-md font-medium transition-colors duration-200", // transition.color
              size === "xs" ? "px-2.5 py-0.5 text-xs" : "px-3 py-1 text-sm",
              on ? "text-primary-foreground" : "text-muted-foreground [@media(hover:hover)_and_(pointer:fine)]:hover:text-foreground",
            )}
          >
            {on && <motion.span layoutId={`segmented-${id}`} aria-hidden className="absolute inset-0 rounded-md bg-primary shadow-sm" transition={glide} />}
            <span className="relative z-10">{o.label}</span>
          </button>
        );
      })}
    </div>
  );
}

/** Card heading: a sentence-case title and an optional one-line hint. */
export function CardHeading({ title, hint, children }: { title: string; hint?: string; children?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-2">
      <div className="min-w-0">
        <h3 className="text-sm font-semibold text-foreground">{title}</h3>
        {hint && <p className="text-meta text-muted-foreground">{hint}</p>}
      </div>
      {children}
    </div>
  );
}

/** Axis ticks at full muted-foreground strength (≥ 4.5:1), not a faded currentColor. */
export const axisTick = { fontSize: 11, fill: "var(--muted-foreground)" };
export const gridStroke = "var(--border)";

/** Tooltip box style shared by the recharts charts. */
export const tooltipStyle = {
  backgroundColor: "var(--popover)",
  color: "var(--popover-foreground)",
  border: "1px solid var(--border)",
  borderRadius: "10px",
  boxShadow: "var(--shadow-overlay)",
  fontSize: "12px",
  padding: "8px 10px",
};

export const tooltipLabelStyle = {
  color: "var(--muted-foreground)",
  fontWeight: 500,
  marginBottom: 2,
};

export const providerNames: Record<string, string> = {
  claude: "Claude",
  codex: "Codex",
  antigravity: "Antigravity",
};
export const providerName = (id: string) => providerNames[id] ?? id;
