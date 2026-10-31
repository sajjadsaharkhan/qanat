import AxeBuilder from "@axe-core/playwright";
import { expect, test as base, type Page } from "@playwright/test";

const oidcURL = process.env.QANAT_OIDC_URL ?? "http://localhost:59180";

export type Role = "user" | "admin";

const people: Record<Role, { subject: string; email: string; name: string }> = {
  user: { subject: "e2e-user", email: "dev@example.com", name: "Dev User" },
  // The control plane under test must run with BOOTSTRAP_ADMIN_EMAIL=admin@example.com
  admin: { subject: "e2e-admin", email: "admin@example.com", name: "Admin User" },
};

/** Signs in through the real OIDC code flow, with oidcmock standing in for Keycloak. */
export async function signIn(page: Page, role: Role, groups = ["qanat-users"]) {
  const res = await page.request.post(`${oidcURL}/test/login-as`, { data: { ...people[role], groups } });
  expect(res.status()).toBe(204);
  await page.goto("/");
  await expect(page.getByRole("navigation")).toBeVisible();
}

/** WCAG 2.1 AA, zero violations allowed (design.md §11). */
/**
 * Axe reads computed colors, so a tile caught mid-fade reports blended,
 * failing contrast. Wait for finite animations (entrance fades, count-ups)
 * to finish; infinite ones (spinners, pulses) never will.
 */
async function settle(page: Page) {
  await page.waitForFunction(
    () => document.getAnimations().every((a) => a.playState !== "running" || (a.effect?.getTiming().iterations ?? 1) === Infinity),
    undefined,
    { timeout: 10_000 },
  );
  await page.waitForTimeout(500); // motion's JS-driven springs aren't in getAnimations()
}

export async function expectAccessible(page: Page) {
  await settle(page);
  const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  expect(results.violations, JSON.stringify(results.violations, null, 2)).toEqual([]);
}

/** No element may still be animating once reduced motion is requested. */
export async function expectStillUnderReducedMotion(page: Page) {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.waitForTimeout(300);
  const running = await page.evaluate(
    () =>
      document
        .getAnimations()
        .filter((a) => a.playState === "running" && (a.effect?.getTiming().iterations ?? 1) === Infinity).length,
  );
  expect(running, "infinite animations running under prefers-reduced-motion").toBe(0);
}

export const test = base;
export { expect };
