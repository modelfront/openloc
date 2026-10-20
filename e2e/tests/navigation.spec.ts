import { test, expect } from "@playwright/test";
import { getSeedTemplateMTRouter, login, openJob } from "./helpers";

test.describe("Main Navigation", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("can navigate to all main pages", async ({ page }) => {
    // Projects (home)
    await expect(
      page.getByRole("heading", { name: /projects/i })
    ).toBeVisible();

    // Templates
    await page.getByRole("link", { name: /templates/i }).click();
    await expect(page).toHaveURL(/\/templates/);
    await expect(page.getByText(/default template/i)).toBeVisible();

    // Translation memories
    await page.getByRole("link", { name: /translation memor/i }).click();
    await expect(page).toHaveURL(/\/memories/);
    await expect(page.getByText(/default tm/i)).toBeVisible();

    // MT — lands on the Routers tab; Engines and Prompts are sibling tabs.
    await page.getByRole("link", { name: /^MT$/ }).click();
    await expect(page).toHaveURL(/\/mt$/);
    const seedRouter = await getSeedTemplateMTRouter(page);
    await expect(page.getByTestId(`mt-router-${seedRouter.id}`)).toContainText(seedRouter.name);
    await page.getByRole("link", { name: /^Engines$/ }).click();
    await expect(page).toHaveURL(/\/mt\/engines$/);
    await expect(page.getByText(/libretranslate/i).first()).toBeVisible();
  });

  test("left nav rail collapses from its brand row on any page and stays collapsed across pages", async ({ page }) => {
    const rail = page.getByTestId("nav-rail");
    const toggle = page.getByTestId("nav-rail-toggle");
    const wordmark = rail.getByText("OpenLoc", { exact: true });
    const brandRow = toggle.locator("..");
    const projectsIcon = rail.locator('a[href="/projects"] svg');
    // The rail's width is a transition; let it settle before measuring (a
    // restarted transition cancels the old one, hence the loop).
    const settle = () =>
      rail.evaluate(async el => {
        for (let a = el.getAnimations({ subtree: true }); a.length; a = el.getAnimations({ subtree: true }))
          await Promise.allSettled(a.map(x => x.finished));
      });
    const box = async (l: import("@playwright/test").Locator) => (await l.boundingBox())!;

    // Expanded: wordmark on the left, toggle at the right end of the same row.
    await expect(wordmark).toBeVisible();
    await expect(toggle).toHaveAccessibleName("Collapse navigation");
    const expandedWidth = (await box(rail)).width;
    const [w, t, row] = [await box(wordmark), await box(toggle), await box(brandRow)];
    expect(t.x).toBeGreaterThan(w.x + w.width);
    expect(row.x + row.width - (t.x + t.width)).toBeLessThanOrEqual(12);

    try {
      await toggle.click();
      await expect(rail).toHaveAttribute("data-collapsed", "true");
      await settle();
      await expect(wordmark).toHaveCount(0);
      await expect(rail.getByRole("img", { name: "OpenLoc" })).toHaveCount(0);
      await expect(toggle).toHaveAccessibleName("Expand navigation");
      expect((await box(rail)).width).toBeLessThan(expandedWidth / 2);
      // The expand icon matches the nav icons: same size, same column.
      const [toggleIcon, navIcon] = [await box(toggle.locator("svg")), await box(projectsIcon)];
      expect(toggleIcon.width).toBe(navIcon.width);
      expect(toggleIcon.height).toBe(navIcon.height);
      expect(Math.abs(toggleIcon.x - navIcon.x)).toBeLessThanOrEqual(1);
      await expect(rail.getByRole("link", { name: "Projects" })).toBeVisible();

      // The choice follows the user into the editor and survives a reload.
      await page.getByRole("link", { name: /demo project/i }).click();
      await openJob(page, { fileName: "demo.txt" });
      await expect(rail).toHaveAttribute("data-collapsed", "true");
      await page.reload();
      await expect(rail).toHaveAttribute("data-collapsed", "true");

      await toggle.click();
      await expect(rail).toHaveAttribute("data-collapsed", "false");
      await expect(wordmark).toBeVisible();
    } finally {
      await page.evaluate(() => localStorage.removeItem("nav-rail-collapsed"));
    }
  });

  test("account dropdown shows Settings, Organization settings, Sign out", async ({
    page,
  }) => {
    // Open Account dropdown
    await page.getByRole("button", { name: /account/i }).click();

    // All menu items should be visible. The account menu is a DropdownMenu, so
    // its entries are `menuitem` roles (not buttons) — the "Account" trigger
    // itself stays a button.
    await expect(
      page.getByRole("menuitem", { name: /^settings$/i })
    ).toBeVisible();
    await expect(
      page.getByRole("menuitem", { name: /organization settings/i })
    ).toBeVisible();
    await expect(
      page.getByRole("menuitem", { name: /ai settings/i })
    ).toBeVisible();
    await expect(
      page.getByRole("menuitem", { name: /sign out/i })
    ).toBeVisible();
  });

  test("account dropdown navigates to Settings page", async ({ page }) => {
    await page.getByRole("button", { name: /account/i }).click();
    await page.getByRole("menuitem", { name: /^settings$/i }).click();
    await expect(page).toHaveURL(/\/settings/);
    await expect(
      page.getByRole("heading", { name: /settings/i })
    ).toBeVisible();
  });

  test("account dropdown navigates to Organization settings", async ({
    page,
  }) => {
    await page.getByRole("button", { name: /account/i }).click();
    await page.getByRole("menuitem", { name: /organization settings/i }).click();
    await expect(page).toHaveURL(/\/org/);
    await expect(
      page.getByRole("heading", { name: /organization/i })
    ).toBeVisible();
  });

  // AI settings is its own admin page. The dropdown entry is the only nav
  // route to it, so it's the thing worth asserting.
  test("account dropdown navigates to AI settings", async ({ page }) => {
    await page.getByRole("button", { name: /account/i }).click();
    await page.getByRole("menuitem", { name: /ai settings/i }).click();
    await expect(page).toHaveURL(/\/ai-settings/);
    await expect(
      page.getByRole("heading", { name: /^ai settings$/i })
    ).toBeVisible();
    // Both credential surfaces render expanded: the org's vendor connections +
    // API keys (admin-only) and the signed-in user's own MCP tokens.
    // Connections render as one grid, so the anchor is the
    // grid rather than a vendor card.
    await expect(page.getByTestId("ai-connections")).toBeVisible();
    await expect(page.getByTestId("api-tokens-section")).toBeVisible();
  });

  // API tokens live on AI settings, not personal Settings.
  test("personal Settings has no API tokens section", async ({ page }) => {
    await page.goto("/settings");
    await expect(page.getByRole("heading", { name: /settings/i })).toBeVisible();
    await expect(page.getByTestId("api-tokens-section")).toHaveCount(0);
  });

  test("AI page has no Providers section", async ({ page }) => {
    await page.goto("/ai");
    await expect(page.getByRole("heading", { name: /^AI$/ })).toBeVisible();
    await expect(page.getByTestId("ai-connections")).toHaveCount(0);
  });
});
