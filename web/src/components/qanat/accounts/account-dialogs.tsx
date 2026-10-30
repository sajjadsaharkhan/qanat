/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/providers/[id]/AddApiKeyModal.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat change: no web-OAuth login and no bulk import. A Claude account signs
 * in with a token from the official `claude setup-token` command; Codex and
 * Antigravity take an API key. The Check button calls the API's validate
 * endpoint, and the credential is sealed with the master key on save.
 */
"use client";

import { useState } from "react";
import { Loader2 } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { ProxyChoiceOptions } from "@/components/qanat/proxy-options";
import { api, errorMessage, type Schemas } from "@/lib/api/client";

type Account = Schemas["Account"];

function ProxyField({
  value,
  onChange,
  proxies,
  groups,
  inherit,
  id,
}: {
  value: string;
  onChange: (v: string) => void;
  proxies: Schemas["Proxy"][];
  groups: Schemas["ProxyGroup"][];
  inherit: string;
  id: string;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>Outbound proxy</Label>
      <Select value={value} onValueChange={onChange}>
        <SelectTrigger id={id}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <ProxyChoiceOptions proxies={proxies} groups={groups} inherit={inherit} />
        </SelectContent>
      </Select>
    </div>
  );
}

function CheckBadge({ state, message }: { state: "idle" | "loading" | "ok" | "bad"; message?: string }) {
  if (state === "idle" || state === "loading") return null;
  return (
    <div className="flex flex-col gap-0.5">
      <Badge variant="secondary" className={state === "ok" ? "bg-success/15 text-success" : "bg-destructive/15 text-destructive-text"}>
        {state === "ok" ? "Valid — the provider accepted it" : "Invalid"}
      </Badge>
      {message && <p className="text-xs text-muted-foreground">{message}</p>}
    </div>
  );
}

