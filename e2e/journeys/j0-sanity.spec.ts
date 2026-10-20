import { test, expect } from "@playwright/test";
import { login, openJob } from "./helpers";

/**
 * J0 — Sanity Check
 *
 * Fail fast. If any of these break, nothing else will work.
 * Must complete in <10s total.
 */
test.describe.serial("J0 — Sanity", () => {
  test("S1: login lands on projects page", async ({ page }) => {
    await login(page);
    await expect(page).toHaveURL(/\//);
    await expect(page.getByRole("heading", { name: /projects/i })).toBeVisible();
  });

  test("S2: sidebar shows all nav links", async ({ page }) => {
    await login(page);
    for (const label of [
      /projects/i,
      /translation memories/i,
      /terminology/i,
      /templates/i,
      /^MT$/,
      /qa profiles/i,
    ]) {
      await expect(page.getByRole("link", { name: label })).toBeVisible();
    }
  });

  test("S3: Demo Project visible in project list", async ({ page }) => {
    await login(page);
    await expect(page.getByText(/demo project/i).first()).toBeVisible();
  });

  test("S4: Demo Project detail page loads with Files tab", async ({ page }) => {
    await login(page);
    await page.getByRole("link", { name: /demo project/i }).click();
    await page.getByRole("link", { name: /^files$/i }).click();
    await expect(page.getByText(/demo\.txt/i).first()).toBeVisible({ timeout: 5000 });
  });

  test("S5: CAT editor loads for a demo job", async ({ page }) => {
    await login(page);
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });
    // Verify at least one source segment is visible
    await expect(page.locator("[data-translation-cell]").first()).toBeVisible({ timeout: 10000 });
  });
});
