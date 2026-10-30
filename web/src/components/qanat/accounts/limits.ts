"use client";

import { useQueries, useQueryClient } from "@tanstack/react-query";
import { useCallback, useMemo, useState } from "react";

import { api, fetchClient, type Schemas } from "@/lib/api/client";

export type Account = Schemas["Account"];
export type AccountLimits = Schemas["AccountLimits"];
export type LimitWindow = Schemas["LimitWindow"];

/** Mirrors providerlimits.Supported: subscription tokens publish limits, API keys don't. */
export function supportsLimits(a: Pick<Account, "provider" | "auth_kind" | "has_credentials">) {
  if (!a.has_credentials || a.auth_kind === "api_key") return false;
  if (a.provider === "antigravity") return a.auth_kind === "oauth";
  return a.provider === "claude" || a.provider === "codex";
}

/** The windows that gate the whole account (not one model). */
export function headlineWindows(l: AccountLimits | undefined): LimitWindow[] {
  if (!l) return [];
  const main = l.windows.filter((w) => w.group !== "model");
  return main.length > 0 ? main : l.windows.slice(0, 2);
}

/**
 * Headroom is the percent left in the account's tightest headline window,
 * or null when nothing is known. It sorts accounts and rolls up per provider.
 */
export function headroom(l: AccountLimits | undefined): number | null {
  const ws = headlineWindows(l);
  if (ws.length === 0) return null;
  return Math.max(0, 100 - Math.max(...ws.map((w) => w.used_percent)));
}

/** The soonest reset among windows that are actually spent (≥ 80% used). */
export function nextReset(l: AccountLimits | undefined): Date | null {
  let best: Date | null = null;
  for (const w of headlineWindows(l)) {
    if (!w.resets_at || w.used_percent < 80) continue;
    const d = new Date(w.resets_at);
    if (!best || d < best) best = d;
  }
  return best;
}

const limitsQuery = (id: string) =>
  api.queryOptions("get", "/api/admin/accounts/{id}/limits", { params: { path: { id } } });

/**
 * useAccountLimits loads every supported account's limits (the server caches
 * each read for 5 minutes) and refreshes on demand, skipping that cache.
 */
export function useAccountLimits(accounts: Account[]) {
  const qc = useQueryClient();
  const supported = useMemo(() => accounts.filter(supportsLimits), [accounts]);
  const results = useQueries({
    queries: supported.map((a) => ({
      ...limitsQuery(a.id),
      staleTime: 5 * 60_000,
      refetchInterval: 5 * 60_000,
      refetchIntervalInBackground: false,
    })),
  });

  const byId = new Map<string, { data?: AccountLimits; pending: boolean }>();
  supported.forEach((a, i) => byId.set(a.id, { data: results[i]?.data, pending: !!results[i]?.isPending }));

  const [refreshing, setRefreshing] = useState<Set<string>>(new Set());

  /** Asks the providers now (four accounts at a time) and stores the answers. */
  const refresh = useCallback(
    async (ids: string[]) => {
      const targets = ids.filter((id) => supported.some((a) => a.id === id));
      setRefreshing((s) => new Set([...s, ...targets]));
      const queue = [...targets];
      const worker = async () => {
        for (let id = queue.shift(); id; id = queue.shift()) {
          try {
            const { data } = await fetchClient.GET("/api/admin/accounts/{id}/limits", {
              params: { path: { id }, query: { force: "1" } },
            });
            if (data) qc.setQueryData(limitsQuery(id).queryKey, data);
          } finally {
            setRefreshing((s) => {
              const n = new Set(s);
              n.delete(id);
              return n;
            });
          }
        }
      };
      await Promise.all(Array.from({ length: Math.min(4, targets.length) }, worker));
    },
    [qc, supported],
  );

  return { byId, refresh, refreshing };
}

/** Per-provider roll-up: mean headroom per headline window, and who's out. */
export function poolSummary(accounts: Account[], byId: Map<string, { data?: AccountLimits }>) {
  const sums = new Map<string, { label: string; left: number; n: number; reset: Date | null }>();
  let known = 0;
  let exhausted = 0;
  for (const a of accounts) {
    if (a.disabled_at) continue;
    const l = byId.get(a.id)?.data;
    const ws = headlineWindows(l);
    if (ws.length === 0) continue;
    known++;
    if (l?.limit_reached || ws.some((w) => w.used_percent >= 100)) exhausted++;
    for (const w of ws) {
      const key = w.group === "weekly" ? "weekly" : w.group === "session" ? "session" : w.key;
      const s = sums.get(key) ?? { label: key === "weekly" ? "Weekly" : key === "session" ? "Session" : w.label, left: 0, n: 0, reset: null };
      s.left += 100 - w.used_percent;
      s.n++;
      if (w.resets_at) {
        const d = new Date(w.resets_at);
        if (!s.reset || d < s.reset) s.reset = d;
      }
      sums.set(key, s);
    }
  }
  const windows = [...sums.entries()]
    .sort(([a], [b]) => (a === "session" ? -1 : b === "session" ? 1 : a === "weekly" ? -1 : b === "weekly" ? 1 : 0))
    .map(([key, s]) => ({ key, label: s.label, left: s.left / s.n, reset: s.reset }));
  return { known, exhausted, windows };
}
