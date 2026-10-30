"use client";

import {
  Activity,
  BarChart3,
  Gauge,
  KeyRound,
  LayoutGrid,
  LogOut,
  Network,
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
  { href: "/usage", label: "Usage", icon: BarChart3 },
  { href: "/requests", label: "Requests", icon: Activity },
  { href: "/keys", label: "Keys", icon: KeyRound },
  { href: "/setup", label: "Setup", icon: SquareTerminal },
];

const adminNav: NavItem[] = [
  { href: "/admin", label: "Needs you", icon: Gauge },
  { href: "/admin/users", label: "Users", icon: Users },
  { href: "/admin/plans", label: "Plans", icon: Wallet },
  { href: "/admin/accounts", label: "Accounts", icon: Server },
  { href: "/admin/proxies", label: "Proxies", icon: Network },
  { href: "/admin/models", label: "Models", icon: Shapes },
  { href: "/admin/audit", label: "Audit log", icon: ScrollText },
];

function isActive(pathname: string, href: string) {
  return href === "/" || href === "/admin" ? pathname === href : pathname.startsWith(href);
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
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="sm" aria-label="Account menu" className="gap-2">
          <span aria-hidden className="grid size-6 place-items-center rounded-full bg-primary text-meta font-semibold text-primary-foreground">
            {initials}
          </span>
          <span className="hidden sm:inline">{me.name || me.username || me.email}</span>
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
          <nav aria-label="Main">
            <NavGroup items={userNav} />
            {me.role === "admin" && <NavGroup label="Admin" items={adminNav} />}
          </nav>
        </SidebarContent>
      </Sidebar>
      <SidebarInset className="min-w-0">
        <header className="flex h-12 items-center justify-between gap-2 border-b px-4">
          <SidebarTrigger aria-label="Toggle sidebar" />
          <UserMenu me={me} />
        </header>
        <main className="w-full min-w-0 max-w-[1120px] px-4 py-6 md:px-6">{children}</main>
      </SidebarInset>
    </SidebarProvider>
  );
}
