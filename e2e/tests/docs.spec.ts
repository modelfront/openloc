import { test, expect } from "@playwright/test";
import { login } from "./helpers";

// In-app user documentation (/docs). Role filtering is
// covered by Go tests (internal/productdocs); this spec proves the wiring:
// public access, the docs frame, page rendering, navigation, search and the
// asset route.

test.describe("Docs pages", () => {
  test("signed out, the docs are public and offer Open app", async ({ browser }) => {
    const ctx = await browser.newContext({ storageState: { cookies: [], origins: [] } });
    expect((await ctx.request.get("/api/docs")).status()).toBe(200);
    const page = await ctx.newPage();
    await page.goto("/docs/guides/cat-editor");
    await expect(page.getByRole("heading", { level: 1, name: "The CAT editor" })).toBeVisible();
    await expect(page.getByText("Open app", { exact: true })).toBeVisible();
    await expect(page.getByTestId("nav-rail")).toHaveCount(0);
    // Only the docs are public. The login page signs in by itself in dev;
    // block that so the page stays where the redirect put it.
    await page.route("**/api/auth/dev-login", (route) => route.abort());
    await page.goto("/projects");
    await expect(page).toHaveURL(/\/login/);
    await ctx.close();
  });

  test("Docs is in the account menu and opens in a new tab, without the nav rail", async ({ page, context }) => {
    await login(page);
    await page.getByRole("button", { name: /account menu/i }).click();
    const [docs] = await Promise.all([
      context.waitForEvent("page"),
      page.getByRole("menuitem", { name: "Docs" }).click(),
    ]);
    await expect(docs).toHaveURL(/\/docs$/);
    // exact: the always-rendered sr-only "Search docs" dialog title also matches a substring.
    await expect(docs.getByRole("heading", { name: "Docs", exact: true })).toBeVisible();
    await expect(docs.getByTestId("docs-sidebar")).toContainText("Getting started");
    await expect(docs.getByTestId("docs-sidebar")).toContainText("The CAT editor");
    await expect(docs.getByTestId("nav-rail")).toHaveCount(0);
    await docs.getByText("Back to app", { exact: true }).click();
    await expect(docs.getByTestId("nav-rail")).toBeVisible();
    await docs.close();
  });

  test("an article renders with breadcrumb, updated date and body", async ({ page }) => {
    await page.goto("/docs/guides/cat-editor");
    await expect(page.getByRole("heading", { level: 1, name: "The CAT editor" })).toBeVisible();
    await expect(page.getByRole("navigation", { name: "Breadcrumb" })).toContainText("Docs/Guides");
    await expect(page.getByTestId("docs-updated")).toContainText(/Updated .*20\d\d/);
    // The body is real content, not the stub notice.
    await expect(page.getByTestId("docs-article").getByRole("heading", { level: 2 }).first()).toBeVisible();
    await expect(page.getByText("This page is being written")).toHaveCount(0);
  });

  test("'On this page' jumps to a section and highlights it while scrolling", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 }); // xl: the TOC column is hidden below 1280px
    await page.goto("/docs/guides/cat-editor");
    const toc = page.getByTestId("docs-toc");
    await expect(toc).toBeVisible();
    const links = toc.getByRole("link");
    // Before any scroll the first heading is current.
    await expect(links.first()).toHaveAttribute("aria-current", "location");
    // Jump to a later section: the page scrolls there and the highlight follows.
    const target = links.nth(4);
    const targetId = (await target.getAttribute("href"))!.slice(1);
    await target.click();
    await expect(page.locator(`#${targetId}`)).toBeInViewport();
    await expect(target).toHaveAttribute("aria-current", "location");
    await expect(links.first()).not.toHaveAttribute("aria-current", "location");
  });

  test("a deep link with a hash scrolls to that section after the page loads", async ({ page }) => {
    await page.goto("/docs/getting-started/welcome#the-main-objects");
    await expect(page.locator("#the-main-objects")).toBeInViewport();
    // A cross-page link with a hash, as written inside the docs (router
    // navigation, no full page load), lands on the section too.
    await page.goto("/docs/reference/glossary");
    await page.locator('[data-testid="docs-article"] a[href="/docs/guides/cat-editor#work-with-tags"]').first().click();
    await expect(page).toHaveURL(/\/docs\/guides\/cat-editor#work-with-tags$/);
    await expect(page.locator("#work-with-tags")).toBeInViewport();
  });

  test("a section landing page lists its pages", async ({ page }) => {
    await page.goto("/docs/reference");
    await expect(page.getByTestId("docs-section")).toBeVisible();
    await page.getByRole("link", { name: /Roles and permissions/ }).first().click();
    await expect(page).toHaveURL(/\/docs\/reference\/roles-and-permissions$/);
    await expect(page.getByRole("heading", { level: 1, name: "Roles and permissions" })).toBeVisible();
  });

  test("an unknown page shows a not-found message, not a blank screen", async ({ page }) => {
    await page.goto("/docs/guides/does-not-exist");
    await expect(page.getByText(/does not exist or is not available for your role/)).toBeVisible();
  });

  test("search finds a page and navigates to it", async ({ page }) => {
    await page.goto("/docs");
    await page.getByTestId("docs-search-trigger").click();
    const input = page.getByTestId("docs-search-input");
    await expect(input).toBeVisible();
    const searched = page.waitForResponse(r => r.url().includes("/api/docs/search?q=") && r.status() === 200);
    await input.fill("confirm segment");
    await searched;
    const first = page.getByTestId("docs-search-results").locator("[cmdk-item]").first();
    await expect(first).toBeVisible();
    await first.click();
    await expect(page).toHaveURL(/\/docs\/[a-z-]+\/[a-z-]+$/);
    await expect(page.getByTestId("docs-article")).toBeVisible();
  });

  test("Cmd/Ctrl+K opens search only on the docs page", async ({ page }) => {
    await page.goto("/docs");
    // The page is a lazy chunk; its key listener exists only once it has rendered.
    await expect(page.getByTestId("docs-home")).toBeVisible();
    await page.keyboard.press("ControlOrMeta+k");
    await expect(page.getByTestId("docs-search-input")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("docs-search-input")).toHaveCount(0);
  });

  test("the asset route serves images and rejects anything else", async ({ page }) => {
    await login(page);
    const ok = await page.request.get("/api/docs/assets/cat-editor.png");
    expect(ok.status()).toBe(200);
    expect(ok.headers()["content-type"]).toBe("image/png");
    const missing = await page.request.get("/api/docs/assets/nope.png");
    expect(missing.status()).toBe(404);
  });
});
