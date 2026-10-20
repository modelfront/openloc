import { test, expect, type Page } from "@playwright/test";
import { getSeedProjectId, login } from "./helpers";

// iPhone 16. Below 768px the app switches to its phone layout: content wraps
// or scrolls inside its own box, never the whole page sideways.
test.use({ viewport: { width: 393, height: 852 } });

/** Whether <main> scrolls sideways — content wider than the screen. */
const mainScrollsSideways = (page: Page) =>
  page.locator("main").evaluate((m) => m.scrollWidth > m.clientWidth + 1);

test.describe("Phone layout", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  for (const path of [
    "/projects", "/project/files", "/tasks", "/issues", "/memories", "/memories/profiles",
    "/terminology", "/integrations", "/ai-settings", "/org", "/org/vendors",
  ]) {
    test(`${path} fits the screen`, async ({ page }) => {
      const url = path === "/project/files" ? `/projects/${await getSeedProjectId(page)}/files` : path;
      await page.goto(url);
      await expect(page.locator("main h1, main nav[aria-label=Breadcrumb]").first()).toBeVisible();
      expect(await mainScrollsSideways(page)).toBe(false);
    });
  }

  test("a table wider than the screen scrolls inside its own box", async ({ page }) => {
    await page.goto(`/projects/${await getSeedProjectId(page)}/files`);
    const table = page.getByRole("table").first();
    await expect(table).toBeVisible();
    const box = await table.evaluate((t) => {
      const b = t.parentElement!;
      return { scrolls: b.scrollWidth > b.clientWidth, overflowX: getComputedStyle(b).overflowX };
    });
    expect(box).toEqual({ scrolls: true, overflowX: "auto" });
    expect(await mainScrollsSideways(page)).toBe(false);
  });

  test("the menu opens over the page and closes on a page pick or a tap beside it", async ({ page }) => {
    const rail = page.getByTestId("nav-rail");
    await expect(rail).toHaveAttribute("data-collapsed", "true");
    const mainWidth = () => page.locator("main").evaluate((m) => m.clientWidth);
    const before = await mainWidth();

    await page.getByTestId("nav-rail-toggle").click();
    await expect(rail).toHaveAttribute("data-collapsed", "false");
    await expect(page.getByTestId("nav-rail-backdrop")).toBeVisible();
    // Lying over the page, the open menu takes no width from it.
    expect(await mainWidth()).toBe(before);

    await page.getByTestId("nav-rail-backdrop").click({ position: { x: 380, y: 400 } });
    await expect(rail).toHaveAttribute("data-collapsed", "true");

    await page.getByTestId("nav-rail-toggle").click();
    await rail.getByRole("link", { name: "Translation memories" }).click();
    await expect(page).toHaveURL(/\/memories$/);
    await expect(rail).toHaveAttribute("data-collapsed", "true");
  });

  test("the organization switcher opens its menu on screen", async ({ page }) => {
    await page.getByRole("button", { name: /switch organization/ }).click();
    const current = page.getByRole("menuitem").first();
    await expect(current).toBeVisible();
    const right = await current.evaluate((el) => el.getBoundingClientRect().right);
    expect(right).toBeLessThanOrEqual(393);
    await page.keyboard.press("Escape");
  });
});
