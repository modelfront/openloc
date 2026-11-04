import { test, expect, type Page } from "@playwright/test";
import { getSeedProjectId, login, loginAs, openTaskSteps } from "./helpers";
import { SEED_LINGUIST_EMAIL } from "../seed-identity";

const API = "";

test.describe("Vendors + Tasks", () => {
  // An invite a test created, revoked after it: the hook has its own time
  // budget, so a slow test body cannot leave the invite behind.
  let createdInvite: { id: number; headers: Record<string, string> } | undefined;
  test.afterEach(async ({ request }) => {
    if (!createdInvite) return;
    await request.delete(`${API}/api/invites/${createdInvite.id}`, { headers: createdInvite.headers }).catch(() => {});
    createdInvite = undefined;
  });

  // Separate tests, not one: each page load is a full app load from the dev
  // server, and several of them do not fit one test's time budget on CI.
  test("an in-house linguist sees only Tasks in the navigation", async ({ page }) => {
    await loginAs(page, SEED_LINGUIST_EMAIL);
    await page.goto("/tasks");

    await expect(page.getByRole("link", { name: "Tasks" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Projects" })).toHaveCount(0);
    await expect(page.getByRole("link", { name: "Translation memories" })).toHaveCount(0);
  });

  for (const path of ["/", "/org", "/org/vendors", "/org/settings"]) {
    test(`a linguist opening ${path} is sent to Tasks`, async ({ page }) => {
      await loginAs(page, SEED_LINGUIST_EMAIL);
      await page.goto(path);
      await expect(page).toHaveURL(/\/tasks$/);
    });
  }

  test("a linguist can open an assigned job from its task card", async ({ page }) => {
    await loginAs(page, SEED_LINGUIST_EMAIL);
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

  // Inviting a vendor org from the Members tab: vendor-orgs.spec.ts.
  test("the old vendor addresses land on the Members tab", async ({ page }) => {
    await login(page); // owner via dev-login
    for (const path of ["/vendors", "/org/vendors"]) {
      await page.goto(path);
      await expect(page, `${path} lands on Members`).toHaveURL(/\/org$/);
    }
    await expect(
      page.getByRole("navigation", { name: "Section" }).getByRole("link"),
    ).toHaveText(["Members", "Settings"]);
  });

  // An invited address is user text with no spaces. The pending row has to
  // absorb it: its actions stay inside the card and the page does not scroll sideways.
  test("a long invite address does not push the row's actions out of the card", async ({ page }) => {
    await login(page); // owner via dev-login
    const email = `${"vendor".repeat(12)}-${Date.now()}@test.com`;
    const headers = await authHeaders(page);

    const res = await page.request.post(`${API}/api/invites`, {
      headers,
      data: { email, role: "linguist" },
    });
    expect(res.status(), `invite: ${await res.text()}`).toBe(201);
    const inviteId: number = (await res.json()).data.id;
    createdInvite = { id: inviteId, headers };

    await page.goto("/org");
    const row = page.getByTestId(`org-invite-row-${inviteId}`);
    await expect(row).toBeVisible();

    const box = await row.evaluate((el) => ({
      rowRight: el.getBoundingClientRect().right,
      lastRight: el.lastElementChild!.getBoundingClientRect().right,
      rowScrolls: el.scrollWidth > el.clientWidth,
    }));
    expect(box.rowScrolls, "row wider than its card").toBe(false);
    expect(box.lastRight, "actions pushed past the row").toBeLessThanOrEqual(box.rowRight + 0.5);
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

  test("assigning a job to a linguist surfaces it on their Tasks page", async ({ page }) => {
    // Owner assigns a demo job to the seeded linguist via the assign endpoint.
    await login(page);
    const headers = await authHeaders(page);
    const projectId = await getSeedProjectId(page);

    // Find a demo-project job + the linguist's user id.
    const jobsRes = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    expect(jobsRes.ok()).toBe(true);
    const jobs = (await jobsRes.json()).data;
    expect(jobs.length).toBeGreaterThan(0);
    const jobId = jobs[jobs.length - 1].id; // the seed gives the linguist the lowest-id job, not the last

    const membersRes = await page.request.get(`${API}/api/members`, { headers });
    const members = (await membersRes.json()).data;
    const linguist = members.find((m: any) => m.email === SEED_LINGUIST_EMAIL);
    expect(linguist, "seeded linguist should exist").toBeTruthy();

    // Before the assignment the linguist's Tasks page lists their seeded job but
    // not this one. Wait for the seeded task first, so the empty count below is
    // not just a page that has not loaded.
    await loginAs(page, SEED_LINGUIST_EMAIL);
    await page.goto("/tasks");
    await expect(page.locator('[data-testid^="task-open-"]').first()).toBeVisible();
    await expect(
      page.getByTestId(`task-open-${jobId}`),
      "precondition: the linguist must not see this job before it is assigned",
    ).toHaveCount(0);

    // The owner's token from above still authorizes the assignment.
    const assignRes = await page.request.post(`${API}/api/jobs/${jobId}/assign`, {
      headers,
      data: { user_id: linguist.id },
    });
    expect(assignRes.ok(), `assign: ${assignRes.status()}`).toBe(true);

    // The linguist now sees that job on their Tasks page.
    await page.goto("/tasks");
    await expect(page.getByTestId(`task-open-${jobId}`)).toBeVisible();
  });
});

async function authHeaders(page: Page): Promise<Record<string, string>> {
  const token = await page.evaluate(() => localStorage.getItem("token"));
  return token ? { Authorization: `Bearer ${token}` } : {};
}
