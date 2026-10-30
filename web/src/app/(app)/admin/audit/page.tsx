"use client";

import { ScrollText } from "lucide-react";
import { useInfiniteQuery } from "@tanstack/react-query";

import { EmptyState, PageHeader } from "@/components/qanat/page";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { fetchClient, type Schemas } from "@/lib/api/client";

const timeFmt = new Intl.DateTimeFormat("en-US", { dateStyle: "medium", timeStyle: "short" });

const actionLabel: Record<string, string> = {
  "auth.login": "Signed in",
  "key.create": "Created key",
  "key.revoke": "Revoked key",
  "user.role": "Changed role",
  "user.plan": "Assigned Plan",
  "plan.create": "Created Plan",
  "plan.update": "Edited Plan",
  "plan.delete": "Deleted Plan",
};

/** A short, readable summary of what changed. */
function summary(e: Schemas["AuditEntry"]): string {
  const after = (e.after ?? {}) as Record<string, unknown>;
  const before = (e.before ?? {}) as Record<string, unknown>;
  switch (e.action) {
    case "user.role":
      return `${before.role} → ${after.role}`;
    case "user.plan":
      return `${before.plan} → ${after.plan}`;
    case "key.create":
    case "key.revoke":
      return String(after.name ?? before.name ?? "");
    case "plan.create":
    case "plan.update":
      return String(after.name ?? "");
    case "plan.delete":
      return String(before.name ?? "");
    default:
      return "";
  }
}

const pageSize = 50;

export default function AuditPage() {
  const audit = useInfiniteQuery({
    queryKey: ["audit"],
    initialPageParam: undefined as number | undefined,
    queryFn: async ({ pageParam }) => {
      const { data } = await fetchClient.GET("/api/admin/audit", { params: { query: { before: pageParam, limit: pageSize } } });
      return data ?? [];
    },
    getNextPageParam: (last) => (last.length === pageSize ? last[last.length - 1]?.id : undefined),
  });
  const rows = audit.data?.pages.flat() ?? [];

  return (
    <>
      <PageHeader title="Audit log" icon={ScrollText} description="Every change to Plans, quotas, keys, roles, accounts, mapping and prices, newest first." />
      {audit.isPending ? (
        <Skeleton className="h-40 w-full" />
      ) : rows.length === 0 ? (
        <EmptyState title="Nothing recorded yet">Changes made in the dashboard are listed here with who made them.</EmptyState>
      ) : (
        <>
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>When</TableHead>
                  <TableHead>Who</TableHead>
                  <TableHead>What</TableHead>
                  <TableHead>Details</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody data-volatile>
                {rows.map((e) => (
                  <TableRow key={e.id}>
                    <TableCell className="tabular-nums">
                      {timeFmt.format(new Date(e.at))}
                    </TableCell>
                    <TableCell>{e.actor_email || "System"}</TableCell>
                    <TableCell>{actionLabel[e.action] ?? e.action}</TableCell>
                    <TableCell className="text-muted-foreground">{summary(e)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          {audit.hasNextPage && (
            <Button variant="outline" className="mt-4" onClick={() => audit.fetchNextPage()} disabled={audit.isFetchingNextPage}>
              Load older entries
            </Button>
          )}
        </>
      )}
    </>
  );
}
