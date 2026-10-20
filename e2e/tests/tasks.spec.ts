// /tasks — one card per job assigned on its current workflow step, with a
// filter bar (search, Step, Deadline). The seed login is an owner, so the list
// holds every assignee's tasks; each test scopes itself with a unique search.
import { test, expect, type Page } from "@playwright/test";
import {
  authHeaders,
  login,
  createProjectViaAPI,
  deleteProjectViaAPI,
  uploadFileViaAPI,
} from "./helpers";

interface ProjectTask {
  id: number;
  job_id: number;
  status: string;
}

// Uploads one file per name into the project and assigns each file's job to
// the signed-in user. Returns the job ids in file order. Call it inside the
// try whose finally deletes the project, so a failed upload or assign still
// cleans up.
async function assignMeTasks(page: Page, projectId: number, fileNames: string[]) {
  const headers = await authHeaders(page);
  const me = await (await page.request.get(`/api/auth/me`, { headers })).json();
  const jobIds: number[] = [];
  for (const name of fileNames) {
    await uploadFileViaAPI(page, projectId, name, "one segment");
  }
  const jobs = await (await page.request.get(`/api/projects/${projectId}/jobs`, { headers })).json();
  for (const name of fileNames) {
    const job = (jobs.data ?? jobs).find((j: { file_name: string }) => j.file_name === name);
    expect(job, `job for ${name}`).toBeTruthy();
    const assigned = await page.request.post(`/api/jobs/${job.id}/assign`, {
      headers,
      data: { user_id: me.data.id },
    });
    expect(assigned.ok(), `assign job: ${assigned.status()} ${await assigned.text()}`).toBe(true);
    jobIds.push(job.id);
  }
  return jobIds;
}

// The /api/tasks response for the list the page shows after a filter change.
const tasksResponse = (page: Page, param: string) =>
  page.waitForResponse(
    (r) => r.url().includes("/api/tasks?") && r.url().includes(param) && r.request().method() === "GET",
  );

test.describe("Tasks", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  // A file name is user text and can be one long unbreakable word. The card
  // has to absorb it: the deadline stays inside the card.
  test("a long file name does not push the deadline out of the card", async ({ page }) => {
    const longName = `${"Supercalifragilisticexpialidocious".repeat(4)}-${Date.now()}.txt`;
    const projectId = await createProjectViaAPI(page, `tasks-longname ${Date.now()}`);

    try {
      const [jobId] = await assignMeTasks(page, projectId, [longName]);
      await page.goto("/tasks");
      const card = page.getByTestId(`task-row-${jobId}`);
      await expect(card).toBeVisible();

      // The clipped name is reachable in full through its tooltip.
      await card.getByTestId(`task-open-${jobId}`).locator('[data-slot="tooltip-trigger"]').hover();
      await expect(page.locator('[data-slot="tooltip-content"]')).toHaveText(longName);

      const fits = await card.evaluate((el) => el.scrollWidth <= el.clientWidth);
      expect(fits, "card content wider than the card").toBe(true);
      const cardBox = (await card.boundingBox())!;
      const chipBox = (await card.getByTestId("deadline-pill").boundingBox())!;
      expect(chipBox.x + chipBox.width, "deadline clipped by the card").toBeLessThanOrEqual(cardBox.x + cardBox.width);
    } finally {
      await deleteProjectViaAPI(page, projectId);
    }
  });

  test("search and Deadline filter the list, and Clear filters brings it back", async ({ page }) => {
    const run = `tfilter${Date.now()}`;
    const projectId = await createProjectViaAPI(page, `Tasks filter ${run}`);

    try {
      const [soonJob, openJob] = await assignMeTasks(page, projectId, [`${run}-soon.txt`, `${run}-open.txt`]);
      // Give the first task a deadline 3 hours out; the second has none.
      const headers = await authHeaders(page);
      const projectTasks: ProjectTask[] = (
        await (await page.request.get(`/api/projects/${projectId}/tasks`, { headers })).json()
      ).data;
      const step = projectTasks.find((t) => t.job_id === soonJob && t.status === "in_progress");
      expect(step, "current step of the first job").toBeTruthy();
      const patched = await page.request.patch(`/api/jobs/${soonJob}/workflow/steps/${step!.id}/deadline`, {
        headers,
        data: { deadline: new Date(Date.now() + 3 * 3600_000).toISOString() },
      });
      expect(patched.ok(), `set deadline: ${patched.status()} ${await patched.text()}`).toBe(true);

      await page.goto("/tasks");
      await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();

      const searched = tasksResponse(page, `q=${run}`);
      await page.getByTestId("task-search").fill(run);
      expect((await searched).ok()).toBe(true);
      await expect(page.getByTestId(`task-row-${soonJob}`)).toBeVisible();
      await expect(page.getByTestId(`task-row-${openJob}`)).toBeVisible();
      await expect(page.locator('[data-testid^="task-row-"]')).toHaveCount(2);

      const dueSoon = tasksResponse(page, "deadline=24h");
      await page.getByTestId("filter-deadline").click();
      await page.getByRole("button", { name: "Due within 24h" }).click();
      expect((await dueSoon).ok()).toBe(true);
      await expect(page.getByTestId(`task-row-${soonJob}`)).toBeVisible();
      await expect(page.getByTestId(`task-row-${openJob}`)).toHaveCount(0);

      const noDeadline = tasksResponse(page, "deadline=none");
      await page.getByTestId("filter-deadline").click();
      await page.getByRole("button", { name: "No deadline" }).click();
      expect((await noDeadline).ok()).toBe(true);
      await expect(page.getByTestId(`task-row-${openJob}`)).toBeVisible();
      await expect(page.getByTestId(`task-row-${soonJob}`)).toHaveCount(0);

      const overdue = tasksResponse(page, "deadline=overdue");
      await page.getByTestId("filter-deadline").click();
      await page.getByRole("button", { name: "Overdue" }).click();
      expect((await overdue).ok()).toBe(true);
      await expect(page.getByText("No tasks match these filters.")).toBeVisible();

      await page.getByTestId("filter-clear-all").click();
      await expect(page).toHaveURL(/\/tasks$/);
      await expect(page.getByTestId("task-search")).toHaveValue("");
      await expect(page.getByTestId(`task-row-${soonJob}`)).toBeVisible();
      await expect(page.getByTestId(`task-row-${openJob}`)).toBeVisible();
    } finally {
      await deleteProjectViaAPI(page, projectId);
    }
  });
});
