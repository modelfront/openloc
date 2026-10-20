import { test, expect, type Page } from "@playwright/test";
import { authHeaders, createIsolatedCATJob, login } from "./helpers";

// Each workflow step takes away the previous step's approvals. The Status column
// marks only segments the previous step auto-approved; the History panel keeps
// the full record. A reject gives the reopened step its own statuses back.

interface Step {
  id: number;
  step_name: string;
  step_type: string;
}

interface Translation {
  id: number;
  segment_id: number;
  status: string;
}

async function listSteps(page: Page, jobId: number): Promise<Step[]> {
  const res = await page.request.get(`/api/jobs/${jobId}/workflow/steps`, { headers: await authHeaders(page) });
  expect(res.ok(), `list steps: ${res.status()}`).toBe(true);
  return (await res.json()).data;
}

async function listTranslations(page: Page, jobId: number): Promise<Translation[]> {
  const res = await page.request.get(`/api/jobs/${jobId}/translations`, { headers: await authHeaders(page) });
  expect(res.ok(), `list translations: ${res.status()}`).toBe(true);
  return ((await res.json()).data as Translation[]).sort((a, b) => a.segment_id - b.segment_id);
}

test.describe("Segment statuses per workflow step", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("the next step starts unconfirmed and records the previous step; a reject restores it", async ({ page }) => {
    test.setTimeout(60_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `step-statuses-${Date.now()}.txt`,
      content: "Hello steps world\nSecond steps line\n",
    });
    try {
      const headers = await authHeaders(page);
      const steps = await listSteps(page, job.jobId);
      expect(steps.length, "the default workflow has two human steps").toBeGreaterThanOrEqual(2);
      const [first, second] = steps;

      // Step 1: confirm the first segment, leave the second alone.
      const [a, b] = await listTranslations(page, job.jobId);
      const confirm = await page.request.put(`/api/files/${job.fileId}/translations/${a.id}`, {
        headers,
        data: { human_text: "Hola mundo de pasos", editing_time_ms: 0 },
      });
      expect(confirm.ok(), `confirm: ${confirm.status()} ${await confirm.text()}`).toBe(true);
      const advance = await page.request.post(`/api/jobs/${job.jobId}/workflow/advance`, { headers });
      expect(advance.ok(), `advance: ${advance.status()} ${await advance.text()}`).toBe(true);

      // Step 2 starts with nothing confirmed.
      const inReview = await listTranslations(page, job.jobId);
      expect(inReview.map((t) => t.status)).toEqual(["needs_review", "needs_review"]);

      // Approved and not confirmed are not marked in the Status column.
      await job.navigateToEditor(/Hello steps world/);
      await expect(page.locator("[data-translation-cell]")).toHaveCount(2);
      await expect(page.getByTestId("previous-step-chip")).toHaveCount(0);

      // The full record is in the segment's History panel.
      await page.locator("[data-translation-cell]").first().click();
      await expect(page.getByTestId("segment-step-status-row").first()).toContainText(first.step_name);
      await expect(page.getByTestId("segment-step-status-row").first()).toContainText("Edited");

      // Reject back: step 1 gets its own result back.
      const reject = await page.request.post(`/api/jobs/${job.jobId}/workflow/reject`, {
        headers,
        data: { reason: "Please check the second line" },
      });
      expect(reject.ok(), `reject: ${reject.status()} ${await reject.text()}`).toBe(true);
      const back = await listTranslations(page, job.jobId);
      expect(back.find((t) => t.id === a.id)?.status).toBe("approved");
      expect(back.find((t) => t.id === b.id)?.status).toBe("needs_review");

      await page.reload();
      await page.locator("[data-translation-cell]").first().click();
      await expect(page.getByTestId("segment-step-status-row").last()).toContainText(second.step_name);
    } finally {
      await job.cleanup();
    }
  });
});
