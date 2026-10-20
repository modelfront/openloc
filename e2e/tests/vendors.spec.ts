import { test, expect, type Page } from "@playwright/test";
import { getSeedProjectId, login, loginAs, openTaskSteps } from "./helpers";
import { emailedLink } from "./outbox";
import { SEED_VENDOR_EMAIL } from "../seed-identity";

const API = "";

test.describe("Vendors + Tasks", () => {
  // Separate tests, not one: each page load is a full app load from the dev
  // server, and several of them do not fit one test's time budget on CI.
  test("vendor sees only Tasks in the navigation", async ({ page }) => {
    await loginAs(page, SEED_VENDOR_EMAIL);
    await page.goto("/tasks");

    await expect(page.getByRole("link", { name: "Tasks" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Projects" })).toHaveCount(0);
    await expect(page.getByRole("link", { name: "Translation memories" })).toHaveCount(0);
  });

  for (const path of ["/", "/org", "/org/vendors", "/org/settings"]) {
    test(`a vendor opening ${path} is sent to Tasks`, async ({ page }) => {
      await loginAs(page, SEED_VENDOR_EMAIL);
      await page.goto(path);
      await expect(page).toHaveURL(/\/tasks$/);
    });
  }

  test("vendor can open an assigned job from its task card", async ({ page }) => {
    await loginAs(page, SEED_VENDOR_EMAIL);
    await page.goto("/tasks");

    // The seeded vendor has one assigned job.
    await expect(page.locator('[data-testid^="task-open-"]').first()).toBeVisible();

    // Clicking anywhere on the ROW (not just the filename) opens the CAT
    // editor in a new tab; the task list stays in this one.
    const [editor] = await Promise.all([
      page.waitForEvent("popup"),
      page.locator('[data-testid^="task-row-"]').first().click(),
    ]);
    await expect(editor).toHaveURL(/\/projects\/\d+\/jobs\/\d+\/edit/);
    await expect(editor.getByRole("button", { name: /concordance/i })).toBeVisible({ timeout: 15000 });
    await expect(page).toHaveURL(/\/tasks$/);
    await editor.close();
  });

  test("admin can invite a vendor by email from the Vendors page", async ({ page }) => {
    await login(page); // owner via dev-login
    const email = `vendor-e2e-${Date.now()}@test.com`;
    let inviteId: number | undefined;

    try {
      // The old address still works: it lands on the Organization page's Vendors tab.
      await page.goto("/vendors");
      await expect(page).toHaveURL(/\/org\/vendors$/);
      await expect(page.getByRole("heading", { name: "Organization" })).toBeVisible();
      await expect(
        page.getByRole("navigation", { name: "Section" }).getByRole("link", { name: "Vendors" }),
      ).toHaveAttribute("aria-current", "page");

      await page.getByTestId("vendor-invite-email").fill(email);
      const invitePromise = page.waitForResponse(
        (r) => r.url().endsWith("/api/invites") && r.request().method() === "POST",
      );
      await page.getByTestId("vendor-invite-submit").click();
      const resp = await invitePromise;
      expect(resp.status()).toBe(201);
      inviteId = (await resp.json()).data.id;

      // The pending invite shows up flagged as never-joined.
      await expect(page.getByText(email)).toBeVisible();
      await expect(page.getByText(/not joined yet/i).first()).toBeVisible();
    } finally {
      if (inviteId) {
        const headers = await authHeaders(page);
        await page.request.delete(`${API}/api/invites/${inviteId}`, { headers }).catch(() => {});
      }
    }
  });

  // An invited address is user text with no spaces. The table has to absorb it:
  // the trailing columns stay on screen.
  test("a long invite address does not push the other columns out of the table", async ({ page }) => {
    await login(page); // owner via dev-login
    const email = `${"vendor".repeat(12)}-${Date.now()}@test.com`;
    let inviteId: number | undefined;
    const headers = await authHeaders(page);

    try {
      const res = await page.request.post(`${API}/api/invites`, {
        headers,
        data: { email, role: "linguist" },
      });
      expect(res.status(), `invite: ${await res.text()}`).toBe(201);
      inviteId = (await res.json()).data.id;

      await page.goto("/org/vendors");
      const row = page.getByTestId(`vendor-invite-row-${inviteId}`);
      await expect(row).toBeVisible();

      // The frame clips, so a table wider than it hides its own last columns.
      const box = await page.evaluate(() => {
        const table = document.querySelector("table")!;
        const frame = table.parentElement!;
        return {
          tableWidth: table.scrollWidth,
          frameWidth: frame.clientWidth,
          frameRight: frame.getBoundingClientRect().right,
        };
      });
      expect(box.tableWidth, "table wider than its frame").toBeLessThanOrEqual(box.frameWidth);

      const cell = await row.locator("td").last().boundingBox();
      expect(cell).not.toBeNull();
      expect(cell!.x + cell!.width, "last cell clipped by the frame").toBeLessThanOrEqual(box.frameRight);
    } finally {
      if (inviteId) await page.request.delete(`${API}/api/invites/${inviteId}`, { headers }).catch(() => {});
    }
  });

  // The API never returns invite links, so Resend is how a lost one comes back.
  test("Resend emails a new invite link and the old one stops working", async ({ page }) => {
    await login(page); // owner via dev-login
    const email = `vendor-resend-${Date.now()}@test.com`;
    let inviteId: number | undefined;
    const headers = await authHeaders(page);

    try {
      const res = await page.request.post(`${API}/api/invites`, {
        headers,
        data: { email, role: "linguist" },
      });
      expect(res.status(), `invite: ${await res.text()}`).toBe(201);
      inviteId = (await res.json()).data.id;
      const firstLink = await emailedLink(page, email, "/invite/");

      await page.goto("/org/vendors");
      const resendPromise = page.waitForResponse(
        (r) => r.url().endsWith(`/api/invites/${inviteId}/resend`) && r.request().method() === "POST",
      );
      await page.getByTestId(`vendor-invite-resend-${inviteId}`).click();
      expect((await resendPromise).status()).toBe(200);
      await expect(page.getByText("Invitation sent again")).toBeVisible();

      const secondLink = await emailedLink(page, email, "/invite/");
      expect(secondLink, "resend must email a new link").not.toBe(firstLink);
      const lookup = async (link: string) =>
        (await page.request.get(`${API}/api/invites/lookup/${link.split("/").pop()}`)).status();
      expect(await lookup(firstLink), "the old link must stop working").toBe(404);
      expect(await lookup(secondLink), "the new link must work").toBe(200);
    } finally {
      if (inviteId) await page.request.delete(`${API}/api/invites/${inviteId}`, { headers }).catch(() => {});
    }
  });

  test("Assign from the bulk bar opens the picker without navigating away", async ({ page }) => {
    await login(page);
    const projectId = await getSeedProjectId(page); // seeded Demo Project, one row per job
    const projectUrl = new RegExp(`/projects/${projectId}$`);
    await page.goto(`/projects/${projectId}`);
    await openTaskSteps(page);

    // Selecting a task on the matrix cell must not navigate into the CAT
    // editor.
    await page.locator('[data-testid^="task-select-job:"]').first().click();
    await expect(page).toHaveURL(projectUrl);

    const assignBtn = page.getByTestId("bulk-assign-btn");
    await expect(assignBtn).toBeVisible();
    await assignBtn.click();

    // Member picker open; still on the project page.
    await expect(page).toHaveURL(projectUrl);
    await expect(page.locator('[data-testid^="bulk-assign-user-"]').first()).toBeVisible();
  });

  test("assigning a job to a vendor surfaces it on their Tasks page", async ({ page }) => {
    // Owner assigns a demo job to the seeded vendor via the assign endpoint.
    await login(page);
    const headers = await authHeaders(page);
    const projectId = await getSeedProjectId(page);

    // Find a demo-project job + the vendor's user id.
    const jobsRes = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    expect(jobsRes.ok()).toBe(true);
    const jobs = (await jobsRes.json()).data;
    expect(jobs.length).toBeGreaterThan(0);
    const jobId = jobs[jobs.length - 1].id; // the seed gives the vendor the lowest-id job, not the last

    const membersRes = await page.request.get(`${API}/api/members`, { headers });
    const members = (await membersRes.json()).data;
    const vendor = members.find((m: any) => m.email === SEED_VENDOR_EMAIL);
    expect(vendor, "seeded vendor should exist").toBeTruthy();

    // Before the assignment the vendor's Tasks page lists their seeded job but
    // not this one. Wait for the seeded task first, so the empty count below is
    // not just a page that has not loaded.
    await loginAs(page, SEED_VENDOR_EMAIL);
    await page.goto("/tasks");
    await expect(page.locator('[data-testid^="task-open-"]').first()).toBeVisible();
    await expect(
      page.getByTestId(`task-open-${jobId}`),
      "precondition: the vendor must not see this job before it is assigned",
    ).toHaveCount(0);

    // The owner's token from above still authorizes the assignment.
    const assignRes = await page.request.post(`${API}/api/jobs/${jobId}/assign`, {
      headers,
      data: { user_id: vendor.id },
    });
    expect(assignRes.ok(), `assign: ${assignRes.status()}`).toBe(true);

    // The vendor now sees that job on their Tasks page.
    await page.goto("/tasks");
    await expect(page.getByTestId(`task-open-${jobId}`)).toBeVisible();
  });
});

async function authHeaders(page: Page): Promise<Record<string, string>> {
  const token = await page.evaluate(() => localStorage.getItem("token"));
  return token ? { Authorization: `Bearer ${token}` } : {};
}
