/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/shared/components/UsageStats.js and src/app/(dashboard)/dashboard/usage/page.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat changes: TypeScript; data from the Admin API (one payload, polled
 * every 5 s instead of an SSE stream); scoped to the caller, everyone, or one
 * person; Qanat's theme tokens and Tag badges.
 */
"use client";

import { ReducedMotionConfig } from "@animate-ui/react/utils/motion";
import { CircleX } from "lucide-react";
import dynamic from "next/dynamic";
import { useEffect, useMemo, useState } from "react";

import { Tag } from "@/components/qanat/page";
import { Skeleton } from "@/components/ui/skeleton";
import { useSearchParam } from "@/hooks/use-search-param";
import { api, type Schemas } from "@/lib/api/client";
import { modelTier, tierDotClass } from "@/lib/model-tier";
import { cn } from "@/lib/utils";

import { OverviewCards } from "./overview-cards";
import { Reveal } from "./motion";
import { Card, CardHeading, fmtTokens, providerName, Segmented } from "./shared";
import { summarize, UsageBreakdown, type Group, type Row, type ViewMode } from "./usage-table";

// Lazy-load: keeps @xyflow/react and recharts out of the initial bundle
const ProviderTopology = dynamic(() => import("./provider-topology").then((m) => m.ProviderTopology), { ssr: false });
const UsageChart = dynamic(() => import("./usage-chart").then((m) => m.UsageChart), { ssr: false });
const ProviderBarChart = dynamic(() => import("./provider-bar-chart").then((m) => m.ProviderBarChart), { ssr: false });
const TopModelsChart = dynamic(() => import("./top-models-chart").then((m) => m.TopModelsChart), { ssr: false });

type Overview = Schemas["UsageOverview"];
type UsageGroup = Schemas["UsageGroup"];
export type Period = "today" | "24h" | "7d" | "30d" | "60d" | "all";

