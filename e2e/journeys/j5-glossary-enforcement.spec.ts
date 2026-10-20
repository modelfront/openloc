import { test, expect } from "@playwright/test";
import {
  login,
  segmentEditor,
  assertCompletionPersistedToTM,
  uploadFileViaAPI,
  pretranslateFileViaAPI,
  createTermBaseViaAPI,
  addTermBaseTermViaAPI,
  deleteTermBaseViaAPI,
  createTMViaAPI,
  deleteTMViaAPI,
  createProjectViaAPI,
  deleteProjectViaAPI,
  setProjectTMsViaAPI,
  setProjectTermsViaAPI,
  setProjectMTViaAPI,
  createMTEngineViaAPI,
  deleteMTEngineViaAPI,
  verifyProjectSettings,
} from "./helpers";

/**
 * J5 — Term base Enforcement in the Editor (Adversarial)
 *
 * Exercises: term recognition with overlapping terms, term panel auto-populate,
 * insert from panel, confirm preserves terms, TM write-back (core concern).
 * Uses de-at locale (Austrian German) — different from J1's es-mx, J2's fr-ca,
 * J4's pt-br. Term base terms stored as 'de', project target 'de-at'.
 */

const API = "";

let terminologyId: number;
let mockEngineId: number;
let tmId: number;
let projectId: number;
let fileId: number;
let jobId: number;

