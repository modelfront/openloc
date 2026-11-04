import type { Page } from "@playwright/test";
import {
  authHeaders, expect, freshContext, inviteMember, login, loginAs, openInviteDialog, pickComboboxValue, signInWithToken, test,
} from "./helpers";
import { emailedLink } from "./outbox";
import {
  SEED_ORG_NAME, SEED_VENDOR_ADMIN_EMAIL, SEED_VENDOR_IDLE_ADMIN_EMAIL, SEED_VENDOR_IDLE_LINGUIST_EMAIL,
  SEED_VENDOR_LINGUIST_EMAIL, SEED_VENDOR_ORG_NAME,
} from "../seed-identity";

// Vendor organizations: a client's owner or admin invites a vendor by name and
// admin email; accepting creates the vendor org, linked to the client, with the
// invitee as its admin. A vendor org sees Projects (admin, project manager) and
// Tasks (everyone) only.

/** Invites to revoke after the test, each with the token of the org that sent it. */
let revokeAfter: { id: number; headers: Record<string, string> }[] = [];
/** Members to remove after the test. */
let removeAfter: { id: number; headers: Record<string, string> }[] = [];

// Best-effort, in afterEach and not in a `finally`: a `finally` shares the
// test's time budget, and a cleanup failure must not hide a real result.
test.afterEach(async ({ request }) => {
  for (const { id, headers } of revokeAfter) {
    await request.delete(`/api/invites/${id}`, { headers }).catch(() => {});
  }
  for (const { id, headers } of removeAfter) {
    await request.delete(`/api/members/${id}`, { headers }).catch(() => {});
  }
  revokeAfter = [];
  removeAfter = [];
});

/** Submit an invite form and return the created invite. */
async function submitInvite(page: Page, prefix: string): Promise<{ id: number; role: string; kind: string }> {
  const created = page.waitForResponse((r) => r.url().endsWith("/api/invites") && r.request().method() === "POST");
  await page.getByTestId(`${prefix}-submit`).click();
  const res = await created;
  expect(res.status(), `invite: ${await res.text()}`).toBe(201);
  return (await res.json()).data;
}

/** The left rail's links, by visible name. */
async function navLabels(page: Page): Promise<string[]> {
  return page.getByTestId("nav-rail").getByRole("navigation").getByRole("link").allTextContents();
}

test("an owner invites a vendor org by name and admin email", async ({ page }) => {
  await login(page);
  const headers = await authHeaders(page);
  const vendorName = `E2E Vendor ${Date.now()}`;
  const email = `vendor-admin-${Date.now()}@test.com`;

  await page.goto("/org");
  await openInviteDialog(page, "vendor-invite");
  await page.getByTestId("vendor-invite-org-name").fill(vendorName);
  await page.getByTestId("vendor-invite-email").fill(email);
  const invite = await submitInvite(page, "vendor-invite");
  revokeAfter.push({ id: invite.id, headers });
  expect(invite.kind).toBe("vendor_org");
  expect(invite.role).toBe("admin");

  const row = page.getByTestId(`org-invite-row-${invite.id}`);
  await expect(row).toContainText(email);
  await expect(row.getByTestId(`invite-vendor-name-${invite.id}`)).toHaveText(vendorName);
  await expect(row).toContainText("Vendor org admin");
});

