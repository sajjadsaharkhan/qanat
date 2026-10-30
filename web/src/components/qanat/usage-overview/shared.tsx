/*
 * Ported from 9router (https://github.com/decolua/9router): the formatters and
 * segmented buttons used across src/app/(dashboard)/dashboard/usage.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 */
"use client";

import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export const fmt = (n?: number) => new Intl.NumberFormat().format(n || 0);
export const fmtCost = (n?: number) => `$${(n || 0).toFixed(2)}`;

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

/** 9router's segmented control: a bordered strip of buttons, the active one filled. */
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
  return (
    <div
      role="group"
      aria-label={label}
      className={cn("grid items-center gap-1 rounded-lg border bg-muted p-1 sm:flex", className)}
      style={{ gridTemplateColumns: `repeat(${options.length}, minmax(0, 1fr))` }}
    >
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          aria-pressed={value === o.value}
          disabled={disabled}
          onClick={() => onChange(o.value)}
          className={cn(
            "rounded-md font-medium transition-colors",
            size === "xs" ? "px-2.5 py-0.5 text-xs" : "px-3 py-1 text-sm",
            value === o.value ? "bg-primary text-primary-foreground shadow-sm" : "text-muted-foreground hover:bg-background hover:text-foreground",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

/** Tooltip box style shared by the recharts charts. */
export const tooltipStyle = {
  backgroundColor: "var(--popover)",
  color: "var(--popover-foreground)",
  border: "1px solid var(--border)",
  borderRadius: "8px",
  fontSize: "12px",
};

export const providerNames: Record<string, string> = { claude: "Claude", codex: "Codex", antigravity: "Antigravity" };
export const providerName = (id: string) => providerNames[id] ?? id;
