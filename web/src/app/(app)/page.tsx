"use client";

import { LayoutGrid } from "lucide-react";
import Link from "next/link";

import { EmptyState, PageHeader, SectionHeader } from "@/components/qanat/page";
import { QuotaList, resetText, useNow } from "@/components/qanat/quota-list";
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

export default function OverviewPage() {
  const me = api.useQuery("get", "/api/me");
  const quotas = api.useQuery("get", "/api/me/quotas", {}, { refetchInterval: 5_000 });
  const now = useNow();
  const windows = quotas.data?.windows ?? [];

  return (
    <>
      <PageHeader title="Overview" icon={LayoutGrid} description={me.data ? `You're on the ${me.data.plan_name} Plan.` : undefined} />

      {quotas.isPending ? (
        <Skeleton className="h-48 w-full" />
      ) : windows.length === 0 ? (
        <EmptyState
          title="No quota on your Plan yet"
          action={
            me.data?.role === "admin" ? (
              <Button asChild variant="outline">
                <Link href="/admin/plans">Set up a Plan</Link>
              </Button>
            ) : undefined
          }
        >
          New accounts start on a zero-quota Plan, so requests are refused until an admin assigns a Plan with quota.
        </EmptyState>
      ) : (
        <Card className="gap-0 overflow-hidden py-0 shadow-none" aria-live="polite">
          <div className="border-b bg-sand-soft px-6 py-5">
            <Hero windows={windows} now={now} />
          </div>
          <CardContent className="py-5">
            <QuotaList windows={windows} now={now} />
          </CardContent>
        </Card>
      )}

      <SectionHeader>Recent requests</SectionHeader>
      <p className="text-muted-foreground">Requests show up here once the gateway starts serving traffic.</p>
    </>
  );
}
