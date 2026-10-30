"use client";

import { ReducedMotionConfig, transition, useTempoTransition } from "@animate-ui/react/utils/motion";
import { ChartColumnStacked, CircleDollarSign, Gauge, OctagonX, TriangleAlert, Users, type LucideIcon } from "lucide-react";
import { motion } from "motion/react";
import Link from "next/link";
import { Suspense, useMemo } from "react";

import { PageHeader, SectionHeader, Tag } from "@/components/qanat/page";
import { QuotaBar } from "@/components/qanat/quota-bar";
import { CountUp, Reveal } from "@/components/qanat/usage-overview/motion";
import { Card, CardHeading, Segmented } from "@/components/qanat/usage-overview/shared";
import { PeriodSelector, UsageStats, usePeriod } from "@/components/qanat/usage-overview/usage-stats";
import { Skeleton } from "@/components/ui/skeleton";
import { useSearchParam } from "@/hooks/use-search-param";
import { api, errorMessage, type Schemas } from "@/lib/api/client";
import { formatUsd, quotaLevel, type QuotaLevel } from "@/lib/format";
import { quotaLabel } from "@/lib/quota-labels";
import { cn } from "@/lib/utils";

type UserUsage = Schemas["UserUsage"];

const dayFmt = new Intl.DateTimeFormat("en-US", {
  month: "short",
  day: "numeric",
  timeZone: "UTC",
});
const pct = new Intl.NumberFormat("en-US", {
  style: "percent",
  maximumFractionDigits: 0,
});
const whole = (n: number) => String(Math.round(n));

// design.md §3.2: signal text only for the states that earn it.
const levelText: Record<QuotaLevel, string> = {
  ok: "text-foreground",
  warning: "text-warning-text",
  critical: "text-destructive-text",
  exhausted: "text-destructive-text",
};

/** The window closest to its limit, for the table's quota column. */
function tightestWindow(u: UserUsage) {
  return [...u.quotas].sort((a, b) => b.used_usd / (b.limit_usd || 1) - a.used_usd / (a.limit_usd || 1))[0];
}

function initials(u: UserUsage) {
  const name = (u.name || u.username || u.email).trim();
  const parts = name.split(/[\s@._-]+/).filter(Boolean);
  return ((parts[0]?.[0] ?? "") + (parts[1]?.[0] ?? "")).toUpperCase() || "?";
}

function QuotaCell({ u }: { u: UserUsage }) {
  const w = tightestWindow(u);
  if (!w) return <span className="text-muted-foreground">Zero quota</span>;
  const level = quotaLevel(w.used_usd, w.limit_usd);
  const Icon = level === "exhausted" ? OctagonX : level === "ok" ? null : TriangleAlert;
  return (
    <div className="flex min-w-52 flex-col gap-1.5">
      <div className="flex items-baseline justify-between gap-3 text-sm">
        <span className="font-medium">{quotaLabel(w)}</span>
        <span className={cn("inline-flex items-center gap-1 font-semibold tabular-nums", levelText[level])}>
          {Icon && <Icon className="size-3.5" aria-label={level === "exhausted" ? "Exhausted" : "Near or over the limit"} />}
          {pct.format(w.limit_usd ? w.used_usd / w.limit_usd : 1)}
        </span>
      </div>
      <QuotaBar size="md" used={w.used_usd} limit={w.limit_usd} label={`${u.email} ${quotaLabel(w)} quota used`} />
      <span className="text-meta tabular-nums text-muted-foreground">
        {formatUsd(w.used_usd)} of {formatUsd(w.limit_usd)}
      </span>
    </div>
  );
}

type Tone = "neutral" | "warning" | "danger" | "filled";

