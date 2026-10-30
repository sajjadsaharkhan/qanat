"use client";

import Link from "next/link";
import { useEffect, useState } from "react";

import { EmptyState, PageHeader, SectionHeader } from "@/components/qanat/page";
import { QuotaBar } from "@/components/qanat/quota-bar";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { api, type Schemas } from "@/lib/api/client";
import { formatDuration, formatUsd } from "@/lib/format";
import { quotaLabel } from "@/lib/quota-labels";

/** Ticks once a minute so countdowns stay current without re-fetching. */
function useNow(intervalMs = 60_000) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}

function resetText(w: Schemas["QuotaWindow"], now: number) {
  if (!w.resets_at) return "rolling";
  return `in ${formatDuration(new Date(w.resets_at).getTime() - now)}`;
}

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
      <PageHeader title="Overview" description={me.data ? `You're on the ${me.data.plan_name} Plan.` : undefined} />

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
        <Card className="gap-0 py-5 shadow-none">
          <CardContent className="flex flex-col gap-5" aria-live="polite">
            <Hero windows={windows} now={now} />
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
          </CardContent>
        </Card>
      )}

      <SectionHeader>Recent requests</SectionHeader>
      <p className="text-muted-foreground">Requests show up here once the gateway starts serving traffic.</p>
    </>
  );
}
