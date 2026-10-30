/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/providers/[id]/page.js + components/ConnectionsCard.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat change: the provider is one of Qanat's fixed three; connections are
 * Qanat pool accounts, added with official credentials (no web-OAuth login).
 */
"use client";

import { useState } from "react";
import { useParams } from "next/navigation";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2, Plus, Play } from "lucide-react";
import { toast } from "sonner";

import antigravityLogo from "@/components/qanat/usage-overview/logos/antigravity.png";
import claudeLogo from "@/components/qanat/usage-overview/logos/claude.png";
import codexLogo from "@/components/qanat/usage-overview/logos/codex.png";
import { AddAccountDialog, EditAccountDialog } from "@/components/qanat/accounts/account-dialogs";
import { ConnectionRow } from "@/components/qanat/accounts/connection-row";
import { EmptyState, Tag } from "@/components/qanat/page";
import { defaultLabel, choiceLabel } from "@/components/qanat/proxy-options";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { api, fetchClient, type Schemas } from "@/lib/api/client";

type Account = Schemas["Account"];

const meta: Record<string, { name: string; prefix: string; logo: typeof claudeLogo }> = {
  claude: { name: "Claude Code", prefix: "cc", logo: claudeLogo },
  codex: { name: "OpenAI Codex", prefix: "cx", logo: codexLogo },
  antigravity: { name: "Antigravity", prefix: "ag", logo: antigravityLogo },
};

// The typed client's imperative form, for the sequential Test-all loop (hooks
// can't await one call at a time).
async function testOne(id: string): Promise<Schemas["AccountTestResult"]> {
  const { data, error } = await fetchClient.POST("/api/admin/accounts/{id}/test", {
    params: { path: { id } },
  });
  if (error) throw error;
  return data;
}

export default function ProviderAccountsPage() {
  const params = useParams<{ provider: string }>();
  const provider = params.provider;
  const info = meta[provider];
  const qc = useQueryClient();
  const invalidate = () => qc.invalidateQueries({ queryKey: ["get", "/api/admin/accounts"] });

  const accounts = api.useQuery("get", "/api/admin/accounts");
  const proxies = api.useQuery("get", "/api/admin/proxies");
  const groups = api.useQuery("get", "/api/admin/proxy-groups");
  const providerProxies = api.useQuery("get", "/api/admin/provider-proxies");
  const proxyList = proxies.data ?? [];
  const groupList = groups.data ?? [];
  const inheritFor = (p: string) => {
    const set = providerProxies.data?.find((x) => x.provider === p)?.proxy ?? "inherit";
    return set === "inherit" ? defaultLabel(proxyList) : `${p}: ${set === "direct" ? "Direct" : choiceLabel(set, proxyList, groupList)}`;
  };

  const [addOpen, setAddOpen] = useState(false);
  const [editTarget, setEditTarget] = useState<Account | null>(null);
  const [testAllRunning, setTestAllRunning] = useState(false);

  if (!info) {
    return (
      <EmptyState title="Unknown provider">
        Providers are fixed: <code className="font-mono">claude</code>, <code className="font-mono">codex</code> and{" "}
        <code className="font-mono">antigravity</code>.
      </EmptyState>
    );
  }

  const rows = (accounts.data ?? []).filter((a) => a.provider === provider);

  const testAll = async () => {
    if (testAllRunning) return;
    setTestAllRunning(true);
    try {
      for (const a of rows) {
        if (!a.has_credentials) continue;
        try {
          const res = await testOne(a.id);
          if (!res.ok) toast.error(`${a.label || a.id}: ${res.error ?? `status ${res.status}`}`);
        } catch {
          toast.error(`${a.label || a.id}: the check request failed.`);
        }
      }
      toast("Test run finished.");
    } finally {
      setTestAllRunning(false);
      invalidate();
    }
  };

  return (
    <>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 items-start gap-3">
          <span aria-hidden className="grid size-10 shrink-0 place-items-center rounded-lg bg-brand-soft">
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img src={info.logo.src} alt="" className="max-h-7 max-w-7 rounded object-contain" />
          </span>
          <div className="min-w-0">
            <h1 className="flex items-center gap-2 text-title font-semibold">
              {info.name}
              <Tag tone="brand">
                <code className="font-mono">{info.prefix}/</code>
              </Tag>
            </h1>
            <p className="mt-0.5 max-w-[72ch] text-muted-foreground">
              Connections are tried in pool order, top first, per user. A rate-limited account cools down and traffic moves on.
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={testAll} disabled={testAllRunning || rows.length === 0}>
            {testAllRunning ? <Loader2 className="me-1.5 size-4 animate-spin" /> : <Play className="me-1.5 size-4" />}
            Test all
          </Button>
          <Button size="sm" onClick={() => setAddOpen(true)}>
            <Plus className="me-1.5 size-4" />
            Add connection
          </Button>
        </div>
      </div>

      {accounts.isPending || proxies.isPending ? (
        <Skeleton className="h-40 w-full" />
      ) : (
        <Card className="gap-0 px-4 py-3">
          <div className="mb-2 flex items-center justify-between">
            <h2 className="font-semibold">Connections</h2>
            <span className="text-xs text-muted-foreground">{rows.length} in the pool</span>
          </div>
          {rows.length === 0 ? (
            <p className="py-6 text-sm text-muted-foreground">No connections yet — add the first account.</p>
          ) : (
            <div className="divide-y divide-border/60">
              {rows.map((a, i) => (
                <ConnectionRow
                  key={a.id}
                  account={a}
                  proxies={proxyList}
                  groups={groupList}
                  inherit={inheritFor(a.provider)}
                  isFirst={i === 0}
                  isLast={i === rows.length - 1}
                  testing={testAllRunning}
                  onEdit={(acct) => setEditTarget(acct)}
                />
              ))}
            </div>
          )}
        </Card>
      )}

      <AddAccountDialog
        provider={provider}
        providerName={info.name}
        open={addOpen}
        onOpenChange={setAddOpen}
        proxies={proxyList}
        groups={groupList}
        inherit={inheritFor(provider)}
        onSaved={invalidate}
      />
      <EditAccountDialog
        account={editTarget}
        open={!!editTarget}
        onOpenChange={(o) => {
          if (!o) setEditTarget(null);
        }}
        proxies={proxyList}
        groups={groupList}
        inherit={editTarget ? inheritFor(editTarget.provider) : "direct"}
        onSaved={invalidate}
      />
    </>
  );
}
