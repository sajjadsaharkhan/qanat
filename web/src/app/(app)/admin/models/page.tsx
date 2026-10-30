"use client";

import { Boxes, ChevronLeft, ChevronRight, Pencil, Plus, Search, Trash2, X } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";

import { FacetedFilter } from "@/components/qanat/faceted-filter";
import { MappingEditor } from "@/components/qanat/mapping-editor";
import { ModelPicker, modelProviders, useCatalogs } from "@/components/qanat/model-picker";
import { EmptyState, PageHeader, SectionHeader, Tag } from "@/components/qanat/page";
import { ProviderModelsCard } from "@/components/qanat/provider-models";
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatUsd } from "@/lib/format";
import { api, errorMessage, type Schemas } from "@/lib/api/client";

const dateFmt = new Intl.DateTimeFormat("en-US", { dateStyle: "medium" });

const PRICE_PAGE_SIZE = 20;

type Price = Schemas["Price"];

type Source = "builtin" | "override" | "custom";
const SOURCES: { id: Source; label: string }[] = [
  { id: "builtin", label: "Built-in" },
  { id: "override", label: "Override" },
  { id: "custom", label: "Custom" },
];

// Built-in: the default price applies. Override: an admin price replaces a
// default. Custom: an admin price for a model with no default.
function sourceOf(p: Price): Source {
  return p.source === "default" ? "builtin" : p.has_default ? "override" : "custom";
}

