/** Helpers for the admin Playground: SSE parsing and the numbers a run reports. */

export type SSEEvent = { type?: string; [key: string]: unknown };

/** Splits a streamed buffer into its complete SSE `data:` payloads; `rest` is the unfinished tail. */
export function takeEvents(buffer: string): { events: SSEEvent[]; rest: string } {
  const parts = buffer.split(/\r?\n\r?\n/);
  const rest = parts.pop() ?? "";
  const events: SSEEvent[] = [];
  for (const part of parts) {
    const data = part
      .split(/\r?\n/)
      .filter((l) => l.startsWith("data:"))
      .map((l) => l.slice(5).trimStart())
      .join("\n");
    if (!data) continue;
    try {
      events.push(JSON.parse(data) as SSEEvent);
    } catch {
      // a keep-alive or a partial payload: nothing to show
    }
  }
  return { events, rest };
}

/** What one streamed event adds to a run: text, token counts, or an error. */
export function readEvent(ev: SSEEvent): { text?: string; inTokens?: number; outTokens?: number; error?: string } {
  const out: { text?: string; inTokens?: number; outTokens?: number; error?: string } = {};
  const num = (v: unknown) => (typeof v === "number" ? v : undefined);
  if (ev.type === "content_block_delta") {
    const d = ev.delta as { type?: string; text?: string } | undefined;
    if (d?.type === "text_delta" && d.text) out.text = d.text;
  } else if (ev.type === "message_start") {
    const u = (ev.message as { usage?: { input_tokens?: number; output_tokens?: number } } | undefined)?.usage;
    out.inTokens = num(u?.input_tokens);
    out.outTokens = num(u?.output_tokens);
  } else if (ev.type === "message_delta") {
    const u = ev.usage as { input_tokens?: number; output_tokens?: number } | undefined;
    out.outTokens = num(u?.output_tokens);
    out.inTokens = num(u?.input_tokens);
  } else if (ev.type === "error") {
    out.error = (ev.error as { message?: string } | undefined)?.message ?? "The stream reported an error.";
  }
  return out;
}

/** A model name's provider, by the prefix the gateway lists it under. */
const prefixes = { claude: "cc/", codex: "cx/", antigravity: "ag/" } as const;
export const providerPrefix = (provider: string): string | undefined => prefixes[provider as keyof typeof prefixes];

/** Milliseconds from a response header; undefined when absent or not a number. */
export function msHeader(h: Headers, name: string): number | undefined {
  const v = h.get(name);
  if (v === null || v === "") return undefined;
  const n = Number(v);
  return Number.isFinite(n) ? n : undefined;
}

/** How one run's time splits: gateway, upstream response, wait for the first token, generation. */
export function timeline(r: { gateway?: number; upstream?: number; ttft?: number; total?: number }) {
  const { gateway = 0, upstream = 0, ttft, total } = r;
  if (ttft === undefined || total === undefined) return null;
  const toFirst = Math.max(0, ttft - gateway - upstream);
  return { gateway, upstream, toFirst, generation: Math.max(0, total - ttft) };
}
