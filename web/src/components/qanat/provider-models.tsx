/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/providers/components/ModelsCard.js (ModelRow).
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See usage-overview/LICENSE-9router.
 * Qanat changes: the card lists the provider's stored catalog (fetched from
 * the provider itself and persisted — 9router hardcodes its lists) as
 * prefix/model rows, paginated, with the priced ones tagged.
 */
"use client";

import { Bot, Check, ChevronLeft, ChevronRight, Copy, Loader2, RefreshCw } from "lucide-react";
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Tag } from "@/components/qanat/page";
import { api, errorMessage, type Schemas } from "@/lib/api/client";

const PAGE_SIZE = 8;

function ModelRow({ fullModel, priced }: { fullModel: string; priced: boolean }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    await navigator.clipboard.writeText(fullModel);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <div className="group flex items-center gap-2 rounded-lg border px-3 py-2 hover:bg-muted/50">
      <Bot className="size-4 shrink-0 text-muted-foreground aria-hidden" aria-hidden />
      <code className="truncate rounded bg-muted px-1.5 py-0.5 font-mono text-meta text-muted-foreground">{fullModel}</code>
      {priced && <Tag tone="brand">priced</Tag>}
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

/** One provider: prefix, account count, and its stored catalog as prefix/model rows. */
export function ProviderModelsCard({
  provider,
  models,
  accounts,
}: {
  provider: Schemas["ProviderInfo"];
  models: string[];
  accounts: number;
}) {
  const qc = useQueryClient();
  const catalog = api.useQuery("get", "/api/admin/providers/{provider}/models", {
    params: { path: { provider: provider.id } },
  });
  const sync = api.useMutation("post", "/api/admin/providers/{provider}/models");
  const [page, setPage] = useState(0);

  const stored = catalog.data?.models ?? [];
  const priced = new Set(models);
  const pages = Math.max(1, Math.ceil(stored.length / PAGE_SIZE));
  const current = Math.min(page, pages - 1);
  const rows = stored.slice(current * PAGE_SIZE, (current + 1) * PAGE_SIZE);

  const doSync = () =>
    sync.mutate(
      { params: { path: { provider: provider.id } } },
      {
        onSuccess: (res) => {
          qc.invalidateQueries({ queryKey: ["get", "/api/admin/providers/{provider}/models"] });
          toast(`${provider.name}: ${res.models?.length ?? 0} models loaded from the provider.`);
        },
        onError: (err) => toast.error(errorMessage(err)),
      },
    );

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
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-7"
          onClick={doSync}
          disabled={sync.isPending || accounts === 0}
          aria-label={`Load ${provider.name} models from the provider`}
        >
          {sync.isPending ? <Loader2 aria-hidden className="size-3.5 animate-spin" /> : <RefreshCw aria-hidden className="size-3.5" />}
        </Button>
      </div>
      <p className="text-meta text-muted-foreground">
        Send <code className="font-mono">{provider.prefix}/&lt;model&gt;</code>: Qanat routes to this provider and strips the prefix.
      </p>
      {catalog.isPending ? null : stored.length === 0 ? (
        <p className="text-meta text-muted-foreground">
          No stored catalog yet — press <RefreshCw aria-hidden className="inline size-3" /> to load the models the provider really serves.
        </p>
      ) : (
        <>
          <div className="flex flex-col gap-1.5">
            {rows.map((m) => (
              <ModelRow key={m} fullModel={`${provider.prefix}/${m}`} priced={priced.has(m)} />
            ))}
          </div>
          {pages > 1 && (
            <div className="flex items-center justify-end gap-2 text-meta text-muted-foreground">
              <Button variant="ghost" size="icon" className="size-6" onClick={() => setPage(current - 1)} disabled={current === 0} aria-label="Previous page">
                <ChevronLeft className="size-3.5" />
              </Button>
              <span>
                {current + 1} / {pages}
              </span>
              <Button variant="ghost" size="icon" className="size-6" onClick={() => setPage(current + 1)} disabled={current >= pages - 1} aria-label="Next page">
                <ChevronRight className="size-3.5" />
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  );
}
