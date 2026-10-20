import { test, expect, type Page } from "@playwright/test";
import { authHeaders, createIsolatedCATJob, login } from "./helpers";

// Complete step in the CAT editor: with segments left unapproved the dialog
// counts drafts and untranslated segments and offers "Approve all and
// complete"; with nothing left to approve the dialog only takes the hand-off
// note and completes.

const LINES = ["Line one", "Line two", "Line three", "Line four"];

interface Row {
  id: number;
  segment_id: number;
  active_text: string | null;
  status: string;
}

async function rows(page: Page, jobId: number): Promise<Row[]> {
  const res = await page.request.get(`/api/jobs/${jobId}/translations`, { headers: await authHeaders(page) });
  expect(res.ok()).toBe(true);
  return ((await res.json()).data as Row[]).sort((a, b) => a.segment_id - b.segment_id);
}

async function draft(page: Page, fileId: number, fixes: { translation_id: number; human_text: string }[]) {
  const res = await page.request.put(`/api/files/${fileId}/translations/batch-draft`, {
    headers: await authHeaders(page), data: { fixes },
  });
  expect(res.ok(), await res.text()).toBe(true);
}

async function activeStepName(page: Page, jobId: number): Promise<string | undefined> {
  const res = await page.request.get(`/api/jobs/${jobId}/workflow/steps`, { headers: await authHeaders(page) });
  expect(res.ok()).toBe(true);
  const steps: { step_name: string; status: string }[] = (await res.json()).data;
  return steps.find(s => s.status === "in_progress")?.step_name;
}

test.describe("Complete step", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("the dialog counts drafts and untranslated segments; Approve all and complete approves the drafts and advances", async ({ page }) => {
    test.setTimeout(60_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `complete-approve-all-${Date.now()}.txt`,
      content: LINES.join("\n"),
    });
    try {
      const before = await rows(page, job.jobId);
      await draft(page, job.fileId, [
        { translation_id: before[0].id, human_text: "Zeile eins" },
        { translation_id: before[1].id, human_text: "Zeile zwei" },
      ]);
      const firstStep = await activeStepName(page, job.jobId);
      await job.navigateToEditor(/Line one/);

      await page.getByTestId("task-complete-btn").click();
      const dialog = page.getByRole("dialog");
      await expect(dialog.getByTestId("complete-unapproved")).toContainText("There are 2 drafts and 2 untranslated segments.");

      const confirmed = page.waitForResponse(r => r.url().endsWith(`/jobs/${job.jobId}/translations/confirm-all`) && r.request().method() === "POST");
      const advanced = page.waitForResponse(r => r.url().endsWith(`/jobs/${job.jobId}/workflow/advance`) && r.request().method() === "POST");
      await dialog.getByTestId("complete-approve-all").click();
      expect((await confirmed).ok()).toBe(true);
      expect((await advanced).ok()).toBe(true);

      // The step-advance reset clears the translator's approvals for the next
      // step, so the step's own record is what shows they were approved.
      const statuses = await page.request.get(`/api/jobs/${job.jobId}/step-statuses`, { headers: await authHeaders(page) });
      expect(statuses.ok()).toBe(true);
      expect(await activeStepName(page, job.jobId)).not.toBe(firstStep);
      const recorded: { translation_id: number; status: string }[] = (await statuses.json()).data.previous;
      const byId = new Map(recorded.map(r => [r.translation_id, r.status]));
      expect(byId.get(before[0].id)).toBe("approved");
      expect(byId.get(before[1].id)).toBe("approved");
      expect(byId.get(before[2].id)).toBe("needs_review");
    } finally {
      await job.cleanup();
    }
  });

  test("a QA error stops Approve all and complete: the rest is approved, the step stays open", async ({ page }) => {
    test.setTimeout(60_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `complete-qa-block-${Date.now()}.txt`,
      content: "Line one\nOrder 42",
    });
    try {
      const before = await rows(page, job.jobId);
      // Standard QA's number check is an error: the target drops the 42.
      await draft(page, job.fileId, [
        { translation_id: before[0].id, human_text: "Zeile eins" },
        { translation_id: before[1].id, human_text: "Bestellung" },
      ]);
      const firstStep = await activeStepName(page, job.jobId);
      await job.navigateToEditor(/Line one/);

      await page.getByTestId("task-complete-btn").click();
      const dialog = page.getByRole("dialog");
      const confirmed = page.waitForResponse(r => r.url().endsWith(`/jobs/${job.jobId}/translations/confirm-all`) && r.request().method() === "POST");
      await dialog.getByTestId("complete-approve-all").click();
      expect((await confirmed).ok()).toBe(true);

      await expect(dialog.getByTestId("complete-approve-blocked")).toContainText("Segment 2");
      expect(await activeStepName(page, job.jobId)).toBe(firstStep);
      expect((await rows(page, job.jobId)).map(r => r.status)).toEqual(["approved", "needs_review"]);
    } finally {
      await job.cleanup();
    }
  });

  test("with every segment approved, the dialog shows no warning and no Approve all; Complete step advances", async ({ page }) => {
    test.setTimeout(60_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `complete-no-dialog-${Date.now()}.txt`,
      content: LINES.slice(0, 2).join("\n"),
    });
    try {
      const before = await rows(page, job.jobId);
      await draft(page, job.fileId, before.map((r, i) => ({ translation_id: r.id, human_text: `Zeile ${["eins", "zwei"][i]}` })));
      const confirm = await page.request.post(`/api/jobs/${job.jobId}/translations/confirm-all`, { headers: await authHeaders(page) });
      expect(confirm.ok()).toBe(true);
      const firstStep = await activeStepName(page, job.jobId);
      await job.navigateToEditor(/Line one/);

      await page.getByTestId("task-complete-btn").click();
      const dialog = page.getByRole("dialog");
      await expect(dialog.getByLabel(/comment for next assignee/i)).toBeVisible();
      await expect(dialog.getByTestId("complete-unapproved")).toHaveCount(0);
      await expect(dialog.getByTestId("complete-approve-all")).toHaveCount(0);

      const advanced = page.waitForResponse(r => r.url().endsWith(`/jobs/${job.jobId}/workflow/advance`) && r.request().method() === "POST");
      await dialog.getByRole("button", { name: "Complete step" }).click();
      expect((await advanced).ok()).toBe(true);
      expect(await activeStepName(page, job.jobId)).not.toBe(firstStep);
    } finally {
      await job.cleanup();
    }
  });
});
