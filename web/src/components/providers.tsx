"use client";

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ThemeProvider } from "next-themes";
import { useState, type ReactNode } from "react";

import { Toaster } from "@/components/ui/sonner";

// next-themes renders an inline <script> that sets the theme class before
// paint. The server-rendered copy runs; on the client React 19.2 warns about
// any executable script it renders, so the client copy is typed as inert data.
const themeScriptProps =
  typeof window === "undefined" ? undefined : ({ type: "application/json", suppressHydrationWarning: true } as const);
import { TooltipProvider } from "@/components/ui/tooltip";

export function Providers({ children }: { children: ReactNode }) {
  const [queryClient] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: { staleTime: 5_000, retry: 1, refetchOnWindowFocus: true },
        },
      }),
  );
  return (
    // design.md §9: class strategy, system default, no color fade on switch.
    <ThemeProvider attribute="class" defaultTheme="system" enableSystem disableTransitionOnChange scriptProps={themeScriptProps}>
      <QueryClientProvider client={queryClient}>
        <TooltipProvider>
          {children}
          {/* design.md §8: one toast at a time, bottom-right. */}
          <Toaster position="bottom-right" visibleToasts={1} />
        </TooltipProvider>
      </QueryClientProvider>
    </ThemeProvider>
  );
}
