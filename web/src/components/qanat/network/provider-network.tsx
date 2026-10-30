"use client";

import { motion } from "motion/react";
import Image, { type StaticImageData } from "next/image";
import { memo } from "react";

import antigravityLogo from "@/components/qanat/usage-overview/logos/antigravity.png";
import claudeLogo from "@/components/qanat/usage-overview/logos/claude.png";
import codexLogo from "@/components/qanat/usage-overview/logos/codex.png";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { Schemas } from "@/lib/api/client";
import { fmtMs, pct, reasonLabel, verdict } from "@/lib/network";
import { cn } from "@/lib/utils";

import { Panel, Sparkline } from "./charts";
import { Ago, Left, Since, Until } from "./clock";
import { Rolling, usePress } from "./motion";
import { StatusIcon, StatusLabel, StatusPill, toneEdge, toneText } from "./status";

type Provider = Schemas["ProviderNetwork"];

export const logos: Record<string, StaticImageData> = { claude: claudeLogo, codex: codexLogo, antigravity: antigravityLogo };

function Metric({ label, value, sub }: { label: string; value: React.ReactNode; sub?: string }) {
  return (
    <div className="min-w-0">
      <dt className="truncate text-meta font-medium text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 text-lg leading-tight font-semibold tabular-nums">
        {value}
        {sub && <span className="mt-0.5 block text-meta font-normal text-muted-foreground">{sub}</span>}
      </dd>
    </div>
  );
}

/** The fastest good route's handshake phases, "DNS 12 · connect 40 · TLS 88". */
function phases(p: Provider) {
  const best = p.probes.filter((x) => x.ok).sort((a, b) => a.total_ms - b.total_ms)[0];
  if (!best) return undefined;
  const parts = [
    best.dns_ms != null && `DNS ${fmtMs(best.dns_ms)}`,
    best.connect_ms != null && `${best.route === "direct" ? "connect" : "tunnel"} ${fmtMs(best.connect_ms)}`,
    best.tls_ms != null && `TLS ${fmtMs(best.tls_ms)}`,
  ].filter(Boolean);
  return { total: best.total_ms, text: parts.join(" · ") };
}

