/*
 * One pool account, as a list row or a grid card. Ported from 9router
 * (https://github.com/decolua/9router), src/app/(dashboard)/dashboard/providers/[id]/ConnectionRow.js
 * and usage/components/ProviderLimits/ProviderLimitCard.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat change: the row carries the account's provider limits beside its
 * status; selection feeds the page's bulk bar; less-used actions sit in a menu.
 */
"use client";

import { useQueryClient } from "@tanstack/react-query";
import { ChevronDown, ChevronUp, Hourglass, KeyRound, Loader2, Lock, MoreHorizontal, Pencil, Play, Trash2, Unplug } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";

import { Checkbox, Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/qanat/accounts/animated";
import { AccountLimitsView, LimitsMeta, RefreshLimitsButton } from "@/components/qanat/accounts/limit-bars";
import type { Account, AccountLimits } from "@/components/qanat/accounts/limits";
import { ProxyChoiceOptions } from "@/components/qanat/proxy-options";
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { api, errorMessage, type Schemas } from "@/lib/api/client";
import { cn } from "@/lib/utils";

export type ItemProps = {
  account: Account;
  providerName: string;
  logo: string;
  limits: AccountLimits | undefined;
  limitsPending: boolean;
  supported: boolean;
  refreshing: boolean;
  onRefresh: () => void;
  selected: boolean;
  onSelect: (on: boolean) => void;
  /** Pool-order arrows show only while the list is in pool order. */
  reorder: { first: boolean; last: boolean } | null;
  testing: boolean;
  proxies: Schemas["Proxy"][];
  groups: Schemas["ProxyGroup"][];
  inherit: string;
  onEdit: (a: Account) => void;
  now: number;
};

function useAccountActions(account: Account) {
  const qc = useQueryClient();
  const invalidate = () => qc.invalidateQueries({ queryKey: ["get", "/api/admin/accounts"] });
  const patch = api.useMutation("patch", "/api/admin/accounts/{id}");
  const move = api.useMutation("post", "/api/admin/accounts/{id}/move");
  const test = api.useMutation("post", "/api/admin/accounts/{id}/test");
  const del = api.useMutation("delete", "/api/admin/accounts/{id}");
  const path = { params: { path: { id: account.id } } };
  const name = account.label || account.id;

  return {
    name,
    patching: patch.isPending,
    moving: move.isPending,
    testing: test.isPending,
    setProxy: (proxy: string) =>
      patch.mutate(
        { ...path, body: { proxy } },
        { onSuccess: () => (invalidate(), toast("Proxy updated. New requests use it within seconds.")), onError: (e) => toast.error(errorMessage(e)) },
      ),
    setEnabled: (on: boolean) =>
      patch.mutate(
        { ...path, body: { disabled: !on } },
        { onSuccess: () => (invalidate(), toast(on ? "Account enabled." : "Account disabled. It takes no new traffic.")), onError: (e) => toast.error(errorMessage(e)) },
      ),
    move: (direction: "up" | "down") =>
      move.mutate({ ...path, body: { direction } }, { onSuccess: () => invalidate(), onError: (e) => toast.error(errorMessage(e)) }),
    test: () =>
      test.mutate(path, {
        onSuccess: (res) => {
          invalidate();
          if (res.ok) toast(`${name}: credential valid — ${res.status} in ${res.latency_ms}ms.`);
          else toast.error(`${name}: ${res.error || "the credential was rejected."}`);
        },
        onError: (e) => toast.error(errorMessage(e)),
      }),
    remove: () => del.mutate(path, { onSuccess: () => (invalidate(), toast("Account removed.")), onError: (e) => toast.error(errorMessage(e)) }),
  };
}

/** Countdown shown while an account sits out a rate-limit cooldown. */
function CooldownTimer({ until }: { until: string }) {
  const [remaining, setRemaining] = useState("");

  useEffect(() => {
    const update = () => {
      const diff = new Date(until).getTime() - Date.now();
      if (diff <= 0) {
        setRemaining("");
        return;
      }
      const s = Math.floor(diff / 1000);
      if (s < 60) setRemaining(`${s}s`);
      else if (s < 3600) setRemaining(`${Math.floor(s / 60)}m ${s % 60}s`);
      else setRemaining(`${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`);
    };
    update();
    const t = setInterval(update, 1000);
    return () => clearInterval(t);
  }, [until]);

  if (!remaining) return null;
  return (
    <span data-volatile className="font-mono text-xs text-warning-text">
      {remaining}
    </span>
  );
}

/** Status per design.md §5: shape and words, not color alone. */
function StatusBadges({ account, limits }: { account: Account; limits: AccountLimits | undefined }) {
  const disabled = !!account.disabled_at;
  const cooling = account.state === "cooldown" && account.cooldown_until && new Date(account.cooldown_until) > new Date();
  const expired = account.expires_at && new Date(account.expires_at) < new Date();
  return (
    <>
      {disabled ? (
        <Badge variant="outline">disabled</Badge>
      ) : account.state === "disconnected" ? (
        <Badge variant="destructive">
          <Unplug aria-hidden />
          disconnected
        </Badge>
      ) : cooling ? (
        <Badge variant="outline" className="border-warning/40 text-warning-text">
          <Hourglass aria-hidden />
          cooldown
        </Badge>
      ) : limits?.limit_reached ? (
        <Badge variant="outline" className="border-destructive/40 text-destructive-text">
          limit reached
        </Badge>
      ) : (
        <Badge variant="secondary">active</Badge>
      )}
      {cooling && account.cooldown_until && <CooldownTimer until={account.cooldown_until} />}
      {expired && <Badge variant="destructive">expired</Badge>}
      {account.last_check_ok === false && account.last_check_error && (
        <span className="max-w-[260px] truncate text-xs text-destructive-text" title={account.last_check_error}>
          {account.last_check_error}
        </span>
      )}
      {account.last_refresh_error && (
        <span className="max-w-[260px] truncate text-xs text-destructive-text" title={account.last_refresh_error}>
          {account.last_refresh_error}
        </span>
      )}
    </>
  );
}

function Identity({ account, logo, providerName }: { account: Account; logo: string; providerName: string }) {
  const kind = account.auth_kind === "api_key" ? "API key" : account.auth_kind === "oauth" ? "OAuth" : "Token";
  const KindIcon = account.auth_kind === "api_key" ? KeyRound : Lock;
  return (
    <div className="flex min-w-0 items-center gap-2.5">
      <span aria-hidden className="grid size-8 shrink-0 place-items-center rounded-md bg-brand-soft">
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img src={logo} alt="" className="max-h-5 max-w-5 rounded object-contain" />
      </span>
      <div className="min-w-0">
        <p className="flex min-w-0 items-center gap-1.5 text-sm font-medium">
          <span className="truncate">{account.label || account.id}</span>
          <span className="shrink-0 text-xs font-normal text-muted-foreground">#{account.position}</span>
        </p>
        <p className="flex min-w-0 items-center gap-1 text-xs text-muted-foreground">
          <KindIcon aria-hidden className="size-3 shrink-0" />
          <span className="shrink-0">{kind}</span>
          <span aria-hidden>·</span>
          <span className="truncate" title={account.email ?? providerName}>
            {account.email ?? providerName}
          </span>
        </p>
      </div>
    </div>
  );
}

function MoreMenu({ account, actions, onEdit, onDelete }: { account: Account; actions: ReturnType<typeof useAccountActions>; onEdit: () => void; onDelete: () => void }) {
  return (
    <Menu>
      <MenuTrigger asChild>
        <Button variant="ghost" size="icon" className="size-8" aria-label={`More actions for ${actions.name}`}>
          <MoreHorizontal className="size-4" />
        </Button>
      </MenuTrigger>
      <MenuContent align="end">
        <MenuItem onSelect={onEdit}>
          <Pencil />
          Edit
        </MenuItem>
        <MenuItem onSelect={() => actions.test()} disabled={!account.has_credentials}>
          <Play />
          Test credential
        </MenuItem>
        <MenuSeparator />
        <MenuItem variant="destructive" onSelect={onDelete}>
          <Trash2 />
          Remove from pool
        </MenuItem>
      </MenuContent>
    </Menu>
  );
}

function DeleteDialog({ open, onOpenChange, name, onConfirm }: { open: boolean; onOpenChange: (o: boolean) => void; name: string; onConfirm: () => void }) {
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Remove account</AlertDialogTitle>
          <AlertDialogDescription>
            Remove &quot;{name}&quot; from the pool? In-flight streams complete, but no new requests route to it.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction onClick={onConfirm}>Remove</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function ProxyPicker({ account, actions, proxies, groups, inherit, className }: { account: Account; actions: ReturnType<typeof useAccountActions>; proxies: Schemas["Proxy"][]; groups: Schemas["ProxyGroup"][]; inherit: string; className?: string }) {
  return (
    <Select value={account.proxy} onValueChange={actions.setProxy} disabled={actions.patching}>
      <SelectTrigger size="sm" className={cn("w-36 min-w-0", className)} aria-label={`Proxy for ${actions.name}`}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <ProxyChoiceOptions proxies={proxies} groups={groups} inherit={inherit} />
      </SelectContent>
    </Select>
  );
}

function ReorderArrows({ reorder, actions }: { reorder: NonNullable<ItemProps["reorder"]>; actions: ReturnType<typeof useAccountActions> }) {
  return (
    <div className="flex flex-col">
      <button
        type="button"
        onClick={() => actions.move("up")}
        disabled={reorder.first || actions.moving}
        aria-label={`Move ${actions.name} up`}
        className="rounded p-0.5 text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-30"
      >
        <ChevronUp className="size-3.5" />
      </button>
      <button
        type="button"
        onClick={() => actions.move("down")}
        disabled={reorder.last || actions.moving}
        aria-label={`Move ${actions.name} down`}
        className="rounded p-0.5 text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-30"
      >
        <ChevronDown className="size-3.5" />
      </button>
    </div>
  );
}

/** A dense list row: identity and status, headline limits, then actions. */
export function AccountRow(p: ItemProps) {
  const actions = useAccountActions(p.account);
  const [confirm, setConfirm] = useState(false);
  const disabled = !!p.account.disabled_at;
  const busyTest = actions.testing || p.testing;

  return (
    <div
      data-selected={p.selected || undefined}
      className={cn(
        "grid grid-cols-[auto_1fr_auto] items-center gap-x-3 gap-y-2 rounded-md border border-transparent px-2 py-2 transition-colors hover:bg-accent/40 data-[selected]:border-primary/30 data-[selected]:bg-accent/50",
        "lg:grid-cols-[auto_minmax(0,1.1fr)_minmax(0,1fr)_auto]",
      )}
    >
      <div className="flex items-center gap-1.5">
        <Checkbox checked={p.selected} onCheckedChange={(v) => p.onSelect(v === true)} aria-label={`Select ${actions.name}`} />
        {p.reorder && <ReorderArrows reorder={p.reorder} actions={actions} />}
      </div>

      <div className={cn("min-w-0", disabled && "opacity-60")}>
        <Identity account={p.account} logo={p.logo} providerName={p.providerName} />
        <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 ps-[42px]">
          <StatusBadges account={p.account} limits={p.limits} />
        </div>
      </div>

      <div className={cn("col-span-3 col-start-1 flex min-w-0 items-center gap-1 ps-7 lg:col-span-1 lg:col-start-auto lg:ps-0", disabled && "opacity-60")}>
        <div className="min-w-0 flex-1">
          <AccountLimitsView supported={p.supported} limits={p.limits} pending={p.limitsPending} compact now={p.now} />
        </div>
        {p.supported && <RefreshLimitsButton busy={p.refreshing} onClick={p.onRefresh} label={`Refresh limits for ${actions.name}`} />}
      </div>

      <div className="col-start-3 row-start-1 flex items-center justify-end gap-1 lg:col-start-4">
        <ProxyPicker account={p.account} actions={actions} proxies={p.proxies} groups={p.groups} inherit={p.inherit} className="hidden xl:flex" />
        <Button variant="ghost" size="icon" className="size-8" aria-label={`Test ${actions.name}`} onClick={actions.test} disabled={busyTest || !p.account.has_credentials}>
          {busyTest ? <Loader2 className="size-4 animate-spin motion-reduce:animate-none" /> : <Play className="size-4" />}
        </Button>
        <MoreMenu account={p.account} actions={actions} onEdit={() => p.onEdit(p.account)} onDelete={() => setConfirm(true)} />
        <Switch checked={!disabled} onCheckedChange={actions.setEnabled} aria-label={`${disabled ? "Enable" : "Disable"} ${actions.name}`} />
      </div>
      <DeleteDialog open={confirm} onOpenChange={setConfirm} name={actions.name} onConfirm={actions.remove} />
    </div>
  );
}

/** A card: the account's limits get room, every window listed. */
export function AccountCard(p: ItemProps) {
  const actions = useAccountActions(p.account);
  const [confirm, setConfirm] = useState(false);
  const disabled = !!p.account.disabled_at;
  const busyTest = actions.testing || p.testing;

  return (
    <div
      data-selected={p.selected || undefined}
      className="flex h-full flex-col gap-3 rounded-[10px] border bg-card p-3.5 transition-colors data-[selected]:border-primary/40 data-[selected]:bg-accent/30"
    >
      <div className="flex items-start justify-between gap-2">
        <div className={cn("flex min-w-0 items-start gap-2", disabled && "opacity-60")}>
          <Checkbox className="mt-2" checked={p.selected} onCheckedChange={(v) => p.onSelect(v === true)} aria-label={`Select ${actions.name}`} />
          <Identity account={p.account} logo={p.logo} providerName={p.providerName} />
        </div>
        <div className="flex shrink-0 items-center gap-0.5">
          {p.reorder && <ReorderArrows reorder={p.reorder} actions={actions} />}
          <MoreMenu account={p.account} actions={actions} onEdit={() => p.onEdit(p.account)} onDelete={() => setConfirm(true)} />
        </div>
      </div>

      <div className={cn("flex flex-wrap items-center gap-x-2 gap-y-1", disabled && "opacity-60")}>
        <StatusBadges account={p.account} limits={p.limits} />
      </div>

      <div className={cn("flex flex-1 flex-col gap-2 border-t pt-3", disabled && "opacity-60")}>
        <AccountLimitsView supported={p.supported} limits={p.limits} pending={p.limitsPending} now={p.now} />
        {p.supported && <LimitsMeta limits={p.limits} now={p.now} />}
      </div>

      <div className="flex items-center justify-between gap-2 border-t pt-2.5">
        <div className="min-w-0 flex-1">
          <ProxyPicker account={p.account} actions={actions} proxies={p.proxies} groups={p.groups} inherit={p.inherit} />
        </div>
        <div className="flex shrink-0 items-center gap-1">
          {p.supported && <RefreshLimitsButton busy={p.refreshing} onClick={p.onRefresh} label={`Refresh limits for ${actions.name}`} />}
          <Button variant="ghost" size="icon" className="size-7" aria-label={`Test ${actions.name}`} onClick={actions.test} disabled={busyTest || !p.account.has_credentials}>
            {busyTest ? <Loader2 className="size-3.5 animate-spin motion-reduce:animate-none" /> : <Play className="size-3.5" />}
          </Button>
          <Switch checked={!disabled} onCheckedChange={actions.setEnabled} aria-label={`${disabled ? "Enable" : "Disable"} ${actions.name}`} />
        </div>
      </div>
      <DeleteDialog open={confirm} onOpenChange={setConfirm} name={actions.name} onConfirm={actions.remove} />
    </div>
  );
}
