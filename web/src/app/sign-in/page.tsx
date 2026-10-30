import { redirect } from "next/navigation";

import { currentUser } from "@/lib/server/session";

import { SignInForm } from "./sign-in-form";

export const dynamic = "force-dynamic";
export const metadata = { title: "Sign in" };

/** Resolves sign-in methods on the server so the form renders without a flash. */
export default async function SignInPage() {
  if (await currentUser()) redirect("/");
  const base = process.env.QANAT_CONTROL_URL ?? "http://localhost:8081";
  const methods = await fetch(`${base}/api/auth/methods`, { cache: "no-store" })
    .then((r) => (r.ok ? (r.json() as Promise<{ password: boolean }>) : { password: false }))
    .catch(() => ({ password: false }));
  return <SignInForm passwordEnabled={methods.password} />;
}
