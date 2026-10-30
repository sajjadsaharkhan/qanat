"use client";

import { Plus, Trash2 } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";

import { EmptyState, SectionHeader } from "@/components/qanat/page";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, errorMessage, type Schemas } from "@/lib/api/client";

type Rule = Schemas["MappingRule"];

/**
 * The global mapping table (REQ-MAP-01/02), edited as a whole and saved with
 * one PUT. Like a 9router model alias, each rule points a Qanat name at
 * `prefix/model`; `cc/<name>` then applies the same rule on that provider.
 */
export function MappingEditor({ rules, providers, loading }: { rules?: Rule[]; providers: Schemas["ProviderInfo"][]; loading: boolean }) {
  const qc = useQueryClient();
  const save = api.useMutation("put", "/api/admin/mappings");
  // null until the admin edits; the saved rules show through until then.
  const [edited, setEdited] = useState<Rule[] | null>(null);
  const draft = edited ?? rules ?? [];
  const setDraft = (f: (d: Rule[]) => Rule[]) => setEdited(f(draft));

  const dirty = JSON.stringify(draft) !== JSON.stringify(rules ?? []);
  const prefix = (id: string) => providers.find((p) => p.id === id)?.prefix ?? id;
  const update = (i: number, patch: Partial<Rule>) => setDraft((d) => d.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const add = () => setDraft((d) => [...d, { pattern: "", provider: "claude", upstream_model: null, priority: 0 }]);

  const submit = () =>
    save.mutate(
      { body: draft },
      {
        onSuccess: async () => {
          await qc.invalidateQueries({ queryKey: ["get", "/api/admin/mappings"] });
          setEdited(null);
          toast("Mapping saved");
        },
        onError: (err) => toast.error(errorMessage(err)),
      },
    );

  return (
    <>
      <SectionHeader
        actions={
          <div className="flex gap-2">
            <Button size="sm" variant="outline" onClick={add}>
              <Plus className="me-1.5 size-4" />
              Add rule
            </Button>
            {dirty && (
              <>
                <Button size="sm" variant="ghost" onClick={() => setEdited(null)}>
                  Discard
                </Button>
                <Button size="sm" onClick={submit} disabled={save.isPending}>
                  {save.isPending ? "Saving…" : "Save mapping"}
                </Button>
              </>
            )}
          </div>
        }
      >
        Global model mapping
      </SectionHeader>
      {loading ? (
        <Skeleton className="h-32 w-full" />
      ) : draft.length === 0 ? (
        <EmptyState title="No custom mappings">
          Prefixed names route on their own (<code className="font-mono">cc/claude-opus-4-7</code>), and{" "}
          <code className="font-mono">claude-*</code> goes to the Claude pool. Add a rule for an alias such as{" "}
          <code className="font-mono">opus</code>.
        </EmptyState>
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name or glob</TableHead>
                <TableHead>Provider</TableHead>
                <TableHead>Upstream model</TableHead>
                <TableHead>Routes to</TableHead>
                <TableHead className="w-24">Priority</TableHead>
                <TableHead className="w-10" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {draft.map((r, i) => (
                <TableRow key={i}>
                  <TableCell>
                    <Input
                      aria-label="Name or glob"
                      className="font-mono"
                      value={r.pattern}
                      placeholder="opus or claude-haiku-*"
                      onChange={(e) => update(i, { pattern: e.target.value })}
                    />
                  </TableCell>
                  <TableCell>
                    <Select value={r.provider} onValueChange={(v) => update(i, { provider: v as Rule["provider"] })}>
                      <SelectTrigger aria-label="Provider" className="min-w-40">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {providers.map((p) => (
                          <SelectItem key={p.id} value={p.id}>
                            {p.name} ({p.prefix}/)
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </TableCell>
                  <TableCell>
                    <Input
                      aria-label="Upstream model"
                      className="font-mono"
                      value={r.upstream_model ?? ""}
                      placeholder="Same as incoming"
                      onChange={(e) => update(i, { upstream_model: e.target.value || null })}
                    />
                  </TableCell>
                  <TableCell className="font-mono text-meta text-muted-foreground">
                    {prefix(r.provider)}/{r.upstream_model || r.pattern || "…"}
                  </TableCell>
                  <TableCell>
                    <Input
                      aria-label="Priority"
                      type="number"
                      className="font-mono"
                      value={r.priority}
                      onChange={(e) => update(i, { priority: Number(e.target.value) || 0 })}
                    />
                  </TableCell>
                  <TableCell>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={`Remove ${r.pattern || "rule"}`}
                      onClick={() => setDraft((d) => d.filter((_, j) => j !== i))}
                    >
                      <Trash2 />
                    </Button>
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
