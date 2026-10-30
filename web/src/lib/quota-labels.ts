import type { Schemas, WindowKind } from "@/lib/api/client";

export const windowLabel: Record<WindowKind, string> = {
  rolling_5h: "5-hour",
  daily: "Daily",
  weekly: "Weekly",
  monthly: "Monthly",
};

const windowSuffix: Record<WindowKind, string> = {
  rolling_5h: "5h",
  daily: "day",
  weekly: "week",
  monthly: "month",
};

/** "Daily", or "opus/day" for a per-model window (design.md §7.3). */
export function quotaLabel(w: Pick<Schemas["QuotaWindow"], "window" | "model_scope">): string {
  return w.model_scope ? `${w.model_scope}/${windowSuffix[w.window]}` : windowLabel[w.window];
}

export const weekdays = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
