/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/providers/page.js (ProviderCard).
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat change: no card-level enable toggle — accounts enable individually on
 * the detail page — and status counts Qanat's state, admin disable and the
 * latest Test result.
 */
"use client";

import { ChevronRight } from "lucide-react";
import Link from "next/link";
import type { StaticImageData } from "next/image";

import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import type { Schemas } from "@/lib/api/client";

export type Account = Schemas["Account"];

/** The counts a provider card shows, in 9router's style. */
export function accountStats(accounts: Account[]) {
  const active = accounts.filter((a) => !a.disabled_at);
  const connected = active.filter((a) => a.state !== "disconnected" && a.last_check_ok !== false).length;
  const errored = active.filter((a) => a.state === "disconnected" || a.last_check_ok === false);
  const tag =
    errored.find((a) => a.state === "disconnected") !== undefined
      ? "ERR"
      : (errored.find((a) => a.last_check_error)?.last_check_error ?? "").slice(0, 3).replace(/[^x0-9]/g, "") || null;
  return {
    connected,
    error: errored.length,
    total: accounts.length,
    allDisabled: accounts.length > 0 && active.length === 0,
    tag: tag && tag !== "" ? tag : null,
  };
}

function StatusLine({ ok, children }: { ok?: boolean; children: React.ReactNode }) {
  return (
    <span className="inline-flex items-center gap-1 font-medium">
      <span aria-hidden className={`size-1.5 rounded-full ${ok ? "bg-success" : "bg-destructive"}`} />
      {children}
    </span>
  );
}

export function ProviderCard({
  providerId,
  name,
  logo,
  accounts,
}: {
  providerId: string;
  name: string;
  logo: StaticImageData;
  accounts: Account[];
}) {
  const stats = accountStats(accounts);
  return (
    <Link href={`/admin/accounts/${providerId}`} className="group min-w-0">
      <Card className="h-full gap-0 px-4 py-3 transition-colors group-hover:bg-accent/50">
        <div className="flex min-w-0 items-center justify-between gap-3">
          <div className="flex min-w-0 items-center gap-3">
            <span aria-hidden className="grid size-9 shrink-0 place-items-center rounded-lg bg-brand-soft">
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={logo.src} alt="" className="max-h-6 max-w-6 rounded object-contain" />
            </span>
            <div className="min-w-0">
              <h3 className="truncate font-semibold">{name}</h3>
              <div className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground">
                {stats.allDisabled ? (
                  <Badge variant="outline">Disabled</Badge>
                ) : stats.total === 0 ? (
                  <span>No connections</span>
                ) : (
                  <>
                    {stats.connected > 0 && <StatusLine ok>{stats.connected} connected</StatusLine>}
                    {stats.error > 0 && (
                      <StatusLine>
                        {stats.error} error{stats.tag ? ` (${stats.tag})` : ""}
                      </StatusLine>
                    )}
                  </>
                )}
              </div>
            </div>
          </div>
          <ChevronRight className="size-4 shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
        </div>
      </Card>
    </Link>
  );
}
