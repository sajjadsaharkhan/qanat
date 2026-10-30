"use client";

import { ChartColumnStacked, TriangleAlert } from "lucide-react";
import Link from "next/link";
import { Suspense, useMemo, useState } from "react";

import { PageHeader, SectionHeader, Tag } from "@/components/qanat/page";
import { QuotaBar } from "@/components/qanat/quota-bar";
import { PeriodSelector, UsageStats, type Period } from "@/components/qanat/usage-overview/usage-stats";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, errorMessage, type Schemas } from "@/lib/api/client";
import { formatUsd, quotaLevel } from "@/lib/format";
import { quotaLabel } from "@/lib/quota-labels";

type UserUsage = Schemas["UserUsage"];

const dayFmt = new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", timeZone: "UTC" });
const pct = new Intl.NumberFormat("en-US", { style: "percent", maximumFractionDigits: 0 });

/** The window closest to its limit, for the table's quota column. */
function tightestWindow(u: UserUsage) {
  return [...u.quotas].sort((a, b) => b.used_usd / (b.limit_usd || 1) - a.used_usd / (a.limit_usd || 1))[0];
}

function QuotaCell({ u }: { u: UserUsage }) {
  const w = tightestWindow(u);
  if (!w) return <span className="text-muted-foreground">Zero quota</span>;
  const level = quotaLevel(w.used_usd, w.limit_usd);
  return (
    <div className="flex min-w-48 flex-col gap-1">
      <QuotaBar used={w.used_usd} limit={w.limit_usd} label={`${u.email} ${quotaLabel(w)} quota used`} />
      <span className="flex items-center gap-1 text-meta tabular-nums text-muted-foreground">
        {level !== "ok" && <TriangleAlert className="size-3.5 text-warning-text" aria-label="Near or over the limit" />}
        {quotaLabel(w)} {formatUsd(w.used_usd)} of {formatUsd(w.limit_usd)} · {pct.format(w.limit_usd ? w.used_usd / w.limit_usd : 1)}
      </span>
    </div>
  );
}

type Filter = "all" | "near" | "active";

export default function AdminUsagePage() {
  const [period, setPeriod] = useState<Period>("today");
  const [filter, setFilter] = useState<Filter>("all");
  // The quota table's spend column is a fixed 30 days; the Overview below follows the period.
  const usage = api.useQuery("get", "/api/admin/usage", { params: { query: { days: 30 } } }, { refetchInterval: 15_000 });

  const users = useMemo(() => {
    const all = usage.data?.users ?? [];
    if (filter === "near") return all.filter((u) => u.tightest != null && u.tightest >= 0.8);
    if (filter === "active") return all.filter((u) => u.request_count > 0);
    return all;
  }, [usage.data, filter]);
  const nearCount = usage.data?.users.filter((u) => u.tightest != null && u.tightest >= 0.8).length ?? 0;

  return (
    <>
      <PageHeader
        title="Usage & quotas"
        icon={ChartColumnStacked}
        description="How close each person is to their Plan's limits, and everyone's usage. Select a person for their own view."
        actions={<PeriodSelector period={period} onChange={setPeriod} />}
      />
      {usage.isPending ? (
        <Skeleton className="h-96 w-full" />
      ) : usage.isError ? (
        <p role="alert" className="text-destructive-text">
          {errorMessage(usage.error)}
        </p>
      ) : (
        <>
          <SectionHeader
            actions={
              <div role="group" aria-label="Show" className="inline-flex rounded-md border p-0.5">
                {(
                  [
                    ["all", "Everyone"],
                    ["active", "Active"],
                    ["near", `Near limit (${nearCount})`],
                  ] as [Filter, string][]
                ).map(([v, text]) => (
                  <Button
                    key={v}
                    size="sm"
                    variant={v === filter ? "secondary" : "ghost"}
                    aria-pressed={v === filter}
                    className="h-7 px-2.5"
                    onClick={() => setFilter(v)}
                  >
                    {text}
                  </Button>
                ))}
              </div>
            }
          >
            People
          </SectionHeader>
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Person</TableHead>
                  <TableHead>Plan</TableHead>
                  <TableHead>Tightest quota now</TableHead>
                  <TableHead className="text-end">Spend, 30 days</TableHead>
                  <TableHead className="text-end">Last active</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {users.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={5} className="py-8 text-center text-muted-foreground">
                      {filter === "near" ? "Nobody is at 80% or more of a quota." : "No one has used Qanat in this range."}
                    </TableCell>
                  </TableRow>
                ) : (
                  users.map((u) => (
                    <TableRow key={u.id} className="relative">
                      <TableCell>
                        <Link href={`/admin/usage/${u.id}`} className="font-semibold after:absolute after:inset-0 hover:underline">
                          {u.name || u.username || u.email}
                        </Link>
                        {u.disabled && (
                          <span className="ms-2">
                            <Tag>Disabled</Tag>
                          </span>
                        )}
                        <span className="block text-meta text-muted-foreground">{u.email}</span>
                      </TableCell>
                      <TableCell>{u.plan_name}</TableCell>
                      <TableCell>
                        <QuotaCell u={u} />
                      </TableCell>
                      <TableCell className="text-end font-semibold tabular-nums">{formatUsd(u.cost_usd)}</TableCell>
                      <TableCell className="text-end text-muted-foreground">
                        {u.last_active ? dayFmt.format(new Date(`${u.last_active}T00:00:00Z`)) : "—"}
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </div>
          <SectionHeader>Everyone</SectionHeader>
          <Suspense>
            <UsageStats scope={{ kind: "admin" }} period={period} />
          </Suspense>
        </>
      )}
    </>
  );
}