test("the seeded vendor admin sees Projects and Tasks only, and invites a project manager and a linguist", async ({ page }) => {
  await loginAs(page, SEED_VENDOR_ADMIN_EMAIL);
  await page.goto("/memories");
  // Every page outside the vendor org's few goes home.
  await expect(page).toHaveURL(/\/projects$/);
  await expect.poll(() => navLabels(page)).toEqual(["Projects", "Tasks"]);
  await expect(page.getByText(`Projects that ${SEED_ORG_NAME} sends to ${SEED_VENDOR_ORG_NAME} appear here.`)).toBeVisible();
  await expect(page.getByRole("button", { name: "New project" })).toHaveCount(0);

  // The account menu: Organization settings, but no AI settings and no Learn.
  await page.getByRole("button", { name: /account menu/ }).click();
  await expect(page.getByRole("menuitem", { name: "Organization settings" })).toBeVisible();
  await expect(page.getByRole("menuitem", { name: "AI settings" })).toHaveCount(0);
  await expect(page.getByRole("menuitem", { name: "Learn" })).toHaveCount(0);
  await page.getByRole("menuitem", { name: "Organization settings" }).click();

  await expect(page).toHaveURL(/\/org$/);
  await expect(page.getByText(`Vendor for ${SEED_ORG_NAME}`)).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Section" })).toHaveCount(0);
  await expect(page.getByTestId("vendor-invite-email")).toHaveCount(0);

  const headers = await authHeaders(page);
  const stamp = Date.now();
  const invited: [string, string, string][] = [
    [`vendor-pm-${stamp}@test.com`, "project_manager", "Project manager"],
    [`vendor-linguist-${stamp}@test.com`, "linguist", "Linguist"],
  ];
  for (const [email, role, label] of invited) {
    await openInviteDialog(page, "member-invite");
    await page.getByTestId("member-invite-email").fill(email);
    await pickComboboxValue(page, "member-invite-role", role);
    const invite = await submitInvite(page, "member-invite");
    revokeAfter.push({ id: invite.id, headers });
    expect(invite.role).toBe(role);
    const row = page.getByTestId(`org-invite-row-${invite.id}`);
    await expect(row).toContainText(email);
    await expect(row).toContainText(label);
    // The toast sits bottom-right, over where the next role picker opens.
    const toast = page.locator("[data-sonner-toast]").filter({ hasText: `Invite sent to ${email}` });
    await toast.getByRole("button", { name: /close/i }).click();
    await expect(toast).toHaveCount(0);
  }
});

test("the seeded vendor linguist sees Tasks only", async ({ page }) => {
  await loginAs(page, SEED_VENDOR_LINGUIST_EMAIL);
  await page.goto("/projects");
  await expect(page).toHaveURL(/\/tasks$/);
  await expect.poll(() => navLabels(page)).toEqual(["Tasks"]);
  await page.goto("/org");
  await expect(page).toHaveURL(/\/tasks$/);
});

// Read-only: Enable is covered by the API and component tests, and no API
// disables a member again, so the seed's idle people stay disabled.
test("a member disabled for disuse is told why; the vendor admin and the client see Enable", async ({ page }) => {
  // The test starts as the seed owner, in the client org. The Disabled chip
  // is a label; the row opens the window that holds Enable. Nothing here
  // clicks it.
  await login(page);
  await page.goto("/org#vendors");
  const vendorRow = page.getByRole("row").filter({ hasText: SEED_VENDOR_ADMIN_EMAIL });
  await expect(vendorRow.getByTestId("member-disabled")).toBeVisible();
  await vendorRow.click();
  const vendorDialog = page.getByRole("dialog", { name: SEED_VENDOR_ORG_NAME });
  await expect(vendorDialog.getByRole("button", { name: `Enable ${SEED_VENDOR_IDLE_ADMIN_EMAIL}` })).toBeVisible();
  // The client enables admins only, and an enabled admin has nothing to enable.
  await expect(vendorDialog.getByText(SEED_VENDOR_IDLE_LINGUIST_EMAIL)).toHaveCount(0);
  await expect(vendorDialog.getByRole("button", { name: `Enable ${SEED_VENDOR_ADMIN_EMAIL}` })).toHaveCount(0);
  await page.keyboard.press("Escape");

  await loginAs(page, SEED_VENDOR_ADMIN_EMAIL);
  await page.goto("/org");
  const memberRow = page.getByTestId(/^member-row-/).filter({ hasText: SEED_VENDOR_IDLE_LINGUIST_EMAIL });
  await expect(memberRow.getByTestId("member-disabled")).toBeVisible();
  await expect(page.getByTestId(/^member-row-/).filter({ hasText: SEED_VENDOR_LINGUIST_EMAIL }).getByTestId("member-disabled")).toHaveCount(0);
  await memberRow.click();
  const memberDialog = page.getByTestId("member-dialog");
  await expect(memberDialog.getByTestId("member-access-disabled")).toContainText("Access disabled");
  await expect(memberDialog.getByRole("button", { name: `Enable ${SEED_VENDOR_IDLE_LINGUIST_EMAIL}` })).toBeVisible();
  await page.keyboard.press("Escape");

  await loginAs(page, SEED_VENDOR_IDLE_LINGUIST_EMAIL);
  await page.goto("/tasks");
  await expect(page).toHaveURL(/\/awaiting-approval$/);
  await expect(page.getByRole("heading", { name: `Your access to ${SEED_VENDOR_ORG_NAME} is disabled` })).toBeVisible();
});

