"use client";

import { useEffect, useState } from "react";

import { QuotaBar } from "@/components/qanat/quota-bar";
import type { Schemas } from "@/lib/api/client";
import { formatDuration, formatUsd } from "@/lib/format";
import { quotaLabel } from "@/lib/quota-labels";

type QuotaWindow = Schemas["QuotaWindow"];

/** Ticks once a minute so countdowns stay current without re-fetching. */
export function useNow(intervalMs = 60_000) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}

export function resetText(w: QuotaWindow, now: number) {
  if (!w.resets_at) return "rolling";
  return `in ${formatDuration(new Date(w.resets_at).getTime() - now)}`;
}

/** One row per quota window: label, bar, used of limit, reset countdown. */
export function QuotaList({ windows, now }: { windows: QuotaWindow[]; now: number }) {
  return (
    <ul className="flex flex-col gap-3">
      {windows.map((w) => (
        <li key={`${w.window}/${w.model_scope ?? ""}`} className="grid grid-cols-[6rem_1fr] items-center gap-x-4 gap-y-1 sm:grid-cols-[7rem_1fr_9rem_7rem]">
          <span>{quotaLabel(w)}</span>
          <QuotaBar used={w.used_usd} limit={w.limit_usd} label={`${quotaLabel(w)} quota used`} />
          <span className="text-end tabular-nums text-muted-foreground max-sm:col-start-2 max-sm:text-start">
            {formatUsd(w.used_usd)} of {formatUsd(w.limit_usd)}
          </span>
          <span className="text-end tabular-nums text-muted-foreground max-sm:hidden" data-volatile>
            {resetText(w, now)}
          </span>
        </li>
      ))}
    </ul>
  );
}
