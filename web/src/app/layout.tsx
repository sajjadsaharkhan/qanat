import type { Metadata } from "next";
import type { ReactNode } from "react";

import { Providers } from "@/components/providers";

import "./globals.css";

export const metadata: Metadata = {
  title: { default: "Qanat", template: "%s · Qanat" },
  description: "Qanat gateway dashboard",
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    // suppressHydrationWarning: next-themes sets the class before hydration (design.md §9).
    <html lang="en" suppressHydrationWarning>
      <body>
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
