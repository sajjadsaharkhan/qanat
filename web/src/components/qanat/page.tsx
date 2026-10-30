import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

import { QanatMark } from "@/components/qanat/qanat-mark";
import { cn } from "@/lib/utils";

/** A turquoise-tinted tile holding a page's icon (design.md §3.2). */
export function IconTile({ icon: Icon, className }: { icon: LucideIcon; className?: string }) {
  return (
    <span aria-hidden className={cn("grid size-10 shrink-0 place-items-center rounded-lg bg-brand-soft text-brand-text", className)}>
      <Icon className="size-5" />
    </span>
  );
}

/** Page title row: icon tile, h1 and optional actions (design.md §4.1, §7). */
export function PageHeader({
  title,
  description,
  actions,
  icon,
}: {
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
  icon?: LucideIcon;
}) {
  return (
    <div className="mb-6 flex flex-wrap items-start justify-between gap-3">
      <div className="flex min-w-0 items-start gap-3">
        {icon && <IconTile icon={icon} />}
        <div className="min-w-0">
          <h1 className="text-title font-semibold">{title}</h1>
          {description && <p className="mt-0.5 max-w-[72ch] text-muted-foreground">{description}</p>}
        </div>
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  );
}

/** Section header: 13/18 semibold muted, sentence case, no eyebrows. */
export function SectionHeader({ children, actions }: { children: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-2 mt-8 flex items-center justify-between first:mt-0">
      <h2 className="text-section font-semibold text-muted-foreground">{children}</h2>
      {actions}
    </div>
  );
}

/** Empty states invite the next action (design.md §10.3 rule 6). */
export function EmptyState({ title, children, action }: { title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-start gap-3 rounded-lg border border-dashed px-6 py-10">
      <span className="grid size-12 place-items-center rounded-full bg-brand-soft">
        <QanatMark className="size-7 text-muted-foreground" title="" />
      </span>
      <div>
        <p className="font-semibold">{title}</p>
        {children && <div className="mt-1 max-w-[72ch] text-muted-foreground">{children}</div>}
      </div>
      {action}
    </div>
  );
}

/** A pill in brand turquoise (for "Default", "Root", "Active") or neutral. */
export function Tag({ children, tone = "neutral" }: { children: ReactNode; tone?: "brand" | "neutral" }) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full px-2 py-0.5 text-meta font-medium whitespace-nowrap",
        tone === "brand" ? "bg-brand-soft text-brand-text" : "bg-muted text-muted-foreground",
      )}
    >
      {children}
    </span>
  );
}
