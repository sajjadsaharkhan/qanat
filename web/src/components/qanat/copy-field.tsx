"use client";

import { Check, Copy } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

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
