import { test, expect, Page } from "@playwright/test";
import { backgroundAndBehind, getSeedProjectId, login } from "./helpers";

// Appearance / palettes: selecting a palette applies + persists, and
// creating a custom palette saves it and makes it active. The preference is
// per-user, so each test resets to a known-clean slate (defaults, no custom
// palettes) before AND after — the tests must not depend on, or leak, DB state.
// Appearance is a single per-user row, so these tests mutate shared state and
// must not run in parallel with each other (workers would clobber the row).
test.describe.configure({ mode: "serial" });

const CLEAN = { light_palette: "paper", dark_palette: "dim", custom_palettes: [] };

async function resetAppearance(page: Page) {
  const token = await page.evaluate(() => localStorage.getItem("token"));
  if (!token) return;
  const res = await page.request.put("/api/appearance", {
    headers: { Authorization: `Bearer ${token}` },
    data: CLEAN,
  });
  expect(res.ok()).toBe(true);
}

test.describe("Appearance palettes", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
    // Clean slate first, then a full navigation so the app mounts with it.
    await resetAppearance(page);
    await page.goto("/settings");
    await expect(
      page.getByRole("heading", { name: /^appearance$/i })
    ).toBeVisible();
    // Force dark mode so the dark palette is the one actively rendered.
    await page.getByRole("radio", { name: /^dark$/i }).click();
  });

  test.afterEach(async ({ page }) => {
    await resetAppearance(page);
  });

  test("selecting a dark palette applies it and it persists across reload", async ({
    page,
  }) => {
    const saved = page.waitForResponse(
      (r) => r.url().includes("/api/appearance") && r.request().method() === "PUT"
    );
    await page.getByTestId("palette-black-true").click();
    expect((await saved).ok()).toBe(true);

    // True black → the page background is pure black.
    await expect
      .poll(() =>
        page.evaluate(() => getComputedStyle(document.body).backgroundColor)
      )
      .toBe("rgb(0, 0, 0)");

    // The palette survives a reload. Two things on this page load at different
    // times: the background colour is read from localStorage, so it is correct
    // straight away, while the palette cards are drawn only after the appearance
    // GET comes back. Wait for that request, or the last check can run before
    // the cards exist when the machine is busy.
    const reloaded = page.waitForResponse(
      (r) => r.url().includes("/api/appearance") && r.request().method() === "GET"
    );
    await page.reload();
    await reloaded;
    await expect
      .poll(() =>
        page.evaluate(() => getComputedStyle(document.body).backgroundColor)
      )
      .toBe("rgb(0, 0, 0)");
    await expect(page.getByTestId("palette-black-true")).toHaveAttribute(
      "data-selected",
      "true"
    );
  });

  test("creating a custom palette saves it and makes it active", async ({
    page,
  }) => {
    // Open the Dark group's "New palette" (first one on the page).
    await page.getByRole("button", { name: /new palette/i }).first().click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();

    await dialog.getByPlaceholder("My palette").fill("E2E Custom");
    // Give it a distinctive page background we can assert on.
    await dialog.getByTestId("palette-hex-bg").fill("#123456");

    const saved = page.waitForResponse(
      (r) => r.url().includes("/api/appearance") && r.request().method() === "PUT"
    );
    await dialog.getByRole("button", { name: /^create$/i }).click();
    expect((await saved).ok()).toBe(true);

    // A new custom card appears, is labelled, and its background is applied live.
    await expect(page.getByText("E2E Custom")).toBeVisible();
    await expect(page.locator('[data-testid^="palette-custom-"]').first()).toBeVisible();
    await expect
      .poll(() =>
        page.evaluate(() =>
          document.documentElement.style.getPropertyValue("--background")
        )
      )
      .toBe("#123456");
  });
});

// Every built-in palette, rendered in its own mode.
const BUILTIN_PALETTES = [
  { id: "paper", mode: "light" },
  { id: "light-default", mode: "light" },
  { id: "mist", mode: "light" },
  { id: "dim", mode: "dark" },
  { id: "black-true", mode: "dark" },
  { id: "graphite", mode: "dark" },
] as const;

