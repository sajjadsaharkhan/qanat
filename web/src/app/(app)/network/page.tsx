/*
 * Live network analytics per provider, after Tower's Network page
 * (github.com/ghhrmnzdh/tower, Dashboard.swift NetworkPage): a verdict hero,
 * the dual-series latency chart and live traffic, here per provider.
 */
"use client";

import { ReducedMotionConfig } from "@animate-ui/react/utils/motion";
import { motion } from "motion/react";
import { Radar, RefreshCw } from "lucide-react";
import { useIsFetching, useQueryClient } from "@tanstack/react-query";
import { memo, useDeferredValue, useId, useState } from "react";

import { Panel, PathChart, TrafficChart, TTFTChart } from "@/components/qanat/network/charts";
import { ModelStatus } from "@/components/qanat/network/model-status";
import { Ago } from "@/components/qanat/network/clock";
import { Reveal, Rolling, usePress } from "@/components/qanat/network/motion";
import { AccountsTable, ErrorsPanel, LiveTraffic, ProviderNetworkCard, RoutesTable } from "@/components/qanat/network/provider-network";
import { StatusTile } from "@/components/qanat/network/status";
import { PageHeader, SectionHeader } from "@/components/qanat/page";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { api, errorMessage, type Schemas } from "@/lib/api/client";
import { fmtMs, pct, verdict, worst } from "@/lib/network";
import { cn } from "@/lib/utils";

const POLL_MS = 5_000;

// The query this page polls; Controls watches its fetch state on its own.
const networkQuery = api.queryOptions("get", "/api/admin/network", {});

function Reading({ label, children, unit }: { label: string; children: React.ReactNode; unit?: string }) {
  return (
    <div className="min-w-24 px-4 first:ps-0 sm:border-s sm:border-foreground/10 sm:first:border-s-0">
      <p className="text-meta font-medium text-muted-foreground">{label}</p>
      <p className="mt-0.5 flex items-baseline text-3xl leading-none font-semibold tracking-tight tabular-nums">
        {children}
        {unit && <span className="ms-1 text-sm font-medium text-muted-foreground">{unit}</span>}
      </p>
    </div>
  );
}

/** Hero: the worst provider's verdict, and the totals beside it. */
const Hero = memo(function Hero({ data }: { data: Schemas["NetworkOverview"] }) {
  const live = data.providers.filter((p) => p.status.state !== "no_accounts");
  const w = worst(live);
  const allGood = live.length > 0 && live.every((p) => p.status.state === "healthy");
  const v = !w
    ? { title: "No providers connected", detail: "Connect an account to see its network.", tone: "neutral" as const }
    : allGood
      ? { title: "All providers healthy", detail: `${live.map((p) => p.name).join(", ")} reachable and answering normally.`, tone: "ok" as const }
      : (() => {
          const wv = verdict(w, data.internet);
          // Name the provider unless the title already does.
          return { ...wv, title: live.length > 1 && !wv.title.includes(w.name) ? `${w.name}: ${wv.title}` : wv.title };
        })();
  const attempts = live.reduce((n, p) => n + p.stats.attempts, 0);
  const errors = live.reduce((n, p) => n + p.stats.errors, 0);
  const rpm = live.reduce((n, p) => n + p.stats.per_minute, 0);
  const inet = data.internet?.ok ? data.internet.total_ms : null;
  return (
    <Panel
      className={cn(
        "relative flex flex-wrap items-center justify-between gap-6 overflow-hidden p-5 sm:p-6",
        "before:absolute before:inset-y-0 before:start-0 before:w-1",
        v.tone === "warning" ? "before:bg-warning" : v.tone === "destructive" ? "before:bg-destructive" : "before:bg-transparent",
      )}
    >
      <div className="flex min-w-0 items-center gap-4" aria-live="polite">
        <StatusTile state={allGood ? "healthy" : (w?.status.state ?? "no_accounts")} tone={v.tone} />
        <div className="min-w-0">
          <p className="text-xl font-bold tracking-tight">{v.title}</p>
          <p className="text-sm text-muted-foreground">{v.detail}</p>
          <p className="mt-1 text-meta text-muted-foreground">
            {data.gateways.length === 0
              ? "No gateway is reporting"
              : `${data.gateways.length} gateway${data.gateways.length > 1 ? "s" : ""} reporting · checked `}
            {data.gateways.length > 0 && <Ago iso={data.internet?.at} />}
          </p>
        </div>
      </div>
      <div className="flex flex-wrap gap-y-4">
        <Reading label="Our internet" unit={inet != null && inet < 1000 ? "ms" : undefined}>
          {inet == null ? "—" : inet < 1000 ? <Rolling value={inet} /> : fmtMs(inet)}
        </Reading>
        <Reading label="Active connections">
          <Rolling value={live.reduce((n, p) => n + p.active_connections, 0)} />
        </Reading>
        <Reading label="Requests / min">
          <Rolling value={rpm} decimals={1} />
        </Reading>
        <Reading label="Error rate">{attempts ? pct(errors / attempts) : "—"}</Reading>
      </div>
    </Panel>
  );
});

