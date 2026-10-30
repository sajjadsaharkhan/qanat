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

export type PickerOption = { model: string; label: string; description: string; behavesAs: string };

/** "claude-sonnet-5-5" → "Claude Sonnet 5.5"; dots and version tails kept. */
export function prettyModelName(model: string): string {
  return model
    .split("-")
    .map((part) => (/^\d/.test(part) ? part : part.charAt(0).toUpperCase() + part.slice(1)))
    .join(" ");
}

/**
 * The full Claude Code settings template, 9router-style: env defaults plus a
 * modelPicker with every stored provider-catalog model, prefixed. Non-Claude
 * models behave as the Claude slot their name suggests (opus/haiku/sonnet).
 */
export function settingsTemplate(opts: {
  gatewayUrl: string;
  key: string;
  sonnetModel: string;
  opusModel: string;
  haikuModel: string;
  catalogs: { prefix: string; name: string; models: string[] }[];
}): string {
  const behavesAs = (model: string) => {
    if (model.startsWith("claude-")) return model;
    if (model.includes("opus")) return opts.opusModel;
    if (model.includes("haiku") || model.includes("flash") || model.includes("mini")) return opts.haikuModel;
    return opts.sonnetModel;
  };
  const options: PickerOption[] = opts.catalogs.flatMap(({ prefix, name, models }) =>
    models.map((m) => ({
      model: `${prefix}/${m}`,
      label: prettyModelName(m),
      description: `${prettyModelName(m)} via Qanat (${name})`,
      behavesAs: behavesAs(m),
    })),
  );
  return JSON.stringify(
    {
      env: {
        ANTHROPIC_AUTH_TOKEN: opts.key,
        ANTHROPIC_BASE_URL: opts.gatewayUrl,
        API_TIMEOUT_MS: "3000000",
        ANTHROPIC_DEFAULT_HAIKU_MODEL: opts.haikuModel,
        ANTHROPIC_DEFAULT_SONNET_MODEL: opts.sonnetModel,
        ANTHROPIC_DEFAULT_OPUS_MODEL: opts.opusModel,
      },
      modelPicker: { options },
    },
    null,
    2,
  );
}
