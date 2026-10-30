import { describe, expect, it } from "vitest";
import { modelTier } from "./model-tier";

describe("modelTier", () => {
  it.each([
    ["claude-opus-4-7", "opus"],
    ["cc/claude-sonnet-4-6", "sonnet"],
    ["haiku", "haiku"],
    ["claude-fable-5-1", "fable"],
    ["cx/gpt-5.5", "gpt"],
    ["gpt-5.3-codex", "gpt"],
    ["ag/gemini-3-pro", "gemini"],
    ["my-combo", "other"],
  ] as const)("%s → %s", (name, tier) => expect(modelTier(name)).toBe(tier));
});
