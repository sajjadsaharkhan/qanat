"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import { Plus, X } from "lucide-react";
import { useEffect, useMemo } from "react";
import { Controller, useFieldArray, useForm } from "react-hook-form";
import { z } from "zod";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import type { Plan, PlanBody, WindowKind } from "@/lib/api/client";
import { weekdays, windowLabel } from "@/lib/quota-labels";

const windows = ["rolling_5h", "daily", "weekly", "monthly"] as const satisfies readonly WindowKind[];

const count = (what: string) => z.number({ error: `${what} must be a whole number.` }).int(`${what} must be a whole number.`).min(0, `${what} can't be negative.`);

const schema = z
  .object({
    name: z.string().trim().min(1, "Give the Plan a name.").max(64, "Keep the name under 64 characters."),
    timezone: z.string().trim().min(1, "Pick a time zone."),
    daily_anchor: z.string().regex(/^([01]\d|2[0-3]):[0-5]\d$/, "Use 24-hour time, like 00:00."),
    weekly_anchor: z.number().int().min(0).max(6),
    monthly_anchor: z.number({ error: "Pick a day from 1 to 28." }).int().min(1, "Pick a day from 1 to 28.").max(28, "Pick a day from 1 to 28."),
    quotas: z.array(
      z.object({
        window: z.enum(windows),
        model_scope: z.string().trim(),
        limit_usd: z.number({ error: "Enter a dollar amount." }).min(0, "Limits can't be negative."),
      }),
    ),
    rpm: count("Requests per minute"),
    max_concurrent: count("Concurrent streams"),
    models: z.string(),
  })
  .refine((v) => new Set(v.quotas.map((q) => `${q.window}/${q.model_scope}`)).size === v.quotas.length, {
    path: ["quotas"],
    message: "Each window can appear once per model.",
  });

type FormValues = z.infer<typeof schema>;

function toForm(p?: Plan): FormValues {
  return {
    name: p?.name ?? "",
    timezone: p?.timezone ?? "Asia/Tehran",
    daily_anchor: p?.daily_anchor ?? "00:00",
    weekly_anchor: p?.weekly_anchor ?? 6,
    monthly_anchor: p?.monthly_anchor ?? 1,
    quotas: p ? p.quotas.map((q) => ({ window: q.window, model_scope: q.model_scope ?? "", limit_usd: q.limit_usd })) : [{ window: "daily", model_scope: "", limit_usd: 10 }],
    rpm: p?.limits.rpm ?? 0,
    max_concurrent: p?.limits.max_concurrent ?? 0,
    models: p?.models.join(", ") ?? "",
  };
}

function toBody(v: FormValues): PlanBody {
  return {
    name: v.name,
    timezone: v.timezone,
    daily_anchor: v.daily_anchor,
    weekly_anchor: v.weekly_anchor,
    monthly_anchor: v.monthly_anchor,
    quotas: v.quotas.map((q) => ({ window: q.window, model_scope: q.model_scope || null, limit_usd: q.limit_usd })),
    limits: { rpm: v.rpm, max_concurrent: v.max_concurrent },
    models: v.models.split(",").map((m) => m.trim()).filter(Boolean),
  };
}

function FieldError({ message }: { message?: string }) {
  return message ? <p className="text-meta text-destructive-text">{message}</p> : null;
}

