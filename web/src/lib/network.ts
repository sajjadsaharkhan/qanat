/**
 * Wording and formatting for the admin Network page. The verdicts follow
 * Tower's net monitor (github.com/ghhrmnzdh/tower, Dashboard.swift
 * netVerdict): one honest sentence answering "is it our network, the path,
 * or the provider?".
 */
import type { Schemas } from "@/lib/api/client";

export type NetworkState = Schemas["NetworkState"];
export type Tone = "ok" | "warning" | "destructive" | "neutral";

export type Verdict = { title: string; detail: string; tone: Tone };

const kindText: Record<string, string> = {
  dns: "the name doesn't resolve",
  tcp: "the connection is refused",
  proxy: "the proxy can't open a tunnel",
  proxy_auth: "the proxy rejects its sign-in",
  tls: "the TLS handshake fails (certificate or interception)",
  timeout: "the handshake times out",
};

/** fmtMs renders milliseconds: "84 ms", "1.4 s"; "—" when unknown. */
export function fmtMs(ms?: number | null): string {
  if (ms == null) return "—";
  if (ms >= 10_000) return `${Math.round(ms / 1000)} s`;
  if (ms >= 1000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.round(ms)} ms`;
}

/** ago renders how long since iso, against now: "4s ago", "3m ago". */
export function ago(iso: string | null | undefined, now: number): string {
  if (!iso) return "never";
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

/** duration renders a span in seconds: "45s", "12m", "3h 5m". */
export function span(fromIso: string, now: number): string {
  const s = Math.max(0, Math.round((now - new Date(fromIso).getTime()) / 1000));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
}

export function pct(rate: number): string {
  if (rate === 0) return "0%";
  if (rate < 0.01) return "<1%";
  return `${Math.round(rate * 100)}%`;
}

const reasonText: Record<string, string> = {
  connect: "Couldn't connect",
  mid_stream: "Dropped mid-stream",
  rate_limited: "Rate limited (429)",
  unauthorized: "Sign-in rejected (401)",
  pool_exhausted: "No account free",
};

/** reasonLabel names an attempt's failure reason. */
export function reasonLabel(reason: string): string {
  if (reasonText[reason]) return reasonText[reason];
  const m = /^status_(\d{3})$/.exec(reason);
  return m ? `Upstream ${m[1]}` : reason;
}

/** verdict is the provider's status as a title, a sentence and a tone. */
export function verdict(p: Schemas["ProviderNetwork"], internet?: Schemas["NetworkProbe"] | null): Verdict {
  const { state, reason } = p.status;
  const name = p.name;
  const best = p.probes.filter((x) => x.ok).sort((a, b) => a.total_ms - b.total_ms)[0];
  switch (state) {
    case "healthy":
      return { title: "Healthy", detail: `${name} is reachable${best ? ` in ${fmtMs(best.total_ms)}` : ""} and answering normally.`, tone: "ok" };
    case "degraded":
      switch (reason) {
        case "slow_handshake":
          return { title: "Slow path", detail: `Handshake ${fmtMs(best?.total_ms)}: the link works, replies may lag.`, tone: "warning" };
        case "slow_internet":
          return { title: "Our internet is slow", detail: `The gateway's own link takes ${fmtMs(internet?.total_ms)}, not ${name}.`, tone: "warning" };
        case "probe_failed":
          return { title: "A route is failing", detail: "One handshake failed; traffic continues on the routes that work.", tone: "warning" };
        case "rate_limited":
          return { title: "Rate limited", detail: `${name} is answering 429 to part of the traffic.`, tone: "warning" };
        case "error_rate":
          return { title: "Errors above normal", detail: `${pct(p.stats.error_rate)} of attempts failed in the last 5 minutes.`, tone: "warning" };
        case "accounts_out":
          return { title: "Some accounts are out", detail: "Traffic runs on fewer accounts than usual.", tone: "warning" };
      }
      return { title: "Degraded", detail: "Working, but slower or less reliable than usual.", tone: "warning" };
    case "unstable":
      return { title: "Connections keep dropping", detail: `Handshakes pass, but real requests to ${name} are failing upstream.`, tone: "destructive" };
    case "unreachable":
      return {
        title: `${name} unreachable`,
        detail: `Our internet is fine${internet?.ok ? ` (${fmtMs(internet.total_ms)})` : ""}, but ${kindText[reason ?? ""] ?? "the path is down"}.`,
        tone: "destructive",
      };
    case "offline":
      return { title: "Gateway offline", detail: `The gateway can't reach the internet: errors are ours, not ${name}'s.`, tone: "destructive" };
    case "unavailable": {
      const why: Record<string, string> = {
        disconnected: "every account is disconnected",
        cooling: "every account is cooling down",
        no_route: "every account's proxy is unhealthy",
        disabled: "every account is disabled",
      };
      return { title: "No account available", detail: `Requests can't be served: ${why[reason ?? ""] ?? "no account can take traffic"}.`, tone: "destructive" };
    }
    case "checking":
      return reason === "no_gateway"
        ? { title: "No gateway reporting", detail: "Start a gateway, or check that it can reach Redis.", tone: "neutral" }
        : { title: "Measuring…", detail: "The first samples arrive in a few seconds.", tone: "neutral" };
    case "no_accounts":
      return { title: "Not connected", detail: `No ${name} account yet. Connect one to route traffic and see its network.`, tone: "neutral" };
  }
}

/** worst is the provider whose state ranks highest, for the page hero. */
const order: NetworkState[] = ["offline", "unreachable", "unstable", "unavailable", "degraded", "checking", "healthy", "no_accounts"];
export function worst(ps: Schemas["ProviderNetwork"][]): Schemas["ProviderNetwork"] | undefined {
  return [...ps].sort((a, b) => order.indexOf(a.status.state) - order.indexOf(b.status.state))[0];
}