export function AddAccountDialog({
  provider,
  providerName,
  open,
  onOpenChange,
  proxies,
  groups,
  inherit,
  onSaved,
}: {
  provider: string;
  providerName: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  proxies: Schemas["Proxy"][];
  groups: Schemas["ProxyGroup"][];
  inherit: string;
  onSaved: () => void;
}) {
  const qc = useQueryClient();
  const create = api.useMutation("post", "/api/admin/accounts");
  const check = api.useMutation("post", "/api/admin/accounts/validate");

  const [kind, setKind] = useState<"token" | "api_key">("api_key");
  const [label, setLabel] = useState("");
  const [credential, setCredential] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [expiresAt, setExpiresAt] = useState("");
  const [proxy, setProxy] = useState("inherit");

  const reset = () => {
    setKind("api_key");
    setLabel("");
    setCredential("");
    setBaseUrl("");
    setExpiresAt("");
    setProxy("inherit");
  };

  const checkBody = {
    provider: provider as "claude" | "codex" | "antigravity",
    auth_kind: kind,
    base_url: baseUrl.trim() || null,
    access_token: credential.trim(),
  };

  // A Check verdict belongs to one credential + kind pair; drop it when either changes.
  const resetCheck = () => {
    if (check.data || check.error) check.reset();
  };

  const handleCheck = () => {
    check.mutate(
      { body: checkBody },
      {
        onSuccess: (res) => {
          if (!res.ok) toast.error(res.error || "The provider rejected this credential.");
        },
      },
    );
  };

  const handleSave = (e: React.FormEvent) => {
    e.preventDefault();
    create.mutate(
      {
        body: {
          provider: provider as "claude" | "codex" | "antigravity",
          label: label.trim(),
          auth_kind: kind,
          access_token: credential.trim(),
          base_url: baseUrl.trim() || null,
          expires_at: expiresAt ? new Date(expiresAt).toISOString() : null,
          proxy,
        },
      },
      {
        onSuccess: () => {
          qc.invalidateQueries({ queryKey: ["get", "/api/admin/accounts"] });
          toast("Account connected. Run Test to confirm the credential works.");
          onOpenChange(false);
          reset();
          onSaved();
        },
        onError: (err) => toast.error(errorMessage(err)),
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add {providerName} account</DialogTitle>
          <DialogDescription>
            Onboarding stays on official credentials. They are sealed with the master key and never shown again.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSave} className="flex flex-col gap-4">
          {provider === "claude" && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="auth_kind">Credential kind</Label>
              <Select value={kind} onValueChange={(v) => { resetCheck(); setKind(v as "token" | "api_key"); }}>
                <SelectTrigger id="auth_kind">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="api_key">API key (console.anthropic.com)</SelectItem>
                  <SelectItem value="token">Claude Code token (`claude setup-token`)</SelectItem>
                </SelectContent>
              </Select>
            </div>
          )}
          {provider === "claude" && kind === "token" && (
            <p className="rounded-lg bg-brand-soft px-3 py-2 text-xs text-brand-text">
              Run <code className="font-mono">claude setup-token</code> in a terminal, then paste the long-lived token it prints. Claude Code issues it itself; Qanat only stores it.
            </p>
          )}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="add-label">Name</Label>
            <Input id="add-label" value={label} onChange={(e) => setLabel(e.target.value)} placeholder="Production account" required />
          </div>
          <div className="flex items-end gap-2">
            <div className="flex flex-1 flex-col gap-1.5">
              <Label htmlFor="add-credential">{kind === "api_key" ? "API key" : "Token"}</Label>
              <Input
                id="add-credential"
                type="password"
                value={credential}
                onChange={(e) => { resetCheck(); setCredential(e.target.value); }}
                placeholder={provider === "codex" ? "sk-…" : provider === "antigravity" ? "AIza…" : kind === "api_key" ? "sk-ant-…" : "the setup-token output"}
                required
              />
            </div>
            <Button type="button" variant="outline" onClick={handleCheck} disabled={!credential.trim() || check.isPending}>
              {check.isPending ? (
                <>
                  <Loader2 className="me-1.5 size-4 animate-spin" /> Checking…
                </>
              ) : (
                "Check"
              )}
            </Button>
          </div>
          <CheckBadge
            state={check.data ? (check.data.ok ? "ok" : "bad") : check.isPending ? "loading" : check.error ? "bad" : "idle"}
            message={check.data && !check.data.ok ? check.data.error : check.error ? errorMessage(check.error) : undefined}
          />
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="add-base-url">Upstream base URL (optional)</Label>
            <Input id="add-base-url" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} placeholder="Default provider endpoint" />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="add-expires">Credential expires (optional)</Label>
            <Input id="add-expires" type="datetime-local" value={expiresAt} onChange={(e) => setExpiresAt(e.target.value)} />
          </div>
          <ProxyField id="add-proxy" value={proxy} onChange={setProxy} proxies={proxies} groups={groups} inherit={inherit} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending || !label.trim() || !credential.trim()}>
              {create.isPending ? "Connecting…" : "Connect account"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function EditAccountDialog({
  account,
  open,
  onOpenChange,
  proxies,
  groups,
  inherit,
  onSaved,
}: {
  account: Account | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  proxies: Schemas["Proxy"][];
  groups: Schemas["ProxyGroup"][];
  inherit: string;
  onSaved: () => void;
}) {
  const qc = useQueryClient();
  const patch = api.useMutation("patch", "/api/admin/accounts/{id}");

  const [label, setLabel] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [credential, setCredential] = useState("");
  const [expiresAt, setExpiresAt] = useState("");
  const [proxy, setProxy] = useState("inherit");
  const [enabled, setEnabled] = useState(true);
  const [loadedFor, setLoadedFor] = useState<string | null>(null);

  if (account && open && loadedFor !== account.id) {
    setLoadedFor(account.id);
    setLabel(account.label);
    setBaseUrl(account.base_url ?? "");
    setCredential("");
    setExpiresAt(account.expires_at ? account.expires_at.slice(0, 16) : "");
    setProxy(account.proxy);
    setEnabled(!account.disabled_at);
  }

  const handleSave = (e: React.FormEvent) => {
    e.preventDefault();
    if (!account) return;
    patch.mutate(
      {
        params: { path: { id: account.id } },
        body: {
          label: label.trim(),
          base_url: baseUrl.trim(),
          expires_at: expiresAt ? new Date(expiresAt).toISOString() : "",
          proxy,
          disabled: !enabled,
          ...(credential.trim() ? { access_token: credential.trim() } : {}),
        },
      },
      {
        onSuccess: () => {
          qc.invalidateQueries({ queryKey: ["get", "/api/admin/accounts"] });
          toast("Account updated.");
          onOpenChange(false);
          onSaved();
        },
        onError: (err) => toast.error(errorMessage(err)),
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Edit account</DialogTitle>
          <DialogDescription>
            Leave the credential blank to keep the stored one. Changes apply to new requests within seconds.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSave} className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="edit-label">Name</Label>
            <Input id="edit-label" value={label} onChange={(e) => setLabel(e.target.value)} required />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="edit-credential">Replace credential</Label>
            <Input
              id="edit-credential"
              type="password"
              value={credential}
              onChange={(e) => setCredential(e.target.value)}
              placeholder="Leave blank to keep the stored credential"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="edit-base-url">Upstream base URL</Label>
            <Input id="edit-base-url" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} placeholder="Default provider endpoint" />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="edit-expires">Credential expires</Label>
            <Input id="edit-expires" type="datetime-local" value={expiresAt} onChange={(e) => setExpiresAt(e.target.value)} />
            <p className="text-xs text-muted-foreground">Clear the field to remove the expiry.</p>
          </div>
          <ProxyField id="edit-proxy" value={proxy} onChange={setProxy} proxies={proxies} groups={groups} inherit={inherit} />
          <div className="flex items-center justify-between rounded-lg border px-3 py-2.5">
            <div>
              <Label htmlFor="edit-enabled">Enabled</Label>
              <p className="text-xs text-muted-foreground">A disabled account takes no new traffic.</p>
            </div>
            <Switch id="edit-enabled" checked={enabled} onCheckedChange={setEnabled} />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={patch.isPending}>
              {patch.isPending ? "Saving…" : "Save changes"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
