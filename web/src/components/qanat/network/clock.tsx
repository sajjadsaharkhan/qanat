"use client";

import { useSyncExternalStore } from "react";

import { ago, span } from "@/lib/network";

// One shared 1 s ticker. Only the leaves below subscribe, so "4s ago" stays
// true without re-rendering the page, its charts or its tables every second.
let now = Date.now();
const listeners = new Set<() => void>();
let timer: ReturnType<typeof setInterval> | undefined;

function subscribe(cb: () => void) {
  listeners.add(cb);
  if (!timer) {
    timer = setInterval(() => {
      now = Date.now();
      listeners.forEach((l) => l());
    }, 1000);
  }
  return () => {
    listeners.delete(cb);
    if (listeners.size === 0 && timer) {
      clearInterval(timer);
      timer = undefined;
    }
  };
}

function useNow() {
  return useSyncExternalStore(subscribe, () => now, () => now);
}

/** Ago: "4s ago", kept current. */
export function Ago({ iso }: { iso: string | null | undefined }) {
  return <>{ago(iso, useNow())}</>;
}

/** Since: how long since iso, "3m". */
export function Since({ iso }: { iso: string }) {
  return <>{span(iso, useNow())}</>;
}

/** Left: how long until iso, "45s"; "0s" once it has passed. */
export function Left({ iso }: { iso: string }) {
  const n = useNow();
  return <>{span(new Date(n).toISOString(), Math.max(n, Date.parse(iso)))}</>;
}

/** Until renders `active` while iso is in the future, else `after`. */
export function Until({ iso, active, after }: { iso: string; active: React.ReactNode; after: React.ReactNode }) {
  return <>{Date.parse(iso) > useNow() ? active : after}</>;
}
