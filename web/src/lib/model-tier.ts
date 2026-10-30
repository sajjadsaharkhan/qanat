/** Model name → tier, for the tier dot and chart series (design.md §3.3). */
export type ModelTier = "fable" | "opus" | "sonnet" | "haiku" | "gpt" | "gemini" | "other";

const rules: [RegExp, ModelTier][] = [
  [/fable|mythos/i, "fable"],
  [/opus/i, "opus"],
  [/sonnet/i, "sonnet"],
  [/haiku/i, "haiku"],
  [/^(cx\/)?(gpt|o\d)/i, "gpt"],
  [/gemini/i, "gemini"],
];

/** Accepts Qanat names, provider-prefixed names (cc/, cx/, ag/) and upstream IDs. */
export function modelTier(name: string): ModelTier {
  for (const [re, tier] of rules) if (re.test(name)) return tier;
  return "other";
}

// Full class names, not templates: Tailwind only generates classes it can see
// verbatim in source.
const dotClass: Record<ModelTier, string> = {
  fable: "bg-model-fable",
  opus: "bg-model-opus",
  sonnet: "bg-model-sonnet",
  haiku: "bg-model-haiku",
  gpt: "bg-model-gpt",
  gemini: "bg-model-gemini",
  other: "bg-muted-foreground",
};

/** Tailwind class for the tier dot; "other" stays neutral. */
export function tierDotClass(tier: ModelTier): string {
  return dotClass[tier];
}
