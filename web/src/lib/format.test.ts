import { describe, expect, it } from "vitest";
import { formatDuration, formatTokens, formatUsd, nextDailyReset, quotaLevel } from "./format";

describe("quotaLevel", () => {
  it.each([
    [0, 10, "ok"],
    [7.99, 10, "ok"],
    [8, 10, "warning"],
    [9.49, 10, "warning"],
    [9.5, 10, "critical"],
    [10, 10, "exhausted"],
    [0, 0, "exhausted"], // zero-quota default Plan
  ] as const)("%d of %d is %s", (used, limit, level) => {
    expect(quotaLevel(used, limit)).toBe(level);
  });
});

describe("formatUsd", () => {
  it("keeps precision for single-request costs", () => expect(formatUsd(0.0731)).toBe("$0.0731"));
  it("rounds larger amounts to cents", () => expect(formatUsd(14.2)).toBe("$14.20"));
  it("formats zero plainly", () => expect(formatUsd(0)).toBe("$0"));
  it("drops cents from whole amounts", () => {
    expect(formatUsd(10_000)).toBe("$10,000");
    expect(formatUsd(12.004)).toBe("$12");
    expect(formatUsd(10_000.5)).toBe("$10,000.50");
  });
});

describe("formatTokens", () => {
  it.each([
    [950, "950 tok"],
    [18_200, "18.2k tok"],
    [1_400_000, "1.4M tok"],
  ])("%d → %s", (n, s) => expect(formatTokens(n)).toBe(s));
});

describe("formatDuration", () => {
  it.each([
    [45_000, "45s"],
    [12 * 60_000, "12m"],
    [(2 * 60 + 3) * 60_000, "2h 03m"],
    [(3 * 24 + 4) * 3_600_000, "3d 4h"],
    [-5, "0s"],
  ])("%d ms → %s", (ms, s) => expect(formatDuration(ms)).toBe(s));
});

describe("nextDailyReset", () => {
  it("resets at 00:00 Asia/Tehran (UTC+3:30)", () => {
    // 2026-10-30 18:00 UTC = 21:30 Tehran → next reset 2026-10-31 00:00 Tehran = 2026-10-30 20:30 UTC
    const next = nextDailyReset(new Date("2026-10-30T18:00:00Z"));
    expect(next.toISOString()).toBe("2026-10-30T20:30:00.000Z");
  });
  it("rolls to the next day once the anchor has passed", () => {
    // 2026-10-30 21:00 UTC = 00:30 Tehran on the 31st → next reset 2026-11-01 00:00 Tehran
    const next = nextDailyReset(new Date("2026-10-30T21:00:00Z"));
    expect(next.toISOString()).toBe("2026-10-31T20:30:00.000Z");
  });
  it("supports other zones", () => {
    const next = nextDailyReset(new Date("2026-10-30T10:00:00Z"), "Europe/Berlin");
    expect(next.toISOString()).toBe("2026-10-30T23:00:00.000Z"); // CET (UTC+1) after DST end on Oct 25
  });
});
