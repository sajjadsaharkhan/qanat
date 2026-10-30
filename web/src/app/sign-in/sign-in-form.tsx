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

export function SignInForm({ passwordEnabled }: { passwordEnabled: boolean }) {
  return (
    <main className="mx-auto flex min-h-svh w-full max-w-sm flex-col justify-center gap-6 px-4">
      <div className="flex flex-col items-start gap-3">
        <QanatMark className="size-10" title="Qanat" />
        <h1 className="text-title font-semibold">Sign in to Qanat</h1>
        <p className="text-muted-foreground">Root admin sign-in. Team members will sign in with Keycloak.</p>
      </div>
      <Suspense>
        <Form passwordEnabled={passwordEnabled} />
      </Suspense>
    </main>
  );
}
