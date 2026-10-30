# Qanat — Design System

> **Quiet channel, clear signal.** A healthy qanat is silent: water moves underground and nobody thinks about it. The dashboard should feel the same: calm and warm: forest and sage like a well-kept garden by the water, on warm paper, with a touch of sand. When something needs a person, exactly one thing says so.

This document is the source of truth for the dashboard (`web/`). The look and feel starts from [Tower](https://github.com/ghhrmnzdh/tower) ([its design system](https://github.com/ghhrmnzdh/tower/blob/main/docs/DESIGN.md)): Apple-grade restraint, amber and red reserved for real state, motion only when state changes. Unlike Tower, Qanat has its own palette, chosen by the owner on 2026-10-30 for color and character: near-black `#0C0A08`, forest `#2A3723`, sage `#B9BBA8`, off-white `#E8E5DD` and sand `#DCBC98`. It is built with **shadcn/ui** on Tailwind v4 and supports **light and dark** themes from day one.

Requirement IDs (`REQ-…`) refer to [docs/prd.md](docs/prd.md).

---

## 1. Principles

Every component decision traces back to one of these four rules. In review, a design choice that can't name its rule is cut.

1. **The mark is the state.** Status is carried by the Qanat mark and by status symbols, never by decoration. Nothing is colored "to look nice".
2. **Forest is the interactive color** (sage in dark mode, where forest is too dark to see). Buttons, selections, focus rings, links, healthy quota bars and charts use it. **Sand is decoration only** (hero band, avatar, the root pill, the sign-in pattern) and never carries state.
3. **One loudest thing.** Amber means *held, will clear on its own or soon needs attention*. Red means *broken or exhausted, needs a person*. At most one element on screen pulses at a time.
4. **Motion = state change.** Nothing loops when all is well. Motion answers a person's action, or announces a state change once.

---

## 2. The Qanat mark

The one memorable element of the product. A cross-section of a qanat: a sloped underground channel with vertical access shafts. It sits in the sidebar header, on the sign-in page, in empty states and as the favicon. Its look **is** the gateway's state, as seen by the current viewer (user scope or admin scope).

```
   │    │    │    │        shafts (access wells)
 ──┴────┴────┴────┴──▶     channel, sloping toward the outlet
```

| State | Look | Means | Who sees it |
|---|---|---|---|
| `flowing` | primary-colored channel; a droplet travels the channel, one per active stream (max 3) | streams are in flight | everyone |
| `still` | primary-colored channel, no droplets, no motion | healthy and idle | everyone |
| `held` | amber channel segment at one shaft; droplets pause there | a hold: quota ≥ 95% (user), or an account/proxy in cooldown or pool headroom low (admin) | user / admin |
| `broken` | red gap in the channel, hollow outlet | quota exhausted (user), or an account disconnected / pool exhausted (admin) | user / admin |

Rules:

- The mark animates only in `flowing` and only on open surfaces (not the favicon). Under `prefers-reduced-motion` it freezes on a legible still frame for each state.
- Geometry: drawn in a `0…100` box as one SVG component (`<QanatMark state=… />`). Stroke width 6 at 24 px, scaled with the box. No fills except the outlet dot.
- Shafts are ink (`currentColor`); the water (channel, outlet, droplet) is the primary color (forest; sage in dark) unless a state paints it amber or red. On the deep brand panel (sign-in) the whole mark is drawn in the panel's foreground (`water={false}`).

---

## 3. Color

All colors are CSS variables in `web/src/app/globals.css`. **Components never use raw hex values or Tailwind palette colors** (`bg-zinc-100`, `text-amber-500`); they use semantic tokens only (`bg-muted`, `text-warning-text`). A lint rule enforces this (§10).

### 3.1 Base palette

| Token | Light | Dark | Role |
|---|---|---|---|
| `--background` | `#F6F4EF` (warm paper) | `#0C0A08` | page |
| `--foreground` | `#0C0A08` | `#E8E5DD` | primary text |
| `--card` / `--popover` | `#FCFBF8` | `#15130F` | raised surfaces |
| `--primary` | `#2A3723` (forest) | `#B9BBA8` (sage) | buttons, active nav, focus, healthy bars, charts |
| `--primary-foreground` | `#F6F4EF` | `#0C0A08` | text on primary |
| `--secondary` / `--muted` | `#ECEAE2` | `#1F1C17` | quiet fills, hover rows, bar track |
| `--muted-foreground` | `#5E5C52` | `#B9BBA8` | secondary text (≥ 4.5:1 on every surface) |
| `--accent` / `--accent-foreground` | `#DFE0D3` / `#2A3723` | `#2A3723` / `#E8E5DD` | selected row, hover |
| `--border` / `--input` | `#DCD9CF` / `#D2CFC4` | `#2E2B24` / `#36322A` | dividers, inputs |
| `--ring` | `#2A3723` | `#B9BBA8` | focus ring (2 px, 2 px offset) |
| `--sidebar` | `#E8E5DD` | `#12100D` | sidebar surface |
| `--sidebar-accent` / `-foreground` | `#D6D8C8` / `#2A3723` | `#2A3723` / `#E8E5DD` | active nav item |

### 3.1a Brand accents

| Token | Light | Dark | Role |
|---|---|---|---|
| `--brand-soft` / `--brand-text` | `#E3E5D8` / `#2A3723` | `#232C1D` / `#C5C9B0` | icon tiles beside page titles, "Default" and "Active" pills |
| `--brand-deep` / `-foreground` | `#2A3723` / `#E8E5DD` | `#1C2417` / `#E8E5DD` | the sign-in panel |
| `--sand` / `--sand-foreground` | `#DCBC98` / `#0C0A08` | same | avatar, girih pattern on the sign-in panel |
| `--sand-soft` / `--sand-text` | `#F3E6D6` / `#7A5530` | `#2A2118` / `#DCBC98` | Overview hero band, "Root" pill |

Rules: no gradients; the girih pattern appears only on the sign-in panel, at ≤ 16% opacity; sand never signals state.

### 3.2 Signal colors (reserved)

Signal colors have separate **fill** and **text** tokens, because the fill hues fail WCAG contrast as text. Fills meet the 3:1 non-text contrast rule against both the page and the quota-bar track; text tokens meet 4.5:1 on every surface. Tower's amber (`#E6A93C`) is kept in dark mode; in light mode it is deepened to `#B07206` so it holds 3:1 on the warm track.

| Meaning | Fill token (bars, icons, dots) | Light | Dark | Text token | Light | Dark |
|---|---|---|---|---|---|---|
| Hold / warning | `--warning` | `#B07206` | `#E6A93C` | `--warning-text` | `#7F5300` | `#F0C26B` |
| Danger / broken | `--destructive` | `#E5484D` | `#E5484D` | `--destructive-text` | `#B8282D` | `#FF8F92` |
| On (switches only) | `--success` | `#4B7A3F` | `#6F9A5E` | — | — | — |

- Amber and red appear **only** for the states in §5. Nothing else is tinted: info banners, tips, tags and charts below a warning line stay neutral.
- Green appears **only** on switches that are on. A healthy account is not green, it is quiet.

### 3.3 Model tier accents

Carried over from Tower, with one change for accessibility. In Tower the accent colors the tier's text label. Here, every accent fails 4.5:1 as text in one theme or the other (e.g. Fable gold is 2.4:1 on white), so the accent appears **only** as a 6 px dot before the tier label, which stays `muted-foreground`. It is also the series color when a chart is broken down by model. It never colors text, a mark, a row background or a badge fill.

| Tier | Token | Hex |
|---|---|---|
| Fable | `--model-fable` | `#C9A227` |
| Opus | `--model-opus` | `#B0343C` |
| Sonnet | `--model-sonnet` | `#3B6FB5` |
| Haiku | `--model-haiku` | `#E8842C` |
| GPT (Codex) | `--model-gpt` | `#5E6AD2` |
| Gemini (Antigravity) | `--model-gemini` | `#2F9E8F` |

Model names are resolved to a tier by prefix in one helper (`modelTier(name)`), never inline.

### 3.4 Charts

- Single series: primary (`--chart-1`).
- Breakdown by model: tier accents (§3.3).
- Any other breakdown (by key, token type): the primary ramp `--chart-1…5` (100/72/52/36/22% opacity).
- Quota charts draw the limit as a dashed `--muted-foreground` line. Bars above 80% of the limit switch to `--warning`, and above 95% to `--destructive`. This is the only place a chart turns amber or red.
- No gradients, no 3D, no pie charts. Use stacked bars or a ranked list instead.

---

## 4. Typography

**One family: the platform system UI stack**, which is SF Pro on macOS and iOS (Tower's face) and the native UI font elsewhere. [Vazirmatn](https://github.com/rastikerdar/vazirmatn) follows in the stack, so Persian user names and messages render well.

```css
--font-sans: ui-sans-serif, -apple-system, BlinkMacSystemFont, "SF Pro Text", "Segoe UI",
             Roboto, "Helvetica Neue", Vazirmatn, sans-serif;
--font-mono: ui-monospace, "SF Mono", SFMono-Regular, Menlo, Consolas, monospace;
```

Monospace is used **only** for things a person copies verbatim: API keys, model IDs in the mapping editor, the `settings.json` snippet, and request IDs. Labels and numbers are never monospace.

### 4.1 Scale

Dense and calm, based on Tower's 13/11 rhythm, with a larger step only for page titles and the hero number.

| Role | Size / line-height | Weight | Class |
|---|---|---|---|
| Hero number (remaining USD on overview) | 32 / 36 | 600 | `text-hero` |
| Page title | 20 / 28 | 600 | `text-title` |
| Section header | 13 / 18 | 600, `muted-foreground` | `text-section` |
| Row title / body | 13 / 20 | 400 | `text-body` |
| Secondary, meta | 11 / 16 | 400, `muted-foreground` | `text-meta` |
| Counters, money, tokens | inherits size | `tabular-nums` | `tabular-nums` |

Rules:

- Sentence case everywhere: titles, buttons, menu items, column headers. **No all-caps labels** and no eyebrow labels above headings.
- Numbers that update (usage, counters, countdowns) use `tabular-nums` so digits don't jitter.
- Money goes through `formatUsd` only: whole amounts drop the cents (`$10,000`, `$0`), other amounts show cents (`$14.20`), and amounts under $1 keep up to four decimals (`$0.0731`) because a single request costs that little. Never `toFixed` money.
- Line length for prose (help text, empty states) stays under 72 characters.

---

## 5. Status → attention semantics

Done right means you can tell states apart **without color**, by symbol shape and position. Icons come from `lucide-react`.

| State | Fill | Icon | Motion | Where |
|---|---|---|---|---|
| Quota < 80% | primary | — | none | quota bar |
| Quota 80–95% | `warning` | — | none | quota bar + 80% warning row |
| Quota ≥ 95% / exhausted | `destructive` | `octagon-x` when exhausted | one pulse on arrival | quota bar, mark `held` / `broken` |
| Account `healthy` | — | none (quiet) | none | accounts list |
| Account `cooldown` | `warning` | `hourglass` | none; countdown ticks | accounts list, Needs you |
| Account `disconnected` | `destructive` | `unplug` | one pulse on arrival, then sober | Needs you rank 1 |
| Proxy unhealthy | `warning` (failover available) / `destructive` (no healthy member) | `shield-alert` | none | proxies list, Needs you |
| Pool headroom low | `warning` | `gauge` | none | admin overview, Needs you |
| Provider network `healthy` | — | `circle-check` (muted) | none | Network page |
| Provider network `degraded` | `warning` | `triangle-alert` | none | Network page |
| Provider network `unstable` / `unreachable` / `offline` | `destructive` | `octagon-x` | none | Network page |
| Provider network `unavailable` (no account can serve) | `destructive` | `unplug` | none | Network page |
| Provider network `checking` / not connected | `muted-foreground` | `circle-dashed` | none | Network page |
| Request ok | — | none | none | recent requests |
| Request `aborted` | `muted-foreground` | `circle-slash` | none | recent requests |
| Request error | `destructive-text` | `circle-x` | none | recent requests |
| Stream in flight | primary | the mark (`flowing`) | droplet travels | sidebar header |

**Needs you** (admin overview) is a ranked queue, highest first:

1. account disconnected
2. proxy group with no healthy member
3. pool headroom low
4. user at 95%+ of a quota

The order is fixed: admins can hide sections, never reorder what outranks what. When the queue is empty the section is hidden, not shown as an empty card.

---

## 6. Motion

CSS tokens in `globals.css`, adapted from Tower's spring tokens.

| Token | Value | Used for |
|---|---|---|
| `--ease-settle` | `450ms cubic-bezier(0.2, 0.8, 0.2, 1)` | any state swap (bar width, row reorder) |
| `--ease-arrive` | `550ms cubic-bezier(0.34, 1.3, 0.64, 1)` | entrances: dialog, sheet, new row (slight overshoot) |
| `--ease-sober` | `250ms ease-out` | failures: no bounce, ever |
| `--shimmer-period` | `1.8s` | an in-flight row in recent requests |
| `--stagger` | `40ms` per row, max 8 rows | multi-row inserts |
| `--glow-hold` | `900ms` | "Saved" confirmation glow on a row |

Restraint rules, enforced in review:

- At most one glowing or pulsing element at a time; the newest wins.
- No hover lift, no hover shadow growth, no scroll-triggered reveals.
- Counters change by setting the value (no count-up animation), except the hero number on page load.
- **`prefers-reduced-motion: reduce`** stops all loops, turns every transition into a 200 ms opacity fade, and freezes the mark on its still frame.

---

## 7. Layout

### 7.1 Shell

shadcn `Sidebar` (collapsible to icons with `⌘B`), a slim header, and a content column.

```
┌────────────┬─────────────────────────────────────────────────────┐
│ [mark] Qanat│  Usage                              ◐  [avatar ▾]  │ ← header 48px: title, theme, user menu
│            ├─────────────────────────────────────────────────────┤
│ Overview   │   content column, max-width 1120px, left-aligned    │
│ Requests   │   24px gutters (16px on mobile)                     │
│ Keys       │                                                     │
│ Setup      │                                                     │
│ ────────── │                                                     │
│ Admin      │   (admin group visible to role=admin only)          │
│  Needs you │                                                     │
│  Users     │                                                     │
│  Plans     │                                                     │
│  Accounts  │                                                     │
│  Proxies   │                                                     │
│  Models    │                                                     │
│  Audit log │                                                     │
└────────────┴─────────────────────────────────────────────────────┘
  240px (56px collapsed)
```

- Everything is left-aligned. Numbers in tables are right-aligned.
- On mobile (< 768 px) the sidebar becomes a `Sheet`; tables become stacked rows.

### 7.2 Surfaces: rows, not cards

The default container is a **flat list with dividers**, like Tower's popover and the macOS Wi-Fi menu. Cards are used only where the content is genuinely one object: the quota block on Overview and a key's one-time reveal dialog.

- Row height 40 px (comfortable) or 32 px (compact; a density toggle in the user menu).
- Dividers sit only between visible rows, so hiding a section never leaves an orphaned rule.
- Radius: `--radius: 10px` for cards, dialogs and inputs; `6px` for buttons, badges and rows; `9999px` for the status dot only.
- Shadows: none on rows and cards. Only popovers, dropdowns and dialogs get one shadow (`--shadow-overlay`).

### 7.3 User overview: "how much is left, and when does it refill?"

```
Overview
┌───────────────────────────────────────────────────────────────┐
│ $3.42 left today                         resets in 6h 12m     │ ← hero: tightest window wins
│                                                               │
│ 5-hour     ███████████░░░░░░░░░   $5.80 of $10    in 2h 03m   │
│ Daily      ██████████████░░░░░░   $6.58 of $10    in 6h 12m   │
│ Weekly     ██████░░░░░░░░░░░░░░   $14.2 of $50    Sat 00:00   │
│ Monthly    ███░░░░░░░░░░░░░░░░░   $31.0 of $150   Dec 1       │
│ Opus/day   ███████████████████░   $4.80 of $5   ▲ in 6h 12m   │ ← amber: ≥ 80%
└───────────────────────────────────────────────────────────────┘

Last 7 days                                       USD ▾  Tokens
▁▂▅▃▆▇▄  (bar chart, by model, limit as dashed line)

Recent requests                                           View all
claude-sonnet-4-6   Sonnet   212 ms   18.2k tok   $0.07   2m ago
claude-opus-4-7     Opus     340 ms   41.0k tok   $0.62   5m ago   ⊘ aborted
```

Overview is the user's one home page: the quota block, then their usage (the 9router-style stats, cards, chart and breakdown, with the period selector in the page header), including the recent requests panel beside the topology. There is no separate Usage page; `/usage` redirects to `/`, keeping its query (period, filters). Admins get the same shape on Usage & quotas.

The hero sentence is computed from the window with the least remaining headroom. When every window is fine it reads plainly ("$3.42 left today"); it never celebrates.

---

## 8. Components (shadcn/ui)

Install components with the shadcn CLI only (`npx shadcn@latest add <name>`), so they live in `src/components/ui/` as editable source. **Don't edit files in `ui/` for one-off needs**; wrap them in `src/components/` instead. When a `ui/` file is changed on purpose (to apply this design system), note it in its header comment.

| Need | Component | Notes |
|---|---|---|
| App shell | `sidebar`, `breadcrumb`, `sheet` | sidebar variant `inset` off; flat |
| Actions | `button` | variants used: `default` (forest / sage), `outline`, `ghost`, `destructive` |
| Lists, tables | `table` + TanStack Table (data-table pattern) | sticky header, right-aligned numbers, row = 40 px |
| Quota bars | `progress` | wrapped as `<QuotaBar>` applying §5 thresholds |
| Charts | `chart` (Recharts) | colors from tokens only (§3.4) |
| Forms | `form` (react-hook-form + zod), `input`, `select`, `switch`, `textarea` | validation messages under the field, sentence case |
| Overlays | `dialog`, `alert-dialog`, `popover`, `dropdown-menu`, `tooltip` | `alert-dialog` for anything destructive or irreversible |
| Feedback | `sonner` (toasts), `skeleton`, `alert` | one toast at a time, bottom-right; names the action ("Key revoked") |
| Navigation | `tabs`, `command` (`⌘K` palette) | command palette lists pages and admin actions |
| Status | `Tag` (qanat) | `brand` (sage tint), `sand` or `neutral`; signal states use the dot + icon from §5 |

Qanat-specific components in `src/components/qanat/`: `QanatMark`, `QuotaBar`, `PageHeader` (with a sage `IconTile`), `Tag`, `CopyField` (monospace, copy button), `EmptyState`; to come: `StatusDot`, `ModelName`, `Money`, `Tokens`, `Countdown`, `NeedsYouQueue`.

---

## 9. Theming (light / dark)

- `next-themes` with `attribute="class"`, `defaultTheme="system"`, `enableSystem`, and `disableTransitionOnChange` (no color fade on switch).
- The theme control is a three-way choice (Light, Dark, System) in the user menu, plus the shortcut `⌘⇧L`.
- `<html suppressHydrationWarning>` prevents a flash of the wrong theme.
- Dark mode is **not** an inversion: it uses its own tuned neutrals (§3.1) and lighter signal *text* tokens (§3.2). Every component is reviewed in both themes, and Playwright captures both (§11).
- Images and the mark use `currentColor`. There are no theme-specific image assets.

---

## 10. Engineering standards for `web/`

### 10.1 Stack

| Area | Choice |
|---|---|
| Framework | Next.js (App Router), React, TypeScript `strict` |
| Styling | Tailwind CSS v4, tokens in `globals.css` via `@theme inline` |
| Components | shadcn/ui (`components.json`, `cssVariables: true`, `baseColor: neutral`), `lucide-react` icons |
| Data | TanStack Query; typed client generated from the control plane's OpenAPI spec (never hand-written fetch calls in components) |
| Forms | react-hook-form + zod; schemas shared with the API client types |
| Tables | TanStack Table |
| Charts | shadcn `chart` (Recharts) |
| Auth | OIDC Authorization Code + PKCE against Keycloak, handled server-side; the browser never sees a Keycloak token |
| Tests | Vitest + Testing Library (unit, component), MSW (API mocks), Playwright + axe (e2e, both themes) |

### 10.2 Folder structure

```
web/
  src/
    app/                     routes (App Router): (user)/…, admin/…, sign-in
      globals.css            design tokens (§3, §4, §6) — the only place hex values live
    components/
      ui/                    shadcn source (CLI-managed)
      qanat/                 QanatMark, QuotaBar, StatusDot, ModelName, …
    lib/
      api/                   generated client + query hooks
      format.ts              money, tokens, durations, countdowns, quota levels
      model-tier.ts          model name → tier
    test/                    Vitest setup, MSW handlers
  tests/e2e/                 Playwright specs
```

### 10.3 Rules (enforced by lint or review)

1. **Tokens only.** No hex, `rgb()` or Tailwind palette colors outside `globals.css`. ESLint rule: `no-restricted-syntax` on `/#[0-9a-f]{3,8}/i` in `.tsx`, and a Tailwind class lint banning palette colors.
2. **Formatting in one place.** Money, tokens and time go through `lib/format.ts` (`Intl.NumberFormat`, `Intl.RelativeTimeFormat`). Quota color thresholds live in `quotaLevel()` there, mirroring the backend's 80/95 % rules (PRD §10.5).
3. **Accessibility AA.** Text contrast ≥ 4.5:1 and non-text ≥ 3:1 (verified for every token pair in §3 by `src/lib/contrast.test.ts`, which reads `globals.css`), visible focus ring on every interactive element, full keyboard operation, `aria-live="polite"` on quota and status regions, status never conveyed by color alone (§5).
4. **RTL-ready.** Use logical utilities (`ms-*`, `me-*`, `ps-*`, `pe-*`, `start-*`, `end-*`), never `ml-*`/`mr-*`/`left-*`/`right-*`, so a Persian locale can be added by setting `dir="rtl"`.
5. **No prompt content.** The dashboard never renders prompt or response text (PRD §15.2). Recent requests show metadata only.
6. **Copy.** Sentence case; buttons say what happens ("Revoke key", "Reset quota") and the confirming toast reuses the verb ("Key revoked"). Errors state what happened and how to fix it ("Proxy test failed: TLS handshake to api.anthropic.com timed out after 5 s. Check the proxy address."). Empty states invite the next action ("No keys yet. Create a key to connect Claude Code.").
7. **Performance.** Dashboard pages poll at most every 5 s and only while visible (`refetchOnWindowFocus`, `refetchInterval` paused when hidden). The dashboard never talks to the data plane, only to the Admin API.

---

## 11. Testing the UI

Tests live next to the code they cover (`*.test.ts(x)`), plus `tests/e2e/`. See [docs/testing.md](docs/testing.md) for the whole strategy.

- **Unit (Vitest):** `lib/format.ts`, `quotaLevel()`, `modelTier()`, countdown math across timezones (`Asia/Tehran` default reset).
- **Component (Vitest + Testing Library + MSW):** `QuotaBar` thresholds, `NeedsYouQueue` ordering, the key reveal flow (key shown exactly once), forbidden-model and quota errors rendered as readable messages.
- **End-to-end (Playwright):** every page is run in **two projects, `light` and `dark`** (`colorScheme`). Each run:
  - signs in through the test OIDC issuer (`internal/testkit/oidctest`), as `user` and as `admin`;
  - runs `@axe-core/playwright` with WCAG 2.1 AA rules (zero violations allowed);
  - takes a full-page screenshot per theme for visual review (`toHaveScreenshot`, 0.1% threshold);
  - checks that `prefers-reduced-motion` stops every animation.

---

## 12. Review checklist

Before merging any UI change:

- [ ] Looks right in light **and** dark (screenshots attached).
- [ ] Uses tokens only; no new colors.
- [ ] Amber/red appear only for a §5 state; nothing else is tinted.
- [ ] At most one moving or pulsing element on the page.
- [ ] Works with keyboard only; focus ring visible.
- [ ] Reduced motion respected.
- [ ] Copy is sentence case and names the action.
- [ ] Logical (RTL-safe) spacing utilities.
- [ ] Unit/component tests updated; Playwright snapshots updated intentionally.
