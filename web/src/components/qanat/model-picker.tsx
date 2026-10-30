"use client";

import { useQueries } from "@tanstack/react-query";
import { Check, Search } from "lucide-react";
import { useEffect, useRef, useState, type KeyboardEvent } from "react";

import { Tag } from "@/components/qanat/page";
import { Input } from "@/components/ui/input";
import { api, type Schemas } from "@/lib/api/client";
import { cn } from "@/lib/utils";

// useCatalogs returns model id → the providers whose stored catalog lists
// it. The queries are shared with the provider cards, so it costs no extra
// request.
export function useCatalogs(providers: Schemas["ProviderInfo"][]) {
  const catalogs = useQueries({
    queries: providers.map((p) =>
      api.queryOptions("get", "/api/admin/providers/{provider}/models", { params: { path: { provider: p.id } } }),
    ),
  });
  const listedBy = new Map<string, Schemas["ProviderInfo"][]>();
  catalogs.forEach((c, i) => {
    const p = providers[i];
    if (!p) return;
    for (const m of c.data?.models ?? []) {
      listedBy.set(m, [...(listedBy.get(m) ?? []), p]);
    }
  });
  return { listedBy, loading: catalogs.some((c) => c.isPending) };
}

// modelProviders is who serves a model: the providers whose catalog lists
// it, or — for a model no synced catalog lists — its home provider, the
// first in display order whose model-name families match. Antigravity also
// serves the claude- family, but a claude- id no catalog lists belongs to
// Claude Code, which comes first.
export function modelProviders(
  model: string,
  providers: Schemas["ProviderInfo"][],
  listedBy: Map<string, Schemas["ProviderInfo"][]>,
) {
  const listed = listedBy.get(model);
  if (listed) return listed;
  const home = providers.find((p) => p.families.some((f) => model.startsWith(f)));
  return home ? [home] : [];
}

// ModelPicker is a searchable list of the model ids the providers' stored
// catalogs list — the only models a price can be set for.
//
// The list keeps a fixed height in every state (loading, empty, no match,
// results) so typing never resizes the dialog around it. It is a combobox:
// ↑/↓ move the active option, Enter picks it, focus stays in the search box.
export function ModelPicker({
  providers,
  prices,
  value,
  onChange,
}: {
  providers: Schemas["ProviderInfo"][];
  prices: Schemas["Price"][];
  value: string;
  onChange: (modelId: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);
  const { listedBy, loading } = useCatalogs(providers);
  const priced = new Map(prices.map((p) => [p.model_id, p]));
  const q = query.trim().toLowerCase();
  const models = [...listedBy.keys()].filter((m) => m.toLowerCase().includes(q)).sort();
  const activeIndex = Math.min(active, Math.max(models.length - 1, 0));

  // Keep the active option in view without scrolling the page.
  useEffect(() => {
    listRef.current?.querySelector(`[data-index="${activeIndex}"]`)?.scrollIntoView({ block: "nearest" });
  }, [activeIndex]);

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const step = e.key === "ArrowDown" ? 1 : -1;
      setActive(Math.min(Math.max(activeIndex + step, 0), Math.max(models.length - 1, 0)));
    } else if (e.key === "Enter") {
      e.preventDefault(); // pick, don't submit the form
      const m = models[activeIndex];
      if (m) onChange(m);
    }
  };

  const message = (text: React.ReactNode) => (
    <div className="flex h-full items-center justify-center px-6 text-center text-meta text-muted-foreground">{text}</div>
  );

  return (
    <div className="flex flex-col gap-2">
      <div className="relative">
        <Search className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          id="model_id"
          role="combobox"
          aria-expanded="true"
          aria-controls="model-picker-list"
          aria-activedescendant={models[activeIndex] ? `model-option-${activeIndex}` : undefined}
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
          }}
          onKeyDown={onKeyDown}
          placeholder="Search registered models"
          aria-label="Search registered models"
          autoComplete="off"
          className="ps-8"
        />
      </div>
      <div
        ref={listRef}
        id="model-picker-list"
        role="listbox"
        aria-label="Registered models"
        className="h-64 overflow-y-auto overscroll-contain rounded-md border"
      >
        {loading
          ? message("Loading catalogs…")
          : listedBy.size === 0
            ? message("No provider catalog is stored yet. Use Sync on a provider card above to load its models.")
            : models.length === 0
              ? message(
                  <>
                    No registered model matches “{query}”. Try part of the id, like <span className="font-mono">opus</span> or{" "}
                    <span className="font-mono">gpt</span>.
                  </>,
                )
              : models.map((m, i) => {
                  const p = priced.get(m);
                  const selected = m === value;
                  return (
                    <div
                      key={m}
                      id={`model-option-${i}`}
                      data-index={i}
                      role="option"
                      aria-selected={selected}
                      onMouseDown={(e) => e.preventDefault()} // keep focus in the search box
                      onMouseEnter={() => setActive(i)}
                      onClick={() => onChange(m)}
                      className={cn(
                        "flex min-h-10 cursor-pointer items-center gap-2 px-3 py-2 transition-colors duration-150",
                        i === activeIndex && "bg-muted",
                        selected && "bg-brand-soft",
                      )}
                    >
                      <Check className={cn("size-4 shrink-0 text-brand-text", !selected && "invisible")} />
                      <span className="min-w-0 flex-1 truncate font-mono text-sm">{m}</span>
                      <span className="hidden shrink-0 text-meta text-muted-foreground sm:inline">{listedBy.get(m)!.map((p) => p.name).join(", ")}</span>
                      {p ? <Tag tone={p.source === "admin" ? "brand" : "neutral"}>{p.source === "admin" ? "Priced" : "Built-in"}</Tag> : <Tag tone="sand">Unpriced</Tag>}
                    </div>
                  );
                })}
      </div>
    </div>
  );
}
