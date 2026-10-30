"use client";

import { ArrowDown, ArrowUp, CheckCircle2, MapPin, Network, Pencil, Plus, Trash2, XCircle, Zap } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";

import { EmptyState, PageHeader, SectionHeader, Tag } from "@/components/qanat/page";
import { ProxyChoiceOptions, defaultLabel } from "@/components/qanat/proxy-options";
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { api, errorMessage, type Schemas } from "@/lib/api/client";

type Proxy = Schemas["Proxy"];
type ProxyType = Schemas["ProxyType"];
type TestResult = Schemas["ProxyTestResult"];
type ProxyLocation = Schemas["ProxyLocation"];
type ProxyHealth = Schemas["ProxyHealth"];
type Group = Schemas["ProxyGroup"];
type Policy = Schemas["ProxyGroupPolicy"];

const providerNames: Record<string, string> = { claude: "Claude", codex: "Codex", antigravity: "Antigravity" };

const regionNames = new Intl.DisplayNames(["en"], { type: "region" });

/** "SE" → 🇸🇪 (regional indicator symbols). */
function flag(code: string) {
  return /^[A-Z]{2}$/.test(code) ? String.fromCodePoint(...[...code].map((c) => 0x1f1a5 + c.charCodeAt(0))) : "";
}

function countryName(code: string) {
  try {
    return regionNames.of(code) ?? code;
  } catch {
    return code;
  }
}

function Location({ location }: { location: ProxyLocation }) {
  const country = location.country ? countryName(location.country) : "";
  // Skip the region when it repeats the city (Stockholm, Stockholm).
  const place = [location.city, location.region !== location.city ? location.region : "", country].filter(Boolean).join(", ");
  return (
    <div>
      <p className="text-meta text-muted-foreground">Location</p>
      <p className="flex items-center gap-1.5 text-lg">
        {location.country ? (
          <span aria-hidden>{flag(location.country)}</span>
        ) : (
          <MapPin className="size-4 text-muted-foreground" aria-hidden />
        )}
        {place || "Unknown"}
      </p>
      {(location.org || location.timezone) && (
        <p className="text-meta text-muted-foreground">{[location.org, location.timezone].filter(Boolean).join(" · ")}</p>
      )}
    </div>
  );
}

const typeLabels: Record<ProxyType, string> = {
  http: "HTTP",
  https: "HTTPS",
  socks5: "SOCKS5",
  socks5h: "SOCKS5h (DNS on the proxy)",
};

const defaultPorts: Record<ProxyType, number> = { http: 3128, https: 443, socks5: 1080, socks5h: 1080 };

function address(p: Proxy) {
  return `${p.type}://${p.host.includes(":") ? `[${p.host}]` : p.host}:${p.port}`;
}

