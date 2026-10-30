/**
 * Same-origin proxy to the control plane's Admin API. The browser only ever
 * talks to the dashboard, so the session cookie is first-party and the
 * control plane needs no CORS (design.md §10.3 rule 7: the dashboard never
 * talks to the data plane).
 *
 * QANAT_CONTROL_URL is read per request, so one image runs anywhere.
 */
import type { NextRequest } from "next/server";

const forwardRequestHeaders = ["content-type", "cookie", "x-qanat-request", "accept"];
const forwardResponseHeaders = ["content-type", "cache-control", "retry-after"];

async function proxy(req: NextRequest, ctx: { params: Promise<{ path: string[] }> }) {
  const { path } = await ctx.params;
  const base = process.env.QANAT_CONTROL_URL ?? "http://localhost:8081";
  const url = `${base}/api/${path.map(encodeURIComponent).join("/")}${req.nextUrl.search}`;

  const headers = new Headers();
  for (const h of forwardRequestHeaders) {
    const v = req.headers.get(h);
    if (v) headers.set(h, v);
  }
  const hasBody = req.method !== "GET" && req.method !== "HEAD";
  let upstream: Response;
  try {
    upstream = await fetch(url, {
      method: req.method,
      headers,
      body: hasBody ? await req.arrayBuffer() : undefined,
      cache: "no-store",
      redirect: "manual",
    });
  } catch {
    return Response.json(
      { error: { code: "control_unreachable", message: "The dashboard can't reach the control plane. Check that it's running." } },
      { status: 502 },
    );
  }

  const out = new Headers();
  for (const h of forwardResponseHeaders) {
    const v = upstream.headers.get(h);
    if (v) out.set(h, v);
  }
  for (const c of upstream.headers.getSetCookie()) out.append("set-cookie", c);
  return new Response(upstream.status === 204 ? null : upstream.body, { status: upstream.status, headers: out });
}

export { proxy as GET, proxy as POST, proxy as PUT, proxy as PATCH, proxy as DELETE };
