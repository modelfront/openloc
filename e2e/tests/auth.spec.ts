import { test, expect, type Page } from "@playwright/test";
import { signInWithToken } from "./helpers";
import { SEED_ORG_NAME, SEED_ORG_SLUG, SEED_OWNER_EMAIL } from "../seed-identity";

// The seed identity belongs to multiple orgs, so dev-login returns an
// identity token + needs_org_selection. Resolve it to a tenant-scoped token by
// selecting the seed org (where the seed data lives).
async function resolveOrgToken(page: Page, body: any): Promise<string> {
  if (!body.needs_org_selection) return body.token;
  const orgs: Array<{ tenant_id: number; tenant_slug: string }> = body.orgs || [];
  const org = orgs.find((o) => o.tenant_slug === SEED_ORG_SLUG) || orgs[0];
  const sel = await page.request.post(
    `/api/auth/orgs/${org.tenant_id}/select`,
    { headers: { Authorization: `Bearer ${body.token}` } },
  );
  expect(sel.ok()).toBe(true);
  return (await sel.json()).token;
}

// These tests exercise the logged-OUT login/auth flows, so opt out of the
// global storageState (global-setup.ts) that pre-authenticates every other
// test — start each with an empty auth state.
test.use({ storageState: { cookies: [], origins: [] } });

test.describe.configure({ mode: "serial" });
test.describe("Authentication", () => {
  test("redirects unauthenticated users to login", async ({ page }) => {
    // Order matters: register the dev-login abort and clear state on
    // about:blank (no auto-login fires there) before any app page loads.
    // /login auto-logs in on mount, and a route added after that request is
    // in flight does not catch it. Truly unauthenticated → redirect MUST land
    // on /login. No OR-fallback.
    await page.goto("about:blank");
    await page.context().clearCookies();
    await page.route("**/api/auth/dev-login", (route) => route.abort());

    await page.goto("/templates");
    await expect(page).toHaveURL(/\/login/, { timeout: 5000 });
  });

  test("login page shows dev login button before auto-login fires", async ({
    page,
  }) => {
    // With DEV_AUTO_LOGIN=true, the login page auto-logs in immediately.
    // Intercept the auth config to disable auto-login so we can inspect the page.
    await page.route("**/api/auth/config", (route) => {
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          google_enabled: false,
          dev_login_enabled: true,
        }),
      });
    });
    // Block the dev-login POST so auto-login doesn't navigate away
    await page.route("**/api/auth/dev-login", (route) => {
      route.abort();
    });

    // Clear session and navigate
    await page.context().clearCookies();
    await page.goto("/login");
    await page.evaluate(() => localStorage.clear());
    await page.reload();

    // Now we should see the login page with the dev login button
    await expect(
      page.getByRole("heading", { name: /openloc/i })
    ).toBeVisible({ timeout: 5000 });
    await expect(page.getByText(/sign in/i).first()).toBeVisible();
    await expect(
      page.getByRole("button", { name: /dev login/i })
    ).toBeVisible();
  });

  test("dev login flow works (API returns token, app accepts it)", async ({
    page,
  }) => {
    // Test that the dev-login API endpoint works and the token grants access.
    // We test the API + token flow directly rather than the UI auto-login redirect,
    // which is timing-sensitive under parallel test load.
    const response = await page.request.post(
      "/api/auth/dev-login"
    );
    expect(response.ok()).toBe(true);
    const body = await response.json();
    expect(body.token).toBeTruthy();
    expect(body.user.email).toBe(SEED_OWNER_EMAIL);

    // Resolve a tenant token, inject it, and verify the app accepts it.
    const token = await resolveOrgToken(page, body);
    await signInWithToken(page, token);
    await page.goto("/");

    await expect(
      page.getByRole("heading", { name: /projects/i })
    ).toBeVisible();
  });

  test("after login, user can access protected routes", async ({ page }) => {
    // Login via dev-login API
    const response = await page.request.post(
      "/api/auth/dev-login"
    );
    const body = await response.json();
    const token = await resolveOrgToken(page, body);

    await signInWithToken(page, token);
    await page.goto("/");

    await expect(
      page.getByRole("heading", { name: /projects/i })
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: /templates/i })
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: /translation memor/i })
    ).toBeVisible();
  });

  // The org picker. Dev auto-login lands here (2 seed
  // orgs); selecting one enters the app, and the header shows the current org.
  test("org picker: dev auto-login lands on picker, selecting an org enters the app", async ({
    page,
  }) => {
    await page.context().clearCookies();
    await page.goto("/login");
    await page.evaluate(() => localStorage.clear());

    // Dev auto-login fires on the login page → identity has 2 orgs → picker.
    await expect(page).toHaveURL(/\/select-org/, { timeout: 8000 });
    await expect(
      page.getByRole("heading", { name: /choose an organization/i })
    ).toBeVisible();
    await expect(page.getByRole("button", { name: SEED_ORG_NAME })).toBeVisible();
    await expect(page.getByRole("button", { name: /acme localization/i })).toBeVisible();

    // Enter the seed org → app. The workspace switcher shows the current
    // org's name in the top header, so it proves which org we landed in.
    await page.getByRole("button", { name: SEED_ORG_NAME }).click();
    await expect(page.getByRole("heading", { name: /projects/i })).toBeVisible();
    const switcher = page.getByRole("button", { name: new RegExp(`${SEED_ORG_NAME} — switch organization`, "i") });
    await expect(switcher).toBeVisible();
    // The name is on screen, not just in the accessible label.
    await expect(switcher).toHaveText(new RegExp(SEED_ORG_NAME));

    // Hovering opens the org menu, left-aligned under the trigger, listing
    // both orgs; moving away closes it again.
    await switcher.hover();
    const menu = page.getByRole("menu");
    await expect(menu.getByRole("menuitem", { name: /acme localization/i })).toBeVisible();
    await expect(menu.getByRole("menuitem", { name: SEED_ORG_NAME })).toBeVisible();
    // The popup zooms in from the trigger; measure once it has settled.
    await menu.evaluate(el => Promise.allSettled(el.getAnimations().map(a => a.finished)));
    const triggerBox = (await switcher.boundingBox())!;
    const menuBox = (await menu.boundingBox())!;
    expect(Math.abs(menuBox.x - triggerBox.x)).toBeLessThanOrEqual(2);
    expect(menuBox.y).toBeGreaterThanOrEqual(triggerBox.y + triggerBox.height);
    // The open menu covers the page below it, so move to a point clear of it.
    await page.mouse.move(menuBox.x + menuBox.width + 200, menuBox.y + menuBox.height + 200);
    await expect(menu).toBeHidden();
  });
});
