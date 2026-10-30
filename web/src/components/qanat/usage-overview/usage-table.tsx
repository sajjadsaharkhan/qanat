/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/usage/components/UsageTable.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat changes: one "Breakdown" card with its controls in the header; groups
 * sort by their totals (biggest first by default); a search box; the total
 * column carries a share bar; a totals footer; detail rows say what they are
 * on two lines instead of padding the group row with dashes. Sort and search
 * live in the URL. Group rows unfold with Animate UI's tokens.
 */
"use client";

import { spring, transition, useTempoTransition } from "@animate-ui/react/utils/motion";
import { ArrowDown, ArrowUp, ChevronRight, ChevronsDownUp, ChevronsUpDown, Search, X } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { Fragment, useCallback, useEffect, useMemo, useState, type ReactNode } from "react";

import { useSearchParam } from "@/hooks/use-search-param";
import { formatUsd } from "@/lib/format";
import { cn } from "@/lib/utils";

import { Card, fmt, fmtCost, fmtTime, fmtTokens, Segmented } from "./shared";

/** A row's numbers, with the token-share cost split 9router shows. */
export type Sums = {
  requests: number;
  promptTokens: number;
  completionTokens: number;
  cachedTokens: number;
  totalTokens: number;
  cost: number;
  totalCost: number;
  inputCost: number;
  cachedCost: number;
  outputCost: number;
  lastUsed: string | null;
};

export type Row = Sums & { key: string };

export type Group<T extends Row> = { groupKey: string; summary: Sums; items: T[] };

/** summarize adds rows up into one Sums (a group's, or the table's footer). */
export function summarize(rows: Sums[]): Sums {
  const s: Sums = {
    requests: 0,
    promptTokens: 0,
    completionTokens: 0,
    cachedTokens: 0,
    totalTokens: 0,
    cost: 0,
    totalCost: 0,
    inputCost: 0,
    cachedCost: 0,
    outputCost: 0,
    lastUsed: null,
  };
  for (const r of rows) {
    s.requests += r.requests;
    s.promptTokens += r.promptTokens;
    s.completionTokens += r.completionTokens;
    s.cachedTokens += r.cachedTokens;
    s.totalTokens += r.totalTokens;
    s.cost += r.cost;
    s.totalCost += r.totalCost;
    s.inputCost += r.inputCost;
    s.cachedCost += r.cachedCost;
    s.outputCost += r.outputCost;
    if (r.lastUsed && (!s.lastUsed || r.lastUsed > s.lastUsed)) s.lastUsed = r.lastUsed;
  }
  return s;
}

export type ViewMode = "tokens" | "costs";

type SortField = "name" | "requests" | "input" | "cached" | "output" | "total" | "last";
const SORT_FIELDS: SortField[] = ["name", "requests", "input", "cached", "output", "total", "last"];
// One URL param holds field and direction ("-total" is total, high to low), so a click writes the URL once.
type Sort = SortField | `-${SortField}`;
const SORTS = SORT_FIELDS.flatMap((f) => [f, `-${f}`]) as Sort[];

function sortValue(s: Sums, field: SortField, mode: ViewMode, name: string): number | string {
  const tokens = mode === "tokens";
  switch (field) {
    case "name":
      return name.toLowerCase();
    case "requests":
      return s.requests;
    case "input":
      return tokens ? s.promptTokens : s.inputCost;
    case "cached":
      return tokens ? s.cachedTokens : s.cachedCost;
    case "output":
      return tokens ? s.completionTokens : s.outputCost;
    case "total":
      return tokens ? s.totalTokens : s.totalCost || s.cost;
    case "last":
      return s.lastUsed ?? "";
  }
}

function compare(a: number | string, b: number | string, desc: boolean) {
  const d = a < b ? -1 : a > b ? 1 : 0;
  return desc ? -d : d;
}

const sortLabel: Record<SortField, string> = {
  name: "name",
  requests: "requests",
  input: "input",
  cached: "cached",
  output: "output",
  total: "total",
  last: "last used",
};

const pct = new Intl.NumberFormat("en-US", { style: "percent", maximumFractionDigits: 0 });

