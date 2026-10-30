"use client";

import { SquareTerminal } from "lucide-react";
import Link from "next/link";

import { CopyField } from "@/components/qanat/copy-field";
import { PageHeader, SectionHeader } from "@/components/qanat/page";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "@/lib/api/client";
import { settingsSnippet, testCommand } from "@/lib/snippet";

const placeholder = "qnt_live_YOUR_KEY";

export default function SetupPage() {
  const setup = api.useQuery("get", "/api/me/setup");
  const url = setup.data?.gateway_url;

  return (
    <>
      <PageHeader
        title="Setup" icon={SquareTerminal}
        description={
          <>
            Point Claude Code at Qanat with two settings. Replace {placeholder} with a key from{" "}
            <Link href="/keys" className="underline underline-offset-2">
              Keys
            </Link>
            ; the key is filled in for you when you create one.
          </>
        }
      />
      {!url ? (
        <Skeleton className="h-64 w-full" />
      ) : (
        <>
          <SectionHeader>~/.claude/settings.json</SectionHeader>
          <CopyField label="settings.json" value={settingsSnippet(url, placeholder)} multiline />
          <SectionHeader>Test the connection</SectionHeader>
          <CopyField label="test command" value={testCommand(url, placeholder)} />
          <p className="mt-2 max-w-[72ch] text-muted-foreground">
            It lists the models your Plan allows. The gateway answers once it&apos;s enabled in the next milestone.
          </p>
        </>
      )}
    </>
  );
}
