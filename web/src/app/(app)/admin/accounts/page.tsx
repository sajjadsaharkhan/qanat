/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/providers/page.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat change: the grid holds Qanat's fixed provider registry (cc/ cx/ ag/),
 * and connections live one level down, per provider.
 */
"use client";

import { Server } from "lucide-react";

import antigravityLogo from "@/components/qanat/usage-overview/logos/antigravity.png";
import claudeLogo from "@/components/qanat/usage-overview/logos/claude.png";
import codexLogo from "@/components/qanat/usage-overview/logos/codex.png";
import { ProviderCard } from "@/components/qanat/accounts/provider-card";
import { PageHeader } from "@/components/qanat/page";
import { api } from "@/lib/api/client";

const cards = [
  { id: "claude", name: "Claude Code", prefix: "cc", logo: claudeLogo },
  { id: "codex", name: "OpenAI Codex", prefix: "cx", logo: codexLogo },
  { id: "antigravity", name: "Antigravity", prefix: "ag", logo: antigravityLogo },
];

export default function AccountsPage() {
  const accounts = api.useQuery("get", "/api/admin/accounts");

  return (
    <>
      <PageHeader
        title="Accounts"
        icon={Server}
        description="Upstream provider accounts pooled across users. Onboarding takes official API keys, or a Claude Code token from `claude setup-token`; credentials are sealed with the master key."
      />

      {accounts.isPending ? (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          <div className="h-16 animate-pulse rounded-xl bg-muted" />
          <div className="h-16 animate-pulse rounded-xl bg-muted" />
          <div className="h-16 animate-pulse rounded-xl bg-muted" />
        </div>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {cards.map((c) => (
            <ProviderCard
              key={c.id}
              providerId={c.id}
              name={c.name}
              logo={c.logo}
              accounts={(accounts.data ?? []).filter((a) => a.provider === c.id)}
            />
          ))}
        </div>
      )}
    </>
  );
}