/** ProviderNetworkCard: one provider's live numbers; selecting it opens the details. */
export const ProviderNetworkCard = memo(function ProviderNetworkCard({
  p,
  internet,
  selected,
  onSelect,
}: {
  p: Provider;
  internet: Schemas["NetworkProbe"] | null;
  selected: boolean;
  onSelect: (id: string) => void;
}) {
  const v = verdict(p, internet);
  const ph = phases(p);
  const idle = p.status.state === "no_accounts";
  const logo = logos[p.id];
  const press = usePress("card");
  const rpm = p.stats.per_minute;
  return (
    <motion.button
      type="button"
      onClick={() => onSelect(p.id)}
      aria-pressed={selected}
      aria-label={`${p.name}: ${v.title}. Show details`}
      {...press}
      className={cn(
        // The top edge carries warning and broken states (design.md §5).
        "relative flex h-full w-full flex-col overflow-hidden rounded-2xl border bg-card text-start text-card-foreground shadow-sm transition-[box-shadow,border-color] duration-200",
        "before:absolute before:inset-x-0 before:top-0 before:h-1",
        toneEdge[v.tone],
        "focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50",
        "[@media(hover:hover)_and_(pointer:fine)]:hover:border-foreground/30 [@media(hover:hover)_and_(pointer:fine)]:hover:shadow-md",
        selected ? "border-foreground/60 shadow-md ring-1 ring-foreground/60" : "border-foreground/12",
      )}
    >
      <div className="flex flex-col gap-3 p-4 pt-5">
        <div className="flex items-center gap-3">
          {logo && (
            <span className="grid size-10 shrink-0 place-items-center rounded-xl border border-foreground/10 bg-background">
              <Image src={logo} alt="" width={24} height={24} className="size-6 rounded" />
            </span>
          )}
          <div className="min-w-0 flex-1">
            <p className="truncate text-base font-semibold">{p.name}</p>
            <p className="text-meta text-muted-foreground">
              <span className="font-mono">{p.prefix}/</span> · {p.accounts.filter((a) => !a.disabled).length} accounts
            </p>
          </div>
        </div>
        <div aria-live="polite" className="flex flex-col items-start gap-1.5">
          <StatusPill state={p.status.state} tone={v.tone}>
            {v.title}
          </StatusPill>
          <p className="text-sm text-muted-foreground">{v.detail}</p>
        </div>
      </div>
      {!idle && (
        <>
          <dl className="mx-4 grid grid-cols-3 gap-x-3 gap-y-3 rounded-xl bg-muted p-3">
            <Metric label="Connections" value={<Rolling value={p.active_connections} />} sub="active now" />
            <Metric label="TTFT p50" value={fmtMs(p.stats.ttft_ms.p50)} sub={p.stats.ttft_ms.p95 != null ? `p95 ${fmtMs(p.stats.ttft_ms.p95)}` : undefined} />
            <Metric label="Latency p50" value={fmtMs(p.stats.latency_ms.p50)} sub={p.stats.latency_ms.p95 != null ? `p95 ${fmtMs(p.stats.latency_ms.p95)}` : undefined} />
            <Metric label="Handshake" value={fmtMs(ph?.total)} sub={ph?.text || undefined} />
            <Metric label="Requests / min" value={<Rolling value={rpm} decimals={rpm < 10 ? 1 : 0} />} />
            <Metric label="Error rate" value={p.stats.attempts ? pct(p.stats.error_rate) : "—"} sub={p.stats.errors ? `${p.stats.errors} failed` : undefined} />
          </dl>
          <div className="px-4 pt-3">
            <p className="text-meta font-medium text-muted-foreground">TTFT trend</p>
            <Sparkline history={p.history} />
          </div>
          <p className="mt-auto flex flex-wrap gap-x-3 gap-y-1 border-t border-foreground/10 px-4 py-2.5 text-meta tabular-nums text-muted-foreground">
            <span>
              Probed <Ago iso={p.last_probe_at} />
            </span>
            <span>
              Last success <Ago iso={p.stats.last_success_at} />
            </span>
            <span>
              Refreshed <Ago iso={p.refreshed_at} />
            </span>
            {p.status.state !== "checking" && (
              <span>
                For <Since iso={p.status.since} />
              </span>
            )}
          </p>
        </>
      )}
    </motion.button>
  );
});

const errorText: Record<string, string> = {
  dns: "DNS failed",
  tcp: "Refused",
  proxy: "Proxy failed",
  proxy_auth: "Proxy sign-in rejected",
  tls: "TLS failed",
  timeout: "Timed out",
};