/** Create or edit one proxy. `proxy` null means create. */
function ProxyForm({ proxy, onDone }: { proxy: Proxy | null; onDone: () => void }) {
  const qc = useQueryClient();
  const create = api.useMutation("post", "/api/admin/proxies");
  const update = api.useMutation("put", "/api/admin/proxies/{id}");
  const [type, setType] = useState<ProxyType>(proxy?.type ?? "http");
  const [enabled, setEnabled] = useState(proxy?.enabled ?? true);
  const [isDefault, setIsDefault] = useState(proxy?.is_default ?? false);
  const [clearPassword, setClearPassword] = useState(false);
  const pending = create.isPending || update.isPending;

  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const password = String(form.get("password") ?? "");
    const body = {
      label: String(form.get("label") ?? "").trim(),
      type,
      host: String(form.get("host") ?? "").trim(),
      port: Number(form.get("port")),
      username: String(form.get("username") ?? "").trim() || null,
      // On edit: blank keeps the stored password, unless "remove" is ticked.
      password: password !== "" ? password : clearPassword ? "" : null,
      enabled,
      is_default: isDefault,
    };
    const opts = {
      onSuccess: () => {
        qc.invalidateQueries({ queryKey: ["get", "/api/admin/proxies"] });
        toast(proxy ? "Proxy saved" : "Proxy added");
        onDone();
      },
      onError: (err: unknown) => toast.error(errorMessage(err)),
    };
    if (proxy) update.mutate({ params: { path: { id: proxy.id } }, body }, opts);
    else create.mutate({ body }, opts);
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-4">
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="proxy-label">Label</Label>
        <Input id="proxy-label" name="label" defaultValue={proxy?.label} placeholder="e.g. Frankfurt egress" />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="proxy-type">Type</Label>
        <Select value={type} onValueChange={(v) => setType(v as ProxyType)}>
          <SelectTrigger id="proxy-type">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(Object.keys(typeLabels) as ProxyType[]).map((t) => (
              <SelectItem key={t} value={t}>
                {typeLabels[t]}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <div className="grid grid-cols-[1fr_7rem] gap-3">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="proxy-host">Host</Label>
          <Input id="proxy-host" name="host" defaultValue={proxy?.host} placeholder="proxy.example.com" required />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="proxy-port">Port</Label>
          <Input
            id="proxy-port"
            name="port"
            type="number"
            min={1}
            max={65535}
            key={proxy ? "edit" : type}
            defaultValue={proxy?.port ?? defaultPorts[type]}
            required
          />
        </div>
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="proxy-username">Username (optional)</Label>
          <Input id="proxy-username" name="username" defaultValue={proxy?.username ?? ""} autoComplete="off" />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="proxy-password">Password</Label>
          <Input
            id="proxy-password"
            name="password"
            type="password"
            autoComplete="new-password"
            disabled={clearPassword}
            placeholder={proxy?.has_password ? "Unchanged" : "Sealed with master key"}
          />
        </div>
      </div>
      {proxy?.has_password && (
        <label className="flex items-center gap-2 text-sm">
          <Checkbox checked={clearPassword} onCheckedChange={(v) => setClearPassword(v === true)} />
          Remove the stored password
        </label>
      )}
      <label className="flex items-start gap-2 text-sm">
        <Checkbox className="mt-0.5" checked={isDefault} onCheckedChange={(v) => setIsDefault(v === true)} />
        <span>
          Global default
          <span className="block text-muted-foreground">Accounts set to &quot;Inherit&quot; go out through this proxy.</span>
        </span>
      </label>
      <label className="flex items-start gap-2 text-sm">
        <Checkbox className="mt-0.5" checked={enabled} onCheckedChange={(v) => setEnabled(v === true)} />
        <span>
          Enabled
          <span className="block text-muted-foreground">
            While disabled, accounts using this proxy take no requests. They never fall back to another IP.
          </span>
        </span>
      </label>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={pending}>
          {pending ? "Saving…" : proxy ? "Save" : "Add proxy"}
        </Button>
      </DialogFooter>
    </form>
  );
}

/** Periodic health from the gateways (REQ-PROXY-03): one line per upstream origin. */
function HealthCell({ health = [] }: { health?: ProxyHealth[] }) {
  // An older control plane omits the field; treat it as "not checked".
  if (health.length === 0) return <span className="text-meta text-muted-foreground">Not checked</span>;
  const sick = health.filter((h) => !h.healthy);
  const worst = Math.max(...health.map((h) => h.latency_ms));
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span tabIndex={0} className="inline-flex">
          {sick.length === 0 ? (
            <Tag tone="brand">Healthy · {worst} ms</Tag>
          ) : (
            <span className="inline-flex items-center gap-1 text-meta font-medium text-destructive-text">
              <XCircle className="size-3.5" aria-hidden />
              Unhealthy
            </span>
          )}
        </span>
      </TooltipTrigger>
      <TooltipContent>
        {health.map((h) => (
          <p key={h.origin}>
            <span className="font-mono">{h.origin.replace(/^https?:\/\//, "")}</span>:{" "}
            {h.ok ? `${h.latency_ms} ms` : h.error || "failed"}
            {!h.healthy && " (no traffic)"}
          </p>
        ))}
        <p className="opacity-70">Checked {new Date(health[0]?.at ?? "").toLocaleTimeString()}</p>
      </TooltipContent>
    </Tooltip>
  );
}

/** Create or edit a group. Members are kept in failover order. */
function GroupForm({ group, proxies, onDone }: { group: Group | null; proxies: Proxy[]; onDone: () => void }) {
  const qc = useQueryClient();
  const create = api.useMutation("post", "/api/admin/proxy-groups");
  const update = api.useMutation("put", "/api/admin/proxy-groups/{id}");
  const [policy, setPolicy] = useState<Policy>(group?.policy ?? "failover");
  const [members, setMembers] = useState<string[]>(group?.members ?? []);
  const pending = create.isPending || update.isPending;
  const byId = new Map(proxies.map((p) => [p.id, p]));

  const toggle = (id: string, on: boolean) => setMembers((m) => (on ? [...m, id] : m.filter((x) => x !== id)));
  const move = (i: number, d: -1 | 1) =>
    setMembers((m) => {
      const next = [...m];
      next.splice(i + d, 0, ...next.splice(i, 1));
      return next;
    });

  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const body = { label: String(new FormData(e.currentTarget).get("label") ?? "").trim(), policy, members };
    const opts = {
      onSuccess: () => {
        qc.invalidateQueries({ queryKey: ["get", "/api/admin/proxy-groups"] });
        toast(group ? "Group saved" : "Group added");
        onDone();
      },
      onError: (err: unknown) => toast.error(errorMessage(err)),
    };
    if (group) update.mutate({ params: { path: { id: group.id } }, body }, opts);
    else create.mutate({ body }, opts);
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-4">
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="group-label">Label</Label>
        <Input id="group-label" name="label" defaultValue={group?.label} placeholder="e.g. EU egress" required />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="group-policy">Policy</Label>
        <Select value={policy} onValueChange={(v) => setPolicy(v as Policy)}>
          <SelectTrigger id="group-policy">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="failover">Failover: first healthy member, in order</SelectItem>
            <SelectItem value="sticky">Sticky: each account keeps one member</SelectItem>
          </SelectContent>
        </Select>
        <p className="text-meta text-muted-foreground">
          {policy === "failover"
            ? "Unhealthy members are skipped. Switching happens only before a response starts."
            : "Each account's source IP stays fixed. If its member is unhealthy, the account takes no requests until it recovers."}
        </p>
      </div>
      <fieldset className="flex flex-col gap-1.5">
        <legend className="mb-1.5 text-sm font-medium">Members{policy === "failover" && " (in order)"}</legend>
        {members.map((id, i) => (
          <div key={id} className="flex items-center gap-2 rounded-md border px-2 py-1">
            <Checkbox checked onCheckedChange={() => toggle(id, false)} aria-label={`Remove ${byId.get(id)?.label}`} />
            <span className="flex-1 text-sm">
              {policy === "failover" && <span className="me-2 text-muted-foreground tabular-nums">{i + 1}.</span>}
              {byId.get(id)?.label ?? id}
              {byId.get(id)?.enabled === false && <span className="text-muted-foreground"> (disabled)</span>}
            </span>
            <Button type="button" variant="ghost" size="icon" aria-label="Move up" disabled={i === 0} onClick={() => move(i, -1)}>
              <ArrowUp className="size-4" />
            </Button>
            <Button type="button" variant="ghost" size="icon" aria-label="Move down" disabled={i === members.length - 1} onClick={() => move(i, 1)}>
              <ArrowDown className="size-4" />
            </Button>
          </div>
        ))}
        {proxies
          .filter((p) => !members.includes(p.id))
          .map((p) => (
            <label key={p.id} className="flex items-center gap-2 px-2 py-1 text-sm">
              <Checkbox checked={false} onCheckedChange={() => toggle(p.id, true)} />
              {p.label}
              {!p.enabled && <span className="text-muted-foreground"> (disabled)</span>}
            </label>
          ))}
      </fieldset>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={pending || members.length === 0}>
          {pending ? "Saving…" : group ? "Save" : "Add group"}
        </Button>
      </DialogFooter>
    </form>
  );
}

function GroupsSection({ proxies }: { proxies: Proxy[] }) {
  const qc = useQueryClient();
  const groups = api.useQuery("get", "/api/admin/proxy-groups");
  const del = api.useMutation("delete", "/api/admin/proxy-groups/{id}");
  const [editing, setEditing] = useState<Group | null>(null);
  const [open, setOpen] = useState(false);
  const byId = new Map(proxies.map((p) => [p.id, p]));

  const openForm = (g: Group | null) => {
    setEditing(g);
    setOpen(true);
  };
  const handleDelete = (g: Group) =>
    del.mutate(
      { params: { path: { id: g.id } } },
      {
        onSuccess: () => {
          qc.invalidateQueries({ queryKey: ["get", "/api/admin/proxy-groups"] });
          toast("Group deleted");
        },
        onError: (err) => toast.error(errorMessage(err)),
      },
    );

  return (
    <>
      <SectionHeader
        actions={
          <Button size="sm" variant="outline" onClick={() => openForm(null)} disabled={proxies.length === 0}>
            <Plus className="me-1.5 size-4" />
            Add group
          </Button>
        }
      >
        Proxy groups
      </SectionHeader>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{editing ? `Edit ${editing.label}` : "Add proxy group"}</DialogTitle>
            <DialogDescription>Accounts and providers can use a group wherever they can use a proxy.</DialogDescription>
          </DialogHeader>
          {open && <GroupForm key={editing?.id ?? "new"} group={editing} proxies={proxies} onDone={() => setOpen(false)} />}
        </DialogContent>
      </Dialog>
      {groups.isPending ? (
        <Skeleton className="h-20 w-full" />
      ) : groups.isError ? (
        <p role="alert" className="text-destructive-text">
          {errorMessage(groups.error)}
        </p>
      ) : groups.data.length === 0 ? (
        <p className="text-muted-foreground">
          No groups. A failover group moves traffic to the next proxy when one is unhealthy; a sticky group spreads accounts over
          several proxies, each account keeping one.
        </p>
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Group</TableHead>
                <TableHead>Policy</TableHead>
                <TableHead>Members</TableHead>
                <TableHead className="text-end">Accounts</TableHead>
                <TableHead className="text-end">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {groups.data.map((g) => (
                <TableRow key={g.id}>
                  <TableCell className="font-semibold">{g.label}</TableCell>
                  <TableCell>
                    <Tag tone={g.policy === "sticky" ? "sand" : "neutral"}>{g.policy === "sticky" ? "Sticky" : "Failover"}</Tag>
                  </TableCell>
                  <TableCell className="whitespace-normal text-meta">
                    {g.members.map((id) => byId.get(id)?.label ?? id).join(g.policy === "failover" ? " → " : ", ")}
                  </TableCell>
                  <TableCell className="text-end tabular-nums">{g.account_count}</TableCell>
                  <TableCell className="text-end">
                    <Button variant="ghost" size="icon" aria-label={`Edit ${g.label}`} onClick={() => openForm(g)}>
                      <Pencil className="size-4 text-muted-foreground" />
                    </Button>
                    <AlertDialog>
                      <AlertDialogTrigger asChild>
                        <Button variant="ghost" size="icon" aria-label={`Delete ${g.label}`}>
                          <Trash2 className="size-4 text-muted-foreground hover:text-destructive-text" />
                        </Button>
                      </AlertDialogTrigger>
                      <AlertDialogContent>
                        <AlertDialogHeader>
                          <AlertDialogTitle>Delete {g.label}?</AlertDialogTitle>
                          <AlertDialogDescription>
                            The proxies stay. Accounts or providers that use this group must be moved first.
                          </AlertDialogDescription>
                        </AlertDialogHeader>
                        <AlertDialogFooter>
                          <AlertDialogCancel>Cancel</AlertDialogCancel>
                          <AlertDialogAction onClick={() => handleDelete(g)}>Delete</AlertDialogAction>
                        </AlertDialogFooter>
                      </AlertDialogContent>
                    </AlertDialog>
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

/** §8.2 level 2: a provider's accounts set to Inherit use this before the global default. */
function ProvidersSection({ proxies }: { proxies: Proxy[] }) {
  const qc = useQueryClient();
  const settings = api.useQuery("get", "/api/admin/provider-proxies");
  const groups = api.useQuery("get", "/api/admin/proxy-groups");
  const set = api.useMutation("put", "/api/admin/provider-proxies/{provider}");

  const change = (provider: Schemas["Provider"], proxy: string) =>
    set.mutate(
      { params: { path: { provider } }, body: { proxy } },
      {
        onSuccess: () => {
          qc.invalidateQueries({ queryKey: ["get", "/api/admin/provider-proxies"] });
          toast("Provider proxy updated. New requests use it within seconds.");
        },
        onError: (err) => toast.error(errorMessage(err)),
      },
    );

  return (
    <>
      <SectionHeader>Provider proxies</SectionHeader>
      {settings.isPending ? (
        <Skeleton className="h-20 w-full" />
      ) : settings.isError ? (
        <p role="alert" className="text-destructive-text">
          {errorMessage(settings.error)}
        </p>
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Provider</TableHead>
                <TableHead>Proxy for accounts set to Inherit</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {settings.data.map((p) => (
                <TableRow key={p.provider}>
                  <TableCell className="font-semibold">{providerNames[p.provider] ?? p.provider}</TableCell>
                  <TableCell>
                    <Select value={p.proxy} onValueChange={(v) => change(p.provider, v)} disabled={set.isPending}>
                      <SelectTrigger size="sm" className="min-w-44" aria-label={`Proxy for ${providerNames[p.provider] ?? p.provider}`}>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <ProxyChoiceOptions proxies={proxies} groups={groups.data ?? []} inherit={defaultLabel(proxies)} />
                      </SelectContent>
                    </Select>
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

function TestResults({ result }: { result: TestResult }) {
  return (
    <div className="flex flex-col gap-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <div>
          <p className="text-meta text-muted-foreground">Egress IP</p>
          {result.egress_ip ? (
            <p className="font-mono text-lg">{result.egress_ip}</p>
          ) : (
            <p className="text-destructive-text">{result.egress_error || "Unknown"}</p>
          )}
        </div>
        {result.location && <Location location={result.location} />}
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Provider</TableHead>
            <TableHead>Result</TableHead>
            <TableHead className="text-end">Latency</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {result.providers.map((p) => (
            <TableRow key={p.provider}>
              <TableCell>
                <span className="font-semibold">{p.provider}</span>
                <span className="block font-mono text-meta text-muted-foreground">{p.host}</span>
              </TableCell>
              <TableCell className="whitespace-normal">
                {p.ok ? (
                  <span className="inline-flex items-center gap-1.5 text-brand-text">
                    <CheckCircle2 className="size-4" aria-hidden />
                    {p.tls_version ?? "Reached"}
                  </span>
                ) : (
                  <span className="inline-flex items-start gap-1.5 text-destructive-text">
                    <XCircle className="mt-0.5 size-4 shrink-0" aria-hidden />
                    {p.error}
                  </span>
                )}
              </TableCell>
              <TableCell className="text-end font-mono tabular-nums">{p.ok ? `${p.latency_ms} ms` : "—"}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <p className="text-meta text-muted-foreground">
        Latency covers connect, TLS and the first response, through the proxy, on a fresh connection. Location is
        approximate (geo-IP).
      </p>
    </div>
  );
}

export default function ProxiesPage() {
  const qc = useQueryClient();
  const proxies = api.useQuery("get", "/api/admin/proxies");
  const del = api.useMutation("delete", "/api/admin/proxies/{id}");
  const test = api.useMutation("post", "/api/admin/proxies/{id}/test");

  const [editing, setEditing] = useState<Proxy | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [testing, setTesting] = useState<Proxy | null>(null);
  const [result, setResult] = useState<TestResult | null>(null);

  const openForm = (p: Proxy | null) => {
    setEditing(p);
    setFormOpen(true);
  };

  const runTest = (p: Proxy) => {
    setTesting(p);
    setResult(null);
    test.mutate(
      { params: { path: { id: p.id } } },
      {
        onSuccess: (data) => setResult(data),
        onError: (err) => {
          setTesting(null);
          toast.error(errorMessage(err));
        },
      },
    );
  };

  const handleDelete = (p: Proxy) => {
    del.mutate(
      { params: { path: { id: p.id } } },
      {
        onSuccess: () => {
          qc.invalidateQueries({ queryKey: ["get", "/api/admin/proxies"] });
          toast("Proxy deleted");
        },
        onError: (err) => toast.error(errorMessage(err)),
      },
    );
  };

  const addButton = (
    <Button size="sm" onClick={() => openForm(null)}>
      <Plus className="me-1.5 size-4" />
      Add proxy
    </Button>
  );

  return (
    <>
      <PageHeader
        title="Proxies"
        icon={Network}
        description="Outbound proxies for upstream accounts. An account uses its own setting (Accounts page), else its provider's, else the global default, else a direct connection."
        actions={addButton}
      />

      <Dialog open={formOpen} onOpenChange={setFormOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{editing ? `Edit ${editing.label}` : "Add proxy"}</DialogTitle>
            <DialogDescription>
              Changes apply to new requests within seconds. Streams already running finish on their current connection.
            </DialogDescription>
          </DialogHeader>
          {formOpen && <ProxyForm key={editing?.id ?? "new"} proxy={editing} onDone={() => setFormOpen(false)} />}
        </DialogContent>
      </Dialog>

      <Dialog open={testing !== null} onOpenChange={(o) => !o && setTesting(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Test {testing?.label}</DialogTitle>
            <DialogDescription>{testing && <span className="font-mono">{address(testing)}</span>}</DialogDescription>
          </DialogHeader>
          {result ? (
            <TestResults result={result} />
          ) : (
            <div className="flex flex-col gap-2" aria-busy>
              <p className="text-muted-foreground">Reaching each provider through the proxy…</p>
              <Skeleton className="h-32 w-full" />
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => testing && runTest(testing)} disabled={test.isPending}>
              Run again
            </Button>
            <Button onClick={() => setTesting(null)}>Close</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {proxies.isPending ? (
        <Skeleton className="h-40 w-full" />
      ) : proxies.isError ? (
        <p role="alert" className="text-destructive-text">
          {errorMessage(proxies.error)}
        </p>
      ) : proxies.data.length === 0 ? (
        <EmptyState title="No proxies yet" action={addButton}>
          Without a proxy every account connects directly from this server. Add one to give accounts a fixed egress IP.
        </EmptyState>
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Proxy</TableHead>
                <TableHead>Auth</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Health</TableHead>
                <TableHead className="text-end">Accounts</TableHead>
                <TableHead className="text-end">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {proxies.data.map((p) => (
                <TableRow key={p.id}>
                  <TableCell>
                    <span className="font-semibold">{p.label}</span>
                    <span className="block font-mono text-meta text-muted-foreground">{address(p)}</span>
                  </TableCell>
                  <TableCell className="text-meta text-muted-foreground">
                    {p.username ? `${p.username}${p.has_password ? " · password set" : ""}` : "None"}
                  </TableCell>
                  <TableCell>
                    <span className="flex gap-1.5">
                      {p.enabled ? <Tag tone="brand">Enabled</Tag> : <Tag>Disabled</Tag>}
                      {p.is_default && <Tag tone="sand">Default</Tag>}
                    </span>
                  </TableCell>
                  <TableCell>
                    <HealthCell health={p.health} />
                  </TableCell>
                  <TableCell className="text-end tabular-nums">
                    {p.account_count}
                    {p.is_default && <span className="text-muted-foreground"> + inherit</span>}
                  </TableCell>
                  <TableCell className="text-end">
                    <Button variant="outline" size="sm" onClick={() => runTest(p)} disabled={test.isPending && testing?.id === p.id}>
                      <Zap className="me-1 size-3.5" />
                      Test
                    </Button>
                    <Button variant="ghost" size="icon" aria-label={`Edit ${p.label}`} onClick={() => openForm(p)}>
                      <Pencil className="size-4 text-muted-foreground" />
                    </Button>
                    <AlertDialog>
                      <AlertDialogTrigger asChild>
                        <Button variant="ghost" size="icon" aria-label={`Delete ${p.label}`}>
                          <Trash2 className="size-4 text-muted-foreground hover:text-destructive-text" />
                        </Button>
                      </AlertDialogTrigger>
                      <AlertDialogContent>
                        <AlertDialogHeader>
                          <AlertDialogTitle>Delete {p.label}?</AlertDialogTitle>
                          <AlertDialogDescription>
                            {p.account_count > 0
                              ? `${p.account_count} account(s) use this proxy. Move them to another proxy on the Accounts page first. Groups and providers that use it must be changed too.`
                              : p.is_default
                              ? "This is the global default. Accounts set to Inherit will connect directly after it's gone."
                              : "No account uses this proxy."}
                          </AlertDialogDescription>
                        </AlertDialogHeader>
                        <AlertDialogFooter>
                          <AlertDialogCancel>Cancel</AlertDialogCancel>
                          <AlertDialogAction onClick={() => handleDelete(p)}>Delete</AlertDialogAction>
                        </AlertDialogFooter>
                      </AlertDialogContent>
                    </AlertDialog>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {proxies.data && (
        <>
          <ProvidersSection proxies={proxies.data} />
          <GroupsSection proxies={proxies.data} />
        </>
      )}
    </>
  );
}
