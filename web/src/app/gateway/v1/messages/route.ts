/**
 * Pass-through to the gateway's POST /v1/messages for the admin Playground,
 * so the page can stream a real request without the gateway opening CORS
 * (production's nginx serves /gateway/ itself and never reaches this).
 * The key, the pinned account and the Anthropic headers go upstream; the
 * body streams back untouched, with the gateway's routing details (x-qanat-*).
 *
 * QANAT_GATEWAY_URL is read per request, so one image runs anywhere.
 */
import type { NextRequest } from "next/server";

const forwardRequestHeaders = ["authorization", "x-api-key", "anthropic-version", "anthropic-beta", "content-type", "x-qanat-account"];

export async function POST(req: NextRequest) {
  const base = process.env.QANAT_GATEWAY_URL ?? "http://localhost:18080";
  const headers = new Headers();
  for (const h of forwardRequestHeaders) {
    const v = req.headers.get(h);
    if (v) headers.set(h, v);
  }
  let upstream: Response;
  try {
    upstream = await fetch(`${base}/v1/messages`, {
      method: "POST",
      headers,
      body: await req.arrayBuffer(),
      signal: req.signal, // Stop in the page cancels the upstream call too
      cache: "no-store",
      redirect: "manual",
    });
  } catch {
    if (req.signal.aborted) return new Response(null, { status: 499 });
    return Response.json(
      { type: "error", error: { type: "api_error", message: "The dashboard can't reach the gateway. Check that it's running." } },
      { status: 502 },
    );
  }
  const out = new Headers({ "content-type": upstream.headers.get("content-type") ?? "application/json", "cache-control": "no-store" });
  upstream.headers.forEach((v, k) => {
    if (k.startsWith("x-qanat-")) out.set(k, v);
  });
  return new Response(upstream.body, { status: upstream.status, headers: out });
}
