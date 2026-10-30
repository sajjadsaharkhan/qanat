import { permanentRedirect } from "next/navigation";

/** Usage now lives on Overview (design.md §7.3); old links keep their filters. */
export default async function UsagePage({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  const params = new URLSearchParams();
  for (const [k, v] of Object.entries(await searchParams)) {
    for (const value of Array.isArray(v) ? v : v === undefined ? [] : [v]) params.append(k, value);
  }
  const q = params.toString();
  permanentRedirect(q ? `/?${q}` : "/");
}
