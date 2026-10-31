"use client";

import {
  Activity,
  ChartColumnStacked,
  KeyRound,
  LayoutGrid,
  LogOut,
  Network,
  Radar,
  ScrollText,
  Server,
  Shapes,
  SquareTerminal,
  Users,
  Wallet,
} from "lucide-react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useTheme } from "next-themes";
import { useEffect, type ReactNode } from "react";

import { QanatMark } from "@/components/qanat/qanat-mark";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar";
import { fetchClient, type Me } from "@/lib/api/client";

type NavItem = { href: string; label: string; icon: typeof LayoutGrid };

const userNav: NavItem[] = [
  { href: "/", label: "Overview", icon: LayoutGrid },
  { href: "/requests", label: "Requests", icon: Activity },
  { href: "/keys", label: "Keys", icon: KeyRound },
  { href: "/setup", label: "Setup", icon: SquareTerminal },
];

// Admin sections, grouped by what they manage: who gets access and how much,
// where requests go upstream, and how the gateway is behaving.
const adminNav: { label: string; items: NavItem[] }[] = [
  {
    label: "People",
    items: [
      { href: "/admin/users", label: "Users", icon: Users },
      { href: "/admin/plans", label: "Plans", icon: Wallet },
      { href: "/admin/usage", label: "Usage & quotas", icon: ChartColumnStacked },
    ],
  },
  {
    label: "Providers",
    items: [
      { href: "/admin/accounts", label: "Accounts", icon: Server },
      { href: "/admin/models", label: "Models", icon: Shapes },
      { href: "/admin/proxies", label: "Proxies", icon: Network },
    ],
  },
  {
    label: "Operations",
    items: [
      { href: "/admin/network", label: "Network", icon: Radar },
      { href: "/admin/audit", label: "Audit log", icon: ScrollText },
    ],
  },
];

function isActive(pathname: string, href: string) {
  return href === "/" ? pathname === href : pathname.startsWith(href);
}

function NavGroup({ label, items }: { label?: string; items: NavItem[] }) {
  const pathname = usePathname();
  return (
    <SidebarGroup>
      {label && <SidebarGroupLabel>{label}</SidebarGroupLabel>}
      <SidebarMenu>
        {items.map((item) => (
          <SidebarMenuItem key={item.href}>
            <SidebarMenuButton asChild isActive={isActive(pathname, item.href)} tooltip={item.label}>
              <Link href={item.href} aria-current={isActive(pathname, item.href) ? "page" : undefined}>
                <item.icon />
                <span>{item.label}</span>
              </Link>
            </SidebarMenuButton>
          </SidebarMenuItem>
        ))}
      </SidebarMenu>
    </SidebarGroup>
  );
}

const themes = ["light", "dark", "system"] as const;

function UserMenu({ me }: { me: Me }) {
  const { theme, setTheme } = useTheme();
  const router = useRouter();

  // ⌘⇧L cycles Light → Dark → System (design.md §9).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.key.toLowerCase() === "l") {
        e.preventDefault();
        const i = themes.indexOf((theme as (typeof themes)[number]) ?? "system");
        setTheme(themes[(i + 1) % themes.length]!);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [theme, setTheme]);

  const signOut = async () => {
    await fetchClient.POST("/api/auth/logout");
    router.replace("/sign-in");
    router.refresh();
  };

  const initials = (me.name || me.username || me.email).slice(0, 1).toUpperCase();
  const displayName = me.name || me.username || me.email;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="sm" className="gap-2">
          <span aria-hidden className="grid size-6 place-items-center rounded-full bg-sand text-meta font-semibold text-sand-foreground">
            {initials}
          </span>
          <span className="hidden sm:inline">{displayName}</span>
          <span className="sr-only">Account menu</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuLabel className="font-normal">
          <div className="font-semibold">{me.name || me.username}</div>
          <div className="text-meta text-muted-foreground">{me.email}</div>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuLabel className="text-meta font-normal text-muted-foreground">Theme</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={theme ?? "system"} onValueChange={setTheme}>
          <DropdownMenuRadioItem value="light">Light</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="dark">Dark</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="system">System</DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={signOut}>
          <LogOut />
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function AppShell({ me, children }: { me: Me; children: ReactNode }) {
  return (
    <SidebarProvider>
      <Sidebar collapsible="icon">
        <SidebarHeader>
          <Link href="/" className="flex h-8 items-center gap-2 px-1 font-semibold">
            <QanatMark title="Qanat" />
            <span className="group-data-[collapsible=icon]:hidden">Qanat</span>
          </Link>
        </SidebarHeader>
        <SidebarContent>
          <NavGroup items={userNav} />
          {me.role === "admin" && adminNav.map((g) => <NavGroup key={g.label} label={g.label} items={g.items} />)}
        </SidebarContent>
      </Sidebar>
      <SidebarInset className="min-w-0">
        <nav aria-label="Main" className="flex h-12 items-center justify-between gap-2 border-b px-4">
          <SidebarTrigger aria-label="Toggle sidebar" />
          <UserMenu me={me} />
        </nav>
        <main className="w-full min-w-0 max-w-[1120px] px-4 py-6 md:px-6">{children}</main>
      </SidebarInset>
    </SidebarProvider>
  );
}
