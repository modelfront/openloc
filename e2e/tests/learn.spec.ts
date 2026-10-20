import { test, expect } from "@playwright/test";
import { login } from "./helpers";

/**
 * The Learn page: the account menu opens it, and a tour started there runs in
 * the Playground (on by default outside production).
 */
test.describe("Learn page", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("account menu → Learn → Start tour opens the tour in the Playground", async ({ page }) => {
    await page.getByRole("button", { name: /account menu/i }).click();
    await page.getByTestId("user-menu-learn").click();
    await expect(page).toHaveURL(/\/learn$/);
    await expect(page.getByTestId("learn-tour-demo-overview")).toBeVisible();
    await expect(page.getByTestId("learn-playground")).toBeVisible();

    await page.getByTestId("learn-start-demo-overview").click();
    await expect(page.getByTestId("guide-card-label")).toContainText(/1 of \d+/);
  });
});