test.describe("Surfaces stand apart from what is behind them", () => {
  test.afterEach(async ({ page }) => {
    await resetAppearance(page);
  });

  for (const p of BUILTIN_PALETTES) {
    test(`cards and the rows inside them each differ from what is behind them (${p.id}, ${p.mode})`, async ({ page }) => {
      await login(page);
      const token = await page.evaluate(() => localStorage.getItem("token"));
      const headers = { Authorization: `Bearer ${token}` };
      const projectId = await getSeedProjectId(page);

      const res = await page.request.put("/api/appearance", {
        headers,
        data: { ...CLEAN, [p.mode === "light" ? "light_palette" : "dark_palette"]: p.id },
      });
      expect(res.ok()).toBe(true);
      await page.evaluate(mode => localStorage.setItem("openloc-theme", mode), p.mode);
      // The palette cache in localStorage is written when the appearance GET returns.
      const loaded = page.waitForResponse(r => r.url().endsWith("/api/appearance") && r.request().method() === "GET");
      await page.goto("/org/settings");
      await loaded;
      const card = page.locator("section").filter({ has: page.getByRole("heading", { name: "Analysis weights" }) });
      await expect(card).toBeVisible();

      await expect.poll(async () => {
        const shades = await backgroundAndBehind(card);
        return shades.self !== shades.behind;
      }).toBe(true);

      // A row inside a card: the files table's `bg-muted` header row on its
      // `bg-card` box.
      await page.goto(`/projects/${projectId}/files`);
      const tableHeader = page.locator("thead tr").first();
      await expect(tableHeader).toBeVisible();
      const headerShades = await backgroundAndBehind(tableHeader);
      const sitsOnCard = await tableHeader.evaluate((el) => {
        for (let p = el.parentElement; p; p = p.parentElement) {
          const bg = getComputedStyle(p).backgroundColor;
          if (bg !== "rgba(0, 0, 0, 0)" && bg !== "transparent") return p.classList.contains("bg-card");
        }
        return false;
      });
      expect(sitsOnCard, "the header row's backdrop is a card").toBe(true);
      expect(headerShades.self, "table header row vs its card").not.toBe(headerShades.behind);

      // The signed-out pages render with the cached palette too. `signedout=1`
      // keeps dev auto-login away and leaves the stored token in place.
      await page.goto("/login?signedout=1");
      const loginCard = page.getByRole("heading", { name: "OpenLoc", level: 1 }).locator("..");
      await expect(loginCard).toBeVisible();
      const loginShades = await backgroundAndBehind(loginCard);
      expect(loginShades.self, "login card vs page").not.toBe(loginShades.behind);
    });
  }
});

// The favicon follows the app's theme, not the OS: the OS is dark here, so a
// light icon proves the app's choice won.
test.describe("Favicon follows the app theme", () => {
  const favicon = (page: Page) => page.locator('link[rel="icon"]');

  test("signed in, switching the theme switches the favicon", async ({ page }) => {
    await page.emulateMedia({ colorScheme: "dark" });
    await login(page);
    await page.goto("/settings");
    await expect(page.getByRole("heading", { name: /^appearance$/i })).toBeVisible();

    await page.getByRole("radio", { name: /^light$/i }).click();
    await expect(favicon(page)).toHaveCount(1);
    await expect(favicon(page)).toHaveAttribute("href", /\/favicon\.svg$/);

    await page.getByRole("radio", { name: /^dark$/i }).click();
    await expect(favicon(page)).toHaveAttribute("href", /\/favicon-dark\.svg$/);
  });

  test("signed out, the login page uses the stored theme", async ({ page }) => {
    await page.emulateMedia({ colorScheme: "dark" });
    // `signedout=1` keeps dev auto-login away.
    await page.goto("/login?signedout=1");
    await page.evaluate(() => localStorage.setItem("openloc-theme", "light"));
    await page.reload();
    await expect(page.getByRole("heading", { name: "OpenLoc", level: 1 })).toBeVisible();
    await expect(favicon(page)).toHaveCount(1);
    await expect(favicon(page)).toHaveAttribute("href", /\/favicon\.svg$/);

    await page.evaluate(() => localStorage.setItem("openloc-theme", "dark"));
    await page.reload();
    await expect(favicon(page)).toHaveAttribute("href", /\/favicon-dark\.svg$/);
  });
});
