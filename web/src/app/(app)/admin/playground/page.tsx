"use client";

import { useQuery } from "@tanstack/react-query";
import { Check, FlaskConical, ImagePlus, Play, Search, Square, Trash2, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { toast } from "sonner";

import { EmptyState, PageHeader, SectionHeader, Tag } from "@/components/qanat/page";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, errorMessage, type Schemas } from "@/lib/api/client";
import { msHeader, providerPrefix, readEvent, takeEvents, timeline } from "@/lib/playground";
import { cn } from "@/lib/utils";

/*
 * Playground (admin): one real request through the gateway, either balanced
 * across a provider's pool (Auto) or pinned to a single account, with what it
 * cost at each leg. The gateway tells admin keys where a request went
 * (x-qanat-* headers); TTFT and total are measured here, in the browser.
 *
 * The request is signed with a short-lived key of the admin's own, named
 * "Playground", kept in sessionStorage; it is the same key a user would use,
 * so the plan, access rules and pricing all apply.
 */

type Account = Schemas["Account"];
const AUTO = "auto";
const KEY_STORE = "qanat.playground.key";
const MAX_IMAGE = 5 * 1024 * 1024; // Anthropic's per-image limit
const PROVIDER_NAMES: Record<string, string> = { claude: "Claude Code", codex: "OpenAI Codex", antigravity: "Antigravity" };

type Run = {
  id: number;
  model: string;
  target: string; // AUTO or an account id
  status: number | null;
  error?: string;
  text: string;
  running: boolean;
  ttft?: number;
  total?: number;
  inTokens?: number;
  outTokens?: number;
  gateway?: number;
  upstream?: number;
  connect?: number;
  proxy?: string;
  account?: string;
  upstreamModel?: string;
};

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const fmtMs = (v?: number) => (v === undefined ? "—" : v >= 1000 ? `${(v / 1000).toFixed(2)} s` : v >= 10 ? `${Math.round(v)} ms` : `${v.toFixed(1)} ms`);

function readFile(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result).split(",")[1] ?? "");
    r.onerror = () => reject(r.error);
    r.readAsDataURL(file);
  });
}

