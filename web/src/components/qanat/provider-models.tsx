/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/providers/components/ModelsCard.js (ModelRow).
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See usage-overview/LICENSE-9router.
 * Qanat changes: priced models come from the price table (D13) filtered by
 * provider family; the live catalog is fetched from the provider itself with
 * a stored account's credential ("Load from provider") — 9router hardcodes
 * its lists. Any name after the prefix routes on demand.
 */
"use client";

import { Bot, Check, Copy, Loader2, RefreshCw } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { Tag } from "@/components/qanat/page";
import { Button } from "@/components/ui/button";
import { errorMessage, fetchClient, type Schemas } from "@/lib/api/client";

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

/** One provider: its prefix, account count, priced models, and its live catalog on demand. */
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
  const [catalog, setCatalog] = useState<string[] | null>(null);
  const [loading, setLoading] = useState(false);

  const loadCatalog = async () => {
    setLoading(true);
    try {
      const { data, error } = await fetchClient.GET("/api/admin/providers/{provider}/models", {
        params: { path: { provider: provider.id } },
      });
      if (error) {
        toast.error(errorMessage(error));
        return;
      }
      setCatalog(data.models ?? []);
    } finally {
      setLoading(false);
    }
  };

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
      <div className="flex items-center gap-2">
        <Button type="button" variant="outline" size="sm" onClick={loadCatalog} disabled={loading || accounts === 0}>
          {loading ? <Loader2 aria-hidden className="me-1.5 size-3.5 animate-spin" /> : <RefreshCw aria-hidden className="me-1.5 size-3.5" />}
          Load from provider
        </Button>
        {catalog && <span className="text-meta text-muted-foreground">{catalog.length} models upstream</span>}
      </div>
      {catalog && (
        <div className="flex flex-col gap-1.5">
          {catalog.length === 0 ? (
            <p className="text-meta text-muted-foreground">The provider listed no models.</p>
          ) : (
            catalog.map((m) => (
              <div key={m} className="group flex items-center gap-2 rounded-lg border px-3 py-1.5 hover:bg-muted/50">
                <code className="truncate font-mono text-meta">{m}</code>
                {served.includes(m) && <Tag tone="brand">priced</Tag>}
                <CopyModelButton model={m} />
              </div>
            ))
          )}
        </div>
      )}
    </div>
  );
}

function CopyModelButton({ model }: { model: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      onClick={async () => {
        await navigator.clipboard.writeText(model);
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      }}
      className="ms-auto rounded p-0.5 text-muted-foreground opacity-0 transition-opacity hover:text-brand-text group-hover:opacity-100"
      aria-label={copied ? "Copied" : `Copy ${model}`}
    >
      {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
    </button>
  );
}
