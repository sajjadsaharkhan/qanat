"use client";

import { KeyRound, ListRestart, SquareTerminal } from "lucide-react";
import Link from "next/link";
import { type FormEvent, useState } from "react";

import { CopyField } from "@/components/qanat/copy-field";
import { PageHeader, SectionHeader } from "@/components/qanat/page";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { api, errorMessage } from "@/lib/api/client";
import { tierDotClass, modelTier } from "@/lib/model-tier";
import { type ClaudeFamily, claudeFamilies, latestClaude, settingsSnippet, settingsTemplate, testCommand } from "@/lib/snippet";

const placeholder = "qnt_live_YOUR_KEY";
const none = "__none__";

const groups = [
  { prefix: "cc/", name: "Claude" },
  { prefix: "cx/", name: "Codex" },
  { prefix: "ag/", name: "Antigravity" },
];

/** /v1/models names by provider prefix; unprefixed names are mapping rules and combos. */
function groupModels(models: string[]): { name: string; models: string[] }[] {
  const out = groups.map((g) => ({
    name: g.name,
    models: models.filter((m) => m.startsWith(g.prefix)),
  }));
  out.push({
    name: "Mapping rules",
    models: models.filter((m) => !groups.some((g) => m.startsWith(g.prefix))),
  });
  return out.filter((g) => g.models.length > 0);
}

