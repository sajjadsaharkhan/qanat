/*
 * The Accounts page's controls on Animate UI's Radix primitives
 * (@animate-ui/react/primitives/radix-ui), styled like the shadcn/ui ones:
 * the tabs' and the view toggle's selection slides, the menu grows from its
 * trigger with a gliding highlight, a checkbox draws its check and a
 * disclosure unfolds. The shared components/ui files stay as they are.
 */
"use client";

import {
  Checkbox as CheckboxPrimitive,
  Collapsible as CollapsiblePrimitive,
  DropdownMenu as MenuPrimitive,
  Tabs as TabsPrimitive,
  ToggleGroup as ToggleGroupPrimitive,
} from "@animate-ui/react/primitives/radix-ui";
import { CheckIcon, MinusIcon } from "lucide-react";
import type { ComponentProps } from "react";

import { cn } from "@/lib/utils";

// --- tabs -------------------------------------------------------------------

export const Tabs = TabsPrimitive.Root;

export function TabsList({ className, ...props }: ComponentProps<typeof TabsPrimitive.List>) {
  return (
    <TabsPrimitive.List
      className={cn("inline-flex h-9 max-w-full items-center gap-0.5 overflow-x-auto rounded-lg bg-muted p-[3px] text-muted-foreground", className)}
      {...props}
    />
  );
}

export function TabsTrigger({ className, ...props }: ComponentProps<typeof TabsPrimitive.Trigger>) {
  return (
    <TabsPrimitive.Trigger
      className={cn(
        "inline-flex h-full shrink-0 items-center gap-1.5 rounded-md border border-transparent px-2.5 text-sm font-medium whitespace-nowrap transition-colors outline-none",
        "hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50",
        "data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm dark:data-[state=active]:border-input dark:data-[state=active]:bg-input/30",
        className,
      )}
      {...props}
    />
  );
}

// --- toggle group -----------------------------------------------------------

export function ToggleGroup({ className, ...props }: ComponentProps<typeof ToggleGroupPrimitive.Root>) {
  return <ToggleGroupPrimitive.Root className={cn("inline-flex items-center rounded-md border p-0.5", className)} {...props} />;
}

export function ToggleGroupItem({ className, ...props }: ComponentProps<typeof ToggleGroupPrimitive.Item>) {
  return (
    <ToggleGroupPrimitive.Item
      className={cn(
        "inline-flex size-7 items-center justify-center rounded-sm text-muted-foreground transition-colors outline-none",
        "hover:bg-muted hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50",
        "data-[state=on]:bg-muted data-[state=on]:text-foreground [&_svg]:size-4",
        className,
      )}
      {...props}
    />
  );
}

// --- checkbox ---------------------------------------------------------------

export function Checkbox({
  className,
  checked,
  ...props
}: Omit<ComponentProps<typeof CheckboxPrimitive.Root>, "checked"> & { checked: boolean | "indeterminate" }) {
  return (
    <CheckboxPrimitive.Root
      checked={checked}
      className={cn(
        "peer grid size-4 shrink-0 place-content-center rounded-[4px] border border-input shadow-xs transition-colors outline-none",
        "focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50",
        "data-[state=checked]:border-primary data-[state=checked]:bg-primary data-[state=checked]:text-primary-foreground",
        "data-[state=indeterminate]:border-primary data-[state=indeterminate]:bg-primary data-[state=indeterminate]:text-primary-foreground dark:bg-input/30",
        className,
      )}
      {...props}
    >
      <CheckboxPrimitive.Indicator className="grid place-content-center text-current">
        {checked === "indeterminate" ? <MinusIcon className="size-3.5" /> : <CheckIcon className="size-3.5" />}
      </CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  );
}

// --- disclosure -------------------------------------------------------------

export const Collapsible = CollapsiblePrimitive.Root;
export const CollapsibleTrigger = CollapsiblePrimitive.Trigger;
export const CollapsibleContent = CollapsiblePrimitive.Content;

// --- menu -------------------------------------------------------------------

export const Menu = MenuPrimitive.Root;
export const MenuTrigger = MenuPrimitive.Trigger;
export const MenuRadioGroup = MenuPrimitive.RadioGroup;

export function MenuContent({ className, sideOffset = 4, ...props }: ComponentProps<typeof MenuPrimitive.Content>) {
  return (
    <MenuPrimitive.Portal>
      <MenuPrimitive.Content
        sideOffset={sideOffset}
        className={cn(
          "z-50 max-h-(--radix-dropdown-menu-content-available-height) min-w-[10rem] origin-(--radix-dropdown-menu-content-transform-origin) overflow-x-hidden overflow-y-auto rounded-md border bg-popover p-1 text-popover-foreground shadow-md",
          className,
        )}
        {...props}
      />
    </MenuPrimitive.Portal>
  );
}

const itemClass =
  "relative flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-hidden select-none focus:bg-accent focus:text-accent-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 [&_svg:not([class*='text-'])]:text-muted-foreground";

export function MenuItem({
  className,
  variant = "default",
  ...props
}: ComponentProps<typeof MenuPrimitive.Item> & { variant?: "default" | "destructive" }) {
  return (
    <MenuPrimitive.Item
      data-variant={variant}
      className={cn(
        itemClass,
        "data-[variant=destructive]:text-destructive-text data-[variant=destructive]:focus:bg-destructive/10 data-[variant=destructive]:*:[svg]:text-destructive-text!",
        className,
      )}
      {...props}
    />
  );
}

export function MenuRadioItem({ className, children, ...props }: ComponentProps<typeof MenuPrimitive.RadioItem>) {
  return (
    <MenuPrimitive.RadioItem className={cn(itemClass, "ps-8", className)} {...props}>
      <span className="pointer-events-none absolute start-2 flex size-3.5 items-center justify-center">
        <MenuPrimitive.ItemIndicator>
          <CheckIcon className="size-4" />
        </MenuPrimitive.ItemIndicator>
      </span>
      {children}
    </MenuPrimitive.RadioItem>
  );
}

export function MenuLabel({ className, ...props }: ComponentProps<typeof MenuPrimitive.Label>) {
  return <MenuPrimitive.Label className={cn("px-2 py-1.5 text-xs font-medium text-muted-foreground", className)} {...props} />;
}

export function MenuSeparator({ className, ...props }: ComponentProps<typeof MenuPrimitive.Separator>) {
  return <MenuPrimitive.Separator className={cn("-mx-1 my-1 h-px bg-border", className)} {...props} />;
}
