/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/providers/[id]/AddApiKeyModal.js and
 * src/shared/components/OAuthModal.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat change: Claude, Codex and Antigravity sign in through their own OAuth
 * flows (OAuthSignIn here, relayed by the /callback page; Codex's fixed
 * loopback redirect is pasted instead) or take an API key; Claude also takes a
 * `claude setup-token` token. The Check button calls the API's validate
 * endpoint, and credentials are sealed with the master key on save.
 */
"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { ExternalLink, Loader2 } from "lucide-react";
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
import { api, errorMessage, fetchClient, type Schemas } from "@/lib/api/client";

type Account = Schemas["Account"];
type AuthKind = "oauth" | "token" | "api_key";
type ProviderID = Schemas["Provider"];

/** Providers with a sign-in flow, and whose account the button signs in with ("" = the provider's own). */
const signInWith: Partial<Record<string, string>> = { claude: "", codex: "ChatGPT", antigravity: "Google" };

/**
 * Providers whose client only accepts its own loopback redirect (Codex CLI's
 * localhost:1455), so the browser can't come back to /callback: the admin
 * pastes the address the sign-in lands on.
 */
const pasteOnly = new Set(["codex"]);

/** Credential kinds offered per provider, in menu order. */
const kindOptions: Partial<Record<string, { value: AuthKind; label: string }[]>> = {
  claude: [
    { value: "oauth", label: "Claude subscription (Pro/Max) — sign in" },
    { value: "api_key", label: "API key (console.anthropic.com)" },
    { value: "token", label: "Claude Code token (`claude setup-token`)" },
  ],
  codex: [
    { value: "oauth", label: "ChatGPT subscription (Codex) — sign in" },
    { value: "api_key", label: "API key (platform.openai.com)" },
  ],
  antigravity: [
    { value: "oauth", label: "Google account (Antigravity) — sign in" },
    { value: "api_key", label: "Gemini API key (aistudio.google.com)" },
  ],
};

const hasSignIn = (provider: string) => signInWith[provider] !== undefined;
const defaultKind = (provider: string): AuthKind => (hasSignIn(provider) ? "oauth" : "api_key");

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

/** The redirect_uri the provider comes back to — this dashboard's /callback on loopback. */
function callbackRedirectUri(): string {
  const port = window.location.port || (window.location.protocol === "https:" ? "443" : "80");
  return `http://localhost:${port}/callback`;
}

/*
 * OAuthSignIn ports 9router's OAuthModal: it asks the API for the provider's
 * authorize URL, opens it in a popup, and waits for the /callback page to
 * relay the code. The paste field covers remote dashboards, blocked popups
 * and Codex, whose redirect never reaches this dashboard.
 */
