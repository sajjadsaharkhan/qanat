"use client";

import { Check, Copy } from "lucide-react";
import { useState, type ReactNode } from "react";

import { QanatMark } from "@/components/qanat/qanat-mark";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/** Page title row: h1 plus optional actions (design.md §4.1, §7). */
export function PageHeader({ title, description, actions }: { title: string; description?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-6 flex flex-wrap items-start justify-between gap-3">
      <div className="min-w-0">
        <h1 className="text-title font-semibold">{title}</h1>
        {description && <p className="mt-1 max-w-[72ch] text-muted-foreground">{description}</p>}
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
      <QanatMark className="size-8 text-muted-foreground" title="" />
      <div>
        <p className="font-semibold">{title}</p>
        {children && <div className="mt-1 max-w-[72ch] text-muted-foreground">{children}</div>}
      </div>
      {action}
    </div>
  );
}

/** Monospace value with a copy button, for things copied verbatim (design.md §4). */
export function CopyField({ value, label, multiline, className }: { value: string; label: string; multiline?: boolean; className?: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    await navigator.clipboard.writeText(value);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <div className={cn("relative rounded-lg border bg-muted", className)}>
      <pre
        aria-label={label}
        tabIndex={0}
        className={cn("overflow-x-auto py-3 ps-3 pe-12 font-mono text-body", multiline ? "whitespace-pre" : "whitespace-nowrap")}
      >
        {value}
      </pre>
      <Button variant="ghost" size="icon" className="absolute end-1.5 top-1.5" onClick={copy} aria-label={copied ? "Copied" : `Copy ${label}`}>
        {copied ? <Check /> : <Copy />}
      </Button>
    </div>
  );
}
