import Link from "next/link";

import { QanatMark } from "@/components/qanat/qanat-mark";
import { Button } from "@/components/ui/button";

export const metadata = { title: "Access denied" };

export default function AccessDeniedPage() {
  return (
    <main className="mx-auto flex min-h-svh max-w-md flex-col items-start justify-center gap-4 px-4">
      <QanatMark state="broken" className="size-10" title="" />
      <h1 className="text-title font-semibold">You don&apos;t have access</h1>
      <p className="text-muted-foreground">
        Only members of the <code className="rounded bg-muted px-1 py-0.5 font-mono text-sm">qanat-users</code> group
        can use Qanat. Ask your Keycloak admin to add you to the group, then sign in again.
      </p>
      <Button asChild variant="outline">
        <Link href="/sign-in">Back to sign in</Link>
      </Button>
    </main>
  );
}
