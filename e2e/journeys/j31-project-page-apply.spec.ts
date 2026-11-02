import { test, expect } from "@playwright/test";
import {
  selectFirstTask,
  login,
  uploadFileViaAPI,
  createProjectViaAPI,
  deleteProjectViaAPI,
  createMTEngineViaAPI,
  deleteMTEngineViaAPI,
  createTMViaAPI,
  deleteTMViaAPI,
  setProjectMTViaAPI,
  setProjectTMsViaAPI,
  openBulkPretranslate,
} from "./helpers";

/**
 * J31 — Apply TM / MT from the project-page bulk-action bar.
 *
 * Apply TM/MT live in the checkbox-driven bulk bar's Pre-translate menu
 * (BulkJobActionsBar + useBulkJobActions). B1: Apply
 * TM/MT enabled before any pre-translation. B: Apply TM then Apply MT
 * fan out, toasts fire, URL stable.
 *
 * QE+APE runs as a workflow step (predict-on-start), so the bar has no
 * Predict action. This journey
 * covers TM/MT only.
 *
 * Anti-tautology proof:
 *   - HandleMTApply writes nothing ⇒ B fails at the "[FR] " translation
 *     check, even though the toast still fires.
 */

const API = "";

let projectId: number;
let mtEngineId: number;
let tmId: number;

test.describe.serial("J31 — Apply TM/MT from project page", () => {
  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    await login(page);
    if (projectId) {
      await deleteProjectViaAPI(page, projectId);
    }
    if (tmId) {
      await deleteTMViaAPI(page, tmId);
    }
    if (mtEngineId) {
      await deleteMTEngineViaAPI(page, mtEngineId);
    }
    await page.close();
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase A — Setup: project with MT + TM assignments, small file, nav
  // ════════════════════════════════════════════════════════════════════════

  test("A1: create project en→fr with MT + TM assignments and a single-segment file", async ({ page }) => {
    await login(page);
    projectId = await createProjectViaAPI(page, "J31 Apply Buttons", "en", ["fr"]);
    expect(projectId).toBeGreaterThan(0);

    // Apply MT/TM both require a project-level assignment for the lang
    // pair — without one, the handler returns 400 with a help message
    // ("No MT engine configured for en → fr — add one in project
    // settings"). That contract is exercised by J32; this journey
    // tests the happy path, so we need to wire the assignments up
    // front. Mock engine + empty TM keep the test deterministic
    // (no external network, no TM matches to manage).
    mtEngineId = await createMTEngineViaAPI(page, "J31 Mock", "mock");
    await setProjectMTViaAPI(page, projectId, [
      { mt_engine_id: mtEngineId, source_language: "en", target_language: "fr" },
    ]);

    tmId = await createTMViaAPI(page, "J31 TM");
    await setProjectTMsViaAPI(page, projectId, [
      { tm_id: tmId, source_language: "en", target_language: "fr", mode: "read_write", priority: 1 },
    ]);

    const uploaded = await uploadFileViaAPI(
      page,
      projectId,
      "j31.txt",
      "Hello, world.",
      "text/plain",
    );
    // Upload returns `{data: {id: ...}}`. We don't need the file id for
    // the rest of the journey — the project page lists jobs across all
    // files and we have exactly one.
    expect(uploaded?.data?.id, "uploaded file id").toBeGreaterThan(0);
  });

  test("A2: navigate to project detail, Files tab shows the seeded file", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/files`);
    // Assert the file is rendered so we know we're not racing the fetch.
    await expect(page.getByText("j31.txt")).toBeVisible({ timeout: 10000 });
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B — Bulk-action bar: apply pre-translation to the selected job
  // ════════════════════════════════════════════════════════════════════════

  async function selectTheJob(page: import("@playwright/test").Page) {
    await selectFirstTask(page);
    await expect(page.getByText("j31.txt")).toBeVisible({ timeout: 10000 });
    return page.getByTestId("bulk-actions-bar");
  }

  test("B1: bar exposes Apply TM/MT enabled before pretranslate", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}`);
    await selectTheJob(page);

    // Apply TM/MT live in the Pre-translate menu. The
    // trigger's enabled state IS the eligibility signal — both items share
    // one eligibility rule.
    await expect(page.getByTestId("bulk-pretranslate-btn")).toBeEnabled();
    await expect(await openBulkPretranslate(page, "tm")).toBeVisible();
    await expect(page.getByTestId("bulk-apply-mt-btn")).toBeVisible();
    // No Apply Predict bulk button — QE+APE is a workflow step.
    await expect(page.getByTestId("bulk-apply-predict-btn")).toHaveCount(0);
  });

  test("B: Apply TM then Apply MT via the bar — toasts fire, URL stable", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}`);
    await selectTheJob(page);

    await (await openBulkPretranslate(page, "tm")).click();
    await expect(page.getByText(/Apply TM: 1 job\(s\) queued/)).toBeVisible({ timeout: 30_000 });
    await expect(page).toHaveURL(new RegExp(`/projects/${projectId}$`));

    // The queued TM run finishes on the worker: wait until the job no longer
    // reads as running (the page's isPipelineRunning), or MT would skip it.
    const authHeaders = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    await expect.poll(async () => {
      const r = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers: authHeaders });
      const [j] = (await r.json()).data as Array<{ pipeline_status: string; current_step: string | null }>;
      const live = j.pipeline_status === "queued" || j.pipeline_status === "in_progress";
      return live && !(j.current_step ?? "").toLowerCase().endsWith("complete") ? "running" : "idle";
    }, { message: "the queued TM run must finish before Apply MT", timeout: 30_000 }).toBe("idle");
    await expect(page.getByTestId("bulk-pretranslate-btn")).toBeEnabled({ timeout: 30_000 });
    await (await openBulkPretranslate(page, "mt")).click();
    await expect(page.getByText(/Apply MT: 1 job\(s\) queued/)).toBeVisible({ timeout: 30_000 });
    await expect(page).toHaveURL(new RegExp(`/projects/${projectId}$`));

    // The toast says the run is queued, not done. The mock engine prefixes
    // its output with the target language, so once the worker has run every
    // segment of the job carries "[FR] ".
    const jobsResp = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers: authHeaders });
    expect(jobsResp.ok(), "list project jobs").toBe(true);
    const jobs = (await jobsResp.json()).data as Array<{ id: number }>;
    expect(jobs, "the project has exactly one job").toHaveLength(1);
    await expect.poll(async () => {
      const transResp = await page.request.get(`${API}/api/jobs/${jobs[0].id}/translations`, { headers: authHeaders });
      const translations = (await transResp.json()).data as Array<{ active_text: string | null }>;
      return translations.length > 0 && translations.every(t => /^\[FR\] /.test(t.active_text ?? ""));
    }, { message: "Apply MT must fill every segment with the mock engine's [FR] output", timeout: 30_000 }).toBe(true);
  });
});
