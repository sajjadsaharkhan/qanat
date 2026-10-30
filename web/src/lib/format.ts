/**
 * The single place money, tokens, durations and quota levels are formatted
 * (design.md §10.3 rule 2). Thresholds mirror the backend (PRD §10.5).
 */

export type QuotaLevel = "ok" | "warning" | "critical" | "exhausted";

/** Warning at 80% used, critical at 95%, exhausted at 100% (PRD §10.5). */
export function quotaLevel(usedUsd: number, limitUsd: number): QuotaLevel {
  if (limitUsd <= 0) return "exhausted"; // zero-quota Plan (REQ-PLAN-03)
  const ratio = usedUsd / limitUsd;
  if (ratio >= 1) return "exhausted";
  if (ratio >= 0.95) return "critical";
  if (ratio >= 0.8) return "warning";
  return "ok";
}

const usdSmall = new Intl.NumberFormat("en-US", {
  style: "currency",
  currency: "USD",
  minimumFractionDigits: 2,
  maximumFractionDigits: 4,
});
const usd = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", maximumFractionDigits: 2 });

/** "$0.0731" below one dollar (cost of a single request), "$14.20" above. */
export function formatUsd(value: number): string {
  if (value !== 0 && Math.abs(value) < 1) return usdSmall.format(value);
  return usd.format(value);
}

const compact = new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 });

/** "950 tok", "18.2k tok", "1.4M tok". */
export function formatTokens(value: number): string {
  return `${compact.format(value).replace("K", "k")} tok`;
}

/** Short duration for countdowns: "45s", "12m", "2h 03m", "3d 4h". */
export function formatDuration(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${String(m).padStart(2, "0")}m`;
  if (m > 0) return `${m}m`;
  return `${s}s`;
}

/**
 * Next daily reset instant for a reset anchor of hh:mm in an IANA timezone
 * (default Asia/Tehran, PRD §10.3). Pure: `now` is injected for tests.
 */
export function nextDailyReset(now: Date, timeZone = "Asia/Tehran", hour = 0, minute = 0): Date {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone,
    hourCycle: "h23",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).formatToParts(now);
  const part = (type: Intl.DateTimeFormatPartTypes): number => {
    const v = parts.find((p) => p.type === type)?.value;
    if (v === undefined) throw new Error(`nextDailyReset: missing ${type} for ${timeZone}`);
    return Number(v);
  };
  const [y, mo, d] = [part("year"), part("month") - 1, part("day")];
  // Wall-clock time in the zone, expressed as if it were UTC.
  const wallAsUtc = Date.UTC(y, mo, d, part("hour"), part("minute"), part("second"));
  const offsetMs = wallAsUtc - Math.floor(now.getTime() / 1000) * 1000;
  let target = Date.UTC(y, mo, d, hour, minute, 0);
  if (target <= wallAsUtc) target += 86_400_000;
  return new Date(target - offsetMs);
}
