import { forbidden } from "next/navigation";
import type { ReactNode } from "react";

import { currentUser } from "@/lib/server/session";

/** Admin pages answer 403 to anyone without role admin (D10). */
export default async function AdminLayout({ children }: { children: ReactNode }) {
  const me = await currentUser();
  if (me?.role !== "admin") forbidden();
  return children;
}