function OAuthSignIn({ provider, providerName, onDone }: { provider: string; providerName: string; onDone: () => void }) {
  const identity = signInWith[provider] || providerName;
  const paste = pasteOnly.has(provider);
  const qc = useQueryClient();
  const { mutate: exchangeMutate, isPending: exchanging, error: exchangeError } = api.useMutation(
    "post",
    "/api/admin/accounts/oauth/{provider}/exchange",
  );

  const [starting, setStarting] = useState(false);
  const [pending, setPending] = useState<{ state: string; redirectUri: string; authUrl: string } | null>(null);
  const [manualUrl, setManualUrl] = useState("");

  const connect = useCallback(
    (code: string, state: string, redirectUri: string) => {
      exchangeMutate(
        { params: { path: { provider: provider as ProviderID } }, body: { code, state, redirect_uri: redirectUri } },
        {
          onSuccess: (res) => {
            qc.invalidateQueries({ queryKey: ["get", "/api/admin/accounts"] });
            toast(res.merged ? "Signed in again — the existing account was refreshed." : `Connected to ${providerName} via sign-in.`);
            onDone();
          },
          onError: (err) => toast.error(errorMessage(err)),
        },
      );
    },
    [exchangeMutate, qc, provider, providerName, onDone],
  );

  useEffect(() => {
    if (!pending) return;
    let done = false; // the pending sign-in is single-use server-side; ignore re-deliveries
    const finish = (code: string, state: string) => {
      if (done) return;
      done = true;
      connect(code, state, pending.redirectUri);
    };
    const onRelay = (ev: MessageEvent) => {
      const d = ev.data as Partial<Relay>;
      if (d?.source !== "qanat-oauth" || !d.code) return;
      finish(d.code, d.state || "");
    };
    const onMessage = (ev: MessageEvent) => {
      if (ev.origin !== window.location.origin) return;
      onRelay(ev);
    };
    window.addEventListener("message", onMessage);
    const channel = "BroadcastChannel" in window ? new BroadcastChannel("oauth_callback") : null;
    channel?.addEventListener("message", onRelay);
    return () => {
      window.removeEventListener("message", onMessage);
      channel?.close();
    };
  }, [pending, connect]);

  const redirectUri = useMemo(() => callbackRedirectUri(), []);

  const start = async () => {
    setStarting(true);
    try {
      const { data, error } = await fetchClient.GET("/api/admin/accounts/oauth/{provider}/authorize", {
        params: { path: { provider: provider as ProviderID }, query: { redirect_uri: redirectUri } },
      });
      if (error) {
        toast.error(errorMessage(error));
        return;
      }
      setPending({ state: data.state, redirectUri: data.redirect_uri, authUrl: data.auth_url });
      const popup = window.open(data.auth_url, "qanat-oauth", "width=600,height=700");
      if (!popup) toast.error("The sign-in popup was blocked — open the URL below manually.");
    } finally {
      setStarting(false);
    }
  };

  const pasteRedirect = () => {
    const raw = manualUrl.trim();
    if (!raw) return;
    let code = "";
    let state = "";
    try {
      const u = new URL(raw);
      code = u.searchParams.get("code") ?? "";
      state = u.searchParams.get("state") ?? (u.hash.length > 1 ? decodeURIComponent(u.hash.slice(1)) : "");
    } catch {
      toast.error("Paste the full URL the provider redirected to.");
      return;
    }
    if (!code) {
      toast.error("That URL has no authorization code in it.");
      return;
    }
    connect(code, state, pending?.redirectUri ?? redirectUri);
  };

  return (
    <div className="flex flex-col gap-3">
      <p className="rounded-lg bg-brand-soft px-3 py-2 text-xs text-brand-text">
        Signs in through {providerName}&apos;s own OAuth flow — the same one the official client uses. The tokens are sealed with the master
        key and refreshed by Qanat.
      </p>
      {pending && paste ? (
        <p className="text-sm text-muted-foreground">
          Complete the sign-in in the popup. It ends on a <code className="font-mono">localhost:1455</code> page that won&apos;t load — that&apos;s
          expected. Copy that page&apos;s full address and paste it below.
        </p>
      ) : pending ? (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 aria-hidden className="size-4 animate-spin" /> Waiting for {identity}… complete the sign-in in the popup.
        </p>
      ) : null}
      <Button type="button" onClick={start} disabled={starting || exchanging} className="self-start">
        {starting ? <Loader2 aria-hidden className="me-1.5 size-4 animate-spin" /> : <ExternalLink aria-hidden className="me-1.5 size-4" />}
        Sign in with {identity}
      </Button>
      {pending && (
        <div className="flex flex-col gap-2 rounded-lg border px-3 py-2.5">
          <p className="text-xs text-muted-foreground">
            {paste
              ? "Popup blocked? Open this URL yourself, sign in, then paste the address it redirects to."
              : "On another machine, or the popup was blocked? Open this URL yourself, then paste the address it redirects to."}
          </p>
          <div className="flex items-center gap-2">
            <Button type="button" variant="outline" size="sm" onClick={() => navigator.clipboard.writeText(pending.authUrl)}>
              Copy sign-in URL
            </Button>
          </div>
          <div className="flex items-end gap-2">
            <div className="flex flex-1 flex-col gap-1.5">
              <Label htmlFor="oauth-paste">Redirect URL</Label>
              <Input
                id="oauth-paste"
                value={manualUrl}
                onChange={(e) => setManualUrl(e.target.value)}
                placeholder={`${pending.redirectUri}?code=…`}
                autoComplete="off"
              />
            </div>
            <Button type="button" variant="outline" onClick={pasteRedirect} disabled={!manualUrl.trim() || exchanging}>
              {exchanging ? <Loader2 aria-hidden className="me-1.5 size-4 animate-spin" /> : null}
              Connect
            </Button>
          </div>
        </div>
      )}
      {exchangeError && <p className="text-xs text-destructive-text">{errorMessage(exchangeError)}</p>}
    </div>
  );
}

type Relay = { source: "qanat-oauth"; code: string; state: string };

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

  const [kind, setKind] = useState<AuthKind>(defaultKind(provider));
  const [label, setLabel] = useState("");
  const [credential, setCredential] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [expiresAt, setExpiresAt] = useState("");
  const [proxy, setProxy] = useState("inherit");

  const reset = () => {
    setKind(defaultKind(provider));
    setLabel("");
    setCredential("");
    setBaseUrl("");
    setExpiresAt("");
    setProxy("inherit");
  };

  const checkBody = {
    provider: provider as "claude" | "codex" | "antigravity",
    auth_kind: kind === "api_key" ? ("api_key" as const) : ("token" as const),
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
          auth_kind: kind === "api_key" ? ("api_key" as const) : ("token" as const),
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
            Sign in through the provider&apos;s own flow, or paste an official credential. Everything is sealed with the master key and never
            shown again.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSave} className="flex flex-col gap-4">
          {kindOptions[provider] && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="auth_kind">Credential kind</Label>
              <Select value={kind} onValueChange={(v) => { resetCheck(); setKind(v as AuthKind); }}>
                <SelectTrigger id="auth_kind">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {kindOptions[provider]!.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}
          {hasSignIn(provider) && kind === "oauth" ? (
            <OAuthSignIn
              provider={provider}
              providerName={providerName}
              onDone={() => {
                onOpenChange(false);
                reset();
                onSaved();
              }}
            />
          ) : (
            <>
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
            </>
          )}
          <ProxyField id="add-proxy" value={proxy} onChange={setProxy} proxies={proxies} groups={groups} inherit={inherit} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            {kind !== "oauth" && (
              <Button type="submit" disabled={create.isPending || !label.trim() || !credential.trim()}>
                {create.isPending ? "Connecting…" : "Connect account"}
              </Button>
            )}
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
          {account?.auth_kind === "oauth" ? (
            <p className="rounded-lg bg-brand-soft px-3 py-2 text-xs text-brand-text">
              OAuth account — its tokens are managed by Qanat. If sign-in breaks, use the provider card to sign in again; Qanat refreshes the
              existing account.
            </p>
          ) : (
            <>
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
                <Label htmlFor="edit-expires">Credential expires</Label>
                <Input id="edit-expires" type="datetime-local" value={expiresAt} onChange={(e) => setExpiresAt(e.target.value)} />
                <p className="text-xs text-muted-foreground">Clear the field to remove the expiry.</p>
              </div>
            </>
          )}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="edit-base-url">Upstream base URL</Label>
            <Input id="edit-base-url" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} placeholder="Default provider endpoint" />
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