test.describe.serial("J5 — Term base Enforcement", () => {

  // ════════════════════════════════════════════════════════════════════════
  // Phase A: Setup
  // ════════════════════════════════════════════════════════════════════════

  test("A1: create term base with de (base code) terms", async ({ page }) => {
    await login(page);
    // Term base uses base code 'de' — project uses 'de-at'.
    // Locale-aware matching should bridge them (same as TM).
    terminologyId = await createTermBaseViaAPI(page, "Term base Journey", "en", "de");
    expect(terminologyId).toBeGreaterThan(0);
  });

  test("A2: add overlapping terms (password + reset password)", async ({ page }) => {
    await login(page);
    // Overlapping terms: "password" and "reset password" both match in
    // "Reset your password now". Both should highlight.
    await addTermBaseTermViaAPI(page, terminologyId, "en", "de", "password", "Passwort");
    await addTermBaseTermViaAPI(page, terminologyId, "en", "de", "reset password", "Passwort zurücksetzen");
    await addTermBaseTermViaAPI(page, terminologyId, "en", "de", "email address", "E-Mail-Adresse");
    await addTermBaseTermViaAPI(page, terminologyId, "en", "de", "account", "Konto");

    // Verify in browser. The view opens filtered to the source language, so
    // assert on the source term ("password") — the German target is hidden here
    // (its enforcement is verified in the CAT editor below).
    await page.goto(`/terminology/${terminologyId}`);
    await expect(page.getByText(/password/i).first()).toBeVisible({ timeout: 5000 });
  });

  test("A3: create project de-at + assign term base + mock MT + write-back TM", async ({ page }) => {
    await login(page);

    // Mock engine supports de-at natively (no fallback needed)
    mockEngineId = await createMTEngineViaAPI(page, "Term base Journey Mock MT", "mock");

    // Empty TM for write-back. Pre-translate falls through to MT (no entries
    // → no matches), but completing the job in D0 lands confirmed segments here so
    // we can verify TM write-back works end-to-end.
    tmId = await createTMViaAPI(page, "Term base Journey TM");

    projectId = await createProjectViaAPI(page, "Term base Journey Project", "en", ["de-at"]);

    // Assign term base (en→de-at) + mock MT (en→de-at) + read_write TM (en→de-at)
    await setProjectTermsViaAPI(page, projectId, [
      { term_base_id: terminologyId, source_language: "en", target_language: "de-at", mode: "read", priority: 1 },
    ]);
    const routerId = await setProjectMTViaAPI(page, projectId, [
      { mt_engine_id: mockEngineId, source_language: "en", target_language: "de-at" },
    ]);
    await setProjectTMsViaAPI(page, projectId, [
      { tm_id: tmId, source_language: "en", target_language: "de-at", mode: "read_write", priority: 1 },
    ]);

    await verifyProjectSettings(page, projectId, {
      term_count: 1,
      mt_router_id: routerId,
      tm_count: 1,
    });
  });

  test("A4: upload file + pre-translate", async ({ page }) => {
    test.setTimeout(30_000);
    await login(page);

    // 4 segments — first 3 contain term base terms, last is clean
    const content = "Reset your password now\nEnter your email address\nCheck your account\nNo terms in this sentence\n";
    const result = await uploadFileViaAPI(page, projectId, "term base-journey.txt", content, "text/plain");
    fileId = result.data?.id ?? result.id;

    await pretranslateFileViaAPI(page, projectId, fileId);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const jobsResp = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    const jobs = (await jobsResp.json()).data;
    expect(jobs.length).toBeGreaterThan(0);
    jobId = jobs[0].id;
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B: Verify term recognition in editor
  // ════════════════════════════════════════════════════════════════════════

  test("B1-B2: click 'Reset your password' — term panel shows Passwort", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // Click first segment
    await page.getByText(/Reset your password/i).first().click();
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });

    // Term panel should auto-populate (QA finding #20).
    // LOCALE BRIDGE ASSERTION: term base terms are stored with target='de' (base code)
    // but the project uses 'de-at'. If locale-aware matching doesn't work, the
    // term panel would be empty here. Finding "Passwort" PROVES the de→de-at
    // bridge works for term base lookups — this is not implicit, it's the test.
    await expect(page.getByText(/Passwort/i).first()).toBeVisible({ timeout: 10000 });
  });

  test("B3: click 'Enter your email address' — term panel shows E-Mail-Adresse", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // Click second segment
    const translationCells = page.locator("[data-translation-cell]");
    await translationCells.nth(1).click();
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });

    // "email address" → "E-Mail-Adresse"
    await expect(page.getByText(/E-Mail-Adresse/i).first()).toBeVisible({ timeout: 10000 });
  });

  test("B4: click 'No terms' segment — no term matches", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // Click fourth segment (no term base terms)
    const translationCells = page.locator("[data-translation-cell]");
    await translationCells.nth(3).click();
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });

    // Term lookup is cache-gated (SegmentRow termLookupCache) so it may not fire a
    // request at all, and this asserts ABSENCE (expect 0 matches) — there's no
    // response to await, so a bounded settle window is the honest choice here.
    await page.waitForTimeout(2000);

    // Term panel should NOT show any of our term base terms for this segment.
    // Assert the panel's visible empty state, not the badge span's textContent:
    // the count badge is absent at 0. exact: true keeps the segment source
    // text "No terms in this sentence" from satisfying the assertion.
    await expect(page.getByText('No terms', { exact: true })).toBeVisible({ timeout: 10000 });
    // And none of the seeded German terms may be visible anywhere.
    for (const term of ['Passwort', 'E-Mail-Adresse', 'Konto']) {
      await expect(page.getByText(term)).toHaveCount(0);
    }
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C: Insert from term panel + confirm
  // The core term base interaction: click a term → it appears in the editor.
  // Skipping this would defeat the entire purpose of J5.
  // ════════════════════════════════════════════════════════════════════════

  test("C1: click 'Passwort' in term panel — inserts into editor", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // Click seg 1 to activate editor + trigger term lookup
    await page.getByText(/Reset your password/i).first().click();
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });

    // Wait for the terminology panel to populate
    const termSection = page.getByTestId("terminology-section");
    await expect(termSection.getByText(/Passwort/).first()).toBeVisible({ timeout: 10000 });

    // Clear existing MT text so we can see the insert clearly
    await editor.click();
    const isMac = process.platform === "darwin";
    await page.keyboard.press(isMac ? "ControlOrMeta+a" : "Control+a");
    await page.keyboard.press("Backspace");

    // Click the term in the panel — it should insert into the editor.
    // The term panel renders terms as clickable items; click the "Passwort" one.
    await termSection.getByText(/Passwort/).first().click();

    // Verify the editor now contains the inserted term
    await expect(editor).toContainText("Passwort");

    // Confirm the segment with the inserted term
    const [resp] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/translations/") && r.request().method() === "PUT" && !r.url().includes("/draft"),
        { timeout: 10000 },
      ),
      page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
    ]);
    expect(resp.ok()).toBe(true);
  });

  test("C2-C4: confirm remaining segments", async ({ page }) => {
    test.setTimeout(30_000);
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    const isMac = process.platform === "darwin";
    const translationCells = page.locator("[data-translation-cell]");

    // Confirm segments 2, 3, 4 (indices 1, 2, 3)
    for (const idx of [1, 2, 3]) {
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
      expect(resp.ok(), `Confirm segment ${idx + 1} failed`).toBe(true);
    }
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D: Export + TM write-back (core concerns)
  // ════════════════════════════════════════════════════════════════════════

  test("D0: complete the job — TM write-back lands confirmed translation", async ({ page }) => {
    // C2-C4 confirmed seg 3 ("Check your account") with the pre-translated MT
    // output. Mock engine prefixes with `[<UPPER_LOCALE>] ` (see internal/mt/mock.go),
    // so target = "[DE-AT] Check your account". Completion should persist that
    // pair to the project's read_write TM. Audit M-3: tightened from
    // `expect(status).toBeLessThan(500)` to a full round-trip check.
    await login(page);
    await assertCompletionPersistedToTM(page, jobId, {
      tmId,
      sourceText: "Check your account",
      targetText: "[DE-AT] Check your account",
    });
  });

  test("D1: export the job", async ({ page }) => {
    await login(page);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const exportResp = await page.request.get(`${API}/api/files/${fileId}/export?lang=de-at`, { headers });
    expect(exportResp.ok(), `Export failed: ${exportResp.status()}`).toBe(true);
    const body = await exportResp.body();
    expect(body.length, "exported file should have content").toBeGreaterThan(0);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase E: Cleanup
  // ════════════════════════════════════════════════════════════════════════

  test("E1: cleanup", async ({ page }) => {
    await login(page);
    if (projectId) await deleteProjectViaAPI(page, projectId);
    if (tmId) await deleteTMViaAPI(page, tmId);
    if (terminologyId) await deleteTermBaseViaAPI(page, terminologyId);
    if (mockEngineId) await deleteMTEngineViaAPI(page, mockEngineId);
  });

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    try {
      await login(page);
      if (projectId) await deleteProjectViaAPI(page, projectId).catch(() => {});
      if (tmId) await deleteTMViaAPI(page, tmId).catch(() => {});
      if (terminologyId) await deleteTermBaseViaAPI(page, terminologyId).catch(() => {});
      if (mockEngineId) await deleteMTEngineViaAPI(page, mockEngineId).catch(() => {});
    } catch {} finally {
      await page.close();
    }
  });
});
