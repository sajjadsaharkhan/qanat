"use client";

import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useCallback } from "react";

/**
 * A page filter kept in the URL's query string, so a view can be shared,
 * bookmarked and restored with Back. With `allowed`, unknown values read as
 * the fallback; without it any text is accepted (a search box). The fallback
 * itself is left out of the URL to keep links short.
 * Callers must sit under a <Suspense> boundary (useSearchParams).
 */
export function useSearchParam<T extends string>(key: string, fallback: T, allowed?: readonly T[]): [T, (value: T) => void] {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const raw = searchParams.get(key);
  const value = raw !== null && (!allowed || (allowed as readonly string[]).includes(raw)) ? (raw as T) : fallback;

  const set = useCallback(
    (next: T) => {
      // Read the live URL, not the render's snapshot, so two quick changes don't drop one.
      const params = new URLSearchParams(window.location.search);
      if (next === fallback) params.delete(key);
      else params.set(key, next);
      const qs = params.toString();
      router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false });
    },
    [router, pathname, key, fallback],
  );

  return [value, set];
}
