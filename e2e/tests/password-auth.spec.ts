import { test, expect, type Page } from "@playwright/test";
import { login } from "./helpers";
import { emailedLink } from "./outbox";
import { SEED_ORG_SLUG } from "../seed-identity";

/**
 * Email+password auth journey: owner invites a fresh email → the
 * invitee opens the emailed invite link, sets a password, lands in the app →
 * signs out → signs back in with email+password.
 *
 * The invite link comes from the dev outbox: the API never returns the token.
 * The reset-email flow is covered by Go integration tests; the platform
 * create-org chain is likewise covered in Go (PLATFORM_ADMIN_EMAILS isn't set
 * in the E2E env).
 */

// Create an invite via API as the seed owner and return {id, ownerToken}.
// storageState only carries localStorage (the Bearer token lives there), and
// page.request doesn't send it — so dev-login and select the seed org
// (the seed identity belongs to 2 orgs, so dev-login alone yields an
// org-unscoped identity token that tenant routes reject).
async function createInviteViaAPI(page: Page, email: string, role: string) {
  const loginRes = await page.request.post(`/api/auth/dev-login`, { data: {} });
  expect(loginRes.ok(), `dev-login: ${loginRes.status()}`).toBe(true);
  const identity = await loginRes.json();
  const org = (identity.orgs ?? []).find((o: any) => o.tenant_slug === SEED_ORG_SLUG);
  expect(org, `seed org '${SEED_ORG_SLUG}' in dev-login orgs`).toBeTruthy();
  const selectRes = await page.request.post(`/api/auth/orgs/${org.tenant_id}/select`, {
    headers: { Authorization: `Bearer ${identity.token}` },
  });
  expect(selectRes.ok(), `select org: ${selectRes.status()}`).toBe(true);
  const ownerToken = (await selectRes.json()).token;
  const res = await page.request.post(`/api/invites`, {
    data: { email, role },
    headers: { Authorization: `Bearer ${ownerToken}` },
  });
  expect(res.ok(), `create invite: ${res.status()}`).toBe(true);
  const body = await res.json();
  return { id: body.data.id as number, ownerToken };
}

test.describe("Password auth", () => {
  // Cleanup state lives at describe scope so afterEach can reach it.
  let createdUserId: number | undefined;
  let createdOwnerToken = "";

  // Cleanup belongs here, NOT in a `finally` inside the test: a `finally`
  // shares the test's timeout budget, so under load the DELETE can be killed
  // after every assertion passed. afterEach has its own budget. Best-effort:
  // a cleanup failure must not mask a real result.
  test.afterEach(async ({ request }) => {
    if (!createdUserId || !createdOwnerToken) return;
    await request
      .delete(`/api/members/${createdUserId}`, {
        headers: { Authorization: `Bearer ${createdOwnerToken}` },
      })
      .catch(() => {});
    createdUserId = undefined;
    createdOwnerToken = "";
  });

  test("invite → set password → land → sign back in with password", async ({ page }) => {
    const email = `pw-e2e-${Date.now()}@test.com`;
    const password = "e2e-password-42";

    const invite = await createInviteViaAPI(page, email, "project_manager");
    createdOwnerToken = invite.ownerToken;

    // The accept page validates the token server-side and shows the org+email.
    await page.goto(await emailedLink(page, email, "/invite/", invite.ownerToken));
    await expect(page.getByText(email)).toBeVisible();

    // New identity → the set-password form is offered.
    await page.getByLabel("Name").fill("E2E Password Person");
    await page.getByLabel("Password", { exact: true }).fill(password);
    await page.getByLabel("Confirm password").fill(password);
    const acceptResp = page.waitForResponse((r) => r.url().includes("/api/auth/accept-invite"));
    await page.getByRole("button", { name: /create account & join/i }).click();
    const accepted = await acceptResp;
    expect(accepted.status(), await accepted.text()).toBe(200);
    createdUserId = (await accepted.json()).user.id as number;

    // Single org → straight into the app as that user.
    await expect(page.getByRole("heading", { name: /projects/i })).toBeVisible();

    // Sign back in with email+password. Block dev auto-login so LoginPage
    // doesn't clobber the form flow (see testing.md multi-context pattern).
    await page.evaluate(() => localStorage.removeItem("token"));
    await page.context().clearCookies();
    await page.route("**/api/auth/dev-login", (r) => r.abort());
    await page.goto("/login?signedout=1");

    await page.getByLabel("Email").fill(email);
    await page.getByLabel("Password", { exact: true }).fill(password);
    const loginResp = page.waitForResponse((r) => r.url().includes("/api/auth/login"));
    await page.getByRole("button", { name: /^sign in$/i }).click();
    expect((await loginResp).status()).toBe(200);
    await expect(page.getByRole("heading", { name: /projects/i })).toBeVisible();

    // Wrong password shows the uniform error and stays on the page.
    await page.evaluate(() => localStorage.removeItem("token"));
    await page.context().clearCookies();
    await page.goto("/login?signedout=1");
    await page.getByLabel("Email").fill(email);
    await page.getByLabel("Password", { exact: true }).fill("wrong-password-1");
    const badResp = page.waitForResponse((r) => r.url().includes("/api/auth/login"));
    await page.getByRole("button", { name: /^sign in$/i }).click();
    expect((await badResp).status()).toBe(401);
    await expect(page.getByText(/invalid email or password/i)).toBeVisible();
  });

  test("invalid or expired invite link shows a clear error", async ({ page }) => {
    await page.goto("/invite/definitely-not-a-real-token");
    await expect(page.getByText(/invalid or has expired/i)).toBeVisible();
    await expect(page.getByRole("link", { name: /sign in/i })).toBeVisible();
  });

  test("login page offers the password form and forgot-password link", async ({ page }) => {
    // Block auto-login so the form is actually visible in dev mode.
    await page.route("**/api/auth/dev-login", (r) => r.abort());
    await page.goto("/login?signedout=1");
    await expect(page.getByLabel("Email")).toBeVisible();
    await expect(page.getByLabel("Password", { exact: true })).toBeVisible();
    await page.getByRole("link", { name: /forgot password/i }).click();
    await expect(page.getByRole("heading", { name: /reset password/i })).toBeVisible();

    // The forgot form always confirms without revealing account existence.
    await page.getByLabel("Email").fill("nobody-here@test.com");
    const resp = page.waitForResponse((r) => r.url().includes("/api/auth/forgot-password"));
    await page.getByRole("button", { name: /send reset link/i }).click();
    expect((await resp).status()).toBe(200);
    await expect(page.getByText(/if an account exists/i)).toBeVisible();
  });

  test("seed owner session lands (storageState sanity)", async ({ page }) => {
    await login(page);
  });
});
