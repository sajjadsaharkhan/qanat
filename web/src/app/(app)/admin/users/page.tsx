"use client";

import { Ban, ChevronDown, CircleCheck, CirclePause, MoreHorizontal, RotateCcw, Users } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";

import { PageHeader, Tag } from "@/components/qanat/page";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, errorMessage } from "@/lib/api/client";
import { quotaLabel } from "@/lib/quota-labels";

import { DisableDialog, displayName, EnableDialog, PlanDialog, ResetQuotaDialog, RoleDialog, type User } from "./user-dialogs";

const dateFmt = new Intl.DateTimeFormat("en-US", { dateStyle: "medium" });

export default function UsersPage() {
  const qc = useQueryClient();
  const me = api.useQuery("get", "/api/me");
  const users = api.useQuery("get", "/api/admin/users");
  const plans = api.useQuery("get", "/api/admin/plans");
  const update = api.useMutation("patch", "/api/admin/users/{id}");
  const disable = api.useMutation("post", "/api/admin/users/{id}/disable");
  const enable = api.useMutation("post", "/api/admin/users/{id}/enable");
  const reset = api.useMutation("post", "/api/admin/users/{id}/quota/reset");

  const [roleChange, setRoleChange] = useState<{ user: User; role: User["role"] } | null>(null);
  const [planFor, setPlanFor] = useState<User | null>(null);
  const [disabling, setDisabling] = useState<{ user: User; ban: boolean } | null>(null);
  const [enabling, setEnabling] = useState<User | null>(null);
  const [resetting, setResetting] = useState<User | null>(null);

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["get", "/api/admin/users"] });
    qc.invalidateQueries({ queryKey: ["get", "/api/admin/plans"] });
    qc.invalidateQueries({ queryKey: ["get", "/api/me"] });
    qc.invalidateQueries({ queryKey: ["get", "/api/me/quotas"] });
  };
  const handlers = (done: string, close: () => void) => ({
    onSuccess: () => {
      close();
      refresh();
      toast(done);
    },
    onError: (err: unknown) => toast.error(errorMessage(err)),
  });

  return (
    <>
      <PageHeader
        title="Users" icon={Users}
        description="People are added on their first Keycloak sign-in, on the zero-quota default Plan. Assign a Plan to let them spend."
      />
      {users.isPending || plans.isPending ? (
        <Skeleton className="h-40 w-full" />
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Person</TableHead>
                <TableHead>Role</TableHead>
                <TableHead>Plan</TableHead>
                <TableHead className="text-end">Active keys</TableHead>
                <TableHead>Joined</TableHead>
                <TableHead>
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.data?.map((u) => {
                const isRoot = u.username === "root";
                const isMe = u.id === me.data?.id;
                const who = displayName(u);
                return (
                  <TableRow key={u.id} className={u.disabled_at ? "text-muted-foreground" : undefined}>
                    <TableCell>
                      <div className="flex items-center gap-2 font-semibold">
                        {who}
                        {isRoot && <Tag tone="sand">Root</Tag>}
                        {u.banned_at ? <Tag tone="sand">Banned</Tag> : u.disabled_at && <Tag>Disabled</Tag>}
                      </div>
                      <div className="text-meta text-muted-foreground">
                        {u.email}
                        {u.disabled_at && u.disabled_reason && <> · {u.disabled_reason}</>}
                      </div>
                    </TableCell>
                    <TableCell>
                      <Select
                        value={u.role}
                        disabled={isRoot || isMe}
                        onValueChange={(role) => setRoleChange({ user: u, role: role as User["role"] })}
                      >
                        <SelectTrigger size="sm" className="w-28" aria-label={`Role for ${who}`}>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="user">User</SelectItem>
                          <SelectItem value="admin">Admin</SelectItem>
                        </SelectContent>
                      </Select>
                    </TableCell>
                    <TableCell>
                      <Button variant="outline" size="sm" className="w-44 justify-between font-normal" aria-label={`Plan for ${who}: ${u.plan_name}`} onClick={() => setPlanFor(u)}>
                        <span className="truncate">{u.plan_name}</span>
                        <ChevronDown className="opacity-50" />
                      </Button>
                    </TableCell>
                    <TableCell className="text-end tabular-nums">{u.active_keys}</TableCell>
                    <TableCell data-volatile>{dateFmt.format(new Date(u.created_at))}</TableCell>
                    <TableCell className="text-end">
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button variant="ghost" size="icon-sm" aria-label={`Actions for ${who}`}>
                            <MoreHorizontal />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem onSelect={() => setResetting(u)}>
                            <RotateCcw /> Reset quota…
                          </DropdownMenuItem>
                          {!isRoot && !isMe && (
                            <>
                              <DropdownMenuSeparator />
                              {u.disabled_at ? (
                                <DropdownMenuItem onSelect={() => setEnabling(u)}>
                                  <CircleCheck /> {u.banned_at ? "Lift ban…" : "Enable account…"}
                                </DropdownMenuItem>
                              ) : (
                                <>
                                  <DropdownMenuItem onSelect={() => setDisabling({ user: u, ban: false })}>
                                    <CirclePause /> Disable account…
                                  </DropdownMenuItem>
                                  <DropdownMenuItem variant="destructive" onSelect={() => setDisabling({ user: u, ban: true })}>
                                    <Ban /> Ban…
                                  </DropdownMenuItem>
                                </>
                              )}
                            </>
                          )}
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}

      <RoleDialog
        change={roleChange}
        busy={update.isPending}
        onCancel={() => setRoleChange(null)}
        onConfirm={() => {
          if (!roleChange) return;
          const { user, role } = roleChange;
          update.mutate(
            { params: { path: { id: user.id } }, body: { role } },
            handlers(role === "admin" ? "Made admin" : "Admin role removed", () => setRoleChange(null)),
          );
        }}
      />
      <PlanDialog
        user={planFor}
        plans={plans.data ?? []}
        busy={update.isPending}
        onCancel={() => setPlanFor(null)}
        onConfirm={(plan) => {
          if (!planFor) return;
          update.mutate({ params: { path: { id: planFor.id } }, body: { plan_id: plan.id } }, handlers(`Moved to ${plan.name}`, () => setPlanFor(null)));
        }}
      />
      <DisableDialog
        target={disabling}
        busy={disable.isPending}
        onCancel={() => setDisabling(null)}
        onConfirm={(reason) => {
          if (!disabling) return;
          const { user, ban } = disabling;
          disable.mutate(
            { params: { path: { id: user.id } }, body: { ban, reason } },
            handlers(ban ? `${displayName(user)} banned` : `${displayName(user)} disabled`, () => setDisabling(null)),
          );
        }}
      />
      <EnableDialog
        user={enabling}
        busy={enable.isPending}
        onCancel={() => setEnabling(null)}
        onConfirm={() => {
          if (!enabling) return;
          enable.mutate({ params: { path: { id: enabling.id } } }, handlers(`${displayName(enabling)} can sign in again`, () => setEnabling(null)));
        }}
      />
      <ResetQuotaDialog
        user={resetting}
        busy={reset.isPending}
        onCancel={() => setResetting(null)}
        onConfirm={(ws) => {
          if (!resetting) return;
          const id = resetting.id;
          const [only] = ws;
          reset.mutate(
            { params: { path: { id } }, body: { windows: ws.map(({ window, model_scope }) => ({ window, model_scope })) } },
            {
              ...handlers(only && ws.length === 1 ? `${quotaLabel(only)} quota reset` : `${ws.length} quota windows reset`, () => setResetting(null)),
              onSettled: () => qc.invalidateQueries({ queryKey: ["get", "/api/admin/users/{id}/quotas", { params: { path: { id } } }] }),
            },
          );
        }}
      />
    </>
  );
}
