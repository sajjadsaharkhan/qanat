import { defineConfig, devices } from "@playwright/test";

/**
 * Dashboard end-to-end tests (design.md §11). Every spec runs twice, once per
 * theme, because the dashboard must be correct in light and dark.
 *
 * Requires `make stack-up` first: it provides the control plane Admin API
 * (QANAT_CONTROL_URL) and oidcmock, the Keycloak stand-in (QANAT_OIDC_URL).
 * Playwright then builds and starts the dashboard itself.
 */
const webURL = process.env.QANAT_WEB_URL ?? "http://127.0.0.1:53000";
const oidcURL = process.env.QANAT_OIDC_URL ?? "http://localhost:59180";

export default defineConfig({
  testDir: "tests/e2e",
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  // Baselines are reviewed on macOS (*-darwin.png); Linux CI renders fonts
  // differently, so CI keeps the axe and reduced-motion checks and skips
  // screenshot comparisons.
  ignoreSnapshots: !!process.env.CI,
  reporter: process.env.CI ? [["github"], ["html", { open: "never" }]] : "list",
  expect: { toHaveScreenshot: { maxDiffPixelRatio: 0.001, animations: "disabled" } },
  use: {
    baseURL: webURL,
    trace: "retain-on-failure",
    timezoneId: "Asia/Tehran", // default quota reset zone (PRD §10.3)
    locale: "en-US",
  },
  projects: [
    { name: "light", use: { ...devices["Desktop Chrome"], colorScheme: "light" } },
    { name: "dark", use: { ...devices["Desktop Chrome"], colorScheme: "dark" } },
    { name: "mobile-light", use: { ...devices["Pixel 7"], colorScheme: "light" }, grep: /@mobile/ },
  ],
  webServer: {
    command: "npm run build && npm run start",
    url: `${webURL}/access-denied`,
    timeout: 180_000,
    reuseExistingServer: !process.env.CI,
    env: {
      PORT: "53000",
      HOSTNAME: "127.0.0.1",
      QANAT_OIDC_ISSUER: `${oidcURL}/realms/qanat`,
      QANAT_OIDC_CLIENT_ID: "qanat-dashboard",
      QANAT_CONTROL_URL: process.env.QANAT_CONTROL_URL ?? "http://localhost:58081",
    },
  },
});