export function UsageBreakdown<T extends Row>({
  noun,
  groups,
  detail,
  searchText,
  groupDecor,
  storageKey,
  emptyMessage,
  viewMode,
  onViewMode,
  groupBy,
}: {
  /** What a group is, singular and plural: ["model", "models"]. */
  noun: [string, string];
  groups: Group<T>[];
  /** A detail row's name cell: what it is, and a quieter line under it. */
  detail: (item: T) => { primary: ReactNode; secondary?: ReactNode };
  /** The words a detail row answers to in search (and its name when sorting). */
  searchText: (item: T) => string;
  groupDecor?: (groupKey: string) => ReactNode;
  storageKey: string;
  emptyMessage: string;
  viewMode: ViewMode;
  onViewMode: (v: ViewMode) => void;
  /** The grouping control, shown in the card header. */
  groupBy: ReactNode;
}) {
  const [sort, setSort] = useSearchParam<Sort>("sort", "-total", SORTS);
  const [q, setQ] = useSearchParam<string>("q", "");
  const [draft, setDraft] = useState(q);
  const field = sort.replace(/^-/, "") as SortField;
  const desc = sort.startsWith("-");

  // Typing stays instant; the URL follows a beat later.
  useEffect(() => {
    if (draft.trim() === q) return;
    const id = setTimeout(() => setQ(draft.trim()), 250);
    return () => clearTimeout(id);
  }, [draft, q, setQ]);

  // Expanded groups are remembered per browser (a convenience; may be
  // unavailable). The table mounts only after data loads, in the browser.
  const [expanded, setExpanded] = useState<Set<string>>(() => {
    try {
      const saved = typeof window === "undefined" ? null : localStorage.getItem(storageKey);
      return new Set(saved ? (JSON.parse(saved) as string[]) : []);
    } catch {
      return new Set();
    }
  });

  useEffect(() => {
    try {
      localStorage.setItem(storageKey, JSON.stringify([...expanded]));
    } catch {
      // storage blocked: expansion just isn't remembered
    }
  }, [expanded, storageKey]);

  const toggleGroup = useCallback((groupKey: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(groupKey)) next.delete(groupKey);
      else next.add(groupKey);
      return next;
    });
  }, []);

  // Search keeps a group whose name matches whole, else only its matching rows.
  const visible = useMemo(() => {
    const needle = q.toLowerCase();
    const filtered = !needle
      ? groups
      : groups.flatMap((g) => {
          if (g.groupKey.toLowerCase().includes(needle)) return [g];
          const items = g.items.filter((i) => searchText(i).toLowerCase().includes(needle));
          return items.length ? [{ ...g, items, summary: summarize(items) }] : [];
        });
    return filtered
      .map((g) => ({
        ...g,
        items: [...g.items].sort((a, b) => compare(sortValue(a, field, viewMode, searchText(a)), sortValue(b, field, viewMode, searchText(b)), desc)),
      }))
      .sort((a, b) => compare(sortValue(a.summary, field, viewMode, a.groupKey), sortValue(b.summary, field, viewMode, b.groupKey), desc));
  }, [groups, q, field, desc, viewMode, searchText]);

  const total = useMemo(() => summarize(visible.map((g) => g.summary)), [visible]);
  const tokens = viewMode === "tokens";
  const grand = tokens ? total.totalTokens : total.totalCost || total.cost;
  const allOpen = visible.length > 0 && visible.every((g) => expanded.has(g.groupKey));

  const turn = useTempoTransition(spring.selection);
  const unfold = useTempoTransition(transition.disclosure);

  // Names read A→Z first; numbers biggest first.
  const onSort = (f: SortField) => setSort((f === field ? (desc ? f : `-${f}`) : f === "name" ? f : `-${f}`) as Sort);

  const num = (n: number) => (tokens ? fmtTokens(n) : fmtCost(n));
  const exact = (n: number) => (tokens ? `${fmt(n)} tokens` : formatUsd(n));
  const parts = (s: Sums) =>
    tokens ? [s.promptTokens, s.cachedTokens, s.completionTokens, s.totalTokens] : [s.inputCost, s.cachedCost, s.outputCost, s.totalCost || s.cost];

  const headers: { f: SortField; label: string; className?: string }[] = [
    { f: "name", label: noun[0].charAt(0).toUpperCase() + noun[0].slice(1), className: "sm:ps-5" },
    { f: "requests", label: "Requests", className: "hidden text-end sm:table-cell" },
    { f: "input", label: "Input", className: "hidden text-end md:table-cell" },
    { f: "cached", label: "Cached", className: "hidden text-end md:table-cell" },
    { f: "output", label: "Output", className: "hidden text-end md:table-cell" },
    { f: "total", label: "Total", className: "text-end" },
    { f: "last", label: "Last used", className: "hidden text-end sm:table-cell" },
  ];

  const valueCells = (s: Sums, isGroup: boolean) => {
    const [input = 0, cached = 0, output = 0, sum = 0] = parts(s);
    const share = grand > 0 ? sum / grand : 0;
    return (
      <>
        <td className="hidden px-4 py-3 text-end tabular-nums sm:table-cell">{fmt(s.requests)}</td>
        <td className="hidden px-4 py-3 text-end tabular-nums md:table-cell" title={exact(input)}>
          {num(input)}
        </td>
        <td className="hidden px-4 py-3 text-end tabular-nums text-muted-foreground md:table-cell" title={exact(cached)}>
          {cached ? num(cached) : "—"}
        </td>
        <td className="hidden px-4 py-3 text-end tabular-nums md:table-cell" title={exact(output)}>
          {num(output)}
        </td>
        <td className="px-4 py-3 text-end" title={exact(sum)}>
          <span className={cn("tabular-nums", isGroup ? "font-semibold" : "font-medium")}>{num(sum)}</span>
          {isGroup && (
            <span className="mt-1 hidden items-center justify-end gap-2 sm:flex">
              <span className="h-1 w-14 overflow-hidden rounded-full bg-muted" aria-hidden>
                <span className="block h-full rounded-full bg-primary" style={{ width: `${Math.max(2, share * 100)}%` }} />
              </span>
              <span className="w-8 text-meta tabular-nums text-muted-foreground">{pct.format(share)}</span>
            </span>
          )}
        </td>
        <td className="hidden whitespace-nowrap px-4 py-3 text-end text-muted-foreground sm:table-cell">{fmtTime(s.lastUsed)}</td>
      </>
    );
  };

  return (
    <Card className="overflow-hidden">
      {/* Header: what this is, then how to slice it */}
      <div className="flex flex-col gap-3 border-b p-4 sm:px-5">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <h3 className="text-sm font-semibold">Breakdown</h3>
            <p className="text-meta text-muted-foreground" aria-live="polite">
              {visible.length} {visible.length === 1 ? noun[0] : noun[1]}
              {q && ` matching “${q}”`} · {tokens ? "tokens" : "estimated cost"} · by {sortLabel[field]}, {desc ? "high to low" : "low to high"}
            </p>
          </div>
          <Segmented
            label="Table values"
            options={[
              { value: "costs", label: "Costs" },
              { value: "tokens", label: "Tokens" },
            ]}
            value={viewMode}
            onChange={onViewMode}
            className="grid-cols-2"
          />
        </div>
        <div className="flex flex-col gap-2 lg:flex-row lg:items-center">
          <div className="flex min-w-0 flex-col gap-1.5 sm:flex-row sm:items-center sm:gap-2">
            <span className="shrink-0 text-sm text-muted-foreground">Group by</span>
            {groupBy}
          </div>
          <div className="flex flex-1 items-center gap-2 lg:justify-end">
            <label className="relative flex min-w-0 flex-1 items-center lg:max-w-64">
              <span className="sr-only">Search the breakdown</span>
              <Search aria-hidden className="pointer-events-none absolute start-2.5 size-4 text-muted-foreground" />
              <input
                type="search"
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                onKeyDown={(e) => e.key === "Escape" && setDraft("")}
                placeholder={`Search ${noun[1]}…`}
                className="h-9 w-full rounded-lg border bg-background pe-8 ps-8 text-sm placeholder:text-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring [&::-webkit-search-cancel-button]:hidden"
              />
              {draft && (
                <button
                  type="button"
                  aria-label="Clear search"
                  onClick={() => setDraft("")}
                  className="absolute end-1.5 grid size-6 place-items-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
                >
                  <X className="size-3.5" />
                </button>
              )}
            </label>
            <button
              type="button"
              onClick={() => setExpanded(allOpen ? new Set() : new Set(visible.map((g) => g.groupKey)))}
              disabled={!visible.length}
              className="inline-flex h-9 shrink-0 items-center gap-1.5 rounded-lg border px-3 text-sm font-medium transition-colors duration-200 hover:bg-muted disabled:opacity-50"
            >
              {allOpen ? <ChevronsDownUp aria-hidden className="size-4" /> : <ChevronsUpDown aria-hidden className="size-4" />}
              {allOpen ? "Collapse all" : "Expand all"}
            </button>
          </div>
        </div>
      </div>

      <div className="overflow-x-auto">
        <table className="w-full text-left text-sm">
          <thead className="bg-muted/60 text-meta text-muted-foreground">
            <tr>
              {headers.map((h) => (
                <th
                  key={h.f}
                  aria-sort={field === h.f ? (desc ? "descending" : "ascending") : "none"}
                  className={cn("whitespace-nowrap px-4 py-2.5 font-medium", h.className)}
                >
                  <button
                    type="button"
                    onClick={() => onSort(h.f)}
                    className={cn(
                      "inline-flex items-center gap-1 rounded-sm transition-colors duration-200 hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
                      field === h.f && "text-foreground",
                    )}
                  >
                    {h.label}
                    {field !== h.f ? (
                      <ChevronsUpDown aria-hidden className="size-3.5 opacity-40" />
                    ) : desc ? (
                      <ArrowDown aria-hidden className="size-3.5" />
                    ) : (
                      <ArrowUp aria-hidden className="size-3.5" />
                    )}
                  </button>
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y">
            {visible.map((group) => {
              const open = expanded.has(group.groupKey);
              return (
                <Fragment key={group.groupKey}>
                  <tr
                    className={cn("cursor-pointer transition-colors duration-200 hover:bg-muted/50", open && "bg-muted/30")}
                    onClick={() => toggleGroup(group.groupKey)}
                  >
                    <td className="max-w-[12rem] sm:max-w-[22rem] px-4 py-3 sm:ps-5">
                      <button
                        type="button"
                        className="flex min-w-0 max-w-full items-center gap-2 rounded-sm text-left focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                        aria-expanded={open}
                        onClick={(e) => {
                          e.stopPropagation();
                          toggleGroup(group.groupKey);
                        }}
                      >
                        <motion.span aria-hidden className="inline-flex shrink-0" animate={{ rotate: open ? 90 : 0 }} transition={turn}>
                          <ChevronRight className="size-4 text-muted-foreground" />
                        </motion.span>
                        {groupDecor?.(group.groupKey)}
                        <span className="truncate font-semibold" title={group.groupKey}>
                          {group.groupKey}
                        </span>
                        <span className="shrink-0 rounded-full bg-muted px-1.5 text-meta tabular-nums text-muted-foreground">
                          {group.items.length}
                          <span className="sr-only"> rows</span>
                        </span>
                      </button>
                    </td>
                    {valueCells(group.summary, true)}
                  </tr>
                  <AnimatePresence initial={false}>
                    {open &&
                      group.items.map((item, i) => {
                        const d = detail(item);
                        return (
                          <motion.tr
                            key={`detail-${item.key}`}
                            className="bg-muted/20 transition-colors duration-200 hover:bg-muted/40"
                            initial={{ opacity: 0, y: -4 }}
                            animate={{ opacity: 1, y: 0, transition: { ...unfold, delay: Math.min(i, 8) * 0.03 } }}
                            exit={{ opacity: 0, transition: { ...unfold, duration: unfold.duration / 2 } }}
                          >
                            <td className="max-w-[12rem] sm:max-w-[22rem] py-2.5 pe-4 ps-11 sm:ps-12">
                              <span className="block truncate">{d.primary}</span>
                              {d.secondary && <span className="block truncate text-meta text-muted-foreground">{d.secondary}</span>}
                            </td>
                            {valueCells(item, false)}
                          </motion.tr>
                        );
                      })}
                  </AnimatePresence>
                </Fragment>
              );
            })}
            {visible.length === 0 && (
              <tr>
                <td colSpan={headers.length} className="px-4 py-10 text-center text-muted-foreground">
                  {q ? (
                    <>
                      Nothing matches “{q}”.{" "}
                      <button type="button" className="font-medium text-foreground underline underline-offset-4" onClick={() => setDraft("")}>
                        Clear search
                      </button>
                    </>
                  ) : (
                    emptyMessage
                  )}
                </td>
              </tr>
            )}
          </tbody>
          {visible.length > 1 && (
            <tfoot className="border-t-2 bg-muted/40">
              <tr className="font-semibold">
                <td className="px-4 py-3 sm:ps-5">
                  Total{" "}
                  <span className="font-normal text-muted-foreground">
                    · {visible.length} {noun[1]}
                  </span>
                </td>
                <td className="hidden px-4 py-3 text-end tabular-nums sm:table-cell">{fmt(total.requests)}</td>
                {parts(total).map((v, i) => (
                  <td
                    key={i}
                    className={cn("px-4 py-3 text-end tabular-nums", i < 3 && "hidden md:table-cell", i === 1 && "text-muted-foreground")}
                    title={exact(v)}
                  >
                    {i === 1 && !v ? "—" : num(v)}
                  </td>
                ))}
                <td className="hidden px-4 py-3 sm:table-cell" />
              </tr>
            </tfoot>
          )}
        </table>
      </div>
    </Card>
  );
}