export default function PlaygroundPage() {
  const accounts = api.useQuery("get", "/api/admin/accounts");
  const proxies = api.useQuery("get", "/api/admin/proxies");
  const createKey = api.useMutation("post", "/api/me/keys");

  const [target, setTarget] = useState(AUTO);
  const [picked, setPicked] = useState("");
  const [prompt, setPrompt] = useState("");
  const [maxTokens, setMaxTokens] = useState("512");
  const [image, setImage] = useState<{ name: string; type: string; data: string; url: string; size: number } | null>(null);
  const [runs, setRuns] = useState<Run[]>([]);
  const [selected, setSelected] = useState<number | null>(null);
  const [now, setNow] = useState(0);
  const abort = useRef<AbortController | null>(null);
  const nextId = useRef(1);
  const fileInput = useRef<HTMLInputElement>(null);

  const accountList = useMemo(() => accounts.data ?? [], [accounts.data]);
  const accountById = useMemo(() => new Map(accountList.map((a) => [a.id, a])), [accountList]);
  const label = useCallback((id?: string) => (id ? (accountById.get(id)?.email || accountById.get(id)?.label || id) : "—"), [accountById]);
  const proxyLabel = useCallback((id?: string) => (!id || id === "direct" ? "direct" : (proxies.data?.find((p) => p.id === id)?.label ?? id)), [proxies.data]);

  // The admin's own key, created on first use. A key made a moment ago may not
  // have reached the gateway's snapshot yet, so a 401 is retried briefly.
  const keyRef = useRef<string | null>(null);
  const getKey = useCallback(
    async (fresh = false) => {
      if (!fresh) {
        try {
          keyRef.current ??= sessionStorage.getItem(KEY_STORE);
        } catch {
          /* storage blocked: a new key per page load */
        }
        if (keyRef.current) return { key: keyRef.current, created: false };
      }
      const res = await createKey.mutateAsync({ body: { name: "Playground", expires_in_days: 1 } });
      keyRef.current = res.key;
      try {
        sessionStorage.setItem(KEY_STORE, res.key);
      } catch {
        /* see above */
      }
      return { key: res.key, created: true };
    },
    [createKey],
  );
  const gateway = useCallback(
    async (path: string, init: RequestInit & { headers?: Record<string, string> }): Promise<Response> => {
      let { key, created } = await getKey();
      let res: Response | undefined;
      for (let i = 0; i < 8; i++) {
        res = await fetch(path, { ...init, headers: { ...init.headers, authorization: `Bearer ${key}`, "anthropic-version": "2023-06-01" }, cache: "no-store" });
        if (res.status !== 401) return res;
        if (!created) ({ key, created } = await getKey(true)); // the stored key expired or was revoked
        else await sleep(700);
      }
      return res!;
    },
    [getKey],
  );

  const models = useQuery({
    queryKey: ["playground-models"],
    staleTime: 60_000,
    retry: false,
    queryFn: async () => {
      const res = await gateway("/gateway/v1/models", {});
      const body = await res.json().catch(() => null);
      if (!res.ok) throw new Error(body?.error?.message ?? `The gateway answered ${res.status}.`);
      return ((body?.data ?? []) as { id: string }[]).map((m) => m.id).sort();
    },
  });

  // Models the chosen route can serve: a pinned account serves its provider's prefix only.
  const pinned = target === AUTO ? undefined : accountById.get(target);
  const prefix = pinned ? providerPrefix(pinned.provider) : undefined;
  const modelOptions = useMemo(() => (models.data ?? []).filter((m) => !prefix || m.startsWith(prefix)), [models.data, prefix]);
  // The pick, or the first model the route serves when it doesn't (after switching route).
  const model = modelOptions.includes(picked) ? picked : (modelOptions[0] ?? "");

  const run = runs.find((r) => r.id === selected) ?? runs[0];
  const running = runs.some((r) => r.running);
  useEffect(() => {
    if (!running) return;
    const t = setInterval(() => setNow(performance.now()), 100);
    return () => clearInterval(t);
  }, [running]);
  const startedAt = useRef(0);

  const patch = (id: number, p: Partial<Run>) => setRuns((rs) => rs.map((r) => (r.id === id ? { ...r, ...p } : r)));

  const pickImage = async (file?: File) => {
    if (!file) return;
    if (!/^image\/(png|jpeg|webp|gif)$/.test(file.type)) return void toast.error("Use a PNG, JPEG, WebP or GIF image.");
    if (file.size > MAX_IMAGE) return void toast.error("Images can be up to 5 MB.");
    setImage({ name: file.name, type: file.type, data: await readFile(file), url: URL.createObjectURL(file), size: file.size });
  };

  const send = async () => {
    if (running || !model || (!prompt.trim() && !image)) return;
    const id = nextId.current++;
    const ctl = new AbortController();
    abort.current = ctl;
    setRuns((rs) => [{ id, model, target, status: null, text: "", running: true }, ...rs].slice(0, 12));
    setSelected(id);
    const content: unknown[] = [];
    if (image) content.push({ type: "image", source: { type: "base64", media_type: image.type, data: image.data } });
    content.push({ type: "text", text: prompt.trim() || "Describe this image." });
    // Claude subscription accounts only serve Claude Code: its system prompt is part of every request it makes.
    const system = model.startsWith("cc/") ? [{ type: "text", text: "You are Claude Code, Anthropic's official CLI for Claude." }] : undefined;
    const body = JSON.stringify({ model, max_tokens: Math.max(1, Number(maxTokens) || 512), stream: true, system, messages: [{ role: "user", content }] });

    const t0 = performance.now();
    startedAt.current = t0;
    let text = "";
    try {
      const res = await gateway("/gateway/v1/messages", {
        method: "POST",
        body,
        signal: ctl.signal,
        headers: { "content-type": "application/json", ...(target !== AUTO ? { "x-qanat-account": target } : {}) },
      });
      const h = res.headers;
      patch(id, {
        status: res.status,
        gateway: msHeader(h, "x-qanat-gateway-ms"),
        upstream: msHeader(h, "x-qanat-upstream-ms"),
        connect: msHeader(h, "x-qanat-connect-ms"),
        proxy: h.get("x-qanat-proxy") ?? undefined,
        account: h.get("x-qanat-account") ?? undefined,
        upstreamModel: h.get("x-qanat-upstream-model") ?? undefined,
      });
      if (!res.ok || !res.body) {
        const err = await res.json().catch(() => null);
        patch(id, { running: false, total: performance.now() - t0, error: err?.error?.message ?? `The gateway answered ${res.status}.` });
        return;
      }
      const reader = res.body.getReader();
      const dec = new TextDecoder();
      let buf = "";
      let first = false;
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        const parsed = takeEvents((buf += dec.decode(value, { stream: true })));
        buf = parsed.rest;
        const p: Partial<Run> = {};
        for (const ev of parsed.events) {
          const r = readEvent(ev);
          if (r.text) {
            text += r.text;
            if (!first) {
              first = true;
              p.ttft = performance.now() - t0;
            }
          }
          if (r.inTokens !== undefined) p.inTokens = r.inTokens;
          if (r.outTokens !== undefined) p.outTokens = r.outTokens;
          if (r.error) p.error = r.error;
        }
        patch(id, { ...p, text });
      }
      patch(id, { running: false, total: performance.now() - t0 });
    } catch (e) {
      const stopped = ctl.signal.aborted;
      patch(id, { running: false, total: performance.now() - t0, ...(stopped ? { error: "Stopped." } : { error: e instanceof Error ? e.message : "The request failed." }) });
    } finally {
      abort.current = null;
    }
  };

  const grouped = useMemo(() => {
    const m = new Map<string, Account[]>();
    for (const a of accountList) m.set(a.provider, [...(m.get(a.provider) ?? []), a]);
    return [...m.entries()];
  }, [accountList]);

  const live = run?.running && run.ttft === undefined ? now - startedAt.current : undefined;
  const tl = run ? timeline(run) : null;
  const tps = run?.outTokens && run.total !== undefined && run.ttft !== undefined && run.total > run.ttft ? run.outTokens / ((run.total - run.ttft) / 1000) : undefined;

  return (
    <div>
      <PageHeader
        icon={FlaskConical}
        title="Playground"
        description="Send one real request through the gateway — balanced across a provider's pool, or pinned to one account — and see where the time went."
      />
      <div className="grid gap-4 lg:grid-cols-[minmax(0,380px)_minmax(0,1fr)]">
        <Card className="gap-4 py-4 self-start">
          <CardContent className="flex flex-col gap-4 px-4">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="pg-target">Route</Label>
              <Select value={target} onValueChange={setTarget}>
                <SelectTrigger id="pg-target" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={AUTO}>Auto — load-balanced across the pool</SelectItem>
                  {grouped.map(([provider, list]) => (
                    <SelectGroup key={provider}>
                      <SelectLabel>{PROVIDER_NAMES[provider] ?? provider}</SelectLabel>
                      {list.map((a) => (
                        <SelectItem key={a.id} value={a.id} disabled={!!a.disabled_at}>
                          {a.email || a.label}
                          {a.disabled_at ? " · disabled" : a.state !== "healthy" ? ` · ${a.state}` : ""}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  ))}
                </SelectContent>
              </Select>
              <p className="text-meta text-muted-foreground">
                {pinned ? "Only this account is tried — no failover, even if it is cooling down." : "The provider's routing strategy picks the account, with failover."}
              </p>
            </div>

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="pg-model">Model</Label>
              <ModelSearch id="pg-model" options={modelOptions} value={model} onChange={setPicked} loading={models.isPending} />
              {models.error && <p className="text-meta text-destructive-text">{errorMessage(models.error)}</p>}
            </div>

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="pg-prompt">Prompt</Label>
              <textarea
                id="pg-prompt"
                value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                    e.preventDefault();
                    void send();
                  }
                }}
                rows={6}
                placeholder="Say hello in one short sentence."
                className="min-h-24 w-full resize-y rounded-md border border-input bg-transparent px-3 py-2 text-body shadow-xs outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
              />
              <div className="flex items-center gap-2">
                <input ref={fileInput} type="file" accept="image/png,image/jpeg,image/webp,image/gif" hidden onChange={(e) => { void pickImage(e.target.files?.[0]); e.target.value = ""; }} />
                {image ? (
                  <span className="flex min-w-0 items-center gap-2 rounded-md border px-1.5 py-1">
                    {/* eslint-disable-next-line @next/next/no-img-element -- a local blob preview */}
                    <img src={image.url} alt="Attached image preview" className="size-8 rounded object-cover" />
                    <span className="min-w-0 truncate text-meta">{image.name}</span>
                    <span className="text-meta text-muted-foreground">{Math.max(1, Math.round(image.size / 1024))} KB</span>
                    <button type="button" aria-label="Remove image" onClick={() => setImage(null)} className="grid size-6 cursor-pointer place-items-center rounded text-muted-foreground hover:bg-muted">
                      <X aria-hidden className="size-3.5" />
                    </button>
                  </span>
                ) : (
                  <Button type="button" variant="outline" size="sm" onClick={() => fileInput.current?.click()}>
                    <ImagePlus aria-hidden className="me-1.5 size-4" /> Attach image
                  </Button>
                )}
              </div>
            </div>

            <div className="flex items-end justify-between gap-3">
              <div className="flex w-28 flex-col gap-1.5">
                <Label htmlFor="pg-max">Max tokens</Label>
                <Input id="pg-max" type="number" min={1} max={32000} value={maxTokens} onChange={(e) => setMaxTokens(e.target.value)} className="font-mono" />
              </div>
              {running ? (
                <Button type="button" variant="outline" onClick={() => abort.current?.abort()}>
                  <Square aria-hidden className="me-1.5 size-4" /> Stop
                </Button>
              ) : (
                <Button type="button" onClick={() => void send()} disabled={!model || (!prompt.trim() && !image)}>
                  <Play aria-hidden className="me-1.5 size-4" /> Send
                </Button>
              )}
            </div>
            <p className="-mt-2 text-meta text-muted-foreground">Counts against your own plan, like any request. ⌘/Ctrl + Enter sends.</p>
          </CardContent>
        </Card>

        <div className="flex min-w-0 flex-col gap-4">
          {!run ? (
            <EmptyState title="Nothing sent yet">Pick a route and a model, write a prompt (or attach an image) and press Send. The response streams in here with its timings.</EmptyState>
          ) : (
            <Card className="gap-3 py-4">
              <CardContent className="flex flex-col gap-4 px-4">
                <div className="flex flex-wrap items-center gap-2">
                  <Tag tone={run.error ? "neutral" : "brand"}>{run.running ? "Streaming" : run.error ? (run.status && run.status !== 200 ? `Error ${run.status}` : "Interrupted") : "Done"}</Tag>
                  <span className="font-mono text-meta text-muted-foreground">{run.model}</span>
                  <span className="text-meta text-muted-foreground">→</span>
                  <span className="text-meta">{run.account ? label(run.account) : run.target === AUTO ? "Auto" : label(run.target)}</span>
                  {run.account && <Tag>{run.target === AUTO ? "balanced" : "pinned"}</Tag>}
                </div>

                <div aria-live="polite" aria-busy={run.running} className="min-h-24 whitespace-pre-wrap break-words rounded-md bg-muted/50 px-3 py-2.5 text-body">
                  {run.text || (run.running ? <span className="text-muted-foreground">Waiting for the first token… {live !== undefined ? fmtMs(live) : ""}</span> : null)}
                  {run.error && <p className="mt-2 text-destructive-text">{run.error}</p>}
                </div>

                <dl className="grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-4">
                  <Stat name="TTFT" hint="Send to first token, measured here" value={fmtMs(run.ttft)} strong />
                  <Stat name="Total" hint="Send to end of the stream" value={fmtMs(run.total)} strong />
                  <Stat name="Tokens" hint="Input / output" value={run.inTokens !== undefined || run.outTokens !== undefined ? `${run.inTokens ?? "—"} / ${run.outTokens ?? "—"}` : "—"} />
                  <Stat name="Speed" hint="Output tokens per second after the first token" value={tps ? `${tps.toFixed(1)} tok/s` : "—"} />
                  <Stat name="Gateway" hint="Auth, routing and quota before the upstream call" value={fmtMs(run.gateway)} />
                  <Stat name="Upstream" hint="Upstream call to its response headers (includes connecting)" value={fmtMs(run.upstream)} />
                  <Stat
                    name={run.proxy && run.proxy !== "direct" ? "Proxy connect" : "Connect"}
                    hint={run.connect === undefined ? "A warm connection was reused, so nothing to connect" : "Opening a new upstream connection"}
                    value={run.connect === undefined ? (run.status ? "reused" : "—") : fmtMs(run.connect)}
                  />
                  <Stat name="Via" hint="The outbound proxy this account routes through" value={run.status ? proxyLabel(run.proxy) : "—"} />
                </dl>

                {tl && run.total ? (
                  <div className="flex flex-col gap-1.5">
                    <p className="text-meta font-medium text-muted-foreground">Where the time went</p>
                    <div className="flex h-2 w-full overflow-hidden rounded-full bg-muted" role="img" aria-label={`Gateway ${fmtMs(tl.gateway)}, upstream ${fmtMs(tl.upstream)}, to first token ${fmtMs(tl.toFirst)}, generation ${fmtMs(tl.generation)}`}>
                      {[
                        ["bg-muted-foreground/60", tl.gateway],
                        ["bg-primary/40", tl.upstream],
                        ["bg-primary/70", tl.toFirst],
                        ["bg-primary", tl.generation],
                      ].map(([c, v], i) => (
                        <span key={i} className={cn("h-full", c as string)} style={{ width: `${(Number(v) / run.total!) * 100}%` }} />
                      ))}
                    </div>
                    <div className="flex flex-wrap gap-x-4 gap-y-1 text-meta text-muted-foreground">
                      <Legend c="bg-muted-foreground/60" t={`Gateway ${fmtMs(tl.gateway)}`} />
                      <Legend c="bg-primary/40" t={`Upstream response ${fmtMs(tl.upstream)}`} />
                      <Legend c="bg-primary/70" t={`To first token ${fmtMs(tl.toFirst)}`} />
                      <Legend c="bg-primary" t={`Generation ${fmtMs(tl.generation)}`} />
                    </div>
                  </div>
                ) : null}
              </CardContent>
            </Card>
          )}

          {runs.length > 0 && (
            <div>
              <SectionHeader
                actions={
                  <Button type="button" variant="ghost" size="sm" onClick={() => { setRuns([]); setSelected(null); }}>
                    <Trash2 aria-hidden className="me-1.5 size-3.5" /> Clear
                  </Button>
                }
              >
                This session
              </SectionHeader>
              <div className="rounded-lg border">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Route</TableHead>
                      <TableHead>Model</TableHead>
                      <TableHead className="text-right">TTFT</TableHead>
                      <TableHead className="text-right">Total</TableHead>
                      <TableHead className="text-right">Tokens</TableHead>
                      <TableHead>Status</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {runs.map((r) => (
                      <TableRow key={r.id} data-state={r.id === run?.id ? "selected" : undefined} onClick={() => setSelected(r.id)} className="cursor-pointer">
                        <TableCell className="max-w-48 truncate">{r.account ? label(r.account) : r.target === AUTO ? "Auto" : label(r.target)}</TableCell>
                        <TableCell className="font-mono text-meta">{r.model}</TableCell>
                        <TableCell className="text-right font-mono tabular-nums">{fmtMs(r.ttft)}</TableCell>
                        <TableCell className="text-right font-mono tabular-nums">{fmtMs(r.total)}</TableCell>
                        <TableCell className="text-right font-mono tabular-nums">{r.outTokens ?? "—"}</TableCell>
                        <TableCell>{r.running ? "Streaming" : r.error ? <span className="text-destructive-text">{r.status && r.status !== 200 ? r.status : "Failed"}</span> : "OK"}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function Stat({ name, value, hint, strong }: { name: string; value: string; hint: string; strong?: boolean }) {
  return (
    <div title={hint}>
      <dt className="text-meta text-muted-foreground">{name}</dt>
      <dd className={cn("font-mono tabular-nums", strong ? "text-title font-semibold" : "text-body")}>{value}</dd>
    </div>
  );
}

function Legend({ c, t }: { c: string; t: string }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <span aria-hidden className={cn("size-2 rounded-full", c)} />
      {t}
    </span>
  );
}

/** A searchable model list: type to filter, ↑/↓ to move, Enter or click to pick. */
function ModelSearch({ id, options, value, onChange, loading }: { id: string; options: string[]; value: string; onChange: (m: string) => void; loading: boolean }) {
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);
  const q = query.trim().toLowerCase();
  const shown = useMemo(() => options.filter((m) => m.toLowerCase().includes(q)), [options, q]);
  const activeIndex = Math.min(active, Math.max(shown.length - 1, 0));

  useEffect(() => {
    listRef.current?.querySelector(`[data-index="${activeIndex}"]`)?.scrollIntoView({ block: "nearest" });
  }, [activeIndex]);

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      setActive(Math.min(Math.max(activeIndex + (e.key === "ArrowDown" ? 1 : -1), 0), Math.max(shown.length - 1, 0)));
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (shown[activeIndex]) onChange(shown[activeIndex]);
    }
  };

  return (
    <div className="flex flex-col gap-2">
      <div className="relative">
        <Search aria-hidden className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          id={id}
          role="combobox"
          aria-expanded="true"
          aria-controls={`${id}-list`}
          aria-activedescendant={shown[activeIndex] ? `${id}-option-${activeIndex}` : undefined}
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
          }}
          onKeyDown={onKeyDown}
          placeholder={value ? `Search models — now ${value}` : "Search models"}
          autoComplete="off"
          className="ps-8"
        />
      </div>
      <div ref={listRef} id={`${id}-list`} role="listbox" aria-label="Models" className="h-44 overflow-y-auto overscroll-contain rounded-md border">
        {shown.length === 0 ? (
          <div className="flex h-full items-center justify-center px-6 text-center text-meta text-muted-foreground">
            {loading ? "Loading models…" : query ? <>No model matches “{query}”.</> : "No model available for this route."}
          </div>
        ) : (
          shown.map((m, i) => (
            <div
              key={m}
              id={`${id}-option-${i}`}
              data-index={i}
              role="option"
              aria-selected={m === value}
              onMouseDown={(e) => e.preventDefault()} // keep focus in the search box
              onMouseEnter={() => setActive(i)}
              onClick={() => onChange(m)}
              className={cn("flex min-h-9 cursor-pointer items-center gap-2 px-3 py-1.5 transition-colors duration-150", i === activeIndex && "bg-muted", m === value && "bg-brand-soft")}
            >
              <Check aria-hidden className={cn("size-4 shrink-0 text-brand-text", m !== value && "invisible")} />
              <span className="min-w-0 flex-1 truncate font-mono text-sm">{m}</span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
