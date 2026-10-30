"use client";

import { Users } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { PageHeader, Tag } from "@/components/qanat/page";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, errorMessage } from "@/lib/api/client";

const dateFmt = new Intl.DateTimeFormat("en-US", { dateStyle: "medium" });

export default function UsersPage() {
  const qc = useQueryClient();
  const me = api.useQuery("get", "/api/me");
  const users = api.useQuery("get", "/api/admin/users");
  const plans = api.useQuery("get", "/api/admin/plans");
  const update = api.useMutation("patch", "/api/admin/users/{id}");

  const change = (id: string, body: { role?: "user" | "admin"; plan_id?: string }, done: string) =>
    update.mutate(
      { params: { path: { id } }, body },
      {
        onSuccess: () => {
          qc.invalidateQueries({ queryKey: ["get", "/api/admin/users"] });
          qc.invalidateQueries({ queryKey: ["get", "/api/admin/plans"] });
          qc.invalidateQueries({ queryKey: ["get", "/api/me"] });
          qc.invalidateQueries({ queryKey: ["get", "/api/me/quotas"] });
          toast(done);
        },
        onError: (err) => toast.error(errorMessage(err)),
      },
    );

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
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.data?.map((u) => {
                const isRoot = u.username === "root";
                const isMe = u.id === me.data?.id;
                const who = u.name || u.username || u.email;
                return (
                  <TableRow key={u.id}>
                    <TableCell>
                      <div className="flex items-center gap-2 font-semibold">
                        {who}
                        {isRoot && <Tag tone="sand">Root</Tag>}
                        {u.disabled_at && <Tag>Disabled</Tag>}
                      </div>
                      <div className="text-meta text-muted-foreground">{u.email}</div>
                    </TableCell>
                    <TableCell>
                      <Select
                        value={u.role}
                        disabled={isRoot || isMe}
                        onValueChange={(role) => change(u.id, { role: role as "user" | "admin" }, role === "admin" ? "Made admin" : "Admin role removed")}
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
                      <Select value={u.plan_id} onValueChange={(plan_id) => change(u.id, { plan_id }, "Plan assigned")}>
                        <SelectTrigger size="sm" className="w-44" aria-label={`Plan for ${who}`}>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {plans.data?.map((p) => (
                            <SelectItem key={p.id} value={p.id}>
                              {p.name}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </TableCell>
                    <TableCell className="text-end tabular-nums">{u.active_keys}</TableCell>
                    <TableCell data-volatile>{dateFmt.format(new Date(u.created_at))}</TableCell>
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