export default function SetupPage() {
  const setup = api.useQuery("get", "/api/me/setup");
  const keys = api.useQuery("get", "/api/me/keys");
  const createKey = api.useMutation("post", "/api/me/keys");
  const loadModels = api.useMutation("post", "/api/me/setup/models");
  const url = setup.data?.gateway_url;

  const [keyDraft, setKeyDraft] = useState("");
  // The key the model list was loaded with; the template uses it, not the draft.
  const [key, setKey] = useState("");
  const [models, setModels] = useState<string[] | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [slotDraft, setSlotDraft] = useState<Partial<Record<ClaudeFamily, string>>>({});
  const [error, setError] = useState<string | null>(null);

  const load = (k: string) => {
    setError(null);
    loadModels.mutate(
      { body: { key: k } },
      {
        onSuccess: (r) => {
          setKey(k);
          setModels(r.models);
          setSelected(new Set(r.models));
          setSlotDraft({});
        },
        onError: (err) => setError(errorMessage(err)),
      },
    );
  };
  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    load(keyDraft.trim());
  };
  const newKey = () => {
    setError(null);
    createKey.mutate(
      { body: { name: "Claude Code (Setup)", expires_in_days: null } },
      {
        onSuccess: (k) => {
          setKeyDraft(k.key);
          keys.refetch();
          load(k.key);
        },
        onError: (err) => setError(errorMessage(err)),
      },
    );
  };

  if (!url) {
    return (
      <>
        <PageHeader title="Setup" icon={SquareTerminal} description="Point Claude Code at Qanat." />
        <Skeleton className="h-64 w-full" />
      </>
    );
  }

  const activeKeys = (keys.data ?? []).filter((k) => !k.revoked_at);
  const chosen = (models ?? []).filter((m) => selected.has(m));
  // Slots default to the newest Claude model of each family, until picked by hand.
  const latest = latestClaude(chosen);
  const slots = Object.fromEntries(
    claudeFamilies.map((f) => {
      const draft = slotDraft[f];
      return [f, draft === none ? "" : draft && chosen.includes(draft) ? draft : (latest[f] ?? "")];
    }),
  ) as Record<ClaudeFamily, string>;
  const toggle = (m: string, on: boolean) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (on) next.add(m);
      else next.delete(m);
      return next;
    });

  return (
    <>
      <PageHeader
        title="Setup"
        icon={SquareTerminal}
        description={
          <>
            Point Claude Code at Qanat with two settings. Replace {placeholder} with a key from{" "}
            <Link href="/keys" className="underline underline-offset-2">
              Keys
            </Link>
            , or build a full settings.json with your models below.
          </>
        }
      />
      <SectionHeader>~/.claude/settings.json</SectionHeader>
      <CopyField label="settings.json" value={settingsSnippet(url, placeholder)} multiline />
      <SectionHeader>Test the connection</SectionHeader>
      <CopyField label="test command" value={testCommand(url, placeholder)} />

      <SectionHeader>Build settings.json from your models</SectionHeader>
      <form onSubmit={submit} className="flex max-w-3xl flex-col gap-1.5">
        <Label htmlFor="tpl-key">API key</Label>
        <div className="flex flex-wrap gap-2">
          <Input
            id="tpl-key"
            className="min-w-0 flex-1 font-mono"
            value={keyDraft}
            onChange={(e) => setKeyDraft(e.target.value)}
            placeholder={placeholder}
            autoComplete="off"
            spellCheck={false}
          />
          <Button type="submit" disabled={!keyDraft.trim() || loadModels.isPending}>
            <ListRestart />
            {loadModels.isPending ? "Loading…" : "Load models"}
          </Button>
          <Button type="button" variant="outline" onClick={newKey} disabled={createKey.isPending}>
            <KeyRound />
            New key
          </Button>
        </div>
        {error ? (
          <p role="alert" className="text-destructive">
            {error}
          </p>
        ) : (
          <p className="text-muted-foreground">
            Lists what <code className="font-mono">/v1/models</code> returns for this key.
            {activeKeys.length > 0 &&
              ` Your keys: ${activeKeys.map((k) => k.name || k.id.slice(0, 8)).join(", ")} (keys are never re-shown, so paste one or create a new one).`}
          </p>
        )}
      </form>

      {models && models.length === 0 && (
        <p className="mt-4 max-w-[72ch] text-muted-foreground">
          This key can&apos;t use any model yet. Ask an admin to connect a provider account or widen your Plan.
        </p>
      )}

      {models && models.length > 0 && (
        <>
          <SectionHeader
            actions={
              <div className="flex gap-2">
                <Button variant="ghost" size="sm" onClick={() => setSelected(new Set(models))}>
                  Select all
                </Button>
                <Button variant="ghost" size="sm" onClick={() => setSelected(new Set())}>
                  Clear
                </Button>
              </div>
            }
          >
            Models in the picker ({chosen.length} of {models.length})
          </SectionHeader>
          <div className="grid max-w-5xl gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {groupModels(models).map((g) => (
              <fieldset key={g.name} className="flex flex-col gap-1.5 rounded-lg border p-3">
                <legend className="px-1 font-medium">{g.name}</legend>
                {g.models.map((m) => (
                  <label key={m} className="flex min-w-0 cursor-pointer items-center gap-2 font-mono text-body">
                    <Checkbox checked={selected.has(m)} onCheckedChange={(v) => toggle(m, v === true)} />
                    <span className={`size-2 shrink-0 rounded-full ${tierDotClass(modelTier(m))}`} aria-hidden />
                    <span className="truncate">{m}</span>
                  </label>
                ))}
              </fieldset>
            ))}
          </div>

          <SectionHeader>Default models</SectionHeader>
          <div className="grid max-w-3xl gap-3 sm:grid-cols-3">
            {claudeFamilies.map((f) => (
              <div key={f} className="flex min-w-0 flex-col gap-1.5">
                <Label htmlFor={`tpl-${f}`} className="capitalize">
                  {f}
                </Label>
                <Select value={slots[f] || none} onValueChange={(v) => setSlotDraft((d) => ({ ...d, [f]: v }))}>
                  <SelectTrigger id={`tpl-${f}`} className="w-full font-mono">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value={none}>Not set</SelectItem>
                    {chosen.map((m) => (
                      <SelectItem key={m} value={m} className="font-mono">
                        {m}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            ))}
          </div>

          <SectionHeader>settings.json</SectionHeader>
          <CopyField
            label="settings.json with model picker"
            value={settingsTemplate({
              gatewayUrl: url,
              key,
              slots,
              models: chosen,
            })}
            multiline
          />
          <p className="mt-2 max-w-[72ch] text-muted-foreground">
            Only the newest Claude Opus, Sonnet and Haiku carry <code className="font-mono">behavesAs</code>; other models appear in the
            picker as they are.
          </p>
        </>
      )}
    </>
  );
}
