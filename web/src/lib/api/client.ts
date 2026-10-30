/**
 * The typed Admin API client, generated from internal/control/openapi.yaml
 * (`npm run api:generate`). Components never hand-write fetch calls
 * (design.md §10.1).
 */
import createFetchClient, { type Middleware } from "openapi-fetch";
import createClient from "openapi-react-query";

import type { components, paths } from "./schema";

export type Schemas = components["schemas"];
export type Me = Schemas["Me"];
export type Plan = Schemas["Plan"];
export type PlanBody = Schemas["PlanBody"];
export type WindowKind = Schemas["WindowKind"];

/** Every state-changing request carries the CSRF header the API requires. */
const csrf: Middleware = {
  onRequest({ request }) {
    if (request.method !== "GET") request.headers.set("X-Qanat-Request", "1");
    return request;
  },
  onResponse({ response }) {
    // A session that ended mid-visit sends the person back to sign in.
    if (response.status === 401 && typeof window !== "undefined" && !location.pathname.startsWith("/sign-in")) {
      // Outside React (fetch middleware): a full load also drops stale query cache.
      // eslint-disable-next-line @next/next/no-location-assign-relative-destination
      window.location.href = `/sign-in?next=${encodeURIComponent(location.pathname)}`;
    }
    return response;
  },
};

export const fetchClient = createFetchClient<paths>({ baseUrl: "", credentials: "same-origin" });
fetchClient.use(csrf);

export const api = createClient(fetchClient);

/** The readable message from an API error body, for toasts and inline errors. */
export function errorMessage(error: unknown, fallback = "Something went wrong. Try again."): string {
  if (error && typeof error === "object" && "error" in error) {
    const e = (error as { error?: { message?: unknown } }).error;
    if (typeof e?.message === "string") return e.message;
  }
  return fallback;
}
