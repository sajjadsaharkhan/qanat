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
