import Link from "next/link";

import { QanatMark } from "@/components/qanat/qanat-mark";
import { Button } from "@/components/ui/button";

export default function Forbidden() {
  return (
    <main className="mx-auto flex min-h-svh max-w-md flex-col items-start justify-center gap-4 px-4">
      <QanatMark state="broken" className="size-10" title="" />
      <h1 className="text-title font-semibold">This page is for admins</h1>
      <p className="text-muted-foreground">Your account doesn&apos;t have the admin role. Ask an admin if you need access.</p>
      <Button asChild variant="outline">
        <Link href="/">Back to overview</Link>
      </Button>
    </main>
  );
}
