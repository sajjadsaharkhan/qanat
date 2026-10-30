/**
 * The Claude provider sign-in dialog (PRD §6.6): OAuth sign-in is the default
 * kind, the dialog is accessible, and matches its reviewed screenshot in both
 * themes. The sign-in itself isn't clicked here — it would leave for the real
 * provider; the flow behind it is covered by the control-plane tests.
 */
import { expect, expectAccessible, signIn, test } from "./fixtures";

test("claude add-connection dialog defaults to provider sign-in", async ({ page }) => {
  await signIn(page, "admin");
  await page.goto("/admin/accounts/claude");
  await page.getByRole("button", { name: /add connection/i }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("combobox", { name: "Credential kind" })).toContainText(/Claude subscription/);
  await expect(dialog.getByRole("button", { name: /sign in with claude/i })).toBeVisible();
  await expectAccessible(page);
  await expect(dialog).toHaveScreenshot();
});
