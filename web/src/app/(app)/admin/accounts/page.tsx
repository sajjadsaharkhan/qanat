/*
 * Ported from 9router (https://github.com/decolua/9router),
 * src/app/(dashboard)/dashboard/providers/page.js, providers/[id]/page.js and
 * usage/components/ProviderLimits.
 * MIT License, Copyright (c) 2024-2026 decolua and contributors. See LICENSE-9router.
 * Qanat change: every provider's pool on one page (a tab per provider, in the
 * URL), each account with its provider limits; filter, sort, search, a list
 * or grid view and bulk actions over a selection.
 */
"use client";

import { ReducedMotionConfig, spring, transition, useTempoTransition } from "@animate-ui/react/utils/motion";
import { useQueryClient } from "@tanstack/react-query";
import { CheckCheck, LayoutGrid, List, Loader2, Play, Plus, Power, PowerOff, RefreshCw, Search, Server, X } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { Suspense, useMemo, useState } from "react";
import { toast } from "sonner";

import { AccountCard, AccountRow, type ItemProps } from "@/components/qanat/accounts/account-item";
import { AddAccountDialog, EditAccountDialog } from "@/components/qanat/accounts/account-dialogs";
import { Checkbox, Menu, MenuContent, MenuItem, MenuLabel, MenuTrigger, Tabs, TabsList, TabsTrigger, ToggleGroup, ToggleGroupItem } from "@/components/qanat/accounts/animated";
import { useNow } from "@/components/qanat/accounts/limit-bars";
import { headroom, nextReset, supportsLimits, useAccountLimits, type Account } from "@/components/qanat/accounts/limits";
import { PoolSummaryCard } from "@/components/qanat/accounts/pool-summary";
import antigravityLogo from "@/components/qanat/usage-overview/logos/antigravity.png";
import claudeLogo from "@/components/qanat/usage-overview/logos/claude.png";
import codexLogo from "@/components/qanat/usage-overview/logos/codex.png";
import { EmptyState, PageHeader } from "@/components/qanat/page";
import { choiceLabel, defaultLabel } from "@/components/qanat/proxy-options";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { useSearchParam } from "@/hooks/use-search-param";
import { api, errorMessage, fetchClient } from "@/lib/api/client";

const providers = [
  { id: "claude", name: "Claude Code", prefix: "cc", logo: claudeLogo.src },
  { id: "codex", name: "OpenAI Codex", prefix: "cx", logo: codexLogo.src },
  { id: "antigravity", name: "Antigravity", prefix: "ag", logo: antigravityLogo.src },
] as const;
type ProviderId = (typeof providers)[number]["id"];
const providerOf = (id: string) => providers.find((p) => p.id === id) ?? providers[0];

const tabs = ["all", "claude", "codex", "antigravity"] as const;
type Tab = (typeof tabs)[number];
const statuses = ["all", "active", "limited", "cooldown", "error", "disabled"] as const;
type Status = (typeof statuses)[number];
const kinds = ["all", "oauth", "token", "api_key"] as const;
type Kind = (typeof kinds)[number];
const sorts = ["pool", "quota-low", "quota-high", "reset", "name"] as const;
type Sort = (typeof sorts)[number];
const views = ["list", "grid"] as const;
type View = (typeof views)[number];

const statusLabel: Record<Status, string> = {
  all: "Any status",
  active: "Active",
  limited: "At limit",
  cooldown: "Cooling down",
  error: "Errors",
  disabled: "Disabled",
};
const kindLabel: Record<Kind, string> = { all: "Any credential", oauth: "OAuth", token: "Token", api_key: "API key" };
const sortLabel: Record<Sort, string> = {
  pool: "Pool order",
  "quota-low": "Least quota left",
  "quota-high": "Most quota left",
  reset: "Soonest reset",
  name: "Name",
};

export default function AccountsPage() {
  // Tab, filters and view live in the URL (useSearchParams), which needs a Suspense boundary.
  return (
    <Suspense fallback={<Skeleton className="h-96 w-full" />}>
      <Accounts />
    </Suspense>
  );
}

