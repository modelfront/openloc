import { test, expect } from "@playwright/test";
import {
  selectFirstTask,
  login,
  uploadFileViaAPI,
  createProjectViaAPI,
  deleteProjectViaAPI,
  openBulkPretranslate,
} from "./helpers";

/**
 * J32 — Apply TM / MT on a project with no assignment tells the user why
 *
 * "No assignment" flows through a typed PhaseSkipReason in the pipeline
 * outcome. The bulk bar queues the run, so its toast says "queued"; the
 * worker then fails the job with the help message ("No MT engine configured
 * for en → fr — add one in project settings"), which the job's row shows as
 * a red Error with the message on hover.
 *
 * What this journey pins:
 *   - Apply MT on a project with no MT assignment ends with the job's row
 *     naming "MT" + "settings", so the user knows what to do.
 *   - Apply TM on a project with no TM assignment does the same for TM.
 *
 * Locale: en→fr. Locale isn't load-bearing (this is about the
 * config check, not language resolution); en→fr is the universal
 * vanilla pair already used by J31.
 *
 * Anti-tautology proof:
 *   - worker/handler.go: drop finishManualRun's UnactionableMessage check ⇒
 *     the job reads "Pipeline complete", no Error chip; the message
 *     assertion fails.
 */

let projectId: number;

test.describe.serial("J32 — Apply TM/MT with no assignment tells the user why", () => {
  test.afterAll(async ({ browser }) => {
    if (!projectId) return;
    const page = await browser.newPage();
    await login(page);
    await deleteProjectViaAPI(page, projectId);
    await page.close();
  });

  test("A: create bare project en→fr (no template applied → no TM/MT assignments)", async ({ page }) => {
    await login(page);
    projectId = await createProjectViaAPI(page, "J32 No-Config Toasts", "en", ["fr"]);
    expect(projectId).toBeGreaterThan(0);

    const uploaded = await uploadFileViaAPI(
      page,
      projectId,
      "j32.txt",
      "Hello, world.",
      "text/plain",
    );
    expect(uploaded?.data?.id, "uploaded file id").toBeGreaterThan(0);
  });

  async function selectTheJob(page: import("@playwright/test").Page) {
    await selectFirstTask(page);
    await expect(page.getByText("j32.txt")).toBeVisible({ timeout: 10000 });
  }

  // The job's row turns red once the worker has failed it; hovering the chip
  // shows the job's message. Test C reuses the job test B failed, so the
  // job's own message must match first: the chip alone could be B's.
  async function expectRowMessage(page: import("@playwright/test").Page, message: RegExp) {
    const headers = { Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}` };
    await expect.poll(async () => {
      const r = await page.request.get(`/api/projects/${projectId}/jobs`, { headers });
      const [job] = (await r.json()).data as Array<{ error_message?: string | null }>;
      return job?.error_message ?? "";
    }, { message: "the job fails with the help message", timeout: 30_000 }).toMatch(message);
    const chip = page.locator('[data-testid^="task-row-job:"]').getByTestId("task-error-chip").first();
    await expect(chip).toBeVisible({ timeout: 30_000 });
    await chip.hover();
    await expect(page.getByText(message)).toBeVisible();
  }

  test("B: Apply MT is queued, then the job's row names MT + points the user at settings", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}`);
    await selectTheJob(page);

    await (await openBulkPretranslate(page, "mt")).click();
    await expect(page.getByText(/Apply MT: 1 job\(s\) queued/)).toBeVisible();

    // Contract: name the capability + point at where to fix it (exact wording not load-bearing).
    await expectRowMessage(page, /No MT engine configured.*settings/i);
    await expect(page).toHaveURL(new RegExp(`/projects/${projectId}$`));
  });

  test("C: Apply TM is queued, then the job's row names TM + points the user at settings", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}`);
    await selectTheJob(page);

    await (await openBulkPretranslate(page, "tm")).click();
    await expect(page.getByText(/Apply TM: 1 job\(s\) queued/)).toBeVisible();

    await expectRowMessage(page, /No TM configured.*settings/i);
    await expect(page).toHaveURL(new RegExp(`/projects/${projectId}$`));
  });
});
