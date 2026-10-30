"use client";

import { Activity } from "lucide-react";

import { EmptyState, PageHeader, Tag } from "@/components/qanat/page";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api/client";

const timeFmt = new Intl.DateTimeFormat("en-US", {
  dateStyle: "short",
  timeStyle: "medium",
});

export default function RequestsPage() {
  const reqs = api.useQuery("get", "/api/me/requests");

  return (
    <>
      <PageHeader
        title="Recent requests"
        icon={Activity}
        description="Your last 200 requests from the gateway ring buffer. Metadata and usage only — prompt and response bodies are never recorded."
      />
      {reqs.isPending ? (
        <Skeleton className="h-40 w-full" />
      ) : !reqs.data || reqs.data.length === 0 ? (
        <EmptyState title="No requests yet">
          Your last 200 requests show here with model, time to first token, tokens, cost and status. Prompts are never shown.
        </EmptyState>
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Time</TableHead>
                <TableHead>Model</TableHead>
                <TableHead>Provider</TableHead>
                <TableHead className="text-end">TTFT</TableHead>
                <TableHead className="text-end">Duration</TableHead>
                <TableHead className="text-end">Tokens (In / Out)</TableHead>
                <TableHead className="text-end">Cost (USD)</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody data-volatile>
              {reqs.data.map((r) => {
                const cost = (r.cost_micro / 1_000_000).toLocaleString("en-US", {
                  style: "currency",
                  currency: "USD",
                  minimumFractionDigits: 4,
                  maximumFractionDigits: 6,
                });
                return (
                  <TableRow key={r.request_id}>
                    <TableCell className="text-meta text-muted-foreground whitespace-nowrap">
                      {timeFmt.format(new Date(r.at))}
                    </TableCell>
                    <TableCell>
                      <div className="font-semibold">{r.model}</div>
                      {r.upstream_model && r.upstream_model !== r.model && (
                        <div className="text-meta text-muted-foreground font-mono">→ {r.upstream_model}</div>
                      )}
                    </TableCell>
                    <TableCell>
                      <Tag tone="brand">{r.provider}</Tag>
                    </TableCell>
                    <TableCell className="text-end font-mono text-meta">{r.ttft_ms} ms</TableCell>
                    <TableCell className="text-end font-mono text-meta">{r.duration_ms} ms</TableCell>
                    <TableCell className="text-end font-mono text-meta">
                      {r.input_tokens.toLocaleString()} / {r.output_tokens.toLocaleString()}
                      {r.cache_read_tokens > 0 && (
                        <div className="text-muted-foreground text-xs font-sans">
                          cache: {r.cache_read_tokens.toLocaleString()}
                        </div>
                      )}
                    </TableCell>
                    <TableCell className="text-end font-mono text-meta">{cost}</TableCell>
                    <TableCell>
                      {r.aborted ? (
                        <Tag tone="sand">Aborted</Tag>
                      ) : r.status >= 200 && r.status < 300 ? (
                        <Tag tone="brand">{r.status}</Tag>
                      ) : (
                        <Tag>{r.status}</Tag>
                      )}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}
    </>
  );
}
