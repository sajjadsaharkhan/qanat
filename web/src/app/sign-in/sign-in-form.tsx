"use client";

import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useState, type FormEvent } from "react";

import { QanatMark } from "@/components/qanat/qanat-mark";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { errorMessage, fetchClient } from "@/lib/api/client";

function Form({ passwordEnabled }: { passwordEnabled: boolean }) {
  const router = useRouter();
  const params = useSearchParams();
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);

  const submit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    setPending(true);
    setError(null);
    const { error } = await fetchClient.POST("/api/auth/login", {
      body: { username: String(form.get("username")), password: String(form.get("password")) },
    });
    setPending(false);
    if (error) {
      setError(errorMessage(error));
      return;
    }
    const next = params.get("next");
    router.replace(next?.startsWith("/") && !next.startsWith("//") ? next : "/");
    router.refresh();
  };

  if (!passwordEnabled) {
    return (
      <p className="text-muted-foreground">
        Password sign-in is turned off on this server, and Keycloak sign-in isn&apos;t set up yet. Ask the operator to set
        QANAT_ROOT_PASSWORD.
      </p>
    );
  }
  return (
    <form onSubmit={submit} className="flex w-full flex-col gap-4" noValidate>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="username">Username</Label>
        <Input id="username" name="username" autoComplete="username" defaultValue="root" required />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="password">Password</Label>
        <Input id="password" name="password" type="password" autoComplete="current-password" required autoFocus />
      </div>
      {error && (
        <p role="alert" className="text-destructive-text">
          {error}
        </p>
      )}
      <Button type="submit" disabled={pending}>
        {pending ? "Signing in…" : "Sign in"}
      </Button>
    </form>
  );
}

/**
 * An eight-point star lattice, the girih motif of Persian tilework, drawn as a
 * faint repeating pattern on the brand panel.
 */
function Girih() {
  return (
    <svg aria-hidden className="absolute inset-0 size-full opacity-[0.12]">
      <defs>
        <pattern id="girih" width="56" height="56" patternUnits="userSpaceOnUse">
          <g fill="none" stroke="currentColor" strokeWidth="1.2">
            <path d="M28 6 L34 22 L50 28 L34 34 L28 50 L22 34 L6 28 L22 22 Z" />
            <rect x="16" y="16" width="24" height="24" transform="rotate(45 28 28)" />
            <circle cx="28" cy="28" r="4" />
            <path d="M0 0 L6 6 M56 0 L50 6 M0 56 L6 50 M56 56 L50 50" />
          </g>
        </pattern>
      </defs>
      <rect width="100%" height="100%" fill="url(#girih)" />
    </svg>
  );
}

export function SignInForm({ passwordEnabled }: { passwordEnabled: boolean }) {
  return (
    <div className="grid min-h-svh lg:grid-cols-[minmax(0,5fr)_minmax(0,6fr)]">
      <aside className="relative hidden overflow-hidden bg-brand-deep text-brand-deep-foreground lg:flex lg:flex-col lg:justify-between lg:p-12">
        <Girih />
        <div className="relative flex items-center gap-2 font-semibold">
          <QanatMark className="size-7" title="" water={false} />
          Qanat
        </div>
        <div className="relative max-w-sm">
          <QanatMark className="mb-6 size-24" title="" water={false} />
          <p className="text-[28px] font-semibold leading-9">Quiet channel, clear signal.</p>
          <p className="mt-3 opacity-80">One gateway for your team&apos;s Claude, Codex and Antigravity accounts, with fair quotas for everyone.</p>
        </div>
        <p className="relative text-meta opacity-70"><span lang="fa">قنات</span> · an underground channel that moves water without being seen</p>
      </aside>
      <main className="mx-auto flex w-full max-w-sm flex-col justify-center gap-6 px-4 py-12">
        <div className="flex flex-col items-start gap-3">
          <QanatMark className="size-10 lg:hidden" title="Qanat" />
          <h1 className="text-title font-semibold">Sign in to Qanat</h1>
          <p className="text-muted-foreground">Root admin sign-in. Team members will sign in with Keycloak.</p>
        </div>
        <Suspense>
          <Form passwordEnabled={passwordEnabled} />
        </Suspense>
      </main>
    </div>
  );
}
