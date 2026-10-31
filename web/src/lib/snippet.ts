/** The Claude Code settings.json snippet from PRD §4.1. */
export function settingsSnippet(gatewayUrl: string, key: string): string {
  return JSON.stringify(
    {
      env: {
        ANTHROPIC_BASE_URL: gatewayUrl,
        ANTHROPIC_AUTH_TOKEN: key,
        ANTHROPIC_DEFAULT_OPUS_MODEL: "opus",
        ANTHROPIC_DEFAULT_SONNET_MODEL: "sonnet",
        ANTHROPIC_DEFAULT_HAIKU_MODEL: "haiku",
        CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY: "1",
        CLAUDE_CODE_ATTRIBUTION_HEADER: "0",
      },
    },
    null,
    2,
  );
}

/** One-line connection test (PRD §14.1): lists the models your Plan allows. */
export function testCommand(gatewayUrl: string, key: string): string {
  return `curl -sS ${gatewayUrl}/v1/models -H "Authorization: Bearer ${key}" -H "anthropic-version: 2023-06-01"`;
}

export type PickerOption = {
  model: string;
  label: string;
  description: string;
  behavesAs?: string;
};

export type ClaudeFamily = "opus" | "sonnet" | "haiku";
export const claudeFamilies: ClaudeFamily[] = ["opus", "sonnet", "haiku"];

/**
 * Whether a model supports a 1M-token context window. Currently Google Gemini
 * models (served via Antigravity) natively support 1M context.
 */
export function supports1M(model: string): boolean {
  return /gemini/i.test(model) || /\[1m\]$/i.test(model);
}

/**
 * Appends Claude Code's "[1m]" context suffix to models that support a 1M
 * context window (e.g. "ag/gemini-3.8-flash-high[1m]"). Claude Code requires
 * this suffix to allow sessions to grow beyond 200k tokens without auto-compacting
 * or warning.
 */
export function setupModelName(model: string): string {
  if (supports1M(model) && !model.toLowerCase().endsWith("[1m]")) {
    return `${model}[1m]`;
  }
  return model;
}

/** The model without its provider prefix: "cc/claude-opus-4-7" → "claude-opus-4-7". */
export function bareModel(model: string): string {
  return model.slice(model.indexOf("/") + 1);
}

/**
 * Sort key for a Claude model of one family, or null when the name is not a
 * Claude model of it. Both id shapes count ("claude-opus-4-7",
 * "claude-3-5-haiku-20241022"); version parts compare numerically, then the
 * snapshot date, and an undated alias outranks its dated twin.
 */
function claudeRank(model: string, family: ClaudeFamily): number[] | null {
  const bare = bareModel(model).replace(/\[1m\]$/i, "");
  if (!bare.startsWith("claude-") || !bare.split("-").includes(family)) return null;
  const parts = bare.split("-").filter((p) => /^\d+$/.test(p));
  const date = parts.find((p) => p.length === 8);
  const version = parts.filter((p) => p.length < 8).map(Number);
  return [...version, ...Array(4 - Math.min(4, version.length)).fill(0), date ? Number(date) : Infinity];
}

function compareRank(a: number[], b: number[]): number {
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    const x = a[i] ?? 0;
    const y = b[i] ?? 0;
    if (x !== y) return x < y ? -1 : 1; // not x - y: Infinity - Infinity is NaN
  }
  return 0;
}

/**
 * The newest Claude model of each family in a /v1/models list. A cc/ name
 * wins over the same model through another provider; families with no
 * Claude model are left out.
 */
export function latestClaude(models: string[]): Partial<Record<ClaudeFamily, string>> {
  const out: Partial<Record<ClaudeFamily, string>> = {};
  for (const family of claudeFamilies) {
    let best: { model: string; rank: number[] } | undefined;
    for (const model of models) {
      const rank = claudeRank(model, family);
      if (!rank) continue;
      const d = best ? compareRank(rank, best.rank) : 1;
      if (d > 0 || (d === 0 && model.startsWith("cc/") && !best!.model.startsWith("cc/"))) best = { model, rank };
    }
    if (best) out[family] = best.model;
  }
  return out;
}

/** "cc/claude-sonnet-5-5" → "Claude Sonnet 5.5"; prefix and [1m] dropped, snapshot dates kept. */
export function prettyModelName(model: string): string {
  const words: string[] = [];
  let version = false;
  const bare = bareModel(model).replace(/\[1m\]$/i, "");
  for (const part of bare.split("-")) {
    const short = /^\d{1,2}$/.test(part);
    if (short && version) words[words.length - 1] += `.${part}`;
    else words.push(/^\d/.test(part) ? part : part.charAt(0).toUpperCase() + part.slice(1));
    version = short;
  }
  return words.join(" ");
}

const providerNames: Record<string, string> = {
  cc: "Claude",
  cx: "Codex",
  ag: "Antigravity",
};

/**
 * The full Claude Code settings.json, 9router-style: env defaults plus a
 * modelPicker with the chosen /v1/models names. Only the newest Claude
 * opus/sonnet/haiku carry behavesAs (their own bare id), so Claude Code
 * applies those models' capabilities; every other option goes without.
 * 1M context models (like Gemini) carry the [1m] suffix required by Claude Code.
 */
export function settingsTemplate(opts: { gatewayUrl: string; key: string; slots: Record<ClaudeFamily, string>; models: string[] }): string {
  const latest = new Set(Object.values(latestClaude(opts.models)));
  const options: PickerOption[] = opts.models.map((rawModel) => {
    const model = setupModelName(rawModel);
    const prefix = model.includes("/") ? model.slice(0, model.indexOf("/")) : "";
    const via = providerNames[prefix] ? `Qanat (${providerNames[prefix]})` : "Qanat";
    const option: PickerOption = {
      model,
      label: prettyModelName(model),
      description: `${prettyModelName(model)} via ${via}`,
    };
    if (latest.has(rawModel) || latest.has(model)) {
      option.behavesAs = bareModel(rawModel).replace(/\[1m\]$/i, "");
    }
    return option;
  });
  const env: Record<string, string> = {
    ANTHROPIC_AUTH_TOKEN: opts.key,
    ANTHROPIC_BASE_URL: opts.gatewayUrl,
    API_TIMEOUT_MS: "3000000",
  };
  if (opts.slots.haiku) env.ANTHROPIC_DEFAULT_HAIKU_MODEL = setupModelName(opts.slots.haiku);
  if (opts.slots.sonnet) env.ANTHROPIC_DEFAULT_SONNET_MODEL = setupModelName(opts.slots.sonnet);
  if (opts.slots.opus) env.ANTHROPIC_DEFAULT_OPUS_MODEL = setupModelName(opts.slots.opus);
  return JSON.stringify({ env, modelPicker: { options } }, null, 2);
}
