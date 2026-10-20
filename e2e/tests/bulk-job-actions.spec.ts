import { test, expect, type Locator, type Page } from "@playwright/test";
import { login, getSeedProjectId, createIsolatedCATJob, openBulkPretranslate, openTaskSteps, setProjectWorkflowStepsViaAPI } from "./helpers";

// Bulk actions on the project page's Tasks tab: selecting task cells reveals
// the bar above the matrix; its buttons fan each action out across the
// selection (see BulkJobActionsBar + useBulkJobActions). These run on the
// page's step sections: each job once, holding its current task.
test.describe("Bulk job actions", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  // Row select boxes in order: Demo Project's de/es/fr jobs all sit at
  // Translation, so boxes 0, 1 and 2 are three different jobs.
  const cells = (page: Page) => page.locator('[data-testid^="task-select-job:"]');

  /** The job id behind a row select box (`task-select-job:<id>`). */
  const jobIdOf = async (cell: Locator) => (await cell.getAttribute("data-testid"))!.replace("task-select-job:", "");

  test("selecting tasks reveals the bar with a live count; Clear dismisses it", async ({ page }) => {
    const projectId = await getSeedProjectId(page, "Demo Project");
    await page.goto(`/projects/${projectId}`);
    await openTaskSteps(page);
    await expect(cells(page).first()).toBeVisible();

    // Bar is conditionally rendered — absent from the DOM with nothing selected.
    const bar = page.getByTestId("bulk-actions-bar");
    await expect(bar).toHaveCount(0);

    await cells(page).nth(0).click();
    await expect(bar).toBeVisible();
    await expect(page.getByTestId("bulk-selected-count")).toHaveText("1 task selected");

    await cells(page).nth(2).click();
    await expect(page.getByTestId("bulk-selected-count")).toHaveText("2 tasks selected");

    await page.getByTestId("bulk-clear-selection").click();
    await expect(bar).toHaveCount(0);
    await expect(cells(page).nth(0)).not.toBeChecked();
    await expect(cells(page).nth(2)).not.toBeChecked();
  });

  test("bulk Apply MT fans out to exactly the selected tasks' jobs", async ({ page }) => {
    test.setTimeout(60_000);
    const projectId = await getSeedProjectId(page, "Demo Project");
    await page.goto(`/projects/${projectId}`);
    await openTaskSteps(page);
    await expect(cells(page).first()).toBeVisible();

    // Collect the job id of every successful mt-apply POST, so the fan-out is
    // proven to hit the selected jobs — not none (silent no-op) and not others.
    const applied = new Set<string>();
    page.on("response", (r) => {
      const match = /\/api\/jobs\/(\d+)\/mt-apply$/.exec(r.url());
      if (match && r.request().method() === "POST" && r.ok()) applied.add(match[1]);
    });

    const selectedJobIds = [await jobIdOf(cells(page).nth(0)), await jobIdOf(cells(page).nth(2))];
    await cells(page).nth(0).click();
    await cells(page).nth(2).click();
    await expect(page.getByTestId("bulk-selected-count")).toHaveText("2 tasks selected");

    await (await openBulkPretranslate(page, "mt")).click();

    await expect(page.getByText(/Apply MT: 2 job\(s\) done/)).toBeVisible({ timeout: 30_000 });
    await expect.poll(() => [...applied].sort()).toEqual([...selectedJobIds].sort());
  });

  test("a step header selects every task of that step", async ({ page }) => {
    const projectId = await getSeedProjectId(page, "Demo Project");
    await page.goto(`/projects/${projectId}`);
    await openTaskSteps(page);
    const rows = page.locator('[data-testid^="task-row-job:"]');
    await expect(rows.first()).toBeVisible();
    const jobCount = await rows.count();

    await page.locator('[data-testid^="task-select-all-"]').first().click();
    await expect(page.getByTestId("bulk-selected-count")).toHaveText(`${jobCount} tasks selected`);
  });

  test("the bar exposes every job action (incl. Complete, Clear, Delete)", async ({ page }) => {
    const projectId = await getSeedProjectId(page, "Demo Project");
    await page.goto(`/projects/${projectId}`);
    await openTaskSteps(page);
    await cells(page).nth(0).click();

    for (const id of [
      "bulk-download-btn",
      "bulk-pretranslate-btn",
      "bulk-complete-btn",
      "bulk-assign-btn",
      "bulk-deadline-btn",
      "bulk-clear-btn",
      "bulk-delete-btn",
    ]) {
      await expect(page.getByTestId(id)).toBeVisible();
    }
    // Apply TM / Apply MT live in the Pre-translate menu.
    await expect(await openBulkPretranslate(page, "tm")).toBeVisible();
    await expect(page.getByTestId("bulk-apply-mt-btn")).toBeVisible();
    // QP+APE is a workflow step, not a project-page bulk action.
    await expect(page.getByTestId("bulk-apply-predict-btn")).toHaveCount(0);
    // Stop only appears for jobs with a pipeline run in flight; the seed
    // project's jobs are idle, so the bar must not offer it.
    await expect(page.getByTestId("bulk-stop-btn")).toHaveCount(0);
  });

  test("bulk Delete removes the selected jobs", async ({ page }) => {
    test.setTimeout(60_000);
    // Isolated project so the destructive action can't touch shared seed data.
    const job = await createIsolatedCATJob(page, {
      fileName: `bulk-delete-${Date.now()}.txt`,
      content: "Hello world\nSecond line",
      targetLanguages: ["es", "fr"],
    });
    try {
      await page.goto(`/projects/${job.projectId}`);
      await openTaskSteps(page);
      await expect(cells(page)).toHaveCount(2); // es, fr, both at Translation

      // The Translation header selects both jobs' first task.
      await page.locator('[data-testid^="task-select-all-"]').first().click();
      await expect(page.getByTestId("bulk-selected-count")).toHaveText("2 tasks selected");

      await page.getByTestId("bulk-delete-btn").click();
      // ConfirmDialog → confirm.
      await page.getByRole("button", { name: "Delete", exact: true }).click();

      await expect(page.getByText(/Delete: 2 job\(s\) done/)).toBeVisible({ timeout: 30_000 });
      // No jobs left, so no tasks: the page moves to Files.
      await expect(page).toHaveURL(new RegExp(`/projects/${job.projectId}/files`));
      await expect(page.getByTestId("bulk-actions-bar")).toHaveCount(0);
    } finally {
      await job.cleanup();
    }
  });

  // Both paths can move `projects.status`
  // via auto-complete, and the badge must follow with no reload. The
  // CAT-editor Complete races its own invalidation, so it isn't an E2E target.
  // RED by dropping `invalidateProjectStatus` from the call site.
  test("auto-completing via bulk Complete step updates the project badge without a reload", async ({
    page,
  }) => {
    test.setTimeout(60_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `inc1240-bulk-${Date.now()}.txt`,
      content: "Bulk complete line.",
      targetLanguages: ["es"],
    });
    try {
      // A new project starts on Translation → Review; the auto-complete under
      // test fires when the job's FINAL step completes, so pin a one-step
      // workflow (dropping the unreached Review is allowed while the job runs).
      await setProjectWorkflowStepsViaAPI(page, job.projectId, [{ name: "Translation" }]);
      // Complete needs an approved segment, and /complete needs the job to be
      // on its final step — one confirm via the API gives both.
      const token = await page.evaluate(() => localStorage.getItem("token"));
      const headers = { Authorization: `Bearer ${token}` };
      const trs = await page.request.get(`/api/files/${job.fileId}/translations?lang=es`, { headers });
      expect(trs.ok(), "seed: list translations").toBe(true);
      for (const tr of (await trs.json()).data) {
        const put = await page.request.put(`/api/files/${job.fileId}/translations/${tr.id}`, {
          headers,
          data: { human_text: "Aprobado.", editing_time_ms: 0 },
        });
        expect(put.ok(), "seed: approve segment").toBe(true);
      }

      await page.goto(`/projects/${job.projectId}`);
      await openTaskSteps(page);
      await expect(page.getByTestId("project-status-badge-in_progress")).toBeVisible();

      await cells(page).first().click();
      await page.getByTestId("bulk-complete-btn").click();
      // ConfirmDialog → confirm.
      await page.getByRole("button", { name: "Complete", exact: true }).click();
      await expect(page.getByText(/Complete step: 1 job\(s\) done/)).toBeVisible({ timeout: 30_000 });

      // The whole point: no reload between the action and this assertion.
      await expect(page.getByTestId("project-status-badge-completed")).toBeVisible({ timeout: 15_000 });
    } finally {
      await job.cleanup();
    }
  });

  test("deleting the last unfinished file updates the project badge without a reload", async ({
    page,
  }) => {
    test.setTimeout(60_000);
    const stamp = Date.now();
    const job = await createIsolatedCATJob(page, {
      fileName: `inc1240-keep-${stamp}.txt`,
      content: "Kept line.",
      targetLanguages: ["es"],
    });
    try {
      // One-step workflow so /complete below finds the job on its final step
      // (a new project starts on Translation → Review).
      await setProjectWorkflowStepsViaAPI(page, job.projectId, [{ name: "Translation" }]);
      const token = await page.evaluate(() => localStorage.getItem("token"));
      const headers = { Authorization: `Bearer ${token}` };

      // A second file whose job stays unfinished — so the project hinges on it.
      const form = {
        file: { name: `inc1240-drop-${stamp}.txt`, mimeType: "text/plain", buffer: Buffer.from("Dropped line.") },
      };
      const up = await page.request.post(`/api/projects/${job.projectId}/files`, { headers, multipart: form });
      expect(up.ok(), "seed: upload second file").toBe(true);
      const dropFileId = (await up.json()).data.id;

      // Finish the first file's job outright, leaving only the second pending.
      const done = await page.request.post(`/api/jobs/${job.jobId}/complete`, { headers });
      expect(done.ok(), "seed: complete the kept job").toBe(true);
      // The fixture must be quiescent: a pipeline enqueued by either upload
      // lands asynchronously and rewrites a completed job back to in_progress.
      // Checking the job's own status here is useless —
      // it still reads 'completed' until the worker lands — so assert instead
      // that nothing is queued, which is true the moment setup goes wrong.
      const seeded = await page.request.get(`/api/projects/${job.projectId}/jobs`, { headers });
      const queued = (await seeded.json()).data.filter(
        (j: { pipeline_status: string }) => j.pipeline_status === "queued",
      );
      expect(
        queued,
        "seed: a pre-translate pipeline is pending and will overwrite the completed job",
      ).toHaveLength(0);

      await page.goto(`/projects/${job.projectId}/files`);
      await expect(page.getByTestId("project-status-badge-in_progress")).toBeVisible();

      const del = page.waitForResponse(
        (r) => r.url().endsWith(`/files/${dropFileId}`) && r.request().method() === "DELETE",
      );
      await page.getByTestId(`delete-file-${dropFileId}`).click();
      await page.getByRole("button", { name: "Delete", exact: true }).click();
      expect((await del).status()).toBe(204);

      await expect(page.getByTestId("project-status-badge-completed")).toBeVisible({ timeout: 15_000 });
    } finally {
      await job.cleanup();
    }
  });
});