/** Controls: live indicator, auto-refresh switch and the manual refresh. */
function Controls({ live, onLive }: { live: boolean; onLive: (v: boolean) => void }) {
  const id = useId();
  const client = useQueryClient();
  // Re-renders on fetch start and end only; the page itself doesn't.
  const fetching = useIsFetching({ queryKey: networkQuery.queryKey }) > 0;
  const updatedAt = client.getQueryState(networkQuery.queryKey)?.dataUpdatedAt;
  const onRefresh = () => void client.refetchQueries({ queryKey: networkQuery.queryKey });
  const press = usePress("button");
  return (
    <div className="flex flex-wrap items-center gap-3">
      <span className="inline-flex items-center gap-2 text-meta tabular-nums text-muted-foreground" aria-live="off">
        <span className="relative flex size-2" aria-hidden>
          {live && <span className="absolute inline-flex size-full rounded-full bg-success opacity-60 motion-safe:animate-ping" />}
          <span className={cn("relative inline-flex size-2 rounded-full", live ? "bg-success" : "bg-muted-foreground")} />
        </span>
        {live ? "Live" : "Paused"} · updated {updatedAt ? <Ago iso={new Date(updatedAt).toISOString()} /> : "never"}
      </span>
      <div className="flex items-center gap-2 rounded-lg border border-foreground/12 bg-card px-2.5 py-1.5 shadow-xs">
        <Switch id={id} checked={live} onCheckedChange={onLive} />
        <Label htmlFor={id} className="text-sm font-medium">
          Auto-refresh
        </Label>
      </div>
      <Button asChild variant="outline" size="sm" className="border-foreground/20 bg-card shadow-xs">
        <motion.button type="button" onClick={onRefresh} disabled={fetching} aria-label="Refresh now" {...press}>
          <RefreshCw className={cn(fetching && "motion-safe:animate-spin")} />
          {fetching ? "Refreshing…" : "Refresh"}
        </motion.button>
      </Button>
    </div>
  );
}

export default function NetworkPage() {
  const [live, setLive] = useState(true);
  // Only data and errors re-render the page: fetch start/end don't (Controls
  // watches those), so each poll costs one render, at low priority.
  const q = api.useQuery("get", "/api/admin/network", {}, { refetchInterval: live ? POLL_MS : false, notifyOnChangeProps: ["data", "error"] });
  const deferred = useDeferredValue(q.data);
  const [picked, setPicked] = useState<string | null>(null);

  const description =
    "Is it our network, the path, or the provider? Handshake probes every 10 s, plus what real requests see. Live, last 30 minutes; Model status below covers 24 hours.";
  const controls = <Controls live={live} onLive={setLive} />;

  if (!deferred && !q.error) {
    return (
      <>
        <PageHeader title="Network" icon={Radar} description={description} actions={controls} />
        <Skeleton className="h-32 w-full rounded-2xl" />
        <div className="mt-8 grid gap-4 lg:grid-cols-3">
          <Skeleton className="h-80 rounded-2xl" />
          <Skeleton className="h-80 rounded-2xl" />
          <Skeleton className="h-80 rounded-2xl" />
        </div>
      </>
    );
  }
  if (!deferred) {
    return (
      <>
        <PageHeader title="Network" icon={Radar} actions={controls} />
        <p role="alert" className="text-destructive-text">
          {errorMessage(q.error)}
        </p>
      </>
    );
  }

  const data = deferred;
  // Default to the provider most in need, else the first with accounts.
  const active = data.providers.filter((p) => p.status.state !== "no_accounts");
  const selected = data.providers.find((p) => p.id === picked) ?? worst(active) ?? data.providers[0];

  return (
    <ReducedMotionConfig>
      <PageHeader title="Network" icon={Radar} description={description} actions={controls} />
      <Reveal>
        <Hero data={data} />
      </Reveal>

      <SectionHeader>Providers</SectionHeader>
      <div className="grid gap-4 lg:grid-cols-3">
        {data.providers.map((p, i) => (
          <Reveal key={p.id} index={i + 1} className="h-full">
            <ProviderNetworkCard p={p} internet={data.internet} selected={p.id === selected?.id} onSelect={setPicked} />
          </Reveal>
        ))}
      </div>

      {selected && selected.status.state !== "no_accounts" && (
        <Reveal index={data.providers.length + 1} className="mt-8">
          <SectionHeader>{selected.name} in detail</SectionHeader>
          <div className="grid gap-4 lg:grid-cols-3">
            <PathChart history={selected.history} internet={data.internet_history} />
            <TTFTChart history={selected.history} />
            <TrafficChart history={selected.history} />
          </div>
          <div className="mt-4 grid gap-4 lg:grid-cols-[2fr_1fr]">
            <RoutesTable probes={selected.probes} />
            <ErrorsPanel p={selected} />
          </div>
          <div className="mt-4 grid gap-4 lg:grid-cols-[3fr_2fr]">
            <AccountsTable accounts={selected.accounts} />
            <LiveTraffic events={selected.recent} />
          </div>
        </Reveal>
      )}

      <div className="mt-8">
        <ModelStatus />
      </div>
    </ReducedMotionConfig>
  );
}
