"use client";

import { Check } from "lucide-react";
import { useState } from "react";

import { Tag } from "@/components/qanat/page";
import { QuotaBar } from "@/components/qanat/quota-bar";
import { resetText, useNow } from "@/components/qanat/quota-list";
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
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { api, type Plan, type Schemas } from "@/lib/api/client";
import { formatUsd } from "@/lib/format";
import { quotaLabel } from "@/lib/quota-labels";
import { cn } from "@/lib/utils";

export type User = Schemas["User"];
type Role = User["role"];
type QuotaWindow = Schemas["QuotaWindow"];

export const displayName = (u: User) => u.name || u.username || u.email;
const windowKey = (w: Pick<QuotaWindow, "window" | "model_scope">) => `${w.window}/${w.model_scope ?? ""}`;

/** Confirms a role change before it's sent; admins get every admin page. */
export function RoleDialog({ change, busy, onCancel, onConfirm }: {
  change: { user: User; role: Role } | null;
  busy: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const who = change ? displayName(change.user) : "";
  const promote = change?.role === "admin";
  return (
    <AlertDialog open={!!change} onOpenChange={(o) => !o && onCancel()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{promote ? `Make ${who} an admin?` : `Remove ${who}’s admin role?`}</AlertDialogTitle>
          <AlertDialogDescription>
            {promote
              ? "Admins manage users, Plans, provider accounts, prices and the audit log."
              : "They lose the admin pages right away. Their keys, Plan and usage stay as they are."}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction variant={promote ? "default" : "destructive"} disabled={busy} onClick={onConfirm}>
            {promote ? "Make admin" : "Remove admin role"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function PlanCard({ plan, current, selected, onSelect }: { plan: Plan; current: boolean; selected: boolean; onSelect: () => void }) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      onClick={onSelect}
      className={cn(
        "flex w-full flex-col gap-1.5 rounded-lg border p-3 text-start transition-colors outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50",
        selected ? "border-primary bg-brand-soft" : "hover:bg-muted/60",
      )}
    >
      <span className="flex items-center gap-2 font-semibold">
        {plan.name}
        {plan.is_default && <Tag tone="brand">Default</Tag>}
        {current && <Tag>Current</Tag>}
        {selected && <Check className="ms-auto size-4 text-primary" aria-hidden />}
      </span>
      <span className="flex flex-wrap gap-x-3 gap-y-0.5 text-meta tabular-nums">
        {plan.quotas.length === 0 ? (
          <span className="text-muted-foreground">Zero quota: requests are refused</span>
        ) : (
          plan.quotas.map((q) => (
            <span key={windowKey(q)} className="whitespace-nowrap">
              <span className="text-muted-foreground">{quotaLabel(q)}</span> {formatUsd(q.limit_usd)}
            </span>
          ))
        )}
      </span>
      <span className="text-meta text-muted-foreground tabular-nums">
        {plan.limits.rpm || "∞"} rpm · {plan.limits.max_concurrent || "∞"} streams ·{" "}
        {plan.models.length ? `${plan.models.length} model${plan.models.length === 1 ? "" : "s"}` : "All models"} ·{" "}
        {plan.user_count} {plan.user_count === 1 ? "person" : "people"}
      </span>
    </button>
  );
}

/** Pick a Plan from cards that show what each one allows, then confirm. */
export function PlanDialog({ user, plans, busy, onCancel, onConfirm }: {
  user: User | null;
  plans: Plan[];
  busy: boolean;
  onCancel: () => void;
  onConfirm: (plan: Plan) => void;
}) {
  const [picked, setPicked] = useState<string | null>(null);
  const selectedId = picked ?? user?.plan_id;
  const selected = plans.find((p) => p.id === selectedId);
  const changed = !!user && !!selected && selected.id !== user.plan_id;
  return (
    <Dialog
      open={!!user}
      onOpenChange={(o) => {
        if (!o) {
          setPicked(null);
          onCancel();
        }
      }}
    >
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Change Plan for {user ? displayName(user) : ""}</DialogTitle>
          <DialogDescription>
            The new limits apply to their next request. What they already spent this period counts against the new Plan.
          </DialogDescription>
        </DialogHeader>
        <div role="radiogroup" aria-label="Plans" className="-mx-1 flex max-h-[55vh] flex-col gap-2 overflow-y-auto px-1 py-0.5">
          {plans.map((p) => (
            <PlanCard key={p.id} plan={p} current={p.id === user?.plan_id} selected={p.id === selectedId} onSelect={() => setPicked(p.id)} />
          ))}
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => {
              setPicked(null);
              onCancel();
            }}
          >
            Cancel
          </Button>
          <Button
            disabled={!changed || busy}
            onClick={() => {
              if (!selected) return;
              setPicked(null);
              onConfirm(selected);
            }}
          >
            {changed ? `Move to ${selected.name}` : "Pick a different Plan"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function keysLine(n: number) {
  if (n === 0) return "They have no active API keys.";
  return `${n === 1 ? "Their API key is" : `All ${n} of their API keys are`} revoked for good; they'll need new ones.`;
}

/** Disable (reversible, keys kept) or ban (keys revoked, reason required). */
export function DisableDialog({ target, busy, onCancel, onConfirm }: {
  target: { user: User; ban: boolean } | null;
  busy: boolean;
  onCancel: () => void;
  onConfirm: (reason: string) => void;
}) {
  const [reason, setReason] = useState("");
  const ban = !!target?.ban;
  const who = target ? displayName(target.user) : "";
  const close = () => {
    setReason("");
    onCancel();
  };
  return (
    <Dialog open={!!target} onOpenChange={(o) => !o && close()}>
      <DialogContent>
        <form
          className="contents"
          onSubmit={(e) => {
            e.preventDefault();
            if (ban && !reason.trim()) return;
            onConfirm(reason.trim());
            setReason("");
          }}
        >
          <DialogHeader>
            <DialogTitle>{ban ? `Ban ${who}?` : `Disable ${who}?`}</DialogTitle>
            <DialogDescription>
              {ban
                ? `They're signed out and can't sign in again. ${keysLine(target?.user.active_keys ?? 0)} You can lift the ban later.`
                : "They're signed out and can't sign in, and their API keys stop working. Enable them again to restore everything as it was."}
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-2">
            <Label htmlFor="disable-reason">Reason{ban ? "" : " (optional)"}</Label>
            <Input
              id="disable-reason"
              value={reason}
              maxLength={500}
              required={ban}
              autoFocus
              placeholder={ban ? "e.g. Shared their key outside the team" : "e.g. On leave until March"}
              onChange={(e) => setReason(e.target.value)}
            />
            <p className="text-meta text-muted-foreground">Admins see this on the Users page and in the audit log.</p>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={close}>
              Cancel
            </Button>
            <Button type="submit" variant="destructive" disabled={busy || (ban && !reason.trim())}>
              {ban ? "Ban and revoke keys" : "Disable account"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** Confirms lifting a disable or ban. */
export function EnableDialog({ user, busy, onCancel, onConfirm }: { user: User | null; busy: boolean; onCancel: () => void; onConfirm: () => void }) {
  const banned = !!user?.banned_at;
  return (
    <AlertDialog open={!!user} onOpenChange={(o) => !o && onCancel()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {banned ? "Lift the ban on" : "Enable"} {user ? displayName(user) : ""}?
          </AlertDialogTitle>
          <AlertDialogDescription>
            {banned
              ? "They can sign in again. The keys revoked by the ban stay revoked, so they'll create new ones."
              : "They can sign in again and their API keys work again."}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction disabled={busy} onClick={onConfirm}>
            {banned ? "Lift ban" : "Enable account"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function ResetQuotaBody({ user, busy, onCancel, onConfirm }: { user: User; busy: boolean; onCancel: () => void; onConfirm: (ws: QuotaWindow[]) => void }) {
  const quotas = api.useQuery("get", "/api/admin/users/{id}/quotas", { params: { path: { id: user.id } } });
  const now = useNow();
  const windows = quotas.data?.windows ?? [];
  const [picked, setPicked] = useState<Set<string> | null>(null);
  // Windows with spend are picked by default; the admin narrows from there.
  const chosen = picked ?? new Set(windows.filter((w) => w.used_usd > 0).map(windowKey));
  const toggle = (k: string, on: boolean) => {
    const next = new Set(chosen);
    if (on) next.add(k);
    else next.delete(k);
    setPicked(next);
  };
  const selected = windows.filter((w) => chosen.has(windowKey(w)));
  const allOn = windows.length > 0 && selected.length === windows.length;

  return (
    <>
      <DialogHeader>
        <DialogTitle>Reset quota for {displayName(user)}</DialogTitle>
        <DialogDescription>
          The chosen windows start over at $0 right now. Their usage history and reports don&apos;t change.
        </DialogDescription>
      </DialogHeader>
      {quotas.isPending ? (
        <Skeleton className="h-28 w-full" />
      ) : windows.length === 0 ? (
        <p className="text-muted-foreground">Their Plan ({user.plan_name}) has zero quota, so there&apos;s nothing to reset. Change their Plan instead.</p>
      ) : (
        <div className="flex flex-col gap-1">
          <label className="flex items-center gap-3 border-b pb-2 text-meta text-muted-foreground">
            <Checkbox
              checked={allOn ? true : selected.length ? "indeterminate" : false}
              onCheckedChange={(v) => setPicked(v === true ? new Set(windows.map(windowKey)) : new Set())}
              aria-label="Select all windows"
            />
            All windows
          </label>
          <ul className="flex flex-col">
            {windows.map((w) => {
              const k = windowKey(w);
              return (
                <li key={k}>
                  <label className="grid cursor-pointer grid-cols-[auto_6rem_1fr] items-center gap-x-3 gap-y-1 rounded-md py-2 sm:grid-cols-[auto_6rem_1fr_11rem]">
                    <Checkbox checked={chosen.has(k)} onCheckedChange={(v) => toggle(k, v === true)} aria-label={`Reset ${quotaLabel(w)}`} />
                    <span>{quotaLabel(w)}</span>
                    <QuotaBar used={w.used_usd} limit={w.limit_usd} label={`${quotaLabel(w)} quota used`} />
                    <span className="text-end text-meta tabular-nums text-muted-foreground max-sm:col-start-3 max-sm:text-start" data-volatile>
                      {formatUsd(w.used_usd)} of {formatUsd(w.limit_usd)} · {resetText(w, now)}
                    </span>
                  </label>
                </li>
              );
            })}
          </ul>
        </div>
      )}
      <DialogFooter>
        <Button variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button variant="destructive" disabled={busy || selected.length === 0} onClick={() => onConfirm(selected)}>
          {selected.length ? `Reset ${selected.length} window${selected.length === 1 ? "" : "s"}` : "Pick windows to reset"}
        </Button>
      </DialogFooter>
    </>
  );
}

/** Lets the admin choose which quota windows to zero. */
export function ResetQuotaDialog({ user, busy, onCancel, onConfirm }: {
  user: User | null;
  busy: boolean;
  onCancel: () => void;
  onConfirm: (ws: QuotaWindow[]) => void;
}) {
  return (
    <Dialog open={!!user} onOpenChange={(o) => !o && onCancel()}>
      <DialogContent className="sm:max-w-xl">
        {user && <ResetQuotaBody key={user.id} user={user} busy={busy} onCancel={onCancel} onConfirm={onConfirm} />}
      </DialogContent>
    </Dialog>
  );
}