export const PERIODS: { value: Period; label: string }[] = [
  { value: "today", label: "Today" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7D" },
  { value: "30d", label: "30D" },
  { value: "60d", label: "60D" },
  { value: "all", label: "All" },
];

/** usePeriod keeps the selected period in the URL (`?period=7d`). */
export function usePeriod() {
  return useSearchParam<Period>("period", "today", PERIOD_VALUES);
}
const PERIOD_VALUES: Period[] = ["today", "24h", "7d", "30d", "60d", "all"];

/** The period selector, as 9router places it on the page's top row. */
export function PeriodSelector({ period, onChange }: { period: Period; onChange: (p: Period) => void }) {
  return <Segmented label="Period" options={PERIODS} value={period} onChange={onChange} className="w-full sm:w-auto" />;
}

function timeAgo(timestamp: string) {
  const diff = Math.floor((Date.now() - new Date(timestamp).getTime()) / 1000);
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  return `${Math.floor(diff / 86400)}d ago`;
}

// Auto-update time display every second without re-rendering parent
function TimeAgo({ timestamp }: { timestamp: string }) {
  const [, setTick] = useState(0);
  useEffect(() => {
    const timer = setInterval(() => setTick((t) => t + 1), 1000);
    return () => clearInterval(timer);
  }, []);
  return <>{timeAgo(timestamp)}</>;
}

function RecentRequests({ requests }: { requests: Schemas["RecentUsage"][] }) {
  return (
    <Card className="flex min-w-0 flex-col gap-3 overflow-hidden p-4 sm:p-5 lg:h-[480px]">
      <CardHeading title="Recent requests" hint={requests.length ? `Latest ${requests.length}` : undefined} />
      {!requests.length ? (
        <div className="flex flex-1 items-center justify-center py-10 text-sm text-muted-foreground">No requests yet.</div>
      ) : (
        <div className="-mx-4 max-h-[420px] flex-1 overflow-y-auto sm:-mx-5 lg:max-h-none">
          <table className="w-full min-w-[300px] border-collapse text-sm">
            <thead className="sticky top-0 z-10 bg-card">
              <tr className="border-b text-meta text-muted-foreground">
                <th className="py-2 ps-4 text-left font-medium sm:ps-5">Model</th>
                <th className="whitespace-nowrap py-2 text-right font-medium">In / out</th>
                <th className="py-2 pe-4 text-right font-medium sm:pe-5">When</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border/60">
              {requests.map((r, i) => {
                const ok = r.status === "ok";
                return (
                  <tr key={`${r.timestamp}-${i}`} className="transition-colors duration-200 hover:bg-muted/60">
                    <td className="max-w-[160px] py-2 ps-4 sm:ps-5" title={r.model}>
                      <span className="flex items-center gap-2">
                        {ok ? (
                          <span aria-hidden className={cn("size-2 shrink-0 rounded-full", tierDotClass(modelTier(r.model)))} />
                        ) : (
                          <CircleX className="size-3.5 shrink-0 text-destructive-text" aria-label="Failed" />
                        )}
                        <span className={cn("truncate font-mono text-xs", !ok && "text-destructive-text")}>{r.model}</span>
                      </span>
                    </td>
                    <td className="whitespace-nowrap py-2 text-right tabular-nums">
                      {fmtTokens(r.prompt_tokens)} <span className="text-muted-foreground">/ {fmtTokens(r.completion_tokens)}</span>
                    </td>
                    <td className="whitespace-nowrap py-2 pe-4 text-right text-muted-foreground sm:pe-5">
                      <TimeAgo timestamp={r.timestamp} />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

type Item = Row & {
  rawModel: string;
  provider: string;
  accountName?: string;
  keyName?: string;
  keyPrefix?: string;
  userEmail?: string;
  endpoint?: string;
};

function toItems(groups: UsageGroup[]): Item[] {
  return groups.map((d, i) => {
    const totalTokens = d.prompt_tokens + d.completion_tokens;
    const totalCost = d.cost || 0;
    // Cost split is a token-share allocation of the server total, not a
    // per-rate recompute (as in 9router). Cached is a subset of prompt.
    const nonCachedInput = Math.max(0, d.prompt_tokens - d.cached_tokens);
    const share = totalTokens > 0 ? totalCost / totalTokens : 0;
    return {
      key: `${i}|${d.model}|${d.provider}|${d.account_id ?? ""}|${d.key_id ?? ""}|${d.endpoint ?? ""}`,
      rawModel: d.model,
      provider: providerName(d.provider),
      accountName: d.account_name,
      keyName: d.key_name,
      keyPrefix: d.key_prefix,
      userEmail: d.user_email,
      endpoint: d.endpoint,
      requests: d.requests,
      promptTokens: d.prompt_tokens,
      completionTokens: d.completion_tokens,
      cachedTokens: d.cached_tokens,
      totalTokens,
      cost: totalCost,
      totalCost,
      inputCost: nonCachedInput * share,
      cachedCost: d.cached_tokens * share,
      outputCost: d.completion_tokens * share,
      lastUsed: d.last_used,
    };
  });
}

type KeyField = "rawModel" | "accountName" | "keyName" | "userEmail" | "endpoint";

function getGroupKey(item: Item, keyField: KeyField) {
  switch (keyField) {
    case "rawModel":
      return item.rawModel || "Unknown Model";
    case "accountName":
      return item.accountName || "Unknown Account";
    case "keyName": {
      const name = item.keyName || "Unknown Key";
      const label = item.keyPrefix ? `${name} (${item.keyPrefix}…)` : name;
      return item.userEmail ? `${label} · ${item.userEmail}` : label;
    }
    case "userEmail":
      return item.userEmail || "Unknown user";
    case "endpoint":
      return item.endpoint || "Unknown Endpoint";
  }
}

function groupDataByKey(data: Item[], keyField: KeyField): Group<Item>[] {
  const groups = new Map<string, Item[]>();
  for (const item of data) {
    const gk = getGroupKey(item, keyField);
    groups.set(gk, [...(groups.get(gk) ?? []), item]);
  }
  return [...groups].map(([groupKey, items]) => ({ groupKey, items, summary: summarize(items) }));
}

type TableView = "model" | "account" | "apiKey" | "user" | "endpoint";
const USER_VIEWS: TableView[] = ["model", "account", "user", "endpoint"];
const KEY_VIEWS: TableView[] = ["model", "account", "apiKey", "endpoint"];
// Everyone's usage breaks down by person; one person's (or your own) by key.
const tableOptions = (byUser: boolean): { value: TableView; label: string }[] => [
  { value: "model", label: "Model" },
  { value: "account", label: "Account" },
  byUser ? { value: "user", label: "User" } : { value: "apiKey", label: "API key" },
  { value: "endpoint", label: "Endpoint" },
];

export type Scope = { kind: "me" } | { kind: "admin"; userId?: string };

function useOverview(scope: Scope, period: Period) {
  const tz = useMemo(() => Intl.DateTimeFormat().resolvedOptions().timeZone, []);
  const opts = {
    refetchInterval: 5_000,
    placeholderData: (prev: Overview | undefined) => prev,
  };
  const me = api.useQuery("get", "/api/me/usage/overview", { params: { query: { period, tz } } }, { ...opts, enabled: scope.kind === "me" });
  const admin = api.useQuery(
    "get",
    "/api/admin/usage/overview",
    {
      params: {
        query: {
          period,
          tz,
          user_id: scope.kind === "admin" ? scope.userId : undefined,
        },
      },
    },
    { ...opts, enabled: scope.kind === "admin" },
  );
  return scope.kind === "me" ? me : admin;
}

export function UsageStats({ scope, period }: { scope: Scope; period: Period }) {
  const byUser = scope.kind === "admin" && !scope.userId;
  // The grouping and values live in the URL beside sortBy/sortOrder.
  const [tableView, setTableView] = useSearchParam<TableView>("group", "model", byUser ? USER_VIEWS : KEY_VIEWS);
  const [viewMode, setViewMode] = useSearchParam<ViewMode>("values", "costs", ["costs", "tokens"]);
  const query = useOverview(scope, period);
  const stats = query.data;
  const loading = query.isPending;
  const fetching = query.isFetching && !loading && query.isPlaceholderData;

  const active = useMemo(() => {
    if (!stats) return null;
    const model = (i: Item) => `${i.rawModel} · ${i.provider}`;
    const tierDot = (name: string) => <span aria-hidden className={cn("size-2 shrink-0 rounded-full", tierDotClass(modelTier(name)))} />;
    switch (tableView) {
      case "model":
        return {
          noun: ["model", "models"] as [string, string],
          groups: groupDataByKey(toItems(stats.by_model), "rawModel"),
          storageKey: "usage-stats:expanded-models",
          emptyMessage: "No usage recorded yet.",
          groupDecor: tierDot,
          detail: (i: Item) => ({ primary: <Tag>{i.provider}</Tag> }),
          searchText: (i: Item) => model(i),
        };
      case "account":
        return {
          noun: ["account", "accounts"] as [string, string],
          groups: groupDataByKey(toItems(stats.by_account), "accountName"),
          storageKey: "usage-stats:expanded-accounts",
          emptyMessage: "No account-specific usage recorded yet.",
          detail: (i: Item) => ({ primary: i.rawModel, secondary: i.provider }),
          searchText: (i: Item) => `${model(i)} ${i.accountName ?? ""}`,
        };
      case "apiKey":
        return {
          noun: ["API key", "API keys"] as [string, string],
          groups: groupDataByKey(toItems(stats.by_api_key), "keyName"),
          storageKey: "usage-stats:expanded-apikeys",
          emptyMessage: "No API key usage recorded yet.",
          detail: (i: Item) => ({ primary: i.rawModel, secondary: i.provider }),
          searchText: (i: Item) => `${model(i)} ${i.keyName ?? ""} ${i.keyPrefix ?? ""}`,
        };
      case "user":
        // Every usage row lands in by_api_key with its owner's email, so grouping it by email covers all usage.
        return {
          noun: ["user", "users"] as [string, string],
          groups: groupDataByKey(toItems(stats.by_api_key), "userEmail"),
          storageKey: "usage-stats:expanded-users",
          emptyMessage: "No usage recorded yet.",
          detail: (i: Item) => ({
            primary: (
              <>
                <span className="font-medium">{i.keyName}</span>
                {i.keyPrefix && <span className="ms-2 font-mono text-xs text-muted-foreground">{i.keyPrefix.slice(0, 12)}…</span>}
              </>
            ),
            secondary: model(i),
          }),
          searchText: (i: Item) => `${model(i)} ${i.keyName ?? ""} ${i.userEmail ?? ""}`,
        };
      case "endpoint":
        return {
          noun: ["endpoint", "endpoints"] as [string, string],
          groups: groupDataByKey(toItems(stats.by_endpoint), "endpoint"),
          storageKey: "usage-stats:expanded-endpoints",
          emptyMessage: "No endpoint usage recorded yet.",
          detail: (i: Item) => ({ primary: i.rawModel, secondary: i.provider }),
          searchText: (i: Item) => `${model(i)} ${i.endpoint ?? ""}`,
        };
    }
  }, [stats, tableView]);

  if (!stats && !loading) return <div className="text-muted-foreground">Failed to load usage statistics.</div>;

  if (loading || !stats || !active) {
    return (
      <div className="flex min-w-0 flex-col gap-4" aria-busy>
        <span className="sr-only">Loading usage</span>
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
          {Array.from({ length: 5 }, (_, i) => (
            <Skeleton key={i} className="h-[118px] rounded-[14px]" />
          ))}
        </div>
        <Skeleton className="h-[320px] rounded-[14px]" />
        <Skeleton className="h-[340px] rounded-[14px]" />
      </div>
    );
  }

  return (
    <ReducedMotionConfig>
      <div className="flex min-w-0 flex-col gap-4" aria-busy={fetching}>
        <OverviewCards stats={stats} />

        <Reveal index={5}>
          <UsageChart chart={stats.chart} granularity={stats.granularity} />
        </Reveal>

        <Reveal index={6} className="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-2">
          <ProviderBarChart byProvider={stats.by_provider} />
          <TopModelsChart byModel={stats.by_model} />
        </Reveal>

        <Reveal index={7} className="grid min-w-0 grid-cols-1 items-stretch gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(300px,1fr)]">
          <ProviderTopology
            providers={stats.providers}
            activeCount={stats.active_count}
            lastProvider={stats.last_provider}
            errorProvider={stats.error_provider}
          />
          <RecentRequests requests={stats.recent_requests} />
        </Reveal>

        <Reveal index={8}>
          <UsageBreakdown
            key={active.storageKey}
            noun={active.noun}
            groups={active.groups}
            detail={active.detail}
            searchText={active.searchText}
            groupDecor={active.groupDecor}
            storageKey={active.storageKey}
            emptyMessage={active.emptyMessage}
            viewMode={viewMode}
            onViewMode={setViewMode}
            groupBy={
              <Segmented label="Group the table by" options={tableOptions(byUser)} value={tableView} onChange={setTableView} className="w-full sm:w-auto" />
            }
          />
        </Reveal>
      </div>
    </ReducedMotionConfig>
  );
}
