/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/usage/components/ProviderBarChart.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat changes: a ranked list with provider logos and printed values instead
 * of a bar chart; bars use the forest ramp (design.md §3.4).
 */
"use client";

import Image, { type StaticImageData } from "next/image";
import { useMemo, useState } from "react";

import type { Schemas } from "@/lib/api/client";

import antigravityLogo from "./logos/antigravity.png";
import claudeLogo from "./logos/claude.png";
import codexLogo from "./logos/codex.png";
import { RankedBars } from "./ranked-bars";
import { Card, CardHeading, fmt, fmtTokens, providerName, Segmented } from "./shared";

const COLORS = ["var(--chart-1)", "var(--chart-2)", "var(--chart-3)"];
const LOGO: Record<string, StaticImageData> = {
  claude: claudeLogo,
  codex: codexLogo,
  antigravity: antigravityLogo,
};

type ViewMode = "tokens" | "requests";

export function ProviderBarChart({ byProvider }: { byProvider: Schemas["UsageGroup"][] }) {
  const [viewMode, setViewMode] = useState<ViewMode>("tokens");

  const items = useMemo(
    () =>
      byProvider
        .map((d) => ({
          id: d.provider,
          tokens: d.prompt_tokens + d.completion_tokens,
          requests: d.requests,
        }))
        .filter((d) => d[viewMode] > 0)
        .sort((a, b) => b[viewMode] - a[viewMode])
        .map((d, i) => {
          const logo = LOGO[d.id];
          return {
            key: d.id,
            value: d[viewMode],
            color: COLORS[Math.min(i, COLORS.length - 1)] ?? "var(--chart-3)",
            label: (
              <>
                {logo ? (
                  <Image src={logo} alt="" width={18} height={18} className="size-[18px] shrink-0 rounded-sm object-contain" />
                ) : (
                  <span aria-hidden className="size-[18px] shrink-0 rounded-sm bg-muted" />
                )}
                <span className="truncate">{providerName(d.id)}</span>
              </>
            ),
          };
        }),
    [byProvider, viewMode],
  );

  return (
    <Card className="flex min-w-0 flex-col gap-4 p-4 sm:p-5">
      <CardHeading title="By provider" hint={viewMode === "tokens" ? "Input and output tokens" : "Requests"}>
        <Segmented
          label="Provider measure"
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
      <RankedBars items={items} format={viewMode === "tokens" ? fmtTokens : fmt} empty="No provider usage yet" />
    </Card>
  );
}
