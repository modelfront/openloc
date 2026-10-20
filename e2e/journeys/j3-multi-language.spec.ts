import { test, expect } from "@playwright/test";
import {
  login,
  segmentEditor,
  uploadFileViaAPI,
  pretranslateFileViaAPI,
  createProjectViaAPI,
  deleteProjectViaAPI,
  setProjectMTViaAPI,
  createMTEngineViaAPI,
  deleteMTEngineViaAPI,
  verifyProjectSettings,
  getMTRouterViaAPI,
} from "./helpers";

/**
 * J3 — Multi-Language Project (Adversarial)
 *
 * Tests job independence: confirming in es-mx MUST NOT affect pt-br or zh-hans.
 * Progress, exports, and pre-translate are per-job. Three regional locales
 * in one project — stresses the pipeline with multiple concurrent targets.
 */

const API = "";

let mockEngineId: number;
let projectId: number;
let fileId: number;
let esMxJobId: number;
let ptBrJobId: number;
let zhHansJobId: number;

test.describe.serial("J3 — Multi-Language", () => {

  // ════════════════════════════════════════════════════════════════════════
  // Phase A: Setup — 3-language project with regional locales
  // ════════════════════════════════════════════════════════════════════════

  test("A1-A2: create project en→es-mx/pt-br/zh-hans + mock MT", async ({ page }) => {
    await login(page);

    // Mock engine supports all locales natively
    mockEngineId = await createMTEngineViaAPI(page, "Multi-Lang Mock MT", "mock");

    projectId = await createProjectViaAPI(page, "Multi-Lang Journey", "en", ["es-mx", "pt-br", "zh-hans"]);

    // Assign mock MT for all 3 pairs
    const routerId = await setProjectMTViaAPI(page, projectId, [
      { mt_engine_id: mockEngineId, source_language: "en", target_language: "es-mx" },
      { mt_engine_id: mockEngineId, source_language: "en", target_language: "pt-br" },
      { mt_engine_id: mockEngineId, source_language: "en", target_language: "zh-hans" },
    ]);

    await verifyProjectSettings(page, projectId, { mt_router_id: routerId });
    // One router, one rule per target language — all three must be routed.
    const router = await getMTRouterViaAPI(page, routerId!);
    expect(router.rules.map((r) => r.target_lang).sort()).toEqual(["es-mx", "pt-br", "zh-hans"]);
  });

  test("A3: upload file — should create 3 jobs (one per target)", async ({ page }) => {
    await login(page);

    const content = "Welcome\nThank you\nSettings\nPrice: $19.99\n";
    const result = await uploadFileViaAPI(page, projectId, "multi-lang.txt", content, "text/plain");
    fileId = result.data?.id ?? result.id;

    // Get all 3 jobs and identify which is which by target_language
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const jobsResp = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    const jobs = (await jobsResp.json()).data;

    // OBSERVABLE OUTCOME: exactly 3 jobs, one per target language
    expect(jobs.length, "should have 3 jobs (one per target language)").toBe(3);

    for (const job of jobs) {
      if (job.target_language === "es-mx") esMxJobId = job.id;
      else if (job.target_language === "pt-br") ptBrJobId = job.id;
      else if (job.target_language === "zh-hans") zhHansJobId = job.id;
    }
    expect(esMxJobId, "es-mx job should exist").toBeGreaterThan(0);
    expect(ptBrJobId, "pt-br job should exist").toBeGreaterThan(0);
    expect(zhHansJobId, "zh-hans job should exist").toBeGreaterThan(0);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B: Per-language independence
  // The CORE test: actions in one job must NOT affect another.
  // ════════════════════════════════════════════════════════════════════════

  test("B1: open es-mx job, pre-translate + confirm segment 1 only", async ({ page }) => {
    test.setTimeout(30_000);
    await login(page);

    // Pre-translate all jobs of this file (tm-apply + mt-apply per job)
    await pretranslateFileViaAPI(page, projectId, fileId);

    // Open es-mx editor
    await page.goto(`/projects/${projectId}/jobs/${esMxJobId}/edit`);
    await expect(page.getByText(/Welcome/i).first()).toBeVisible({ timeout: 10000 });

    // OBSERVABLE OUTCOME: segments should have MT text WITH the [ES-MX] mock
    // prefix. A bare "length > 0" would pass even if the project silently fell
    // back to a different engine (e.g., LibreTranslate returning real Spanish
    // without the prefix), so the prefix locks down locale isolation end-to-end.
    const translationCells = page.locator("[data-translation-cell]");
    const firstCellText = await translationCells.first().textContent();
    expect(firstCellText, "es-mx cell text should be non-null").toBeTruthy();
    expect(
      firstCellText,
      "es-mx mock MT must produce [ES-MX] prefix — no prefix suggests wrong-engine fallback",
    ).toContain("[ES-MX]");

    // Confirm ONLY segment 1
    await translationCells.nth(0).click();
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });
    await editor.click();

    const isMac = process.platform === "darwin";
    const [resp] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/translations/") && r.request().method() === "PUT" && !r.url().includes("/draft"),
        { timeout: 10000 },
      ),
      page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
    ]);
    expect(resp.ok()).toBe(true);
  });

  test("B2: open pt-br job — progress should be 0/4 (es-mx confirm didn't leak)", async ({ page }) => {
    await login(page);

    // Check pt-br job status via API — OBSERVABLE OUTCOME
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const jobResp = await page.request.get(`${API}/api/jobs/${ptBrJobId}`, { headers });
    const job = (await jobResp.json()).data;

    // pt-br should have ZERO approved segments — es-mx's confirm must not leak
    const approved = job.counts?.approved ?? 0;
    expect(approved, "pt-br should have 0 approved (es-mx confirm must not leak)").toBe(0);

    // Open pt-br editor to verify UI matches
    await page.goto(`/projects/${projectId}/jobs/${ptBrJobId}/edit`);
    await expect(page.getByText(/Welcome/i).first()).toBeVisible({ timeout: 10000 });
  });

  test("B3-B4: pre-translate pt-br + confirm all 4 segments", async ({ page }) => {
    test.setTimeout(30_000);
    await login(page);

    // pt-br should already have MT text from the B1 translate call
    // (translate endpoint translates ALL jobs for the file)
    await page.goto(`/projects/${projectId}/jobs/${ptBrJobId}/edit`);
    await expect(page.getByText(/Welcome/i).first()).toBeVisible({ timeout: 10000 });

    // OBSERVABLE OUTCOME: pt-br segments must have MT text with the [PT-BR]
    // mock prefix, which pins which engine produced the output.
    const translationCells = page.locator("[data-translation-cell]");
    const firstText = await translationCells.first().textContent();
    expect(firstText, "pt-br cell text should be non-null").toBeTruthy();
    expect(
      firstText,
      "pt-br mock MT must produce [PT-BR] prefix — proves locale-correct engine ran",
    ).toContain("[PT-BR]");

    // Confirm all 4
    const isMac = process.platform === "darwin";
    for (const idx of [0, 1, 2, 3]) {
      await translationCells.nth(idx).click();
      const editor = segmentEditor(page).first();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.click();

      const [resp] = await Promise.all([
        page.waitForResponse(
          (r) => r.url().includes("/translations/") && r.request().method() === "PUT" && !r.url().includes("/draft"),
          { timeout: 10000 },
        ),
        page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
      ]);
      expect(resp.ok(), `Confirm pt-br segment ${idx + 1} failed`).toBe(true);
    }
  });

  test("B5: verify per-job progress via API (the independence proof)", async ({ page }) => {
    await login(page);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // OBSERVABLE OUTCOMES — the actual numbers, not just "no crash":

    // es-mx: 1/4 approved (we confirmed only segment 1 in B1)
    const esMxResp = await page.request.get(`${API}/api/jobs/${esMxJobId}`, { headers });
    const esMx = (await esMxResp.json()).data;
    expect(esMx.counts?.approved ?? 0, "es-mx should have 1 approved").toBe(1);

    // pt-br: 4/4 approved (confirmed all in B4)
    const ptBrResp = await page.request.get(`${API}/api/jobs/${ptBrJobId}`, { headers });
    const ptBr = (await ptBrResp.json()).data;
    expect(ptBr.counts?.approved ?? 0, "pt-br should have 4 approved").toBe(4);

    // zh-hans: 0/4 approved (never touched)
    const zhResp = await page.request.get(`${API}/api/jobs/${zhHansJobId}`, { headers });
    const zh = (await zhResp.json()).data;
    expect(zh.counts?.approved ?? 0, "zh-hans should have 0 approved").toBe(0);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C: Per-language export
  // ════════════════════════════════════════════════════════════════════════

  test("C1: export pt-br — should have pt-br content, NO es-mx cross-contamination", async ({ page }) => {
    await login(page);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const exportResp = await page.request.get(`${API}/api/files/${fileId}/export?lang=pt-br`, { headers });
    expect(exportResp.ok(), `pt-br export failed: ${exportResp.status()}`).toBe(true);
    const body = await exportResp.text();

    // OBSERVABLE OUTCOME: exported file should contain pt-br translations
    // Mock MT prefixes with [PT-BR], so we can verify actual content
    expect(body, "pt-br export should contain [PT-BR] translations").toContain("[PT-BR]");

    // CROSS-CONTAMINATION CHECK: pt-br export must NOT contain es-mx or zh-hans text
    expect(body, "pt-br export must not contain es-mx text").not.toContain("[ES-MX]");
    expect(body, "pt-br export must not contain zh-hans text").not.toContain("[ZH-HANS]");
  });

  test("C2: export es-mx — should have es-mx content, NO pt-br cross-contamination", async ({ page }) => {
    await login(page);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const exportResp = await page.request.get(`${API}/api/files/${fileId}/export?lang=es-mx`, { headers });
    expect(exportResp.ok(), `es-mx export failed: ${exportResp.status()}`).toBe(true);
    const body = await exportResp.text();

    // OBSERVABLE OUTCOME: exported file should contain es-mx translations
    // We confirmed 1/4 segments with mock MT text → at least one [ES-MX] line
    expect(body, "es-mx export should contain [ES-MX] translations").toContain("[ES-MX]");

    // CROSS-CONTAMINATION CHECK: es-mx export must NOT contain pt-br or zh-hans text
    expect(body, "es-mx export must not contain pt-br text").not.toContain("[PT-BR]");
    expect(body, "es-mx export must not contain zh-hans text").not.toContain("[ZH-HANS]");
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D: Cleanup
  // ════════════════════════════════════════════════════════════════════════

  test("D1: cleanup", async ({ page }) => {
    await login(page);
    if (projectId) await deleteProjectViaAPI(page, projectId);
    if (mockEngineId) await deleteMTEngineViaAPI(page, mockEngineId);
  });

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    try {
      await login(page);
      if (projectId) await deleteProjectViaAPI(page, projectId).catch(() => {});
      if (mockEngineId) await deleteMTEngineViaAPI(page, mockEngineId).catch(() => {});
    } catch {} finally {
      await page.close();
    }
  });
});
