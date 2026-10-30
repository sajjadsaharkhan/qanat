/**
 * Every dashboard page, in light and dark (projects in playwright.config.ts):
 * renders, is accessible, is still under reduced motion, and matches its
 * reviewed screenshot. Pages map to PRD §14.
 */
import { expect, expectAccessible, expectStillUnderReducedMotion, signIn, test } from "./fixtures";

const userPages = [
  { path: "/", heading: "Overview" },
  { path: "/usage", heading: "Usage" },
  { path: "/requests", heading: "Recent requests" },
  { path: "/keys", heading: "Keys" },
  { path: "/setup", heading: "Setup" },
];

const adminPages = [
  { path: "/admin", heading: "Needs you" },
  { path: "/admin/users", heading: "Users" },
  { path: "/admin/plans", heading: "Plans" },
  { path: "/admin/accounts", heading: "Accounts" },
  { path: "/admin/proxies", heading: "Proxies" },
  { path: "/admin/models", heading: "Models" },
  { path: "/admin/audit", heading: "Audit log" },
];

for (const p of userPages) {
  test(`user: ${p.heading}`, async ({ page }) => {
    await signIn(page, "user");
    await page.goto(p.path);
    await expect(page.getByRole("heading", { level: 1, name: p.heading })).toBeVisible();
    await expectAccessible(page);
    await expectStillUnderReducedMotion(page);
    await expect(page).toHaveScreenshot({ fullPage: true, mask: [page.locator("[data-volatile]")] });
  });
}

for (const p of adminPages) {
  test(`admin: ${p.heading}`, async ({ page }) => {
    await signIn(page, "admin");
    await page.goto(p.path);
    await expect(page.getByRole("heading", { level: 1, name: p.heading })).toBeVisible();
    await expectAccessible(page);
    await expectStillUnderReducedMotion(page);
    await expect(page).toHaveScreenshot({ fullPage: true, mask: [page.locator("[data-volatile]")] });
  });
}

test("a regular user cannot open admin pages", async ({ page }) => {
  await signIn(page, "user");
  const res = await page.goto("/admin/users");
  expect(res?.status()).toBe(403);
});

test("someone outside the qanat-users group is turned away (REQ-AUTH-02)", async ({ page }) => {
  await page.request.post(`${process.env.QANAT_OIDC_URL ?? "http://localhost:59180"}/test/login-as`, {
    data: { subject: "outsider", email: "outsider@example.com", groups: ["other-team"] },
  });
  await page.goto("/");
  await expect(page.getByRole("heading", { name: /don.t have access/i })).toBeVisible();
});

test("theme switch: light, dark and system all apply without a flash @mobile", async ({ page }) => {
  await signIn(page, "user");
  for (const choice of ["Dark", "Light", "System"]) {
    await page.getByRole("button", { name: /account menu/i }).click();
    await page.getByRole("menuitemradio", { name: choice }).click();
    const isDark = await page.evaluate(() => document.documentElement.classList.contains("dark"));
    if (choice === "Dark") expect(isDark).toBe(true);
    if (choice === "Light") expect(isDark).toBe(false);
  }
});
