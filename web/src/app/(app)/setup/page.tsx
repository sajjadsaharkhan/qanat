"use client";

import { SquareTerminal } from "lucide-react";
import Link from "next/link";
import { useState } from "react";

import { CopyField } from "@/components/qanat/copy-field";
import { PageHeader, SectionHeader } from "@/components/qanat/page";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "@/lib/api/client";
import { settingsSnippet, settingsTemplate, testCommand } from "@/lib/snippet";

const placeholder = "qnt_live_YOUR_KEY";

/** Newest-looking model of a family from a catalog: highest version tail wins. */
function pickModel(models: string[], family: "sonnet" | "opus" | "haiku"): string {
  const inFamily = models.filter((m) => m.startsWith(`claude-${family}`));
  return inFamily.sort().at(-1) ?? `claude-${family}`;
}

export default function SetupPage() {
  const setup = api.useQuery("get", "/api/me/setup");
  const keys = api.useQuery("get", "/api/me/keys");
  const url = setup.data?.gateway_url;

  const claude = api.useQuery("get", "/api/admin/providers/{provider}/models", { params: { path: { provider: "claude" } } });
  const codex = api.useQuery("get", "/api/admin/providers/{provider}/models", { params: { path: { provider: "codex" } } });
  const antigravity = api.useQuery("get", "/api/admin/providers/{provider}/models", { params: { path: { provider: "antigravity" } } });

  const [key, setKey] = useState(placeholder);
  const [sonnetDraft, setSonnet] = useState("");
  const [opusDraft, setOpus] = useState("");
  const [haikuDraft, setHaiku] = useState("");

  // Auto-picked from the Claude catalog until the admin edits a slot by hand.
  const claudeModels = claude.data?.models ?? [];
  const sonnet = sonnetDraft || pickModel(claudeModels, "sonnet");
  const opus = opusDraft || pickModel(claudeModels, "opus");
  const haiku = haikuDraft || pickModel(claudeModels, "haiku");

  const activeKeys = (keys.data ?? []).filter((k) => !k.revoked_at);
  const catalogs = [
    { prefix: "cc", name: "Claude", models: claudeModels },
    { prefix: "cx", name: "Codex", models: codex.data?.models ?? [] },
    { prefix: "ag", name: "Antigravity", models: antigravity.data?.models ?? [] },
  ];

  if (!url) {
    return (
      <>
        <PageHeader title="Setup" icon={SquareTerminal} description="Point Claude Code at Qanat." />
        <Skeleton className="h-64 w-full" />
      </>
    );
  }

  const template =
    sonnet && opus && haiku
      ? settingsTemplate({ gatewayUrl: url, key, sonnetModel: sonnet, opusModel: opus, haikuModel: haiku, catalogs })
      : "";

  return (
    <>
      <PageHeader
        title="Setup" icon={SquareTerminal}
        description={
          <>
            Point Claude Code at Qanat with two settings. Replace {placeholder} with a key from{" "}
            <Link href="/keys" className="underline underline-offset-2">
              Keys
            </Link>
            ; the key is filled in for you when you create one.
          </>
        }
      />
      <SectionHeader>~/.claude/settings.json</SectionHeader>
      <CopyField label="settings.json" value={settingsSnippet(url, key)} multiline />
      <SectionHeader>Test the connection</SectionHeader>
      <CopyField label="test command" value={testCommand(url, key)} />

      <SectionHeader>Claude Code template with model picker</SectionHeader>
      <div className="mb-3 grid max-w-3xl gap-3 sm:grid-cols-2">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="tpl-key">Auth token</Label>
          <Input id="tpl-key" value={key} onChange={(e) => setKey(e.target.value)} placeholder={placeholder} />
          {activeKeys.length > 0 && (
            <p className="text-xs text-muted-foreground">
              Your keys: {activeKeys.map((k) => k.name || k.id.slice(0, 8)).join(", ")} — paste one above (keys are never re-shown).
            </p>
          )}
        </div>
        <div className="grid grid-cols-3 gap-2">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="tpl-sonnet">Sonnet slot</Label>
            <Input id="tpl-sonnet" value={sonnet} onChange={(e) => setSonnet(e.target.value)} />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="tpl-opus">Opus slot</Label>
            <Input id="tpl-opus" value={opus} onChange={(e) => setOpus(e.target.value)} />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="tpl-haiku">Haiku slot</Label>
            <Input id="tpl-haiku" value={haiku} onChange={(e) => setHaiku(e.target.value)} />
          </div>
        </div>
      </div>
      {template ? (
        <CopyField label="settings.template.json" value={template} multiline />
      ) : (
        <Skeleton className="h-24 w-full max-w-3xl" />
      )}
      <p className="mt-2 max-w-[72ch] text-muted-foreground">
        The model picker lists every stored provider-catalog model with its prefix; non-Claude models behave as the Claude slot their name
        suggests. The catalogs come from the Models page (&quot;load from provider&quot;).
      </p>
    </>
  );
}
