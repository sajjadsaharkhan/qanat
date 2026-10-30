/**
 * Pass-through to the gateway's own GET /v1/models, so the Setup page can
 * list exactly what Claude Code will see for a key without the gateway
 * opening CORS. Only the key headers go upstream; nothing is cached.
 *
 * QANAT_GATEWAY_URL is read per request, so one image runs anywhere.
 */
import type { NextRequest } from "next/server";

const forwardRequestHeaders = ["authorization", "x-api-key", "anthropic-version"];

export async function GET(req: NextRequest) {
  const base = process.env.QANAT_GATEWAY_URL ?? "http://localhost:18080";
  const headers = new Headers();
  for (const h of forwardRequestHeaders) {
    const v = req.headers.get(h);
    if (v) headers.set(h, v);
  }
  let upstream: Response;
  try {
    upstream = await fetch(`${base}/v1/models`, { headers, cache: "no-store", redirect: "manual" });
  } catch {
    return Response.json(
      { type: "error", error: { type: "api_error", message: "The dashboard can't reach the gateway. Check that it's running." } },
      { status: 502 },
    );
  }
  return new Response(upstream.body, {
    status: upstream.status,
    headers: { "content-type": upstream.headers.get("content-type") ?? "application/json" },
  });
}
