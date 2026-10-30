/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/usage/components/TopModelsChart.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat changes: a ranked list with full model names and printed values
 * instead of a bar chart; each bar wears its model tier accent (design.md §3.3).
 */
"use client";

import { useMemo, useState } from "react";

import type { Schemas } from "@/lib/api/client";
import { modelTier, type ModelTier } from "@/lib/model-tier";

import { RankedBars } from "./ranked-bars";
import { Card, CardHeading, fmt, fmtTokens, Segmented } from "./shared";

const tierColor: Record<ModelTier, string> = {
  fable: "var(--model-fable)",
  opus: "var(--model-opus)",
  sonnet: "var(--model-sonnet)",
  haiku: "var(--model-haiku)",
  gpt: "var(--model-gpt)",
  gemini: "var(--model-gemini)",
  other: "var(--muted-foreground)",
};

type ViewMode = "tokens" | "requests";

export function TopModelsChart({ byModel }: { byModel: Schemas["UsageGroup"][] }) {
  const [viewMode, setViewMode] = useState<ViewMode>("tokens");

  const items = useMemo(() => {
    // byModel is per model and provider; merge by model name like 9router's rawModel.
    const merged = new Map<string, { model: string; tokens: number; requests: number }>();
    for (const d of byModel) {
      const m = merged.get(d.model) ?? {
        model: d.model || "Unknown",
        tokens: 0,
        requests: 0,
      };
      m.tokens += d.prompt_tokens + d.completion_tokens;
      m.requests += d.requests;
      merged.set(d.model, m);
    }
    return [...merged.values()]
      .filter((d) => d[viewMode] > 0)
      .sort((a, b) => b[viewMode] - a[viewMode])
      .slice(0, 5)
      .map((d) => {
        const color = tierColor[modelTier(d.model)];
        return {
          key: d.model,
          value: d[viewMode],
          color,
          title: d.model,
          label: (
            <>
              <span aria-hidden className="size-2 shrink-0 rounded-full" style={{ backgroundColor: color }} />
              <span className="truncate">{d.model}</span>
            </>
          ),
        };
      });
  }, [byModel, viewMode]);

  return (
    <Card className="flex min-w-0 flex-col gap-4 p-4 sm:p-5">
      <CardHeading title="Top models" hint="The five busiest, by tier color">
        <Segmented
          label="Top models measure"
          size="xs"
          options={[
            { value: "tokens", label: "Tokens" },
            { value: "requests", label: "Requests" },
          ]}
          value={viewMode}
          onChange={setViewMode}
          className="grid-cols-2"
        />
      </CardHeading>
      <RankedBars items={items} format={viewMode === "tokens" ? fmtTokens : fmt} empty="No model usage yet" />
    </Card>
  );
}
