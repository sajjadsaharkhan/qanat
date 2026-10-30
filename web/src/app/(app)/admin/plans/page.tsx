"use client";

import { useQueryClient } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { PageHeader } from "@/components/qanat/page";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, errorMessage, type Plan, type PlanBody } from "@/lib/api/client";
import { formatUsd } from "@/lib/format";
import { quotaLabel, weekdays } from "@/lib/quota-labels";

import { PlanEditor } from "./plan-editor";

function QuotaSummary({ plan }: { plan: Plan }) {
  if (plan.quotas.length === 0) return <>Zero quota</>;
  return (
    <ul className="flex flex-wrap gap-x-3 gap-y-0.5">
      {plan.quotas.map((q) => (
        <li key={`${q.window}/${q.model_scope ?? ""}`} className="whitespace-nowrap">
          <span className="text-muted-foreground">{quotaLabel(q)}</span> {formatUsd(q.limit_usd)}
        </li>
      ))}
    </ul>
  );
}

export default function PlansPage() {
  const qc = useQueryClient();
  const plans = api.useQuery("get", "/api/admin/plans");
  const create = api.useMutation("post", "/api/admin/plans");
  const update = api.useMutation("put", "/api/admin/plans/{id}");
  const remove = api.useMutation("delete", "/api/admin/plans/{id}");

  const [editing, setEditing] = useState<Plan | "new" | null>(null);
  const [deleting, setDeleting] = useState<Plan | null>(null);
  const [serverError, setServerError] = useState<string | null>(null);

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["get", "/api/admin/plans"] });
    qc.invalidateQueries({ queryKey: ["get", "/api/me/quotas"] });
  };
  const done = (msg: string) => () => {
    setEditing(null);
    refresh();
    toast(msg);
  };
  const save = (body: PlanBody) => {
    setServerError(null);
    const onError = (err: unknown) => setServerError(errorMessage(err));
    if (editing === "new") create.mutate({ body }, { onSuccess: done("Plan created"), onError });
    else if (editing) update.mutate({ params: { path: { id: editing.id } }, body }, { onSuccess: done("Plan saved"), onError });
  };

  return (
    <>
      <PageHeader
        title="Plans"
        description="Each person is on one Plan: dollar quotas per window, rate limits and the models they may use."
        actions={
          <Button
            onClick={() => {
              setServerError(null);
              setEditing("new");
            }}
          >
            <Plus /> New Plan
          </Button>
        }
      />
      {plans.isPending ? (
        <Skeleton className="h-40 w-full" />
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Plan</TableHead>
                <TableHead>Quotas</TableHead>
                <TableHead>Rate limits</TableHead>
                <TableHead>Models</TableHead>
                <TableHead className="text-end">People</TableHead>
                <TableHead>
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {plans.data?.map((p) => (
                <TableRow key={p.id}>
                  <TableCell>
                    <div className="flex items-center gap-2 font-semibold">
                      {p.name}
                      {p.is_default && <Badge variant="outline">Default</Badge>}
                    </div>
                    <div className="text-meta text-muted-foreground">
                      Resets {p.daily_anchor} {p.timezone} · {weekdays[p.weekly_anchor]} · day {p.monthly_anchor}
                    </div>
                  </TableCell>
                  <TableCell className="min-w-56 whitespace-normal tabular-nums">
                    <QuotaSummary plan={p} />
                  </TableCell>
                  <TableCell className="tabular-nums text-muted-foreground">
                    {p.limits.rpm || "∞"} rpm · {p.limits.max_concurrent || "∞"} streams
                  </TableCell>
                  <TableCell className="whitespace-normal text-muted-foreground">{p.models.length ? p.models.join(", ") : "All models"}</TableCell>
                  <TableCell className="text-end tabular-nums">{p.user_count}</TableCell>
                  <TableCell className="whitespace-nowrap text-end">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        setServerError(null);
                        setEditing(p);
                      }}
                    >
                      Edit
                    </Button>
                    {!p.is_default && (
                      <Button variant="ghost" size="sm" onClick={() => setDeleting(p)}>
                        Delete
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <PlanEditor
        open={editing !== null}
        plan={editing === "new" || editing === null ? undefined : editing}
        onOpenChange={(o) => !o && setEditing(null)}
        onSave={save}
        saving={create.isPending || update.isPending}
        serverError={serverError}
      />

      <AlertDialog open={!!deleting} onOpenChange={(o) => !o && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete “{deleting?.name}”?</AlertDialogTitle>
            <AlertDialogDescription>
              {deleting?.user_count ? "People are still on this Plan. Move them to another Plan first." : "This can't be undone."}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={!!deleting?.user_count}
              onClick={() => {
                if (!deleting) return;
                remove.mutate(
                  { params: { path: { id: deleting.id } } },
                  { onSuccess: () => (refresh(), toast("Plan deleted")), onError: (err) => toast.error(errorMessage(err)) },
                );
              }}
            >
              Delete Plan
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
