import { redirect } from "next/navigation";
import type { ReactNode } from "react";

import { AppShell } from "@/components/app-shell";
import { currentUser } from "@/lib/server/session";

export const dynamic = "force-dynamic";

export default async function AppLayout({ children }: { children: ReactNode }) {
  const me = await currentUser();
  if (!me) redirect("/sign-in");
  return <AppShell me={me}>{children}</AppShell>;
}