export default function ModelsPage() {
  const qc = useQueryClient();
  const mappings = api.useQuery("get", "/api/admin/mappings");
  const prices = api.useQuery("get", "/api/admin/prices");
  const providers = api.useQuery("get", "/api/admin/providers");
  const accounts = api.useQuery("get", "/api/admin/accounts");
  const pricedModels = [...new Set((prices.data ?? []).filter((p) => p.confirmed_at).map((p) => p.model_id))];
  const setPrice = api.useMutation("post", "/api/admin/prices");
  const deletePrice = api.useMutation("delete", "/api/admin/prices/{model}");

  // null: closed; "new": add a price; a Price: edit that model's price.
  const [editing, setEditing] = useState<Price | "new" | null>(null);
  const [picked, setPicked] = useState("");
  const [filter, setFilter] = useState("");
  const [page, setPage] = useState(0);
  const [providerFilter, setProviderFilter] = useState<Set<string>>(new Set());
  const [sourceFilter, setSourceFilter] = useState<Set<string>>(new Set());
  const { listedBy } = useCatalogs(providers.data ?? []);
  const current = editing === "new" ? null : editing;
  const modelId = current?.model_id ?? picked;
  // Picking a model that already has a price starts from that price.
  const base = current ?? (prices.data ?? []).find((p) => p.model_id === picked);

  const openForm = (p: Price | "new") => {
    setPicked("");
    setEditing(p);
  };

  // Each price with who serves it. A facet's counts honour the search and the
  // other facet, so a count is what ticking that value would add.
  const rows = (prices.data ?? []).map((p) => ({ ...p, providers: modelProviders(p.model_id, providers.data ?? [], listedBy) }));
  const byText = rows.filter((p) => p.model_id.includes(filter.trim().toLowerCase()));
  const inProviders = (p: (typeof rows)[number]) => providerFilter.size === 0 || p.providers.some((v) => providerFilter.has(v.id));
  const inSources = (p: (typeof rows)[number]) => sourceFilter.size === 0 || sourceFilter.has(sourceOf(p));
  const matching = byText.filter((p) => inProviders(p) && inSources(p));
  const filtered = providerFilter.size > 0 || sourceFilter.size > 0 || filter.trim() !== "";
  const clearFilters = () => {
    setFilter("");
    setProviderFilter(new Set());
    setSourceFilter(new Set());
    setPage(0);
  };
  const resetPage = <T,>(set: (v: T) => void) => (v: T) => {
    set(v);
    setPage(0);
  };
  const pages = Math.max(1, Math.ceil(matching.length / PRICE_PAGE_SIZE));
  const pageIndex = Math.min(page, pages - 1);
  const visible = matching.slice(pageIndex * PRICE_PAGE_SIZE, (pageIndex + 1) * PRICE_PAGE_SIZE);

  const handleSetPrice = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!modelId) {
      toast.error("Pick a model from the list first.");
      return;
    }
    const form = new FormData(e.currentTarget);
    setPrice.mutate(
      {
        body: {
          model_id: modelId,
          p_in: Number(form.get("p_in") || 0),
          p_out: Number(form.get("p_out") || 0),
          p_cache_read: Number(form.get("p_cache_read") || 0),
          p_cache_write: Number(form.get("p_cache_write") || 0),
        },
      },
      {
        onSuccess: () => {
          qc.invalidateQueries({ queryKey: ["get", "/api/admin/prices"] });
          setEditing(null);
          toast("Price saved");
        },
        onError: (err) => toast.error(errorMessage(err)),
      },
    );
  };

  const handleDelete = (p: Price) =>
    deletePrice.mutate(
      { params: { path: { model: p.model_id } } },
      {
        onSuccess: () => {
          qc.invalidateQueries({ queryKey: ["get", "/api/admin/prices"] });
          toast(p.has_default ? `${p.model_id} is back on its built-in price` : `${p.model_id} price deleted`);
        },
        onError: (err) => toast.error(errorMessage(err)),
      },
    );

  return (
    <>
      <PageHeader
        title="Models & Pricing"
        icon={Boxes}
        description="Provider prefixes (cc/, cx/, ag/), global model mapping and versioned token prices. Known models have built-in prices; others are rejected until priced."
      />

      <SectionHeader>Providers</SectionHeader>
      {providers.isPending || prices.isPending ? (
        <Skeleton className="h-40 w-full" />
      ) : (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {(providers.data ?? []).map((p) => (
            <ProviderModelsCard
              key={p.id}
              provider={p}
              models={pricedModels}
              accounts={(accounts.data ?? []).filter((a) => a.provider === p.id).length}
            />
          ))}
        </div>
      )}

      <MappingEditor rules={mappings.data} providers={providers.data ?? []} loading={mappings.isPending} />

      <SectionHeader
        actions={
          <Button size="sm" onClick={() => openForm("new")}>
            <Plus className="me-1.5 size-4" />
            Set price
          </Button>
        }
      >
        Model price table
      </SectionHeader>

      <Dialog open={editing !== null} onOpenChange={(open) => !open && setEditing(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{current ? `Edit ${current.model_id}` : "Set model price"}</DialogTitle>
            <DialogDescription>
              Saving adds a new price version, effective now. Past usage keeps the price it was recorded with.
              {current?.source === "default" && " This overrides the built-in price; delete the override to go back to it."}
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={handleSetPrice} className="flex flex-col gap-4">
            {!current && (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="model_id">Model</Label>
                <ModelPicker providers={providers.data ?? []} prices={prices.data ?? []} value={picked} onChange={setPicked} />
                <p className="text-meta text-muted-foreground">
                  {picked ? (
                    <>
                      Selected: <span className="font-mono text-foreground">{picked}</span>
                    </>
                  ) : (
                    "Only models a provider's catalog lists can be priced."
                  )}
                </p>
              </div>
            )}
            {/* Keyed by model: picking one refills these from its price without remounting the picker. */}
            <div key={modelId || "new"} className="grid grid-cols-2 gap-3">
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="p_in">Input ($ / 1M tokens)</Label>
                <Input id="p_in" name="p_in" type="number" step="0.0001" min="0" placeholder="3.00" defaultValue={base?.p_in} required />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="p_out">Output ($ / 1M tokens)</Label>
                <Input id="p_out" name="p_out" type="number" step="0.0001" min="0" placeholder="15.00" defaultValue={base?.p_out} required />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="p_cache_read">Cache read ($ / 1M)</Label>
                <Input id="p_cache_read" name="p_cache_read" type="number" step="0.0001" min="0" placeholder="0.30" defaultValue={base?.p_cache_read} required />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="p_cache_write">Cache write ($ / 1M)</Label>
                <Input id="p_cache_write" name="p_cache_write" type="number" step="0.0001" min="0" placeholder="3.75" defaultValue={base?.p_cache_write} required />
              </div>
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setEditing(null)}>
                Cancel
              </Button>
              <Button type="submit" disabled={setPrice.isPending || !modelId}>
                {setPrice.isPending ? "Saving…" : "Save price"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {prices.isPending ? (
        <Skeleton className="h-40 w-full" />
      ) : !prices.data || prices.data.length === 0 ? (
        <EmptyState
          title="No models priced"
          action={
            <Button onClick={() => openForm("new")} size="sm">
              <Plus className="me-1.5 size-4" />
              Set price
            </Button>
          }
        >
          Models must have a confirmed price before they can be used under any Plan quota.
        </EmptyState>
      ) : (
        <>
          <div className="mb-3 flex flex-wrap items-center gap-2" role="toolbar" aria-label="Filter prices">
            <div className="relative w-full sm:w-64">
              <Search className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input value={filter} onChange={(e) => resetPage(setFilter)(e.target.value)} placeholder="Search model ids" aria-label="Search model ids" className="h-8 ps-8" />
            </div>
            <FacetedFilter
              title="Provider"
              selected={providerFilter}
              onChange={resetPage(setProviderFilter)}
              options={(providers.data ?? []).map((v) => ({
                value: v.id,
                label: v.name,
                count: byText.filter((p) => inSources(p) && p.providers.some((x) => x.id === v.id)).length,
              }))}
            />
            <FacetedFilter
              title="Source"
              selected={sourceFilter}
              onChange={resetPage(setSourceFilter)}
              options={SOURCES.map((src) => ({
                value: src.id,
                label: src.label,
                count: byText.filter((p) => inProviders(p) && sourceOf(p) === src.id).length,
              }))}
            />
            {filtered && (
              <Button variant="ghost" size="sm" className="h-8 px-2" onClick={clearFilters}>
                Reset
                <X className="size-4" />
              </Button>
            )}
          </div>
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Model id</TableHead>
                  <TableHead>Provider</TableHead>
                  <TableHead className="text-end">Input ($/1M)</TableHead>
                  <TableHead className="text-end">Output ($/1M)</TableHead>
                  <TableHead className="text-end">Cache read ($/1M)</TableHead>
                  <TableHead className="text-end">Cache write ($/1M)</TableHead>
                  <TableHead>Effective from</TableHead>
                  <TableHead>Source</TableHead>
                  <TableHead className="w-24" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {visible.map((p) => (
                  <TableRow key={`${p.model_id}-${p.version}`}>
                    <TableCell className="font-mono font-semibold">{p.model_id}</TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1">
                        {p.providers.length > 0 ? (
                          p.providers.map((v) => (
                            <Tag key={v.id}>
                              <span className="font-mono">{v.prefix}/</span> {v.name}
                            </Tag>
                          ))
                        ) : (
                          <span className="text-meta text-muted-foreground">—</span>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="text-end font-mono text-meta">{formatUsd(p.p_in)}</TableCell>
                    <TableCell className="text-end font-mono text-meta">{formatUsd(p.p_out)}</TableCell>
                    <TableCell className="text-end font-mono text-meta">{formatUsd(p.p_cache_read)}</TableCell>
                    <TableCell className="text-end font-mono text-meta">{formatUsd(p.p_cache_write)}</TableCell>
                    <TableCell className="text-meta text-muted-foreground">{dateFmt.format(new Date(p.effective_from))}</TableCell>
                    <TableCell>
                      {!p.confirmed_at ? (
                        <Tag tone="sand">Proposed</Tag>
                      ) : p.source === "default" ? (
                        <Tag>Built-in</Tag>
                      ) : (
                        <Tag tone="brand">{p.has_default ? "Override" : "Custom"}</Tag>
                      )}
                    </TableCell>
                    <TableCell className="text-end whitespace-nowrap">
                      <Button variant="ghost" size="icon" aria-label={`Edit ${p.model_id}`} onClick={() => openForm(p)}>
                        <Pencil className="size-4 text-muted-foreground" />
                      </Button>
                      {p.source === "admin" && (
                        <AlertDialog>
                          <AlertDialogTrigger asChild>
                            <Button variant="ghost" size="icon" aria-label={`Delete ${p.model_id}`}>
                              <Trash2 className="size-4 text-muted-foreground hover:text-destructive-text" />
                            </Button>
                          </AlertDialogTrigger>
                          <AlertDialogContent>
                            <AlertDialogHeader>
                              <AlertDialogTitle>
                                {p.has_default ? `Reset ${p.model_id} to its built-in price?` : `Delete the ${p.model_id} price?`}
                              </AlertDialogTitle>
                              <AlertDialogDescription>
                                {p.has_default
                                  ? "Your price versions are removed and the built-in price applies again. Past usage keeps the cost it was recorded with."
                                  : "This model has no built-in price, so requests for it are refused until it is priced again. Past usage keeps the cost it was recorded with."}
                              </AlertDialogDescription>
                            </AlertDialogHeader>
                            <AlertDialogFooter>
                              <AlertDialogCancel>Cancel</AlertDialogCancel>
                              <AlertDialogAction onClick={() => handleDelete(p)}>{p.has_default ? "Reset" : "Delete"}</AlertDialogAction>
                            </AlertDialogFooter>
                          </AlertDialogContent>
                        </AlertDialog>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          <div className="mt-3 flex items-center justify-between gap-3 text-meta text-muted-foreground">
            <span aria-live="polite" className="flex items-center gap-2">
              {matching.length === 0
                ? "No priced model matches these filters."
                : `${pageIndex * PRICE_PAGE_SIZE + 1}–${pageIndex * PRICE_PAGE_SIZE + visible.length} of ${matching.length} models`}
              {matching.length === 0 && filtered && (
                <Button variant="link" size="sm" className="h-auto p-0 text-meta" onClick={clearFilters}>
                  Clear filters
                </Button>
              )}
            </span>
            {pages > 1 && (
              <div className="flex items-center gap-1">
                <Button variant="ghost" size="icon" onClick={() => setPage(pageIndex - 1)} disabled={pageIndex === 0} aria-label="Previous page">
                  <ChevronLeft className="size-4" />
                </Button>
                <span className="min-w-14 text-center tabular-nums">
                  {pageIndex + 1} / {pages}
                </span>
                <Button variant="ghost" size="icon" onClick={() => setPage(pageIndex + 1)} disabled={pageIndex >= pages - 1} aria-label="Next page">
                  <ChevronRight className="size-4" />
                </Button>
              </div>
            )}
          </div>
        </>
      )}
    </>
  );
}
