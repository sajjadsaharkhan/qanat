"use client";

import { ArrowLeft, ChartColumnStacked } from "lucide-react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { Suspense } from "react";

import { EmptyState, PageHeader, SectionHeader, Tag } from "@/components/qanat/page";
import { QuotaList, useNow } from "@/components/qanat/quota-list";
import { PeriodSelector, UsageStats, usePeriod } from "@/components/qanat/usage-overview/usage-stats";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { api, errorMessage } from "@/lib/api/client";

/** One person's quota windows and usage report, as an admin sees it. */
export default function AdminUserUsagePage() {
  // Filters live in the URL (useSearchParams), which needs a Suspense boundary.
  return (
    <Suspense fallback={<Skeleton className="h-96 w-full" />}>
      <AdminUserUsage />
    </Suspense>
  );
}

function AdminUserUsage() {
  const { id } = useParams<{ id: string }>();
  const [period, setPeriod] = usePeriod();
  const now = useNow();
  const usage = api.useQuery("get", "/api/admin/usage/{id}", { params: { path: { id }, query: { days: 7 } } }, { refetchInterval: 15_000 });
  const u = usage.data?.user;

  return (
    <>
      <Button asChild variant="ghost" size="sm" className="mb-2 -ms-2 self-start text-muted-foreground">
        <Link href="/admin/usage">
          <ArrowLeft className="me-1 size-4" />
          Usage & quotas
        </Link>
      </Button>
      <PageHeader
        title={u ? u.name || u.username || u.email : "Usage"}
        icon={ChartColumnStacked}
        description={
          u ? (
            <span className="inline-flex flex-wrap items-center gap-2">
              {u.email} · {u.plan_name} Plan · {u.role}
              {u.disabled && <Tag>Disabled</Tag>}
            </span>
          ) : undefined
        }
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
          <SectionHeader>Quota now ({usage.data.timezone})</SectionHeader>
          {usage.data.user.quotas.length === 0 ? (
            <EmptyState title="Zero quota">
              This person&apos;s Plan has no quota windows, so their requests are refused. Assign a Plan on the Users page.
            </EmptyState>
          ) : (
            <Card className="py-5 shadow-none">
              <CardContent>
                <QuotaList windows={usage.data.user.quotas} now={now} />
              </CardContent>
            </Card>
          )}
          <SectionHeader>Usage</SectionHeader>
          <Suspense>
            <UsageStats scope={{ kind: "admin", userId: id }} period={period} />
          </Suspense>
        </>
      )}
    </>
  );
}
