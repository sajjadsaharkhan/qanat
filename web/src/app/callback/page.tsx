/*
 * Ported from 9router (https://github.com/decolua/9router), src/app/callback/page.js.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat change: this page only serves the admin accounts provider sign-in. It
 * relays the authorization code to the opener dialog (postMessage,
 * BroadcastChannel, then localStorage) and offers the URL for manual paste
 * when nothing is listening. It renders in the browser only: its content
 * depends on the URL the provider redirected to, which the server never sees.
 */
"use client";

import dynamic from "next/dynamic";
import { useEffect, useState } from "react";
import { CheckCircle2, Copy } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";

type Relay = { source: "qanat-oauth"; code: string; state: string };

/** Reads code/state from the query, tolerating Claude's "#state" forms. */
function readRelay(): Relay | null {
  const sp = new URLSearchParams(window.location.search);
  const code = sp.get("code") ?? "";
  let state = sp.get("state") ?? "";
  if (!state && window.location.hash.length > 1) {
    const h = decodeURIComponent(window.location.hash.slice(1));
    if (h && !h.startsWith("/")) state = h; // a route path hash would start with "/"
  }
  if (!code) return null;
  return { source: "qanat-oauth", code, state };
}

export default dynamic(() => Promise.resolve(OAuthCallback), { ssr: false });

function OAuthCallback() {
  const [relay] = useState(readRelay);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!relay) return;
    try {
      if (window.opener && !window.opener.closed) {
        window.opener.postMessage(relay, window.location.origin);
      }
    } catch { /* opener cross-origin or gone; fall through */ }
    try {
      const ch = new BroadcastChannel("oauth_callback");
      ch.postMessage(relay);
      ch.close();
    } catch { /* no BroadcastChannel */ }
    try {
      localStorage.setItem("oauth_callback", JSON.stringify(relay));
    } catch { /* storage unavailable */ }
  }, [relay]);

  const url = window.location.href;

  if (!relay) {
    return (
      <Shell>
        <p className="text-sm text-muted-foreground">
          This page only handles provider sign-ins. Close it and paste the full redirect URL in the Add-account dialog.
        </p>
      </Shell>
    );
  }

  return (
    <Shell>
      <p className="flex items-center gap-2 text-sm font-medium">
        <CheckCircle2 aria-hidden className="size-4 text-success" /> Sign-in received — you can close this window.
      </p>
      <p className="text-xs text-muted-foreground">
        If the sign-in dialog didn&apos;t react, copy this URL and paste it there.
      </p>
      <div className="flex items-center gap-2">
        <code className="min-w-0 flex-1 truncate rounded-md bg-muted px-2 py-1.5 font-mono text-xs">{url}</code>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => {
            navigator.clipboard.writeText(url).then(() => setCopied(true));
          }}
        >
          <Copy aria-hidden className="me-1.5 size-3.5" />
          {copied ? "Copied" : "Copy URL"}
        </Button>
      </div>
    </Shell>
  );
}

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <div className="grid min-h-svh place-items-center bg-background p-6">
      <Card className="w-full max-w-md gap-3 p-6">
        <h1 className="font-semibold">Provider sign-in</h1>
        {children}
      </Card>
    </div>
  );
}
