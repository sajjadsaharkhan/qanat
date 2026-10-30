/**
 * Server-side session lookup for layouts: asks the control plane who the
 * signed-in person is, forwarding their cookie. Never runs in the browser.
 */
import "server-only";

import { cookies } from "next/headers";

import type { Me } from "@/lib/api/client";

export async function currentUser(): Promise<Me | null> {
  const jar = await cookies();
  const session = jar.get("qanat_session");
  if (!session) return null;
  const base = process.env.QANAT_CONTROL_URL ?? "http://localhost:8081";
  try {
    const res = await fetch(`${base}/api/me`, {
      headers: { cookie: `qanat_session=${session.value}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Me;
  } catch {
    return null;
  }
}