// Needs the invite-accept backend: accepting a vendor-org invite creates the
// vendor org. No API deletes a vendor org, so the org and its admin stay; the
// linguist the test adds is removed.
test("accepting a vendor invite sets up the vendor org, whose admin invites a linguist", async ({ page, browser }) => {
  await login(page);
  const ownerHeaders = await authHeaders(page);
  const stamp = Date.now();
  const vendorName = `E2E Vendor ${stamp}`;
  const adminEmail = `vendor-admin-${stamp}@test.com`;
  const created = await page.request.post("/api/invites", {
    headers: ownerHeaders,
    data: { email: adminEmail, role: "admin", vendor_org_name: vendorName },
  });
  expect(created.status(), `vendor invite: ${await created.text()}`).toBe(201);
  // Revoked if the test stops before the invite is accepted.
  revokeAfter.push({ id: (await created.json()).data.id, headers: ownerHeaders });
  const link = await emailedLink(page, adminEmail, "/invite/");

  const invitee = await (await freshContext(browser)).newPage();
  await invitee.route("**/api/auth/dev-login", (route) => route.abort());
  await invitee.goto(link);
  await expect(invitee.getByTestId("invite-vendor-copy")).toHaveText(
    `Set up ${vendorName} as a vendor for ${SEED_ORG_NAME}. You'll be its admin.`,
  );
  await invitee.getByLabel("Name").fill("E2E Vendor Admin");
  await invitee.getByLabel("Password", { exact: true }).fill("e2e-password-42");
  await invitee.getByLabel("Confirm password").fill("e2e-password-42");
  const accepted = invitee.waitForResponse((r) => r.url().includes("/api/auth/accept-invite"));
  await invitee.getByRole("button", { name: /create account & join/i }).click();
  expect((await accepted).status()).toBe(200);

  // The new org is open, limited like any vendor org, and names its client.
  await expect(invitee.getByRole("heading", { name: "Projects" })).toBeVisible();
  await expect.poll(() => navLabels(invitee)).toEqual(["Projects", "Tasks"]);
  await invitee.goto("/org");
  await expect(invitee.getByText(`Vendor for ${SEED_ORG_NAME}`)).toBeVisible();

  // Its admin brings in a linguist, who sees Tasks only.
  const vendorHeaders = await authHeaders(invitee);
  const linguist = await inviteMember(invitee, `vendor-linguist-${stamp}@test.com`, "linguist", vendorHeaders);
  removeAfter.push({ id: linguist.userId, headers: vendorHeaders });
  const linguistPage = await (await freshContext(browser)).newPage();
  await signInWithToken(linguistPage, linguist.token);
  await linguistPage.goto("/projects");
  await expect(linguistPage).toHaveURL(/\/tasks$/);
  await expect.poll(() => navLabels(linguistPage)).toEqual(["Tasks"]);
});