function StatTile({
  label,
  icon: Icon,
  value,
  format,
  meta,
  tone = "neutral",
}: {
  label: string;
  icon: LucideIcon;
  value: number;
  format: (n: number) => string;
  meta: string;
  tone?: Tone;
}) {
  const filled = tone === "filled";
  return (
    <Card className={cn("flex h-full flex-col gap-3 p-4", filled && "border-transparent bg-primary text-primary-foreground")}>
      <div className="flex items-center gap-2">
        <span
          aria-hidden
          className={cn(
            "grid size-7 shrink-0 place-items-center rounded-md",
            tone === "neutral" && "bg-brand-soft text-brand-text",
            tone === "warning" && "bg-warning/15 text-warning-text",
            tone === "danger" && "bg-destructive/15 text-destructive-text",
            filled && "bg-primary-foreground/15",
          )}
        >
          <Icon className="size-4" />
        </span>
        <span className="truncate text-sm font-medium">{label}</span>
      </div>
      <div>
        <CountUp
          value={value}
          format={format}
          className={cn(
            "block truncate text-2xl font-semibold tracking-tight sm:text-[28px] sm:leading-8",
            tone === "warning" && "text-warning-text",
            tone === "danger" && "text-destructive-text",
          )}
        />
        <p className={cn("mt-1 truncate text-meta", filled ? "text-primary-foreground/85" : "text-muted-foreground")}>{meta}</p>
      </div>
    </Card>
  );
}

type Filter = "all" | "near" | "active";

export default function AdminUsagePage() {
  // Filters live in the URL (useSearchParams), which needs a Suspense boundary.
  return (
    <Suspense fallback={<Skeleton className="h-96 w-full" />}>
      <AdminUsage />
    </Suspense>
  );
}

