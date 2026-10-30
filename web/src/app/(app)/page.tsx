"use client";

import { LayoutGrid } from "lucide-react";
import Link from "next/link";
import { Suspense } from "react";

import { EmptyState, PageHeader, SectionHeader } from "@/components/qanat/page";
import { QuotaList, resetText, useNow } from "@/components/qanat/quota-list";
import { PeriodSelector, UsageStats, usePeriod } from "@/components/qanat/usage-overview/usage-stats";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { api, type Schemas } from "@/lib/api/client";
import { formatUsd } from "@/lib/format";

/** The hero sentence comes from the window with the least headroom (design.md §7.3). */
function Hero({ windows, now }: { windows: Schemas["QuotaWindow"][]; now: number }) {
  const tightest = [...windows].sort((a, b) => a.remaining_usd - b.remaining_usd)[0];
  if (!tightest) return null;
  const scope = tightest.model_scope ? ` for ${tightest.model_scope}` : "";
  const period = { rolling_5h: "in the 5-hour window", daily: "today", weekly: "this week", monthly: "this month" }[tightest.window];
  return (
    <div className="flex flex-wrap items-baseline justify-between gap-2">
      <p>
        <span className="text-hero font-semibold tabular-nums">{formatUsd(tightest.remaining_usd)}</span>{" "}
        <span className="text-muted-foreground">
          left {period}
          {scope}
        </span>
      </p>
      <p className="text-muted-foreground tabular-nums" data-volatile>
        {tightest.resets_at ? `resets ${resetText(tightest, now)}` : "frees up as usage ages out"}
      </p>
    </div>
  );
}

/** Quotas: how much is left, and when it refills. */
function Quotas({ role }: { role?: string }) {
  const quotas = api.useQuery("get", "/api/me/quotas", {}, { refetchInterval: 5_000 });
  const now = useNow();
  const windows = quotas.data?.windows ?? [];
  if (quotas.isPending) return <Skeleton className="h-48 w-full" />;
  if (windows.length === 0) {
    return (
      <EmptyState
        title="No quota on your Plan yet"
        action={
          role === "admin" ? (
            <Button asChild variant="outline">
              <Link href="/admin/plans">Set up a Plan</Link>
            </Button>
          ) : undefined
        }
      >
        New accounts start on a zero-quota Plan, so requests are refused until an admin assigns a Plan with quota.
      </EmptyState>
    );
  }
  return (
    <Card className="gap-0 overflow-hidden py-0 shadow-none" aria-live="polite">
      <div className="border-b bg-sand-soft px-6 py-5">
        <Hero windows={windows} now={now} />
      </div>
      <CardContent className="py-5">
        <QuotaList windows={windows} now={now} />
      </CardContent>
    </Card>
  );
}

/** Overview: quotas, then usage with its recent requests, on one page (Usage merged in). */
export default function OverviewPage() {
  // The period lives in the URL (useSearchParams), which needs a Suspense boundary.
  return (
    <Suspense fallback={<Skeleton className="h-96 w-full" />}>
      <Overview />
    </Suspense>
  );
}

function Overview() {
  const me = api.useQuery("get", "/api/me");
  const [period, setPeriod] = usePeriod();
  return (
    <>
      <PageHeader
        title="Overview"
        icon={LayoutGrid}
        description={me.data ? `You're on the ${me.data.plan_name} Plan. How much is left, what you've used, and your latest requests.` : undefined}
        actions={<PeriodSelector period={period} onChange={setPeriod} />}
      />
      <Quotas role={me.data?.role} />

      <SectionHeader>Usage</SectionHeader>
      <Suspense fallback={<Skeleton className="h-96 w-full" />}>
        <UsageStats scope={{ kind: "me" }} period={period} />
      </Suspense>
    </>
  );
}
