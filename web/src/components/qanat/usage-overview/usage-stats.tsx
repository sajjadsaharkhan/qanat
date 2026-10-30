/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/shared/components/UsageStats.js and src/app/(dashboard)/dashboard/usage/page.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat changes: TypeScript; data from the Admin API (one payload, polled
 * every 5 s instead of an SSE stream); scoped to the caller, everyone, or one
 * person; Qanat's theme tokens and Tag badges.
 */
"use client";

import { Loader2 } from "lucide-react";
import dynamic from "next/dynamic";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useCallback, useEffect, useMemo, useState } from "react";

import { Tag } from "@/components/qanat/page";
import { api, type Schemas } from "@/lib/api/client";
import { cn } from "@/lib/utils";

import { OverviewCards } from "./overview-cards";
import { Card, fmt, fmtTime, providerName, Segmented } from "./shared";
import { UsageTable, type Column, type Group, type Row } from "./usage-table";

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
    <Card className="flex min-w-0 flex-col overflow-hidden p-4" style={{ height: 480 }}>
      <div className="shrink-0 border-b px-1 py-2">
        <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Recent Requests</span>
      </div>
      {!requests.length ? (
        <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">No requests yet.</div>
      ) : (
        <div className="flex-1 overflow-y-auto">
          <table className="w-full min-w-[300px] border-collapse text-xs">
            <thead className="sticky top-0 z-10 bg-card">
              <tr className="border-b">
                <th className="w-2 py-1.5 text-left font-semibold text-muted-foreground">
                  <span className="sr-only">Status</span>
                </th>
                <th className="py-1.5 text-left font-semibold text-muted-foreground">Model</th>
                <th className="whitespace-nowrap py-1.5 text-right font-semibold text-muted-foreground">In / Out</th>
                <th className="py-1.5 text-right font-semibold text-muted-foreground">When</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border/50">
              {requests.map((r, i) => {
                const ok = r.status === "ok";
                return (
                  <tr key={i} className="transition-colors hover:bg-muted">
                    <td className="py-1.5">
                      <span
                        role="img"
                        aria-label={ok ? "Succeeded" : "Failed"}
                        className={cn("block size-1.5 rounded-full", ok ? "bg-primary" : "bg-destructive")}
                      />
                    </td>
                    <td className="max-w-[120px] truncate py-1.5 font-mono" title={r.model}>
                      {r.model}
                    </td>
                    <td className="whitespace-nowrap py-1.5 text-right tabular-nums">
                      {fmt(r.prompt_tokens)}↑ <span className="text-muted-foreground">{fmt(r.completion_tokens)}↓</span>
                    </td>
                    <td className="whitespace-nowrap py-1.5 text-right text-muted-foreground">
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

type Item = Row & { rawModel: string; provider: string; accountName?: string; keyName?: string; keyPrefix?: string; userEmail?: string; endpoint?: string };

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

function sortData(items: Item[], sortBy: string, sortOrder: string) {
  return [...items].sort((a, b) => {
    let valA = (a as Record<string, unknown>)[sortBy] as string | number | null;
    let valB = (b as Record<string, unknown>)[sortBy] as string | number | null;
    if (typeof valA === "string") valA = valA.toLowerCase();
    if (typeof valB === "string") valB = valB.toLowerCase();
    if ((valA ?? "") < (valB ?? "")) return sortOrder === "asc" ? -1 : 1;
    if ((valA ?? "") > (valB ?? "")) return sortOrder === "asc" ? 1 : -1;
    return 0;
  });
}

type KeyField = "rawModel" | "accountName" | "keyName" | "endpoint";

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
    case "endpoint":
      return item.endpoint || "Unknown Endpoint";
  }
}

function groupDataByKey(data: Item[], keyField: KeyField): Group<Item>[] {
  const groups: Record<string, Group<Item>> = {};
  for (const item of data) {
    const gk = getGroupKey(item, keyField);
    if (!groups[gk]) {
      groups[gk] = {
        groupKey: gk,
        summary: { requests: 0, promptTokens: 0, completionTokens: 0, cachedTokens: 0, totalTokens: 0, cost: 0, totalCost: 0, inputCost: 0, cachedCost: 0, outputCost: 0, lastUsed: null },
        items: [],
      };
    }
    const s = groups[gk].summary;
    s.requests += item.requests;
    s.promptTokens += item.promptTokens;
    s.completionTokens += item.completionTokens;
    s.cachedTokens += item.cachedTokens;
    s.totalTokens += item.totalTokens;
    s.cost += item.cost;
    s.totalCost += item.totalCost;
    s.inputCost += item.inputCost;
    s.cachedCost += item.cachedCost;
    s.outputCost += item.outputCost;
    if (item.lastUsed && (!s.lastUsed || new Date(item.lastUsed) > new Date(s.lastUsed))) s.lastUsed = item.lastUsed;
    groups[gk].items.push(item);
  }
  return Object.values(groups);
}

const MODEL_COLUMNS: Column[] = [
  { field: "rawModel", label: "Model" },
  { field: "provider", label: "Provider" },
  { field: "requests", label: "Requests", align: "right" },
  { field: "lastUsed", label: "Last Used", align: "right" },
];
// 9router lists Model, Provider, Account here but renders account first in
// detail rows; the header follows the rows so columns line up.
const ACCOUNT_COLUMNS: Column[] = [
  { field: "accountName", label: "Account" },
  { field: "rawModel", label: "Model" },
  { field: "provider", label: "Provider" },
  { field: "requests", label: "Requests", align: "right" },
  { field: "lastUsed", label: "Last Used", align: "right" },
];
const API_KEY_COLUMNS: Column[] = [
  { field: "keyName", label: "API Key Name" },
  { field: "rawModel", label: "Model" },
  { field: "provider", label: "Provider" },
  { field: "requests", label: "Requests", align: "right" },
  { field: "lastUsed", label: "Last Used", align: "right" },
];
const ENDPOINT_COLUMNS: Column[] = [
  { field: "endpoint", label: "Endpoint" },
  { field: "rawModel", label: "Model" },
  { field: "provider", label: "Provider" },
  { field: "requests", label: "Requests", align: "right" },
  { field: "lastUsed", label: "Last Used", align: "right" },
];

type TableView = "model" | "account" | "apiKey" | "endpoint";
const TABLE_OPTIONS: { value: TableView; label: string }[] = [
  { value: "model", label: "Usage by Model" },
  { value: "account", label: "Usage by Account" },
  { value: "apiKey", label: "Usage by API Key" },
  { value: "endpoint", label: "Usage by Endpoint" },
];

const dash = <td className="px-6 py-3 text-muted-foreground">—</td>;
const counts = (requests: number, lastUsed: string | null) => (
  <>
    <td className="px-6 py-3 text-right tabular-nums">{fmt(requests)}</td>
    <td className="whitespace-nowrap px-6 py-3 text-right text-muted-foreground">{fmtTime(lastUsed)}</td>
  </>
);

export type Scope = { kind: "me" } | { kind: "admin"; userId?: string };

function useOverview(scope: Scope, period: Period) {
  const tz = useMemo(() => Intl.DateTimeFormat().resolvedOptions().timeZone, []);
  const opts = { refetchInterval: 5_000, placeholderData: (prev: Overview | undefined) => prev };
  const me = api.useQuery("get", "/api/me/usage/overview", { params: { query: { period, tz } } }, { ...opts, enabled: scope.kind === "me" });
  const admin = api.useQuery(
    "get",
    "/api/admin/usage/overview",
    { params: { query: { period, tz, user_id: scope.kind === "admin" ? scope.userId : undefined } } },
    { ...opts, enabled: scope.kind === "admin" },
  );
  return scope.kind === "me" ? me : admin;
}

export function UsageStats({ scope, period }: { scope: Scope; period: Period }) {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();

  const sortBy = searchParams.get("sortBy") || "rawModel";
  const sortOrder = searchParams.get("sortOrder") || "asc";

  const [tableView, setTableView] = useState<TableView>("model");
  const [viewMode, setViewMode] = useState<"costs" | "tokens">("costs");
  const query = useOverview(scope, period);
  const stats = query.data;
  const loading = query.isPending;
  const fetching = query.isFetching && !loading && query.isPlaceholderData;

  const toggleSort = useCallback(
    (field: string) => {
      const params = new URLSearchParams(searchParams.toString());
      if (params.get("sortBy") === field) {
        params.set("sortOrder", params.get("sortOrder") === "asc" ? "desc" : "asc");
      } else {
        params.set("sortBy", field);
        params.set("sortOrder", "asc");
      }
      router.replace(`${pathname}?${params.toString()}`, { scroll: false });
    },
    [searchParams, router, pathname],
  );

  const active = useMemo(() => {
    if (!stats) return null;
    const badge = (provider: string) => <Tag>{provider}</Tag>;
    switch (tableView) {
      case "model":
        return {
          columns: MODEL_COLUMNS,
          groupedData: groupDataByKey(sortData(toItems(stats.by_model), sortBy, sortOrder), "rawModel"),
          storageKey: "usage-stats:expanded-models",
          emptyMessage: "No usage recorded yet.",
          renderSummaryCells: (g: Group<Item>) => (
            <>
              {dash}
              {counts(g.summary.requests, g.summary.lastUsed)}
            </>
          ),
          renderDetailCells: (item: Item) => (
            <>
              <td className="px-6 py-3 font-medium">{item.rawModel}</td>
              <td className="px-6 py-3">{badge(item.provider)}</td>
              {counts(item.requests, item.lastUsed)}
            </>
          ),
        };
      case "account":
        return {
          columns: ACCOUNT_COLUMNS,
          groupedData: groupDataByKey(sortData(toItems(stats.by_account), sortBy, sortOrder), "accountName"),
          storageKey: "usage-stats:expanded-accounts",
          emptyMessage: "No account-specific usage recorded yet.",
          renderSummaryCells: (g: Group<Item>) => (
            <>
              {dash}
              {dash}
              {counts(g.summary.requests, g.summary.lastUsed)}
            </>
          ),
          renderDetailCells: (item: Item) => (
            <>
              <td className="px-6 py-3 font-medium">{item.accountName}</td>
              <td className="px-6 py-3 font-medium">{item.rawModel}</td>
              <td className="px-6 py-3">{badge(item.provider)}</td>
              {counts(item.requests, item.lastUsed)}
            </>
          ),
        };
      case "apiKey":
        return {
          columns: API_KEY_COLUMNS,
          groupedData: groupDataByKey(sortData(toItems(stats.by_api_key), sortBy, sortOrder), "keyName"),
          storageKey: "usage-stats:expanded-apikeys",
          emptyMessage: "No API key usage recorded yet.",
          renderSummaryCells: (g: Group<Item>) => (
            <>
              {dash}
              {dash}
              {counts(g.summary.requests, g.summary.lastUsed)}
            </>
          ),
          renderDetailCells: (item: Item) => (
            <>
              <td className="px-6 py-3 font-mono text-xs font-medium">{item.keyPrefix ? `${item.keyPrefix}…` : item.keyName}</td>
              <td className="px-6 py-3">{item.rawModel}</td>
              <td className="px-6 py-3">{badge(item.provider)}</td>
              {counts(item.requests, item.lastUsed)}
            </>
          ),
        };
      case "endpoint":
        return {
          columns: ENDPOINT_COLUMNS,
          groupedData: groupDataByKey(sortData(toItems(stats.by_endpoint), sortBy, sortOrder), "endpoint"),
          storageKey: "usage-stats:expanded-endpoints",
          emptyMessage: "No endpoint usage recorded yet.",
          renderSummaryCells: (g: Group<Item>) => (
            <>
              {dash}
              {dash}
              {counts(g.summary.requests, g.summary.lastUsed)}
            </>
          ),
          renderDetailCells: (item: Item) => (
            <>
              <td className="px-6 py-3 font-mono text-sm font-medium">{item.endpoint}</td>
              <td className="px-6 py-3">{item.rawModel}</td>
              <td className="px-6 py-3">{badge(item.provider)}</td>
              {counts(item.requests, item.lastUsed)}
            </>
          ),
        };
    }
  }, [stats, tableView, sortBy, sortOrder]);

  if (!stats && !loading) return <div className="text-muted-foreground">Failed to load usage statistics.</div>;

  const spinner = (
    <div className="flex items-center justify-center py-12 text-muted-foreground">
      <Loader2 className="size-8 animate-spin" aria-label="Loading" />
    </div>
  );

  return (
    <div className="flex min-w-0 flex-col gap-6" aria-busy={fetching}>
      {/* Overview cards */}
      {loading || !stats ? spinner : <OverviewCards stats={stats} />}

      {/* Provider topology + Recent Requests */}
      {loading || !stats ? (
        spinner
      ) : (
        <div className="grid min-w-0 grid-cols-1 items-stretch gap-2 lg:grid-cols-[minmax(0,2fr)_minmax(280px,1fr)]">
          <ProviderTopology
            providers={stats.providers}
            activeCount={stats.active_count}
            lastProvider={stats.last_provider}
            errorProvider={stats.error_provider}
          />
          <RecentRequests requests={stats.recent_requests} />
        </div>
      )}

      {/* Token / Cost chart - sync period */}
      {loading || !stats ? spinner : <UsageChart chart={stats.chart} granularity={stats.granularity} />}

      {/* Provider and model breakdown charts */}
      {stats && (
        <div className="grid min-w-0 grid-cols-1 gap-2 lg:grid-cols-2">
          <ProviderBarChart byProvider={stats.by_provider} />
          <TopModelsChart byModel={stats.by_model} />
        </div>
      )}

      {/* Table with dropdown selector */}
      <div className="flex flex-col gap-3">
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
          <select
            aria-label="Table"
            value={tableView}
            onChange={(e) => setTableView(e.target.value as TableView)}
            className="w-full rounded-lg border bg-card px-3 py-1.5 text-sm font-medium focus:outline-none focus:ring-2 focus:ring-ring/50 sm:w-auto"
          >
            {TABLE_OPTIONS.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
          <Segmented
            label="Table values"
            options={[
              { value: "costs", label: "Costs" },
              { value: "tokens", label: "Tokens" },
            ]}
            value={viewMode}
            onChange={setViewMode}
            className="grid-cols-2"
          />
        </div>
        {loading || !active ? (
          spinner
        ) : (
          <UsageTable
            key={active.storageKey}
            title=""
            columns={active.columns}
            groupedData={active.groupedData}
            sortBy={sortBy}
            sortOrder={sortOrder}
            onToggleSort={toggleSort}
            viewMode={viewMode}
            storageKey={active.storageKey}
            renderSummaryCells={active.renderSummaryCells}
            renderDetailCells={active.renderDetailCells}
            emptyMessage={active.emptyMessage}
          />
        )}
      </div>
    </div>
  );
}