function Accounts() {
  const qc = useQueryClient();
  const invalidate = () => qc.invalidateQueries({ queryKey: ["get", "/api/admin/accounts"] });
  const now = useNow();

  const [tab, setTab] = useSearchParam<Tab>("provider", "all", tabs);
  const [status, setStatus] = useSearchParam<Status>("status", "all", statuses);
  const [kind, setKind] = useSearchParam<Kind>("kind", "all", kinds);
  const [sort, setSort] = useSearchParam<Sort>("sort", "pool", sorts);
  const [view, setView] = useSearchParam<View>("view", "list", views);
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [addFor, setAddFor] = useState<ProviderId | null>(null);
  const [editTarget, setEditTarget] = useState<Account | null>(null);
  const [testing, setTesting] = useState<Set<string>>(new Set());
  const [bulkBusy, setBulkBusy] = useState(false);

  const accountsQ = api.useQuery("get", "/api/admin/accounts");
  const proxies = api.useQuery("get", "/api/admin/proxies");
  const groups = api.useQuery("get", "/api/admin/proxy-groups");
  const providerProxies = api.useQuery("get", "/api/admin/provider-proxies");
  const proxyList = proxies.data ?? [];
  const groupList = groups.data ?? [];
  const inheritFor = (p: string) => {
    const set = providerProxies.data?.find((x) => x.provider === p)?.proxy ?? "inherit";
    return set === "inherit" ? defaultLabel(proxyList) : `${p}: ${set === "direct" ? "Direct" : choiceLabel(set, proxyList, groupList)}`;
  };

  const all = useMemo(() => accountsQ.data ?? [], [accountsQ.data]);
  const limits = useAccountLimits(all);

  const inTab = tab === "all" ? all : all.filter((a) => a.provider === tab);
  const q = query.trim().toLowerCase();
  const visible = inTab
    .filter((a) => {
      if (kind !== "all" && a.auth_kind !== kind) return false;
      if (q && ![a.label, a.email ?? "", a.id, a.base_url ?? ""].some((s) => s.toLowerCase().includes(q))) return false;
      const l = limits.byId.get(a.id)?.data;
      const errored = a.state === "disconnected" || a.last_check_ok === false || !!a.last_refresh_error;
      switch (status) {
        case "active":
          return !a.disabled_at && !errored && a.state !== "cooldown";
        case "limited":
          return !!l?.limit_reached || headroom(l) === 0;
        case "cooldown":
          return a.state === "cooldown";
        case "error":
          return !a.disabled_at && errored;
        case "disabled":
          return !!a.disabled_at;
      }
      return true;
    })
    .sort((a, b) => {
      const la = limits.byId.get(a.id)?.data;
      const lb = limits.byId.get(b.id)?.data;
      // Unknown headroom sorts last either way.
      const ha = headroom(la);
      const hb = headroom(lb);
      switch (sort) {
        case "quota-low":
          return (ha ?? 101) - (hb ?? 101);
        case "quota-high":
          return (hb ?? -1) - (ha ?? -1);
        case "reset": {
          const ra = nextReset(la)?.getTime() ?? Infinity;
          const rb = nextReset(lb)?.getTime() ?? Infinity;
          return ra - rb;
        }
        case "name":
          return (a.label || a.id).localeCompare(b.label || b.id);
      }
      return providers.findIndex((p) => p.id === a.provider) - providers.findIndex((p) => p.id === b.provider) || a.position - b.position;
    });

  // In pool order the list groups by provider, so the arrows swap pool neighbours.
  const grouped = sort === "pool" && tab === "all";
  const sections = grouped
    ? providers.map((p) => ({ provider: p, items: visible.filter((a) => a.provider === p.id) })).filter((s) => s.items.length > 0)
    : [{ provider: null, items: visible }];

  const filtersOn = status !== "all" || kind !== "all" || q !== "";
  const visibleIds = visible.map((a) => a.id);
  const selectedVisible = visibleIds.filter((id) => selected.has(id));
  const allChecked = visibleIds.length > 0 && selectedVisible.length === visibleIds.length ? true : selectedVisible.length > 0 ? "indeterminate" : false;

  const toggle = (id: string, on: boolean) =>
    setSelected((s) => {
      const n = new Set(s);
      if (on) n.add(id);
      else n.delete(id);
      return n;
    });

  // --- bulk actions over the selection (or the whole scope from the header) --

  const testMany = async (ids: string[]) => {
    const targets = all.filter((a) => ids.includes(a.id) && a.has_credentials);
    setTesting(new Set(targets.map((a) => a.id)));
    let failed = 0;
    try {
      for (const a of targets) {
        try {
          const { data, error } = await fetchClient.POST("/api/admin/accounts/{id}/test", { params: { path: { id: a.id } } });
          if (error || !data?.ok) {
            failed++;
            toast.error(`${a.label || a.id}: ${data?.error ?? (error ? errorMessage(error) : "the check failed")}`);
          }
        } finally {
          setTesting((s) => {
            const n = new Set(s);
            n.delete(a.id);
            return n;
          });
        }
      }
      toast(failed === 0 ? `Tested ${targets.length} account${targets.length === 1 ? "" : "s"}: all valid.` : `Tested ${targets.length}: ${failed} failed.`);
    } finally {
      invalidate();
    }
  };

  const setEnabledMany = async (ids: string[], on: boolean) => {
    setBulkBusy(true);
    try {
      const results = await Promise.all(
        ids.map((id) => fetchClient.PATCH("/api/admin/accounts/{id}", { params: { path: { id } }, body: { disabled: !on } })),
      );
      const failed = results.filter((r) => r.error).length;
      if (failed > 0) toast.error(`${failed} account${failed === 1 ? "" : "s"} couldn't be ${on ? "enabled" : "disabled"}.`);
      else toast(on ? `${ids.length} enabled.` : `${ids.length} disabled. They take no new traffic.`);
    } finally {
      setBulkBusy(false);
      invalidate();
    }
  };

  const refreshMany = async (ids: string[]) => {
    const n = all.filter((a) => ids.includes(a.id) && supportsLimits(a)).length;
    if (n === 0) {
      toast("None of these accounts report subscription limits (API keys don't).");
      return;
    }
    await limits.refresh(ids);
    toast(`Limits refreshed for ${n} account${n === 1 ? "" : "s"}.`);
  };

  const scopeIds = inTab.map((a) => a.id);
  const anyTesting = testing.size > 0;

  const itemProps = (a: Account, list: Account[]): ItemProps => {
    const p = providerOf(a.provider);
    const peers = list.filter((x) => x.provider === a.provider);
    const at = peers.indexOf(a);
    return {
      account: a,
      providerName: p.name,
      logo: p.logo,
      limits: limits.byId.get(a.id)?.data,
      limitsPending: limits.byId.get(a.id)?.pending ?? false,
      supported: supportsLimits(a),
      refreshing: limits.refreshing.has(a.id),
      onRefresh: () => void limits.refresh([a.id]),
      selected: selected.has(a.id),
      onSelect: (on) => toggle(a.id, on),
      // Pool order with no filter: neighbours in the list are pool neighbours.
      reorder: sort === "pool" && !filtersOn ? { first: at === 0, last: at === peers.length - 1 } : null,
      testing: testing.has(a.id),
      proxies: proxyList,
      groups: groupList,
      inherit: inheritFor(a.provider),
      onEdit: setEditTarget,
      now,
    };
  };

  const addButton =
    tab === "all" ? (
      <Menu>
        <MenuTrigger asChild>
          <Button size="sm">
            <Plus className="me-1.5 size-4" />
            Add connection
          </Button>
        </MenuTrigger>
        <MenuContent align="end">
          <MenuLabel>Provider</MenuLabel>
          {providers.map((p) => (
            <MenuItem key={p.id} onSelect={() => setAddFor(p.id)}>
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={p.logo} alt="" className="size-4 rounded object-contain" />
              {p.name}
            </MenuItem>
          ))}
        </MenuContent>
      </Menu>
    ) : (
      <Button size="sm" onClick={() => setAddFor(tab)}>
        <Plus className="me-1.5 size-4" />
        Add connection
      </Button>
    );

  const loading = accountsQ.isPending || proxies.isPending;

  return (
    <ReducedMotionConfig>
      <PageHeader
        title="Accounts"
        icon={Server}
        description="Upstream provider accounts pooled across users, with each account's remaining subscription quota as its provider reports it. Connections are tried in pool order; a rate-limited account cools down and traffic moves on."
        actions={
          <>
            <Button size="sm" variant="outline" aria-label="Refresh limits" onClick={() => refreshMany(scopeIds)} disabled={limits.refreshing.size > 0 || scopeIds.length === 0}>
              <RefreshCw className={`size-4 sm:me-1.5 ${limits.refreshing.size > 0 ? "animate-spin motion-reduce:animate-none" : ""}`} />
              <span className="hidden sm:inline">Refresh limits</span>
            </Button>
            <Button size="sm" variant="outline" aria-label="Test all" onClick={() => testMany(scopeIds)} disabled={anyTesting || scopeIds.length === 0}>
              {anyTesting ? <Loader2 className="size-4 animate-spin motion-reduce:animate-none sm:me-1.5" /> : <Play className="size-4 sm:me-1.5" />}
              <span className="hidden sm:inline">Test all</span>
            </Button>
            {addButton}
          </>
        }
      />

      <Tabs
        value={tab}
        onValueChange={(v) => {
          setTab(v as Tab);
          setSelected(new Set());
        }}
      >
        <TabsList aria-label="Provider">
          <TabsTrigger value="all">
            All <span className="text-xs text-muted-foreground tabular-nums">{all.length}</span>
          </TabsTrigger>
          {providers.map((p) => (
            <TabsTrigger key={p.id} value={p.id}>
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={p.logo} alt="" className="size-4 rounded object-contain" />
              {p.name}
              <span className="text-xs text-muted-foreground tabular-nums">{all.filter((a) => a.provider === p.id).length}</span>
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>

      {loading ? (
        <div className="mt-4 space-y-3">
          <div className="grid gap-3 md:grid-cols-3">
            <Skeleton className="h-36" />
            <Skeleton className="h-36" />
            <Skeleton className="h-36" />
          </div>
          <Skeleton className="h-64 w-full" />
        </div>
      ) : (
        <>
          <div className={`mt-4 grid gap-3 ${tab === "all" ? "md:grid-cols-3" : ""}`}>
            {(tab === "all" ? providers : providers.filter((p) => p.id === tab)).map((p) => (
              <PoolSummaryCard
                key={p.id}
                name={p.name}
                prefix={p.prefix}
                logo={p.logo}
                accounts={all.filter((a) => a.provider === p.id)}
                byId={limits.byId}
                now={now}
                onSelect={tab === "all" ? () => setTab(p.id) : undefined}
              />
            ))}
          </div>

          {/* Toolbar: select all, search, filters, sort, view. */}
          <div className="mt-6 flex flex-wrap items-center gap-2">
            <Checkbox
              checked={allChecked}
              onCheckedChange={(v) => setSelected(v === true ? new Set([...selected, ...visibleIds]) : new Set([...selected].filter((id) => !visibleIds.includes(id))))}
              aria-label="Select all shown accounts"
              disabled={visibleIds.length === 0}
              className="me-1"
            />
            <div className="relative min-w-48 flex-1 sm:max-w-72">
              <Search aria-hidden className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Search label, email or ID" aria-label="Search accounts" className="h-8 ps-8" />
            </div>
            <Select value={status} onValueChange={(v) => setStatus(v as Status)}>
              <SelectTrigger size="sm" className="w-36" aria-label="Status filter">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {statuses.map((s) => (
                  <SelectItem key={s} value={s}>
                    {statusLabel[s]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select value={kind} onValueChange={(v) => setKind(v as Kind)}>
              <SelectTrigger size="sm" className="w-36" aria-label="Credential filter">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {kinds.map((k) => (
                  <SelectItem key={k} value={k}>
                    {kindLabel[k]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select value={sort} onValueChange={(v) => setSort(v as Sort)}>
              <SelectTrigger size="sm" className="w-40" aria-label="Sort">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {sorts.map((s) => (
                  <SelectItem key={s} value={s}>
                    {sortLabel[s]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <ToggleGroup type="single" value={view} onValueChange={(v) => v && setView(v as View)} aria-label="View" className="ms-auto">
              <ToggleGroupItem value="list" aria-label="List view">
                <List />
              </ToggleGroupItem>
              <ToggleGroupItem value="grid" aria-label="Grid view">
                <LayoutGrid />
              </ToggleGroupItem>
            </ToggleGroup>
          </div>

          <BulkBar
            count={selectedVisible.length}
            busy={bulkBusy || anyTesting || limits.refreshing.size > 0}
            onRefresh={() => refreshMany(selectedVisible)}
            onTest={() => testMany(selectedVisible)}
            onEnable={() => setEnabledMany(selectedVisible, true)}
            onDisable={() => setEnabledMany(selectedVisible, false)}
            onClear={() => setSelected(new Set())}
          />

          <div className="mt-3">
            {inTab.length === 0 ? (
              <EmptyState title={tab === "all" ? "No accounts yet" : `No ${providerOf(tab).name} accounts yet`} action={addButton}>
                Add a provider account to the pool; users&apos; requests are served from it.
              </EmptyState>
            ) : visible.length === 0 ? (
              <EmptyState
                title="No account matches"
                action={
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => {
                      setQuery("");
                      setStatus("all");
                      setKind("all");
                    }}
                  >
                    <X className="me-1.5 size-4" />
                    Clear filters
                  </Button>
                }
              >
                {inTab.length} account{inTab.length === 1 ? " is" : "s are"} hidden by the search or filters.
              </EmptyState>
            ) : (
              sections.map((s) => (
                <section key={s.provider?.id ?? "flat"} className="mb-5 last:mb-0" aria-label={s.provider?.name}>
                  {s.provider && (
                    <h2 className="mb-1.5 flex items-center gap-2 text-section font-semibold text-muted-foreground">
                      {s.provider.name}
                      <span className="text-xs font-normal tabular-nums">{s.items.length}</span>
                    </h2>
                  )}
                  <AccountList view={view} items={s.items} render={(a) => (view === "grid" ? <AccountCard {...itemProps(a, s.items)} /> : <AccountRow {...itemProps(a, s.items)} />)} />
                </section>
              ))
            )}
          </div>
        </>
      )}

      {addFor && (
        <AddAccountDialog
          key={addFor}
          provider={addFor}
          providerName={providerOf(addFor).name}
          open
          onOpenChange={(o) => !o && setAddFor(null)}
          proxies={proxyList}
          groups={groupList}
          inherit={inheritFor(addFor)}
          onSaved={invalidate}
        />
      )}
      <EditAccountDialog
        account={editTarget}
        open={!!editTarget}
        onOpenChange={(o) => !o && setEditTarget(null)}
        proxies={proxyList}
        groups={groupList}
        inherit={editTarget ? inheritFor(editTarget.provider) : "direct"}
        onSaved={invalidate}
      />
    </ReducedMotionConfig>
  );
}

/**
 * The accounts, as rows or cards. Items that stay glide to their new place
 * (filter, sort, reorder); new ones fade up, removed ones fade out
 * (Animate UI's list and filter-grid recipes, spring.reflow).
 */
function AccountList({ view, items, render }: { view: View; items: Account[]; render: (a: Account) => React.ReactNode }) {
  const move = useTempoTransition(spring.reflow);
  const fade = useTempoTransition(transition.fade);
  return (
    <motion.ul layout="position" transition={move} className={view === "grid" ? "grid gap-3 sm:grid-cols-2 xl:grid-cols-3" : "flex flex-col gap-1"}>
      <AnimatePresence mode="popLayout" initial={false}>
        {items.map((a) => (
          <motion.li
            key={a.id}
            layout="position"
            initial={{ opacity: 0, y: 8 }}
            animate={{ opacity: 1, y: 0, transition: { ...move, opacity: fade } }}
            exit={{ opacity: 0, scale: 0.98, transition: fade }}
            transition={move}
            className="min-w-0"
          >
            {render(a)}
          </motion.li>
        ))}
      </AnimatePresence>
    </motion.ul>
  );
}

/** Appears with a selection; acts on the selected accounts that are shown. */
function BulkBar({
  count,
  busy,
  onRefresh,
  onTest,
  onEnable,
  onDisable,
  onClear,
}: {
  count: number;
  busy: boolean;
  onRefresh: () => void;
  onTest: () => void;
  onEnable: () => void;
  onDisable: () => void;
  onClear: () => void;
}) {
  const fade = useTempoTransition(transition.fade);
  const pop = useTempoTransition(spring.pop);
  return (
    <AnimatePresence initial={false}>
      {count > 0 && (
        <motion.div
          role="toolbar"
          aria-label="Selected accounts"
          initial={{ opacity: 0, y: -6 }}
          animate={{ opacity: 1, y: 0, transition: { ...pop, opacity: fade } }}
          exit={{ opacity: 0, y: -6, transition: fade }}
          className="sticky top-14 z-10 mt-3 flex flex-wrap items-center gap-1.5 rounded-lg border bg-popover px-3 py-2 shadow-md"
        >
          <span className="me-1 inline-flex items-center gap-1.5 text-sm font-medium">
            <CheckCheck aria-hidden className="size-4 text-muted-foreground" />
            {count} selected
          </span>
          <Button size="sm" variant="ghost" onClick={onRefresh} disabled={busy}>
            <RefreshCw className="me-1.5 size-4" />
            Refresh limits
          </Button>
          <Button size="sm" variant="ghost" onClick={onTest} disabled={busy}>
            <Play className="me-1.5 size-4" />
            Test
          </Button>
          <Button size="sm" variant="ghost" onClick={onEnable} disabled={busy}>
            <Power className="me-1.5 size-4" />
            Enable
          </Button>
          <Button size="sm" variant="ghost" onClick={onDisable} disabled={busy}>
            <PowerOff className="me-1.5 size-4" />
            Disable
          </Button>
          <Button size="sm" variant="ghost" className="ms-auto" onClick={onClear} aria-label="Clear selection">
            <X className="size-4" />
          </Button>
        </motion.div>
      )}
    </AnimatePresence>
  );
}
