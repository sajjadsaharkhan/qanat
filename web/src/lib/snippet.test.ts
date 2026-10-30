import { describe, expect, it } from "vitest";
import { latestClaude, prettyModelName, settingsTemplate } from "./snippet";

describe("latestClaude", () => {
  it("picks the newest of each family, numerically", () => {
    expect(
      latestClaude([
        "cc/claude-opus-4-1-20250805",
        "cc/claude-opus-4-10",
        "cc/claude-opus-4-7",
        "cc/claude-3-5-haiku-20241022",
        "cc/claude-haiku-4-5-20251001",
        "cc/claude-sonnet-4-5",
        "cx/gpt-5.5",
        "opus",
      ]),
    ).toEqual({
      opus: "cc/claude-opus-4-10",
      haiku: "cc/claude-haiku-4-5-20251001",
      sonnet: "cc/claude-sonnet-4-5",
    });
  });

  it("prefers the undated alias and the cc/ provider on a tie", () => {
    expect(latestClaude(["ag/claude-sonnet-4-5", "cc/claude-sonnet-4-5-20250929", "cc/claude-sonnet-4-5"])).toEqual({
      sonnet: "cc/claude-sonnet-4-5",
    });
  });
});

describe("settingsTemplate", () => {
  it("adds behavesAs only to the newest Claude model of each family", () => {
    const out = JSON.parse(
      settingsTemplate({
        gatewayUrl: "https://gw",
        key: "k",
        slots: {
          opus: "cc/claude-opus-4-7",
          sonnet: "cc/claude-sonnet-4-5",
          haiku: "",
        },
        models: ["cc/claude-opus-4-1", "cc/claude-opus-4-7", "cc/claude-sonnet-4-5", "cx/gpt-5.5", "opus"],
      }),
    );
    expect(out.env.ANTHROPIC_DEFAULT_OPUS_MODEL).toBe("cc/claude-opus-4-7");
    expect(out.env).not.toHaveProperty("ANTHROPIC_DEFAULT_HAIKU_MODEL");
    expect(Object.fromEntries(out.modelPicker.options.map((o: { model: string; behavesAs?: string }) => [o.model, o.behavesAs]))).toEqual({
      "cc/claude-opus-4-1": undefined,
      "cc/claude-opus-4-7": "claude-opus-4-7",
      "cc/claude-sonnet-4-5": "claude-sonnet-4-5",
      "cx/gpt-5.5": undefined,
      opus: undefined,
    });
  });
});

describe("prettyModelName", () => {
  it.each([
    ["cc/claude-sonnet-5-5", "Claude Sonnet 5.5"],
    ["claude-3-5-haiku-20241022", "Claude 3.5 Haiku 20241022"],
    ["cx/gpt-5.5", "Gpt 5.5"],
  ])("%s → %s", (m, want) => expect(prettyModelName(m)).toBe(want));
});
