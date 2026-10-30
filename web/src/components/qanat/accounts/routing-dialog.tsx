/*
 * A provider's account routing, after 9router's per-provider
 * fallbackStrategy setting (https://github.com/decolua/9router,
 * src/sse/services/auth.js). MIT License, Copyright (c) 2024-2026 decolua
 * and contributors. See LICENSE-9router.
 * Qanat change: sticky-balanced (cache-aware, the default) is Qanat's own;
 * round-robin is per user; both are quota-aware (accounts under a headroom
 * threshold are tried last).
 */
"use client";

import { useQueryClient } from "@tanstack/react-query";
import { ChevronRight, ListOrdered, Pin, Shuffle } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { api, errorMessage, type Schemas } from "@/lib/api/client";

type Routing = Schemas["ProviderRouting"];
type Strategy = Routing["strategy"];

/** The chip on a provider card that names its routing and opens the dialog. */
export function RoutingButton({ routing, providerName }: { routing: Routing | undefined; providerName: string }) {
  const [open, setOpen] = useState(false);
  if (!routing) return null;
  const Icon = routing.strategy === "round-robin" ? Shuffle : routing.strategy === "fill-first" ? ListOrdered : Pin;
  const label =
    routing.strategy === "round-robin" ? `Round-robin · ${routing.sticky_limit}` : routing.strategy === "fill-first" ? "Fill-first" : "Sticky-balanced";
  return (
    <>
      <span className="text-xs text-muted-foreground">Routing</span>
      <Button
        variant="ghost"
        size="sm"
        className="h-7 min-w-0 gap-1.5 px-2 text-xs"
        onClick={() => setOpen(true)}
        aria-label={`Routing for ${providerName}: ${label}. Change`}
      >
        <Icon aria-hidden className="size-3.5 shrink-0" />
        <span className="truncate">{label}</span>
        <ChevronRight aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
      </Button>
      {open && <RoutingDialog routing={routing} providerName={providerName} onOpenChange={setOpen} />}
    </>
  );
}

function RoutingDialog({ routing, providerName, onOpenChange }: { routing: Routing; providerName: string; onOpenChange: (o: boolean) => void }) {
  const qc = useQueryClient();
  const save = api.useMutation("put", "/api/admin/provider-routing/{provider}");
  const [strategy, setStrategy] = useState<Strategy>(routing.strategy);
  const [sticky, setSticky] = useState(String(routing.sticky_limit));
  const [threshold, setThreshold] = useState(String(routing.min_headroom_pct));

  const stickyN = Number(sticky);
  const thresholdN = Number(threshold);
  const stickyBad = !Number.isInteger(stickyN) || stickyN < 1 || stickyN > 1000;
  const thresholdBad = !Number.isInteger(thresholdN) || thresholdN < 0 || thresholdN > 100;
  const rr = strategy === "round-robin";
  const balanced = rr || strategy === "sticky-balanced";

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if ((rr && stickyBad) || thresholdBad) return;
    save.mutate(
      { params: { path: { provider: routing.provider } }, body: { strategy, sticky_limit: stickyN, min_headroom_pct: thresholdN } },
      {
        onSuccess: () => {
          qc.invalidateQueries({ queryKey: ["get", "/api/admin/provider-routing"] });
          toast(`${providerName} routing saved. The gateway applies it within seconds.`);
          onOpenChange(false);
        },
        onError: (err) => toast.error(errorMessage(err)),
      },
    );
  };

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>{providerName} routing</DialogTitle>
            <DialogDescription>How the gateway picks among this provider&apos;s accounts. A failed account always fails over to the next one.</DialogDescription>
          </DialogHeader>

          <div className="grid gap-1.5">
            <Label htmlFor="routing-strategy">Strategy</Label>
            <Select value={strategy} onValueChange={(v) => setStrategy(v as Strategy)}>
              <SelectTrigger id="routing-strategy" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="sticky-balanced">Sticky-balanced (recommended)</SelectItem>
                <SelectItem value="round-robin">Round-robin</SelectItem>
                <SelectItem value="fill-first">Fill-first</SelectItem>
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">
              {strategy === "sticky-balanced"
                ? "Each user stays on one account, so the provider's prompt cache for their conversation stays warm. New users, and users idle for 5+ minutes, go to the least-loaded account, weighted by quota left."
                : rr
                  ? "Each user keeps an account for a few requests, then moves to the account used longest ago. Spreads load evenly, but conversations hop accounts and lose prompt cache."
                  : "Each user stays on their last working account; new users start at the top of the pool. Moves on only when an account fails."}
            </p>
          </div>

          {rr && (
            <div className="grid gap-1.5">
              <Label htmlFor="routing-sticky">Requests per account</Label>
              <Input id="routing-sticky" type="number" inputMode="numeric" min={1} max={1000} value={sticky} onChange={(e) => setSticky(e.target.value)} aria-invalid={stickyBad} className="w-32" />
              <p className={stickyBad ? "text-xs text-destructive-text" : "text-xs text-muted-foreground"}>
                {stickyBad ? "Enter a whole number from 1 to 1000." : "Requests in a row a user sends to one account before rotating. Higher keeps the provider's prompt cache warmer."}
              </p>
            </div>
          )}

          <div className="grid gap-1.5">
            <Label htmlFor="routing-threshold">Low-quota threshold</Label>
            <div className="flex items-center gap-2">
              <Input id="routing-threshold" type="number" inputMode="numeric" min={0} max={100} value={threshold} onChange={(e) => setThreshold(e.target.value)} aria-invalid={thresholdBad} className="w-32" />
              <span className="text-sm text-muted-foreground">% left</span>
            </div>
            <p className={thresholdBad ? "text-xs text-destructive-text" : "text-xs text-muted-foreground"}>
              {thresholdBad
                ? "Enter a whole number from 0 to 100."
                : balanced
                  ? "Accounts with less subscription quota left are tried only after every other account; a user pinned to one moves. 0 turns this off."
                  : "Not used by fill-first, which keeps pool order."}
            </p>
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={save.isPending || (rr && stickyBad) || thresholdBad}>
              Save routing
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
