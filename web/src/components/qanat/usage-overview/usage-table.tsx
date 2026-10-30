/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/usage/components/UsageTable.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 */
"use client";

import { ChevronRight } from "lucide-react";
import { Fragment, useCallback, useEffect, useMemo, useState, type ReactNode } from "react";

import { cn } from "@/lib/utils";

import { Card, fmt, fmtCost } from "./shared";

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

export type Column = { field: string; label: string; align?: "right" };

function SortIcon({ field, currentSort, currentOrder }: { field: string; currentSort: string; currentOrder: string }) {
  if (currentSort !== field) return <span className="ms-1 opacity-20">↕</span>;
  return <span className="ms-1">{currentOrder === "asc" ? "↑" : "↓"}</span>;
}

/** Render 4 token or cost cells based on viewMode. */
function ValueCells({ item, viewMode }: { item: Sums; viewMode: "tokens" | "costs" }) {
  if (viewMode === "tokens") {
    return (
      <>
        <td className="px-6 py-3 text-right text-muted-foreground">{fmt(item.promptTokens)}</td>
        <td className="px-6 py-3 text-right text-muted-foreground">{item.cachedTokens ? fmt(item.cachedTokens) : "—"}</td>
        <td className="px-6 py-3 text-right text-muted-foreground">{fmt(item.completionTokens)}</td>
        <td className="px-6 py-3 text-right font-medium">{fmt(item.totalTokens)}</td>
      </>
    );
  }
  return (
    <>
      <td className="px-6 py-3 text-right text-muted-foreground">{fmtCost(item.inputCost)}</td>
      <td className="px-6 py-3 text-right text-muted-foreground">{item.cachedCost ? fmtCost(item.cachedCost) : "—"}</td>
      <td className="px-6 py-3 text-right text-muted-foreground">{fmtCost(item.outputCost)}</td>
      <td className="px-6 py-3 text-right font-medium">{fmtCost(item.totalCost || item.cost)}</td>
    </>
  );
}

/** Reusable sortable usage table with expandable group rows. */
export function UsageTable<T extends Row>({
  title,
  columns,
  groupedData,
  sortBy,
  sortOrder,
  onToggleSort,
  viewMode,
  storageKey,
  renderDetailCells,
  renderSummaryCells,
  emptyMessage,
}: {
  title: string;
  columns: Column[];
  groupedData: Group<T>[];
  sortBy: string;
  sortOrder: string;
  onToggleSort: (field: string) => void;
  viewMode: "tokens" | "costs";
  storageKey: string;
  renderDetailCells: (item: T) => ReactNode;
  renderSummaryCells: (group: Group<T>) => ReactNode;
  emptyMessage: string;
}) {
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

  const valueColumns = useMemo(
    () =>
      viewMode === "tokens"
        ? [
            { field: "promptTokens", label: "Input Tokens" },
            { field: "cachedTokens", label: "Cached" },
            { field: "completionTokens", label: "Output Tokens" },
            { field: "totalTokens", label: "Total Tokens" },
          ]
        : [
            { field: "inputCost", label: "Input Cost" },
            { field: "cachedCost", label: "Cached Cost" },
            { field: "outputCost", label: "Output Cost" },
            { field: "cost", label: "Total Cost" },
          ],
    [viewMode],
  );

  const totalColSpan = columns.length + valueColumns.length;

  return (
    <Card className="overflow-hidden">
      {title && (
        <div className="border-b bg-muted/50 p-4">
          <h3 className="font-semibold">{title}</h3>
        </div>
      )}
      <div className="overflow-x-auto">
        <table className="w-full text-left text-sm">
          <thead className="bg-muted/30 text-xs uppercase text-muted-foreground">
            <tr>
              {[...columns, ...valueColumns.map((c) => ({ ...c, align: "right" as const }))].map((col) => (
                <th
                  key={col.field}
                  aria-sort={sortBy === col.field ? (sortOrder === "asc" ? "ascending" : "descending") : "none"}
                  className={cn("cursor-pointer px-6 py-3 hover:bg-muted/50", col.align === "right" && "text-right")}
                  onClick={() => onToggleSort(col.field)}
                >
                  {col.label} <SortIcon field={col.field} currentSort={sortBy} currentOrder={sortOrder} />
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y">
            {groupedData.map((group) => (
              <Fragment key={group.groupKey}>
                <tr
                  className="cursor-pointer transition-colors hover:bg-muted/50"
                  onClick={() => toggleGroup(group.groupKey)}
                  aria-expanded={expanded.has(group.groupKey)}
                >
                  <td className="px-6 py-3">
                    <div className="flex items-center gap-2">
                      <ChevronRight
                        aria-hidden
                        className={cn("size-[18px] text-muted-foreground transition-transform", expanded.has(group.groupKey) && "rotate-90")}
                      />
                      <span className="font-medium">{group.groupKey}</span>
                    </div>
                  </td>
                  {renderSummaryCells(group)}
                  <ValueCells item={group.summary} viewMode={viewMode} />
                </tr>
                {expanded.has(group.groupKey) &&
                  group.items.map((item) => (
                    <tr key={`detail-${item.key}`} className="transition-colors hover:bg-muted/20">
                      {renderDetailCells(item)}
                      <ValueCells item={item} viewMode={viewMode} />
                    </tr>
                  ))}
              </Fragment>
            ))}
            {groupedData.length === 0 && (
              <tr>
                <td colSpan={totalColSpan} className="px-6 py-8 text-center text-muted-foreground">
                  {emptyMessage}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </Card>
  );
}