export function PlanEditor({
  open,
  plan,
  onOpenChange,
  onSave,
  saving,
  serverError,
}: {
  open: boolean;
  plan?: Plan;
  onOpenChange: (o: boolean) => void;
  onSave: (body: PlanBody) => void;
  saving: boolean;
  serverError: string | null;
}) {
  const form = useForm<FormValues>({ resolver: zodResolver(schema), defaultValues: toForm(plan) });
  const quotas = useFieldArray({ control: form.control, name: "quotas" });
  // The sheet renders only after a click, so this never runs during SSR.
  const zones = useMemo(() => (open ? Intl.supportedValuesOf("timeZone") : []), [open]);
  const errors = form.formState.errors;
  const isDefault = !!plan?.is_default;

  useEffect(() => {
    if (open) form.reset(toForm(plan));
  }, [open, plan, form]);

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-lg">
        <form onSubmit={form.handleSubmit((v) => onSave(toBody(v)))} className="flex min-h-full flex-col" noValidate>
          <SheetHeader>
            <SheetTitle>{plan ? `Edit ${plan.name}` : "New Plan"}</SheetTitle>
            <SheetDescription>Dollar quotas, rate limits and allowed models. Changes apply to everyone on the Plan.</SheetDescription>
          </SheetHeader>

          <div className="flex flex-col gap-6 px-4">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="plan-name">Name</Label>
              <Input id="plan-name" {...form.register("name")} aria-invalid={!!errors.name} />
              <FieldError message={errors.name?.message} />
            </div>

            <fieldset className="flex flex-col gap-3">
              <legend className="mb-2 text-section font-semibold text-muted-foreground">Quotas (USD)</legend>
              {isDefault ? (
                <p className="text-muted-foreground">The default Plan has zero quota, so new people can&apos;t spend until you assign them a Plan.</p>
              ) : (
                <>
                  {quotas.fields.map((f, i) => (
                    <div key={f.id} className="grid grid-cols-[8rem_1fr_6rem_auto] items-start gap-2">
                      <Controller
                        control={form.control}
                        name={`quotas.${i}.window`}
                        render={({ field }) => (
                          <Select value={field.value} onValueChange={field.onChange}>
                            <SelectTrigger className="w-full" aria-label={`Window ${i + 1}`}>
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {windows.map((w) => (
                                <SelectItem key={w} value={w}>
                                  {windowLabel[w]}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        )}
                      />
                      <Input placeholder="All models" aria-label={`Model for window ${i + 1}`} {...form.register(`quotas.${i}.model_scope`)} />
                      <div>
                        <Input
                          type="number"
                          min={0}
                          step="0.01"
                          inputMode="decimal"
                          aria-label={`Limit in dollars for window ${i + 1}`}
                          aria-invalid={!!errors.quotas?.[i]?.limit_usd}
                          {...form.register(`quotas.${i}.limit_usd`, { valueAsNumber: true })}
                        />
                        <FieldError message={errors.quotas?.[i]?.limit_usd?.message} />
                      </div>
                      <Button type="button" variant="ghost" size="icon" aria-label={`Remove window ${i + 1}`} onClick={() => quotas.remove(i)}>
                        <X />
                      </Button>
                    </div>
                  ))}
                  <FieldError message={errors.quotas?.root?.message ?? errors.quotas?.message} />
                  {quotas.fields.length === 0 && <p className="text-muted-foreground">No windows means zero quota: every request is refused.</p>}
                  <Button type="button" variant="outline" size="sm" className="self-start" onClick={() => quotas.append({ window: "daily", model_scope: "", limit_usd: 10 })}>
                    <Plus /> Add window
                  </Button>
                </>
              )}
            </fieldset>

            <fieldset className="grid grid-cols-2 gap-3">
              <legend className="mb-2 text-section font-semibold text-muted-foreground">Rate limits (0 = unlimited)</legend>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="plan-rpm">Requests per minute</Label>
                <Input id="plan-rpm" type="number" min={0} {...form.register("rpm", { valueAsNumber: true })} aria-invalid={!!errors.rpm} />
                <FieldError message={errors.rpm?.message} />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="plan-conc">Concurrent streams</Label>
                <Input id="plan-conc" type="number" min={0} {...form.register("max_concurrent", { valueAsNumber: true })} aria-invalid={!!errors.max_concurrent} />
                <FieldError message={errors.max_concurrent?.message} />
              </div>
            </fieldset>

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="plan-models">Allowed models</Label>
              <Input id="plan-models" placeholder="All models" {...form.register("models")} />
              <p className="text-meta text-muted-foreground">Qanat names separated by commas, such as opus, sonnet. Leave empty to allow every model.</p>
            </div>

            <fieldset className="grid grid-cols-2 gap-3">
              <legend className="mb-2 text-section font-semibold text-muted-foreground">Resets</legend>
              <div className="col-span-2 flex flex-col gap-1.5">
                <Label htmlFor="plan-tz">Time zone</Label>
                <Input id="plan-tz" list="plan-tz-list" {...form.register("timezone")} aria-invalid={!!errors.timezone} />
                <datalist id="plan-tz-list">
                  {zones.map((z) => (
                    <option key={z} value={z} />
                  ))}
                </datalist>
                <FieldError message={errors.timezone?.message} />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="plan-daily">Daily reset</Label>
                <Input id="plan-daily" placeholder="00:00" {...form.register("daily_anchor")} aria-invalid={!!errors.daily_anchor} />
                <FieldError message={errors.daily_anchor?.message} />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="plan-weekly">Weekly reset</Label>
                <Controller
                  control={form.control}
                  name="weekly_anchor"
                  render={({ field }) => (
                    <Select value={String(field.value)} onValueChange={(v) => field.onChange(Number(v))}>
                      <SelectTrigger id="plan-weekly" className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {weekdays.map((d, i) => (
                          <SelectItem key={d} value={String(i)}>
                            {d}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  )}
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="plan-monthly">Monthly reset day</Label>
                <Input id="plan-monthly" type="number" min={1} max={28} {...form.register("monthly_anchor", { valueAsNumber: true })} aria-invalid={!!errors.monthly_anchor} />
                <FieldError message={errors.monthly_anchor?.message} />
              </div>
            </fieldset>

            {serverError && (
              <p role="alert" className="text-destructive-text">
                {serverError}
              </p>
            )}
          </div>

          <SheetFooter className="mt-auto flex-row justify-end">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={saving}>
              {plan ? "Save Plan" : "Create Plan"}
            </Button>
          </SheetFooter>
        </form>
      </SheetContent>
    </Sheet>
  );
}
