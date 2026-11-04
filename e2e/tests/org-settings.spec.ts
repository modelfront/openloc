import { test, expect, type Page } from "@playwright/test";
import { authHeaders, inviteMember, login, openInviteDialog, pickComboboxValue, signInWithToken } from "./helpers";
import { emailedLink } from "./outbox";

const API = "";

test.describe("Org Settings", () => {
  // A member a test created, removed after it with the owner's token. In an
  // afterEach rather than the test's finally: the hook has its own time
  // budget, so a slow test body cannot leave the member behind.
  let createdMember: { userId: number; ownerHeaders: Record<string, string> } | undefined;
  // An invite a test created, revoked after it the same way.
  let createdInvite: { id: number; headers: Record<string, string> } | undefined;
  test.afterEach(async ({ request }) => {
    if (createdInvite) {
      await request.delete(`${API}/api/invites/${createdInvite.id}`, { headers: createdInvite.headers }).catch(() => {});
      createdInvite = undefined;
    }
    if (!createdMember) return;
    await request.delete(`${API}/api/members/${createdMember.userId}`, { headers: createdMember.ownerHeaders }).catch(() => {});
    createdMember = undefined;
  });

  test.beforeEach(async ({ page }) => {
    await login(page);
    await page.getByRole("button", { name: /account/i }).click();
    await page.getByText(/organization settings/i).click();
  });

  // Registration lives on the Settings tab.
  const openSettingsTab = (page: Page) =>
    page.getByRole("navigation", { name: "Section" }).getByRole("link", { name: "Settings" }).click();

  test("shows registration toggle", async ({ page }) => {
    await openSettingsTab(page);
    await expect(page).toHaveURL(/\/org\/settings$/);
    await expect(page.getByText(/registration/i)).toBeVisible();
    const toggle = page.getByRole("switch", { name: /open registration/i });
    await expect(toggle).toBeVisible();
  });

  test("can toggle open registration", async ({ page }) => {
    await openSettingsTab(page);
    const toggle = page.getByRole("switch", { name: /open registration/i });
    await expect(toggle).toBeVisible();

    const initialState = await toggle.getAttribute("aria-checked");
    expect(initialState).toMatch(/^(true|false)$/);

    const settingsSaved = () =>
      page.waitForResponse(r => r.url().endsWith("/api/org/settings") && r.request().method() === "PUT");

    try {
      const [flipped] = await Promise.all([settingsSaved(), toggle.click()]);
      expect(flipped.ok(), `flip open registration: ${flipped.status()}`).toBe(true);
      await expect(toggle).not.toHaveAttribute("aria-checked", initialState!);

      // Toggle back to restore the original state.
      const [restored] = await Promise.all([settingsSaved(), toggle.click()]);
      expect(restored.ok(), `restore open registration: ${restored.status()}`).toBe(true);
      await expect(toggle).toHaveAttribute("aria-checked", initialState!);
    } finally {
      // The flag is tenant-wide: put the original value back even when a step above failed.
      const res = await page.request.put(`${API}/api/org/settings`, {
        headers: await authHeaders(page),
        data: { open_registration: initialState === "true" },
      });
      expect(res.ok(), `restore org settings: ${res.status()} ${await res.text()}`).toBe(true);
    }
  });

  // Owners and admins invite team members and vendors from the Members tab.
  test("the Organization page has Members and Settings tabs", async ({ page }) => {
    const tabs = page.getByRole("navigation", { name: "Section" });
    await expect(tabs.getByRole("link")).toHaveText(["Members", "Settings"]);
    await expect(page).toHaveURL(/\/org$/);
    await openInviteDialog(page, "member-invite");
    await expect(page.getByTestId("member-invite-email")).toBeVisible();
    await page.keyboard.press("Escape");
    await openInviteDialog(page, "vendor-invite");
    await expect(page.getByTestId("vendor-invite-email")).toBeVisible();
  });

  test("a project manager has no Organization page", async ({ page }) => {
    // Five full page loads — sign-in, then each redirect checked from a fresh
    // load — which is more than the default budget under a busy run.
    test.slow();
    const email = `pm-org-${Date.now()}@test.com`;
    const ownerHeaders = await authHeaders(page);
    const pm = await inviteMember(page, email);
    createdMember = { userId: pm.userId, ownerHeaders };
    await signInWithToken(page, pm.token);
    await page.goto("/projects");
    await page.getByRole("button", { name: /account/i }).click();
    await expect(page.getByRole("menuitem", { name: "Settings", exact: true })).toBeVisible();
    await expect(page.getByText(/organization settings/i)).toHaveCount(0);

    for (const path of ["/org", "/org/settings", "/org/vendors", "/vendors"]) {
      await page.goto(path);
      await expect(page, `${path} sends a project manager to Projects`).toHaveURL(/\/projects$/);
    }
  });

  test("a member row opens the member dialog with the role picker", async ({ page }) => {
    // The row only shows the role; it is changed in the dialog.
    const ownerRow = page.locator('[data-testid^="member-row-"]').first();
    await expect(ownerRow).toBeVisible();
    await expect(ownerRow.locator('[data-slot="combobox-trigger"]')).toHaveCount(0);

    await ownerRow.click();
    const dialog = page.getByTestId("member-dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog.getByTestId("pair-row-0")).toBeVisible();

    // Opening the picker reveals the four role options.
    await dialog.getByTestId("member-role").click();
    const popover = page.locator('[data-slot="popover-content"]');
    await expect(popover.getByRole("option")).toHaveCount(4); // owner, admin, project_manager, linguist
  });

  test("can change member role", async ({ page }) => {
    // Self-isolated: invites a second member via API, performs the role
    // change in the UI, asserts the toast; the afterEach removes the member.
    test.setTimeout(20_000);
    const memberEmail = `org-role-test-${Date.now()}@test.com`;
    // Setup: create a second member so the role-change UI has someone to act on.
    // beforeEach already navigated to /org and the member only appears after a
    // page reload, so create THEN reload + re-navigate.
    const ownerHeaders = await authHeaders(page);
    const { userId: memberUserId } = await inviteMember(page, memberEmail);
    createdMember = { userId: memberUserId, ownerHeaders };

    await page.reload();
    await page.getByRole("button", { name: /account/i }).click();
    await page.getByText(/organization settings/i).click();

    // The list is paged, so find the new member by email first.
    await page.getByTestId("member-search").fill(memberEmail);
    // Scope by the new member's row testid: a text-filtered ancestor also
    // contains the owner's row, and changing the owner's role is rejected.
    const newMemberRow = page.getByTestId(`member-row-${memberUserId}`);
    await expect(
      newMemberRow,
      "newly invited member row should be visible",
    ).toBeVisible({ timeout: 5000 });
    const roleChip = newMemberRow.getByTestId(`member-role-${memberUserId}`);
    await expect(roleChip, "role chip for new member").toHaveText("Project manager");

    // Invite created them as project_manager; switch to linguist in the dialog.
    const newRole = "linguist";
    await newMemberRow.click();
    const dialog = page.getByTestId("member-dialog");
    await expect(dialog).toBeVisible();
    await pickComboboxValue(page, "member-role", newRole);
    await dialog.getByTestId("member-save").click();

    // Downgrading an existing member to linguist prompts a confirmation that
    // explains they'll be added to the Vendors list. Confirm to proceed.
    const confirm = page.locator('[data-slot="alert-dialog-content"]');
    await expect(confirm).toBeVisible({ timeout: 3000 });
    await expect(confirm).toContainText(/only the tasks assigned to them/i);
    const saved = page.waitForResponse(r => r.url().includes(`/api/members/${memberUserId}/role`) && r.request().method() === "PUT");
    await confirm.getByRole("button", { name: /change to linguist/i }).click();
    expect((await saved).ok(), "role change request should succeed").toBe(true);

    // OBSERVABLE OUTCOME: toast confirms the role change, the dialog closes and the row's chip reflects it
    await expect(page.getByText(/is now/i).first()).toBeVisible({ timeout: 5000 });
    await expect(dialog).toHaveCount(0);
    await expect(roleChip, `row should show new role "${newRole}"`).toHaveText("Linguist");

    // Round-trip via API to confirm the change actually persisted, not just the UI.
    const verifyRes = await page.request.get(`${API}/api/members`, { headers: ownerHeaders });
    expect(verifyRes.ok(), "members list should load").toBe(true);
    const members = (await verifyRes.json()).data;
    const updated = members.find((m: any) => m.id === memberUserId);
    expect(updated, "updated member should still be in the list").toBeTruthy();
    expect(updated.role, `member ${memberEmail} role should be persisted as "${newRole}"`).toBe(newRole);
  });

  // The API never returns invite links, so Resend is how a lost one comes back.
  test("Resend emails a new invite link from the pending invites list", async ({ page }) => {
    const headers = await authHeaders(page);
    const email = `org-resend-${Date.now()}@test.com`;

    const res = await page.request.post(`${API}/api/invites`, {
      headers,
      data: { email, role: "project_manager" },
    });
    expect(res.status(), `invite: ${await res.text()}`).toBe(201);
    const inviteId: number = (await res.json()).data.id;
    createdInvite = { id: inviteId, headers };
    const firstLink = await emailedLink(page, email, "/invite/");

    await page.goto("/org");
    const resendPromise = page.waitForResponse(
      (r) => r.url().endsWith(`/api/invites/${inviteId}/resend`) && r.request().method() === "POST",
    );
    await page.getByTestId(`org-invite-resend-${inviteId}`).click();
    expect((await resendPromise).status()).toBe(200);
    await expect(page.getByText(`Invite sent again to ${email}`)).toBeVisible();

    const secondLink = await emailedLink(page, email, "/invite/");
    expect(secondLink, "resend must email a new link").not.toBe(firstLink);
    const lookup = async (link: string) =>
      (await page.request.get(`${API}/api/invites/lookup/${link.split("/").pop()}`)).status();
    expect(await lookup(firstLink), "the old link must stop working").toBe(404);
    expect(await lookup(secondLink), "the new link must work").toBe(200);
  });
});
