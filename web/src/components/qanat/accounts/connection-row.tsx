/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/providers/[id]/ConnectionRow.js and the
 * CooldownTimer inside components/ConnectionsCard.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat change: the proxy picker is Qanat's §8.2 Select; status adds the
 * account's Test result; the arrows swap pool position; delete confirms with
 * Qanat's AlertDialog.
 */
"use client";

import { useEffect, useState } from "react";
import { ChevronDown, ChevronUp, KeyRound, Loader2, Lock, Pencil, Play, Trash2 } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { ProxyChoiceOptions, choiceLabel } from "@/components/qanat/proxy-options";
import { api, errorMessage, type Schemas } from "@/lib/api/client";

type Account = Schemas["Account"];

/** Countdown shown while an account sits out a rate-limit cooldown. */
export function CooldownTimer({ until }: { until: string }) {
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
    <span data-volatile className="inline-flex items-center gap-1 font-mono text-xs text-warning-text">
      ⏱ {remaining}
    </span>
  );
}

export function ConnectionRow({
  account,
  proxies,
  groups,
  inherit,
  isFirst,
  isLast,
  testing,
  onEdit,
}: {
  account: Account;
  proxies: Schemas["Proxy"][];
  groups: Schemas["ProxyGroup"][];
  /** Resolved label of what "inherit" falls through to for this account. */
  inherit: string;
  isFirst: boolean;
  isLast: boolean;
  testing: boolean;
  onEdit: (account: Account) => void;
}) {
  const qc = useQueryClient();
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["get", "/api/admin/accounts"] });
  };

  const setProxy = api.useMutation("patch", "/api/admin/accounts/{id}");
  const toggle = api.useMutation("patch", "/api/admin/accounts/{id}");
  const move = api.useMutation("post", "/api/admin/accounts/{id}/move");
  const test = api.useMutation("post", "/api/admin/accounts/{id}/test");
  const del = api.useMutation("delete", "/api/admin/accounts/{id}");

  const disabled = !!account.disabled_at;
  const cooldown = account.state === "cooldown" && account.cooldown_until && new Date(account.cooldown_until) > new Date();
  const hasProxy = account.proxy !== "inherit";
  const kind = account.auth_kind === "api_key" ? "API key" : "Token";

  const handleProxy = (value: string) => {
    setProxy.mutate(
      { params: { path: { id: account.id } }, body: { proxy: value } },
      {
        onSuccess: () => {
          invalidate();
          toast("Proxy updated. New requests use it within seconds.");
        },
        onError: (err) => toast.error(errorMessage(err)),
      },
    );
  };

  const handleToggle = (checked: boolean) => {
    toggle.mutate(
      { params: { path: { id: account.id } }, body: { disabled: !checked } },
      {
        onSuccess: () => {
          invalidate();
          toast(checked ? "Account enabled." : "Account disabled. It takes no new traffic.");
        },
        onError: (err) => toast.error(errorMessage(err)),
      },
    );
  };

  const handleTest = () => {
    test.mutate(
      { params: { path: { id: account.id } } },
      {
        onSuccess: (res) => {
          invalidate();
          if (res.ok) toast(`Credential valid — ${res.status} in ${res.latency_ms}ms.`);
          else toast.error(res.error || "The credential was rejected.");
        },
        onError: (err) => toast.error(errorMessage(err)),
      },
    );
  };

  const handleMove = (direction: "up" | "down") => {
    move.mutate(
      { params: { path: { id: account.id } }, body: { direction } },
      {
        onSuccess: () => {
          invalidate();
          toast("Order updated.");
        },
        onError: (err) => toast.error(errorMessage(err)),
      },
    );
  };

  return (
    <div className={`group flex flex-col gap-3 rounded-lg p-2 transition-colors hover:bg-accent/50 sm:flex-row sm:items-center sm:justify-between ${disabled ? "opacity-60" : ""}`}>
      <div className="flex min-w-0 flex-1 items-start gap-2 sm:items-center">
        <div className="flex flex-col" aria-hidden>
          <button
            onClick={() => handleMove("up")}
            disabled={isFirst || move.isPending}
            aria-label={`Move ${account.label || account.id} up`}
            className="rounded p-0.5 text-muted-foreground hover:bg-sidebar hover:text-brand-text disabled:cursor-not-allowed disabled:opacity-30"
          >
            <ChevronUp className="size-3.5" />
          </button>
          <button
            onClick={() => handleMove("down")}
            disabled={isLast || move.isPending}
            aria-label={`Move ${account.label || account.id} down`}
            className="rounded p-0.5 text-muted-foreground hover:bg-sidebar hover:text-brand-text disabled:cursor-not-allowed disabled:opacity-30"
          >
            <ChevronDown className="size-3.5" />
          </button>
        </div>
        {account.auth_kind === "api_key" ? (
          <KeyRound aria-hidden className="size-4 shrink-0 text-muted-foreground" />
        ) : (
          <Lock aria-hidden className="size-4 shrink-0 text-muted-foreground" />
        )}
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium">
            {account.label || account.id}
            <span className="ms-2 text-xs font-normal text-muted-foreground">#{account.position}</span>
          </p>
          <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1">
            {disabled ? (
              <Badge variant="outline">disabled</Badge>
            ) : account.state === "disconnected" ? (
              <Badge variant="destructive">disconnected</Badge>
            ) : account.state === "cooldown" ? (
              <Badge variant="outline" className="border-warning/40 text-warning-text">
                cooldown
              </Badge>
            ) : (
              <Badge variant="secondary">active</Badge>
            )}
            {cooldown && account.cooldown_until && <CooldownTimer until={account.cooldown_until} />}
            {account.last_check_ok === true && (
              <span data-volatile className="text-xs text-success">
                ✓ checked
              </span>
            )}
            {account.last_check_ok === false && account.last_check_error && (
              <span className="max-w-[300px] truncate text-xs text-destructive-text" title={account.last_check_error}>
                {account.last_check_error}
              </span>
            )}
            {account.expires_at && new Date(account.expires_at) < new Date() && (
              <Badge variant="destructive">expired</Badge>
            )}
            <span className="text-xs text-muted-foreground">{kind}</span>
            {account.base_url && (
              <span className="max-w-[260px] truncate font-mono text-xs text-muted-foreground" title={account.base_url}>
                {account.base_url.replace(/^https?:\/\//, "")}
              </span>
            )}
          </div>
          {hasProxy && (
            <p className="mt-1 text-xs text-muted-foreground">
              Proxy: {choiceLabel(account.proxy, proxies, groups)}
            </p>
          )}
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-1 sm:justify-end">
        <Select value={account.proxy} onValueChange={handleProxy} disabled={setProxy.isPending}>
          <SelectTrigger size="sm" className="min-w-32" aria-label={`Proxy for ${account.label || account.id}`}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <ProxyChoiceOptions proxies={proxies} groups={groups} inherit={inherit} />
          </SelectContent>
        </Select>
        <Button variant="ghost" size="icon" aria-label={`Test ${account.label || account.id}`} onClick={handleTest} disabled={test.isPending || testing}>
          {test.isPending || testing ? <Loader2 className="size-4 animate-spin" /> : <Play className="size-4" />}
        </Button>
        <Button variant="ghost" size="icon" aria-label={`Edit ${account.label || account.id}`} onClick={() => onEdit(account)}>
          <Pencil className="size-4" />
        </Button>
        <AlertDialog>
          <AlertDialogTrigger asChild>
            <Button variant="ghost" size="icon" aria-label={`Delete ${account.label || account.id}`}>
              <Trash2 className="size-4 text-muted-foreground hover:text-destructive-text" />
            </Button>
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Disconnect account</AlertDialogTitle>
              <AlertDialogDescription>
                Remove &quot;{account.label || account.id}&quot; from the pool? In-flight streams will complete, but no new requests will route to it.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction
                onClick={() => {
                  del.mutate(
                    { params: { path: { id: account.id } } },
                    {
                      onSuccess: () => {
                        invalidate();
                        toast("Account removed.");
                      },
                      onError: (err) => toast.error(errorMessage(err)),
                    },
                  );
                }}
              >
                Remove
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
        <Switch checked={!disabled} onCheckedChange={handleToggle} aria-label={`${disabled ? "Enable" : "Disable"} ${account.label || account.id}`} />
      </div>
    </div>
  );
}