function AdminUsage() {
  const [period, setPeriod] = usePeriod();
  const [filter, setFilter] = useSearchParam<Filter>("show", "all", ["all", "active", "near"]);
  const rowFade = useTempoTransition(transition.reveal);
  // The quota table's spend column is a fixed 30 days; the Overview below follows the period.
  const usage = api.useQuery("get", "/api/admin/usage", { params: { query: { days: 30 } } }, { refetchInterval: 15_000 });

  const all = useMemo(() => usage.data?.users ?? [], [usage.data]);
  const users = useMemo(() => {
    if (filter === "near") return all.filter((u) => u.tightest != null && u.tightest >= 0.8);
    if (filter === "active") return all.filter((u) => u.request_count > 0);
    return all;
  }, [all, filter]);
  const summary = useMemo(
    () => ({
      active: all.filter((u) => u.request_count > 0).length,
      near: all.filter((u) => u.tightest != null && u.tightest >= 0.8 && u.tightest < 0.95).length,
      atLimit: all.filter((u) => u.tightest != null && u.tightest >= 0.95).length,
      spend: all.reduce((s, u) => s + u.cost_usd, 0),
    }),
    [all],
  );
  const nearCount = summary.near + summary.atLimit;

  return (
    <ReducedMotionConfig>
      <PageHeader
        title="Usage & quotas"
        icon={ChartColumnStacked}
        description="How close each person is to their Plan's limits, and everyone's usage. Select a person for their own view."
        actions={<PeriodSelector period={period} onChange={setPeriod} />}
      />
      {usage.isPending ? (
        <div className="flex flex-col gap-4" aria-busy>
          <span className="sr-only">Loading usage</span>
          <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
            {Array.from({ length: 4 }, (_, i) => (
              <Skeleton key={i} className="h-[118px] rounded-[14px]" />
            ))}
          </div>
          <Skeleton className="h-96 w-full rounded-[14px]" />
        </div>
      ) : usage.isError ? (
        <p role="alert" className="text-destructive-text">
          {errorMessage(usage.error)}
        </p>
      ) : (
        <>
          <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
            <Reveal index={0}>
              <StatTile label="People" icon={Users} value={all.length} format={whole} meta={`${summary.active} active in 30 days`} />
            </Reveal>
            <Reveal index={1}>
              <StatTile
                label="Near limit"
                icon={Gauge}
                value={summary.near}
                format={whole}
                meta="80–95% of a window"
                tone={summary.near ? "warning" : "neutral"}
              />
            </Reveal>
            <Reveal index={2}>
              <StatTile
                label="At limit"
                icon={OctagonX}
                value={summary.atLimit}
                format={whole}
                meta="95% or more of a window"
                tone={summary.atLimit ? "danger" : "neutral"}
              />
            </Reveal>
            <Reveal index={3}>
              <StatTile
                label="Spend, 30 days"
                icon={CircleDollarSign}
                value={summary.spend}
                format={formatUsd}
                meta="Confirmed prices"
                tone="filled"
              />
            </Reveal>
          </div>

          <Reveal index={4} className="mt-4">
            <Card className="overflow-hidden">
              <div className="p-4 sm:px-5">
                <CardHeading title="People" hint="Each person's tightest quota window right now">
                  <Segmented
                    label="Show"
                    size="sm"
                    options={[
                      { value: "all", label: "Everyone" },
                      { value: "active", label: "Active" },
                      { value: "near", label: `Near limit (${nearCount})` },
                    ]}
                    value={filter}
                    onChange={setFilter}
                  />
                </CardHeading>
              </div>
              <div className="overflow-x-auto">
                <table className="w-full text-left text-sm">
                  <thead className="border-y bg-muted/60 text-meta text-muted-foreground">
                    <tr>
                      <th className="px-4 py-2.5 font-medium sm:ps-5">Person</th>
                      <th className="px-4 py-2.5 font-medium">Plan</th>
                      <th className="px-4 py-2.5 font-medium">Tightest quota now</th>
                      <th className="px-4 py-2.5 text-end font-medium">Spend, 30 days</th>
                      <th className="px-4 py-2.5 text-end font-medium sm:pe-5">Last active</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y">
                    {users.length === 0 ? (
                      <tr>
                        <td colSpan={5} className="py-10 text-center text-muted-foreground">
                          {filter === "near" ? "Nobody is at 80% or more of a quota." : "No one has used Qanat in this range."}
                        </td>
                      </tr>
                    ) : (
                      users.map((u, i) => (
                        <motion.tr
                          key={u.id}
                          className="relative transition-colors duration-200 hover:bg-muted/50"
                          initial={{ opacity: 0 }}
                          animate={{
                            opacity: 1,
                            transition: {
                              ...rowFade,
                              delay: Math.min(i, 8) * 0.04,
                            },
                          }}
                        >
                          <td className="px-4 py-3 sm:ps-5">
                            <div className="flex items-center gap-3">
                              <span
                                aria-hidden
                                className="grid size-9 shrink-0 place-items-center rounded-full bg-sand text-xs font-semibold text-sand-foreground"
                              >
                                {initials(u)}
                              </span>
                              <div className="min-w-0">
                                <Link
                                  href={`/admin/usage/${u.id}`}
                                  className="font-semibold after:absolute after:inset-0 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                                >
                                  {u.name || u.username || u.email}
                                </Link>
                                {u.disabled && (
                                  <span className="ms-2">
                                    <Tag>Disabled</Tag>
                                  </span>
                                )}
                                <span className="block truncate text-meta text-muted-foreground">{u.email}</span>
                              </div>
                            </div>
                          </td>
                          <td className="px-4 py-3">
                            <Tag tone="brand">{u.plan_name}</Tag>
                          </td>
                          <td className="px-4 py-3">
                            <QuotaCell u={u} />
                          </td>
                          <td className="px-4 py-3 text-end">
                            <span className="font-semibold tabular-nums">{formatUsd(u.cost_usd)}</span>
                            <span className="block text-meta tabular-nums text-muted-foreground">{u.request_count.toLocaleString()} requests</span>
                          </td>
                          <td className="px-4 py-3 text-end text-muted-foreground sm:pe-5">
                            {u.last_active ? dayFmt.format(new Date(`${u.last_active}T00:00:00Z`)) : "—"}
                          </td>
                        </motion.tr>
                      ))
                    )}
                  </tbody>
                </table>
              </div>
            </Card>
          </Reveal>

          <SectionHeader>Everyone</SectionHeader>
          <Suspense>
            <UsageStats scope={{ kind: "admin" }} period={period} />
          </Suspense>
        </>
      )}
    </ReducedMotionConfig>
  );
}
