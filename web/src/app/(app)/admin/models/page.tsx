"use client";

import { Boxes, Plus } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";

import { MappingEditor } from "@/components/qanat/mapping-editor";
import { EmptyState, PageHeader, SectionHeader, Tag } from "@/components/qanat/page";
import { ProviderModelsCard } from "@/components/qanat/provider-models";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, errorMessage } from "@/lib/api/client";

const dateFmt = new Intl.DateTimeFormat("en-US", { dateStyle: "medium" });

export default function ModelsPage() {
  const qc = useQueryClient();
  const mappings = api.useQuery("get", "/api/admin/mappings");
  const prices = api.useQuery("get", "/api/admin/prices");
  const providers = api.useQuery("get", "/api/admin/providers");
  const accounts = api.useQuery("get", "/api/admin/accounts");
  const pricedModels = [...new Set((prices.data ?? []).filter((p) => p.confirmed_at).map((p) => p.upstream_model))];
  const setPrice = api.useMutation("post", "/api/admin/prices");

  const [priceOpen, setPriceOpen] = useState(false);

  const handleSetPrice = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const upstreamModel = String(form.get("upstream_model") || "").trim();
    const pIn = Number(form.get("p_in") || 0);
    const pOut = Number(form.get("p_out") || 0);
    const pCacheRead = Number(form.get("p_cache_read") || 0);
    const pCacheWrite = Number(form.get("p_cache_write") || 0);

    setPrice.mutate(
      {
        body: {
          upstream_model: upstreamModel,
          p_in: pIn,
          p_out: pOut,
          p_cache_read: pCacheRead,
          p_cache_write: pCacheWrite,
        },
      },
      {
        onSuccess: () => {
          qc.invalidateQueries({ queryKey: ["get", "/api/admin/prices"] });
          setPriceOpen(false);
          toast("Price updated");
        },
        onError: (err) => toast.error(errorMessage(err)),
      },
    );
  };

  return (
    <>
      <PageHeader
        title="Models & Pricing"
        icon={Boxes}
        description="Provider prefixes (cc/, cx/, ag/), global model mapping and versioned token prices. Unpriced models are rejected until priced."
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
          <Dialog open={priceOpen} onOpenChange={setPriceOpen}>
            <DialogTrigger asChild>
              <Button size="sm">
                <Plus className="me-1.5 size-4" />
                Set price
              </Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Set model price</DialogTitle>
                <DialogDescription>
                  Prices are versioned by effective date. Existing aggregates keep the price version they were recorded with.
                </DialogDescription>
              </DialogHeader>
              <form onSubmit={handleSetPrice} className="flex flex-col gap-4">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="upstream_model">Upstream model name</Label>
                  <Input id="upstream_model" name="upstream_model" placeholder="e.g. claude-3-7-sonnet-20250219" required />
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <div className="flex flex-col gap-1.5">
                    <Label htmlFor="p_in">Input ($ / 1M tokens)</Label>
                    <Input id="p_in" name="p_in" type="number" step="0.0001" min="0" placeholder="3.00" required />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <Label htmlFor="p_out">Output ($ / 1M tokens)</Label>
                    <Input id="p_out" name="p_out" type="number" step="0.0001" min="0" placeholder="15.00" required />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <Label htmlFor="p_cache_read">Cache read ($ / 1M)</Label>
                    <Input id="p_cache_read" name="p_cache_read" type="number" step="0.0001" min="0" placeholder="0.30" required />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <Label htmlFor="p_cache_write">Cache write ($ / 1M)</Label>
                    <Input id="p_cache_write" name="p_cache_write" type="number" step="0.0001" min="0" placeholder="3.75" required />
                  </div>
                </div>
                <DialogFooter>
                  <Button type="button" variant="outline" onClick={() => setPriceOpen(false)}>
                    Cancel
                  </Button>
                  <Button type="submit" disabled={setPrice.isPending}>
                    {setPrice.isPending ? "Saving…" : "Save price"}
                  </Button>
                </DialogFooter>
              </form>
            </DialogContent>
          </Dialog>
        }
      >
        Model price table
      </SectionHeader>

      {prices.isPending ? (
        <Skeleton className="h-40 w-full" />
      ) : !prices.data || prices.data.length === 0 ? (
        <EmptyState
          title="No models priced"
          action={
            <Button onClick={() => setPriceOpen(true)} size="sm">
              <Plus className="me-1.5 size-4" />
              Set price
            </Button>
          }
        >
          Models must have a confirmed price before they can be used under any Plan quota.
        </EmptyState>
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Upstream model</TableHead>
                <TableHead className="text-end">Input ($/1M)</TableHead>
                <TableHead className="text-end">Output ($/1M)</TableHead>
                <TableHead className="text-end">Cache read ($/1M)</TableHead>
                <TableHead className="text-end">Cache write ($/1M)</TableHead>
                <TableHead>Effective from</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {prices.data.map((p) => (
                <TableRow key={`${p.upstream_model}-${p.version}`}>
                  <TableCell className="font-mono font-semibold">{p.upstream_model}</TableCell>
                  <TableCell className="text-end font-mono text-meta">${p.p_in.toFixed(2)}</TableCell>
                  <TableCell className="text-end font-mono text-meta">${p.p_out.toFixed(2)}</TableCell>
                  <TableCell className="text-end font-mono text-meta">${p.p_cache_read.toFixed(2)}</TableCell>
                  <TableCell className="text-end font-mono text-meta">${p.p_cache_write.toFixed(2)}</TableCell>
                  <TableCell className="text-meta text-muted-foreground">
                    {dateFmt.format(new Date(p.effective_from))}
                  </TableCell>
                  <TableCell>
                    {p.confirmed_at ? (
                      <Tag tone="brand">Confirmed</Tag>
                    ) : (
                      <Tag tone="sand">Proposed</Tag>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </>
  );
}
