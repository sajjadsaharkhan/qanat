"use client";

import { BarChart3 } from "lucide-react";
import { Suspense, useState } from "react";

import { PageHeader } from "@/components/qanat/page";
import { PeriodSelector, UsageStats, type Period } from "@/components/qanat/usage-overview/usage-stats";
import { Skeleton } from "@/components/ui/skeleton";

/** Your usage, in 9router's Overview layout (see usage-overview/LICENSE-9router). */
export default function UsagePage() {
  const [period, setPeriod] = useState<Period>("today");
  return (
    <>
      <PageHeader
        title="Usage"
        icon={BarChart3}
        description="Your requests, tokens and estimated cost. Updates every few seconds."
        actions={<PeriodSelector period={period} onChange={setPeriod} />}
      />
      <Suspense fallback={<Skeleton className="h-96 w-full" />}>
        <UsageStats scope={{ kind: "me" }} period={period} />
      </Suspense>
    </>
  );
}
