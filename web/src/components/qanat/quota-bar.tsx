import { quotaLevel, type QuotaLevel } from "@/lib/format";
import { cn } from "@/lib/utils";

// design.md §5: ink below 80%, amber 80–95%, red from 95%.
const fill: Record<QuotaLevel, string> = {
  ok: "bg-primary",
  warning: "bg-warning",
  critical: "bg-destructive",
  exhausted: "bg-destructive",
};

/** A quota usage bar. The label names the window so status isn't color-only. */
export function QuotaBar({ used, limit, label, className }: { used: number; limit: number; label: string; className?: string }) {
  const level = quotaLevel(used, limit);
  const pct = limit > 0 ? Math.min(100, (used / limit) * 100) : 100;
  return (
    <div
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(pct)}
      className={cn("h-1.5 w-full overflow-hidden rounded-full bg-muted", className)}
    >
      <div
        className={cn("h-full rounded-full transition-[width] duration-[450ms] ease-[cubic-bezier(0.2,0.8,0.2,1)]", fill[level])}
        style={{ width: `${pct}%` }}
      />
    </div>
  );
}