/** RoutesTable: the latest handshake per route (proxy or direct) and origin. */
export const RoutesTable = memo(function RoutesTable({ probes }: { probes: Schemas["RoutedProbe"][] }) {
  return (
    <Panel className="min-w-0 overflow-x-auto">
      <h3 className="px-4 pt-4 pb-1 text-sm font-semibold">Routes</h3>
      <Table>
        <TableHeader className="[&_th]:text-meta [&_th]:font-semibold [&_th]:text-foreground/80">
          <TableRow>
            <TableHead>Route</TableHead>
            <TableHead>Result</TableHead>
            <TableHead className="text-end">DNS</TableHead>
            <TableHead className="text-end">Connect</TableHead>
            <TableHead className="text-end">TLS</TableHead>
            <TableHead className="text-end">Total</TableHead>
            <TableHead className="text-end">Checked</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {probes.length === 0 ? (
            <TableRow>
              <TableCell colSpan={7} className="py-6 text-center text-muted-foreground">
                No routes probed yet.
              </TableCell>
            </TableRow>
          ) : (
            probes.map((r) => (
              <TableRow key={r.route + r.origin}>
                <TableCell>
                  <span className="font-medium">{r.route_label}</span>
                  <span className="block text-meta text-muted-foreground">{r.origin.replace(/^https?:\/\//, "")}</span>
                </TableCell>
                <TableCell>
                  {r.ok ? (
                    <span className="inline-flex items-center gap-1.5 font-medium text-success">
                      <StatusIcon state="healthy" />
                      OK
                    </span>
                  ) : (
                    <span className={cn("inline-flex items-center gap-1.5 font-medium", toneText.destructive)} title={r.detail}>
                      <StatusIcon state={r.fails >= 2 ? "unreachable" : "degraded"} />
                      {errorText[r.error ?? ""] ?? "Failed"}
                      {r.fails > 1 && <span className="font-normal">· {r.fails}× in a row</span>}
                    </span>
                  )}
                </TableCell>
                <TableCell className="text-end tabular-nums">{r.dns_ms != null ? fmtMs(r.dns_ms) : r.route === "direct" ? "—" : <span className="text-muted-foreground">via proxy</span>}</TableCell>
                <TableCell className="text-end tabular-nums">{fmtMs(r.connect_ms)}</TableCell>
                <TableCell className="text-end tabular-nums">{fmtMs(r.tls_ms)}</TableCell>
                <TableCell className="text-end font-semibold tabular-nums">
                  {r.ok ? fmtMs(r.total_ms) : "—"}
                  <span className="block text-meta font-normal text-muted-foreground">limit {fmtMs(r.timeout_ms)}</span>
                </TableCell>
                <TableCell className="text-end text-muted-foreground tabular-nums"><Ago iso={r.at} />
                </TableCell>
              </TableRow>
            ))
          )}
        </TableBody>
      </Table>
    </Panel>
  );
});

/** ErrorsPanel: what failed in the window, by reason, plus the latency split. */
export const ErrorsPanel = memo(function ErrorsPanel({ p }: { p: Provider }) {
  const reasons = Object.entries(p.stats.reasons).sort((a, b) => b[1] - a[1]);
  const total = reasons.reduce((n, [, c]) => n + c, 0);
  return (
    <Panel className="flex min-w-0 flex-col gap-3 p-4">
      <h3 className="text-sm font-semibold">Last 5 minutes</h3>
      <dl className="grid grid-cols-2 gap-x-3 gap-y-2.5">
        <Metric label="Attempts" value={String(p.stats.attempts)} sub={`${p.stats.ok} ok`} />
        <Metric label="Client went away" value={String(p.stats.aborted)} />
        <Metric label="Qanat overhead p50" value={fmtMs(p.stats.overhead_ms.p50)} />
        <Metric label="Output speed" value={p.stats.output_tps_p50 != null ? `${Math.round(p.stats.output_tps_p50)}` : "—"} sub={p.stats.output_tps_p50 != null ? "tok/s" : undefined} />
      </dl>
      <div>
        <p className="mb-1.5 text-meta text-muted-foreground">Failures by reason</p>
        {reasons.length === 0 ? (
          <p className="text-sm text-muted-foreground">None.</p>
        ) : (
          <ul className="flex flex-col gap-1.5">
            {reasons.map(([r, n]) => (
              <li key={r} className="flex items-center gap-2 text-sm">
                <span className="w-40 shrink-0 truncate">{reasonLabel(r)}</span>
                <span className="h-2 flex-1 overflow-hidden rounded-full bg-muted ring-1 ring-inset ring-foreground/10">
                  <span className="block h-full origin-left rounded-full bg-chart-1" style={{ width: `${(n / total) * 100}%` }} />
                </span>
                <span className="w-8 text-end tabular-nums">{n}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </Panel>
  );
});

/** AccountsTable: each account's share of the traffic and its routing state. */
export const AccountsTable = memo(function AccountsTable({ accounts }: { accounts: Schemas["AccountNetwork"][] }) {
  return (
    <Panel className="min-w-0 overflow-x-auto">
      <h3 className="px-4 pt-4 pb-1 text-sm font-semibold">Accounts</h3>
      <Table>
        <TableHeader className="[&_th]:text-meta [&_th]:font-semibold [&_th]:text-foreground/80">
          <TableRow>
            <TableHead>Account</TableHead>
            <TableHead>Routing</TableHead>
            <TableHead className="text-end">Active</TableHead>
            <TableHead className="text-end">Attempts</TableHead>
            <TableHead className="text-end">Failed</TableHead>
            <TableHead className="text-end">TTFT p50</TableHead>
            <TableHead className="text-end">Last ok</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {accounts.length === 0 ? (
            <TableRow>
              <TableCell colSpan={7} className="py-6 text-center text-muted-foreground">
                No accounts.
              </TableCell>
            </TableRow>
          ) : (
            accounts.map((a) => {
              return (
                <TableRow key={a.id}>
                  <TableCell className="max-w-48 truncate font-medium" title={a.label}>
                    {a.label}
                  </TableCell>
                  <TableCell>
                    {a.disabled ? (
                      <span className="text-muted-foreground">Disabled</span>
                    ) : a.state === "disconnected" ? (
                      <StatusLabel state="unavailable" tone="destructive">
                        Disconnected
                      </StatusLabel>
                    ) : a.cooling_until ? (
                      <Until
                        iso={a.cooling_until}
                        active={
                          <span className={cn("font-medium", toneText.warning)}>
                            Cooling down, <Left iso={a.cooling_until} /> left
                          </span>
                        }
                        after={a.usable ? "In rotation" : "Back shortly"}
                      />
                    ) : a.usable ? (
                      "In rotation"
                    ) : (
                      <StatusLabel state="degraded" tone="warning">
                        No healthy route
                      </StatusLabel>
                    )}
                  </TableCell>
                  <TableCell className="text-end tabular-nums">{a.active}</TableCell>
                  <TableCell className="text-end tabular-nums">{a.attempts}</TableCell>
                  <TableCell className="text-end tabular-nums">{a.errors}</TableCell>
                  <TableCell className="text-end tabular-nums">{fmtMs(a.ttft_p50_ms)}</TableCell>
                  <TableCell className="text-end text-muted-foreground tabular-nums"><Ago iso={a.last_success_at} />
                  </TableCell>
                </TableRow>
              );
            })
          )}
        </TableBody>
      </Table>
    </Panel>
  );
});

const outcomeText: Record<string, string> = { ok: "OK", error: "Failed", rejected: "No account", aborted: "Client left" };

/** LiveTraffic: the newest attempts, Tower's live traffic list. */
export const LiveTraffic = memo(function LiveTraffic({ events }: { events: Schemas["NetworkEvent"][] }) {
  return (
    <Panel className="min-w-0 overflow-hidden">
      <div className="flex items-baseline justify-between border-b border-foreground/10 px-4 pt-4 pb-2.5">
        <h3 className="text-sm font-semibold">Live traffic</h3>
        <span className="text-meta text-muted-foreground">newest first</span>
      </div>
      {events.length === 0 ? (
        <p className="px-4 pb-4 text-sm text-muted-foreground">No traffic in the last 5 minutes.</p>
      ) : (
        <ul className="max-h-[26rem] overflow-y-auto text-sm">
          {events.map((e, i) => (
            <li key={`${e.at}-${i}`} className="flex items-center gap-3 px-4 py-1.5 tabular-nums odd:bg-muted/70">
              <span className="w-14 shrink-0 text-meta text-muted-foreground"><Ago iso={e.at} />
              </span>
              <span className="min-w-0 flex-1">
                <span className="block truncate">{e.model ?? "—"}</span>
                <span className="block truncate text-meta text-muted-foreground">
                  {[e.account_label, e.ttft_ms ? `TTFT ${fmtMs(e.ttft_ms)}` : null, e.duration_ms ? `${fmtMs(e.duration_ms)} total` : null].filter(Boolean).join(" · ")}
                </span>
              </span>
              <span
                className={cn("shrink-0 text-end font-medium", e.outcome === "error" ? toneText.destructive : e.outcome === "rejected" ? toneText.warning : "text-muted-foreground")}
              >
                {e.outcome === "error" && e.reason ? reasonLabel(e.reason) : outcomeText[e.outcome]}
                {e.status && e.outcome === "ok" ? ` ${e.status}` : ""}
              </span>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
});
