/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/providers/components/ModelsCard.js (ModelRow).
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See usage-overview/LICENSE-9router.
 * Qanat changes: models come from the price table (D13) filtered by provider
 * family; no per-model test button or custom-model store — any name after the
 * prefix routes on demand.
 */
"use client";

import { Bot, Check, Copy } from "lucide-react";
import { useState } from "react";

import { Tag } from "@/components/qanat/page";
import type { Schemas } from "@/lib/api/client";

function ModelRow({ fullModel }: { fullModel: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    await navigator.clipboard.writeText(fullModel);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <div className="group flex items-center gap-2 rounded-lg border px-3 py-2 hover:bg-muted/50">
      <Bot className="size-4 shrink-0 text-muted-foreground" aria-hidden />
      <code className="truncate rounded bg-muted px-1.5 py-0.5 font-mono text-meta text-muted-foreground">{fullModel}</code>
      <button
        type="button"
        onClick={copy}
        className="ms-auto rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-brand-text"
        aria-label={copied ? "Copied" : `Copy ${fullModel}`}
      >
        {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
      </button>
    </div>
  );
}

/** One provider: its prefix, account count and the priced models it serves, as `prefix/model`. */
export function ProviderModelsCard({
  provider,
  models,
  accounts,
}: {
  provider: Schemas["ProviderInfo"];
  models: string[];
  accounts: number;
}) {
  const served = models.filter((m) => provider.families.some((f) => m.startsWith(f))).sort();
  return (
    <div className="flex flex-col gap-3 rounded-xl border bg-card p-4">
      <div className="flex items-center gap-2">
        <span className="font-semibold">{provider.name}</span>
        <Tag tone="brand">
          <span className="font-mono">{provider.prefix}/</span>
        </Tag>
        <span className="ms-auto text-meta text-muted-foreground">
          {accounts === 0 ? "No accounts" : accounts === 1 ? "1 account" : `${accounts} accounts`}
        </span>
      </div>
      <p className="text-meta text-muted-foreground">
        Send <code className="font-mono">{provider.prefix}/&lt;model&gt;</code>: Qanat routes to this provider and strips the prefix. Any
        priced model name works.
      </p>
      {served.length === 0 ? (
        <p className="text-meta text-muted-foreground">No priced {provider.families.map((f) => `${f}*`).join(", ")} models yet.</p>
      ) : (
        <div className="flex flex-col gap-1.5">
          {served.map((m) => (
            <ModelRow key={m} fullModel={`${provider.prefix}/${m}`} />
          ))}
        </div>
      )}
    </div>
  );
}
