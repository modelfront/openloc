import { test, expect, type Page } from "@playwright/test";
import { authHeaders, createIsolatedCATJob, login } from "./helpers";

// Reject in the CAT editor: the dialog shows the job's whole workflow, picks
// the nearest earlier step, blocks the current one, and sends the job back to
// the picked step. Sending back several steps and the AI re-run are covered
// by the Go integration tests (internal/workflow/rollback_integration_test.go).

interface Step {
  id: number;
  step_name: string;
  status: string;
}

async function steps(page: Page, jobId: number): Promise<Step[]> {
  const res = await page.request.get(`/api/jobs/${jobId}/workflow/steps`, { headers: await authHeaders(page) });
  expect(res.ok()).toBe(true);
  return (await res.json()).data;
}

test.describe("Reject step", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("the dialog shows the workflow and sends the job back to the picked step", async ({ page }) => {
    test.setTimeout(60_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `reject-rollback-${Date.now()}.txt`,
      content: "Hello rollback world\nSecond rollback line\n",
    });
    try {
      const advance = await page.request.post(`/api/jobs/${job.jobId}/workflow/advance`, { headers: await authHeaders(page) });
      expect(advance.ok(), await advance.text()).toBe(true);
      const [first, second] = await steps(page, job.jobId);
      expect(second.status).toBe("in_progress");

      await job.navigateToEditor(/Hello rollback world/);
      await page.getByTestId("task-reject-btn").click();
      const dialog = page.getByRole("dialog");
      await expect(dialog.getByTestId("workflow-step-option-0")).toContainText(first.step_name);
      await expect(dialog.getByTestId("workflow-step-option-0")).toContainText("Completed");
      await expect(dialog.getByTestId("workflow-step-option-1")).toContainText("In progress");
      await expect(dialog.getByTestId("workflow-step-pick-0")).toBeChecked();
      await expect(dialog.getByTestId("workflow-step-pick-1")).toBeDisabled();
      // A human step has no AI to run again.
      await expect(dialog.getByTestId("reject-rerun-ai")).toHaveCount(0);

      await dialog.getByLabel(/rejection reason/i).fill("Please check the terms");
      const rejected = page.waitForResponse(r => r.url().endsWith(`/jobs/${job.jobId}/workflow/reject`) && r.request().method() === "POST");
      await dialog.getByTestId("reject-confirm").click();
      const res = await rejected;
      expect(res.ok()).toBe(true);
      expect(res.request().postDataJSON()).toMatchObject({ to_step_id: first.id, rerun_ai: false, reason: "Please check the terms" });
      await expect(dialog).toBeHidden();

      const after = await steps(page, job.jobId);
      expect(after.map(s => s.status)).toEqual(["in_progress", "rejected"]);
    } finally {
      await job.cleanup();
    }
  });
});
