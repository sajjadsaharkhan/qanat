import { describe, expect, it } from "vitest";

import type { Schemas } from "@/lib/api/client";

import { ago, fmtMs, pct, reasonLabel, verdict, worst } from "./network";

function provider(state: Schemas["NetworkState"], reason?: string, over: Partial<Schemas["ProviderNetwork"]> = {}): Schemas["ProviderNetwork"] {
  return {
    id: "claude",
    name: "Claude Code",
    prefix: "cc",
    status: { state, reason, since: "2026-10-31T12:00:00Z" },
    active_connections: 0,
    stats: {
      attempts: 0, ok: 0, errors: 0, rejected: 0, aborted: 0, error_rate: 0, per_minute: 0, reasons: {},
      ttft_ms: { p50: null, p95: null }, latency_ms: { p50: null, p95: null }, overhead_ms: { p50: null, p95: null },
    },
    accounts: [],
    probes: [],
    history: [],
    recent: [],
    refreshed_at: null,
    last_probe_at: null,
    ...over,
  };
}

describe("network formatting", () => {
  it("formats milliseconds", () => {
    expect(fmtMs(null)).toBe("—");
    expect(fmtMs(84.4)).toBe("84 ms");
    expect(fmtMs(1400)).toBe("1.4 s");
    expect(fmtMs(12_400)).toBe("12 s");
  });
  it("formats ages and rates", () => {
    const now = Date.parse("2026-10-31T12:05:00Z");
    expect(ago("2026-10-31T12:04:56Z", now)).toBe("4s ago");
    expect(ago("2026-10-31T12:00:00Z", now)).toBe("5m ago");
    expect(ago(null, now)).toBe("never");
    expect(pct(0.004)).toBe("<1%");
    expect(pct(0.25)).toBe("25%");
  });
  it("names failure reasons", () => {
    expect(reasonLabel("status_502")).toBe("Upstream 502");
    expect(reasonLabel("mid_stream")).toBe("Dropped mid-stream");
  });
});

describe("verdict", () => {
  const probe = { at: "", route: "direct", route_label: "Direct", origin: "o", ok: true, total_ms: 180, fails: 0, timeout_ms: 3000 };
  it("blames the right party, like Tower", () => {
    expect(verdict(provider("healthy", undefined, { probes: [probe] })).detail).toContain("180 ms");
    const unreachable = verdict(provider("unreachable", "tls"), { ...probe, total_ms: 12 });
    expect(unreachable.tone).toBe("destructive");
    expect(unreachable.detail).toMatch(/Our internet is fine \(12 ms\).*TLS/);
    expect(verdict(provider("offline")).detail).toContain("ours");
    expect(verdict(provider("degraded", "slow_internet"), { ...probe, total_ms: 450 }).title).toBe("Our internet is slow");
    expect(verdict(provider("unavailable", "cooling")).detail).toContain("cooling down");
    expect(verdict(provider("checking", "no_gateway")).tone).toBe("neutral");
  });
  it("picks the worst provider for the hero", () => {
    expect(worst([provider("healthy"), provider("unstable"), provider("no_accounts")])?.status.state).toBe("unstable");
  });
});
