import { test, expect, type Page } from "@playwright/test";
import { authHeaders, createIsolatedCATJob, login, openTaskSteps } from "./helpers";

// A finished row in the task matrix: Open lands on its last completed task, a
// manager's edit reopens it (recorded in project history), and Complete
// finishes it again. The dev login is an owner, i.e. a manager.

interface Step {
  id: number;
  step_name: string;
  step_type: string;
  status: string;
}

async function listSteps(page: Page, jobId: number): Promise<Step[]> {
  const res = await page.request.get(`/api/jobs/${jobId}/workflow/steps`, { headers: await authHeaders(page) });
  expect(res.ok(), `list steps: ${res.status()}`).toBe(true);
  return (await res.json()).data;
}

async function jobStatus(page: Page, jobId: number): Promise<string> {
  const res = await page.request.get(`/api/jobs/${jobId}`, { headers: await authHeaders(page) });
  expect(res.ok(), `get job: ${res.status()}`).toBe(true);
  return (await res.json()).data.pipeline_status;
}

test.describe("Reopening a completed task by editing it", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("Open lands on the last completed task; an edit reopens it and Complete finishes it again", async ({ page }) => {
    test.setTimeout(60_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `reopen-${Date.now()}.txt`,
      content: "Hello reopen world\nSecond reopen line\n",
    });
    try {
      const headers = await authHeaders(page);
      const steps = await listSteps(page, job.jobId);
      expect(steps.length, "a project has a workflow").toBeGreaterThan(0);
      expect(steps.some((s) => s.step_type === "ai"), "the default workflow is human steps only").toBe(false);

      // Finish the whole workflow and the job through the API.
      for (let i = 0; i < steps.length; i++) {
        const res = await page.request.post(`/api/jobs/${job.jobId}/workflow/advance`, { headers });
        expect(res.ok(), `advance ${i}: ${res.status()} ${await res.text()}`).toBe(true);
      }
      const done = await page.request.post(`/api/jobs/${job.jobId}/complete`, { headers });
      expect(done.ok(), `complete: ${done.status()} ${await done.text()}`).toBe(true);
      expect(await jobStatus(page, job.jobId)).toBe("completed");

      // Open from the task matrix.
      await page.goto(`/projects/${job.projectId}`);
      await openTaskSteps(page);
      // A row click starts the editor in a new tab; the rest of the test drives it.
      const [editor] = await Promise.all([
        page.waitForEvent("popup"),
        page.getByTestId(`task-row-job:${job.jobId}`).locator("td").nth(2).click(),
      ]);
      await expect(editor).toHaveURL(new RegExp(`/projects/${job.projectId}/jobs/${job.jobId}/edit`));
      const banner = editor.getByTestId("task-actions");
      await expect(banner).toHaveAttribute("data-mode", "reopenable");
      await expect(editor.getByTestId("cat-current-step")).toHaveText(steps[steps.length - 1].step_name);

      // Type into a segment; leaving it flushes the draft, and that first save
      // reopens the task.
      await editor.locator("[data-translation-cell]").first().click();
      await expect(editor.locator('[data-testid="segment-editor"] .ProseMirror').first()).toBeVisible();
      const saved = editor.waitForResponse(
        (r) => /\/translations\/\d+\/draft$/.test(r.url()) && r.request().method() === "PUT",
      );
      await editor.keyboard.type("Hola reabierto");
      await editor.locator("[data-translation-cell]").nth(1).click();
      const res = await saved;
      expect(res.ok(), `draft: ${res.status()}`).toBe(true);
      expect((await res.json()).task_reopened, "the first edit reports the reopen").toBe(true);

      await expect(editor.locator("[data-sonner-toast]").filter({ hasText: "Task reopened" })).toBeVisible();
      await expect(banner).toHaveAttribute("data-mode", "editable");
      const after = await listSteps(page, job.jobId);
      expect(after[after.length - 1].status).toBe("in_progress");
      expect(await jobStatus(page, job.jobId)).not.toBe("completed");

      const events = await page.request.get(`/api/projects/${job.projectId}/events?type=workflow.step_reopened`, { headers });
      expect(events.ok()).toBe(true);
      const reopened = ((await events.json()).data as { event_type: string }[]).filter(
        (e) => e.event_type === "workflow.step_reopened",
      );
      expect(reopened, "project history records the reopen once").toHaveLength(1);

      // Complete the reopened task the normal way.
      await editor.getByTestId("task-complete-btn").click();
      const advanced = editor.waitForResponse(
        (r) => r.url().endsWith(`/api/jobs/${job.jobId}/workflow/advance`) && r.request().method() === "POST",
      );
      const completed = editor.waitForResponse(
        (r) => r.url().endsWith(`/api/jobs/${job.jobId}/complete`) && r.request().method() === "POST",
      );
      await editor.getByRole("dialog").getByRole("button", { name: /^Complete (final )?step$/ }).click();
      expect((await advanced).ok()).toBe(true);
      expect((await completed).ok()).toBe(true);
      expect(await jobStatus(page, job.jobId)).toBe("completed");
    } finally {
      await job.cleanup();
    }
  });
});
