"use client";

import { PlusCircle } from "lucide-react";
import type { ReactNode } from "react";

import { Tag } from "@/components/qanat/page";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Separator } from "@/components/ui/separator";

export type FacetOption = { value: string; label: ReactNode; count: number };

// FacetedFilter is the data-table toolbar filter (shadcn data-table pattern,
// design.md "Lists, tables"): a dashed button that opens a checklist with a
// count per value. Picked values show inside the button, so the toolbar stays
// one row however many filters are on. Multi-select; the menu stays open
// while ticking.
export function FacetedFilter({
  title,
  options,
  selected,
  onChange,
}: {
  title: string;
  options: FacetOption[];
  selected: Set<string>;
  onChange: (next: Set<string>) => void;
}) {
  const toggle = (value: string) => {
    const next = new Set(selected);
    if (next.has(value)) next.delete(value);
    else next.add(value);
    onChange(next);
  };
  const picked = options.filter((o) => selected.has(o.value));

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="h-8 border-dashed">
          <PlusCircle className="size-4" />
          {title}
          {picked.length > 0 && (
            <>
              <Separator orientation="vertical" className="mx-1 h-4" />
              {picked.length > 2 ? (
                <Tag tone="brand">{picked.length} selected</Tag>
              ) : (
                picked.map((o) => (
                  <Tag key={o.value} tone="brand">
                    {o.label}
                  </Tag>
                ))
              )}
            </>
          )}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-60">
        <DropdownMenuLabel className="text-meta text-muted-foreground">Filter by {title.toLowerCase()}</DropdownMenuLabel>
        {options.map((o) => (
          <DropdownMenuCheckboxItem
            key={o.value}
            checked={selected.has(o.value)}
            onCheckedChange={() => toggle(o.value)}
            onSelect={(e) => e.preventDefault()}
          >
            <span className="flex-1">{o.label}</span>
            <span className="ms-4 font-mono text-meta tabular-nums text-muted-foreground">{o.count}</span>
          </DropdownMenuCheckboxItem>
        ))}
        {picked.length > 0 && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem className="justify-center" onSelect={() => onChange(new Set())}>
              Clear {title.toLowerCase()} filter
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
