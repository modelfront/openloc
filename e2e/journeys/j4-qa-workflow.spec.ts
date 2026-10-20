import { test, expect } from "@playwright/test";
import {
  login,
  segmentEditor,
  assertCompletionPersistedToTM,
  assertQARunSucceeded,
  getQAReportViaAPI,
  uploadFileViaAPI,
  pretranslateFileViaAPI,
  createTMViaAPI,
  importTMXViaAPI,
  deleteTMViaAPI,
  createProjectViaAPI,
  deleteProjectViaAPI,
  createQAProfileViaAPI,
  deleteQAProfileViaAPI,
  setProjectTMsViaAPI,
  setProjectMTViaAPI,
  updateProjectSettingsViaAPI,
  verifyProjectSettings,
  createMTEngineViaAPI,
  deleteMTEngineViaAPI,
  type QARule,
} from "./helpers";

/**
 * J4 — QA Workflow (Adversarial)
 *
 * Exercises every QA check type, the fix→recheck cycle, and verifies QA
 * doesn't error out (QA finding #31). Uses pt-br locale per the "repeat
 * core concerns" rule — different from J1's es-mx and J2's fr-ca.
 */

const API = "";

let qaProfileId: number;
let tmId: number;
let mockEngineId: number;
let projectId: number;
let fileId: number;
let jobId: number;

test.describe.serial("J4 — QA Workflow", () => {

  // ════════════════════════════════════════════════════════════════════════
  // Phase A: Setup — QA profile with all checks + project with pt-br
  // ════════════════════════════════════════════════════════════════════════

  test("A1: create QA profile with all valid check types", async ({ page }) => {
    await login(page);

    // Valid check_types: empty_target, tag_checks, number_consistency,
    // punctuation, terminology, consistency, target_identical_to_source
    const rules: QARule[] = [
      { check_type: "empty_target", enabled: true, severity: "error" },
      { check_type: "tag_checks", enabled: true, severity: "error" },
      { check_type: "number_consistency", enabled: true, severity: "warning" },
      { check_type: "punctuation", enabled: true, severity: "warning" },
      { check_type: "terminology", enabled: true, severity: "error" },
      { check_type: "target_identical_to_source", enabled: true, severity: "warning" },
    ];
    qaProfileId = await createQAProfileViaAPI(page, "QA Journey Profile", rules);
    expect(qaProfileId).toBeGreaterThan(0);
  });

  test("A2: create TM + project with pt-br locale + assign QA profile", async ({ page }) => {
    await login(page);

    // TM with pt entries — locale-aware matching will bridge pt-br → pt
    tmId = await createTMViaAPI(page, "QA Journey TM");
    const tmx = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header creationtool="test" srclang="en" datatype="plaintext" segtype="sentence"/>
  <body>
    <tu><tuv xml:lang="en"><seg>Hello world</seg></tuv><tuv xml:lang="pt"><seg>Olá mundo</seg></tuv></tu>
  </body>
</tmx>`;
    await importTMXViaAPI(page, tmId, tmx);

    // Create a mock MT engine — it supports pt-br natively (unlike LibreTranslate
    // which only has en/es/fr/de). Using mock exercises the non-fallback locale path.
    mockEngineId = await createMTEngineViaAPI(page, "QA Journey Mock MT", "mock");

    // Create project with pt-br — exercises locale handling AGAIN
    projectId = await createProjectViaAPI(page, "QA Journey Project", "en", ["pt-br"]);

    // Assign QA profile + TM + MT (mock engine, not LibreTranslate)
    await updateProjectSettingsViaAPI(page, projectId, { qa_profile_id: qaProfileId });
    await setProjectTMsViaAPI(page, projectId, [
      { tm_id: tmId, source_language: "en", target_language: "pt-br", mode: "read_write", priority: 1 },
    ]);
    const routerId = await setProjectMTViaAPI(page, projectId, [
      { mt_engine_id: mockEngineId, source_language: "en", target_language: "pt-br" },
    ]);

    // Verify
    await verifyProjectSettings(page, projectId, {
      qa_profile_id: qaProfileId,
      tm_count: 1,
      mt_router_id: routerId,
    });
  });

  test("A3: upload file + pre-translate", async ({ page }) => {
    test.setTimeout(30_000);
    await login(page);

    // 5 segments: some will have QA issues after pre-translate
    // "Hello world" → TM match (locale-aware pt-br→pt). Tests TM matching again.
    // "Price: $49.99" → MT, number present. We'll check number_consistency later.
    // "End of sentence." → MT. Punctuation present.
    // "Empty segment" → we'll clear this to test empty_target
    // "Normal text" → clean baseline
    const content = "Hello world\nPrice: $49.99\nEnd of sentence.\nEmpty segment\nNormal text\n";
    const result = await uploadFileViaAPI(page, projectId, "qa-journey.txt", content, "text/plain");
    fileId = result.data?.id ?? result.id;

    // Pre-translate via per-job phase endpoints (production manual-button path)
    await pretranslateFileViaAPI(page, projectId, fileId);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Get job ID
    const jobsResp = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    const jobs = (await jobsResp.json()).data;
    expect(jobs.length).toBeGreaterThan(0);
    jobId = jobs[0].id;
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B: Run QA — should NOT error out (QA finding #31)
  // ════════════════════════════════════════════════════════════════════════

  test("B1-B2: open editor + run QA without error", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Hello world/i).first()).toBeVisible({ timeout: 10000 });

    // Run QA — must return 200 (QA finding #31).
    await assertQARunSucceeded(page);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B2: Editor features — formatting + hidden chars (QA #7, #8)
  // ════════════════════════════════════════════════════════════════════════

  test("B3: Ctrl+B applies bold formatting in editor (QA finding #7)", async ({ page }) => {
    // *** QA #7: Bold/Italic/Sup/Sub formatting ***
    // B/I/Sup/Sub come from TipTap. This test verifies Ctrl+B actually produces bold markup — not just
    // "editor loaded" (which would be beating the purpose).
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Hello world/i).first()).toBeVisible({ timeout: 10000 });

    const isMac = process.platform === "darwin";
    const translationCells = page.locator("[data-translation-cell]");

    // Click seg 5 ("Normal text") to activate the editor
    await translationCells.nth(4).click();
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });
    await editor.click();

    // Select all text and apply bold
    await page.keyboard.press(isMac ? "ControlOrMeta+a" : "Control+a");
    await page.keyboard.press(isMac ? "ControlOrMeta+b" : "Control+b");

    // OBSERVABLE OUTCOME: the editor should now contain <strong> markup
    // TipTap renders bold as <strong> in its HTML output
    const editorHTML = await editor.evaluate((el) => el.querySelector(".ProseMirror")?.innerHTML ?? el.innerHTML);
    expect(
      editorHTML.includes("<strong>") || editorHTML.includes("font-weight"),
      `Ctrl+B should produce bold markup, got: ${editorHTML.substring(0, 200)}`,
    ).toBe(true);

    // Undo to not pollute later tests
    await page.keyboard.press(isMac ? "ControlOrMeta+z" : "Control+z");
  });

  test("B4: hidden characters toggle shows special chars (QA finding #8)", async ({ page }) => {
    // *** QA #8: Hidden characters toggle ***
    // Toggle reveals zero-width spaces, soft hyphens, etc.
    // The ¶ button in the toolbar controls this.
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Hello world/i).first()).toBeVisible({ timeout: 10000 });

    // Find the ¶ toggle button
    const toggleBtn = page.getByRole("button", { name: /¶/i }).or(page.locator("button:has-text('¶')"));

    // BEATING THE PURPOSE CHECK: if the button doesn't exist, the feature
    // isn't shipped and this test should FAIL, not silently skip.
    await expect(toggleBtn, "¶ hidden chars toggle button should exist").toBeVisible({ timeout: 5000 });

    // Verify localStorage before click
    const beforeToggle = await page.evaluate(() => localStorage.getItem("cat-show-whitespace"));

    // Click to enable hidden characters
    await toggleBtn.click();
    await page.waitForTimeout(300);

    // OBSERVABLE OUTCOME: localStorage should reflect the toggle state
    // The component uses: setShowWhitespace → localStorage.setItem('cat-show-whitespace', ...)
    const afterToggle = await page.evaluate(() => localStorage.getItem("cat-show-whitespace"));
    expect(afterToggle, "toggle should set localStorage to 'true'").toBe("true");
    expect(afterToggle, "toggle should change from previous state").not.toBe(beforeToggle);

    // Click again to restore
    await toggleBtn.click();
    await page.waitForTimeout(200);
    const restored = await page.evaluate(() => localStorage.getItem("cat-show-whitespace"));
    expect(restored, "second click should restore to 'false'").toBe("false");
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C: Create QA issues by editing segments, then re-run
  // ════════════════════════════════════════════════════════════════════════

  test("C1: create QA violations by editing segments adversarially", async ({ page }) => {
    test.setTimeout(30_000);
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Hello world/i).first()).toBeVisible({ timeout: 10000 });

    const isMac = process.platform === "darwin";
    const translationCells = page.locator("[data-translation-cell]");

    // Seg 1 ("Hello world"): confirm as-is (TM match, should be clean)
    await translationCells.nth(0).click();
    let editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });
    await editor.click();
    await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/translations/") && r.request().method() === "PUT" && !r.url().includes("/draft"),
        { timeout: 10000 },
      ),
      page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
    ]);

    // Seg 2 ("Price: $49.99"): REMOVE the number to trigger number_consistency
    await translationCells.nth(1).click();
    editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });
    await editor.click();
    await page.keyboard.press(isMac ? "ControlOrMeta+a" : "Control+a");
    await page.keyboard.type("Preço sem número");
    await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/translations/") && r.request().method() === "PUT" && !r.url().includes("/draft"),
        { timeout: 10000 },
      ),
      page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
    ]);

    // Seg 3 ("End of sentence."): REMOVE trailing period to trigger punctuation
    await translationCells.nth(2).click();
    editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });
    await editor.click();
    await page.keyboard.press(isMac ? "ControlOrMeta+a" : "Control+a");
    await page.keyboard.type("Fim da frase");  // no period — source has one
    await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/translations/") && r.request().method() === "PUT" && !r.url().includes("/draft"),
        { timeout: 10000 },
      ),
      page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
    ]);

    // Seg 4 ("Empty segment"): CLEAR to empty, then Ctrl+Enter.
    // An empty confirm copies the source into the target, so human_text
    // becomes "Empty segment" (= source text). The "target_identical_to_source"
    // QA check should catch this.
    await translationCells.nth(3).click();
    editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });
    await editor.click();
    await page.keyboard.press(isMac ? "ControlOrMeta+a" : "Control+a");
    await page.keyboard.press("Backspace");
    await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/translations/") && r.request().method() === "PUT" && !r.url().includes("/draft"),
        { timeout: 10000 },
      ),
      page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
    ]);

    // Seg 5 ("Normal text"): confirm as-is (should be clean)
    await translationCells.nth(4).click();
    editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });
    await editor.click();
    await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/translations/") && r.request().method() === "PUT" && !r.url().includes("/draft"),
        { timeout: 10000 },
      ),
      page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
    ]);
  });

  test("C2: run QA — should detect violations from adversarial edits", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Hello world/i).first()).toBeVisible({ timeout: 10000 });

    //
    await assertQARunSucceeded(page);

    // We introduced: missing number "$49.99" (seg 2), missing period (seg 3).
    //
    // NOTE on seg 4 "empty target": an empty confirm copies the source into
    // the target, and the API rejects an empty human_text with 400, so no
    // code path produces an empty human_text in the DB.
    await expect(page.getByText(/QA completed/i).first()).toBeVisible({ timeout: 5000 });

    // "No crash != correct behavior": check the QA REPORT via API
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const reportResp = await page.request.get(`${API}/api/jobs/${jobId}/qa-report`, { headers });
    expect(reportResp.ok()).toBe(true);
    const report = await reportResp.json();
    const totalIssues = (report.data?.summary?.error ?? 0) + (report.data?.summary?.warning ?? 0);

    // QA should find at LEAST one issue per adversarial edit:
    //   Seg 2 → number_consistency (removed "$49.99")
    //   Seg 3 → punctuation         (removed trailing period)
    //   Seg 4 → target_identical_to_source (frontend copied source→target on empty confirm)
    // Three known violations means totalIssues >= 3, NOT just > 0 — a > 0
    // check passes even if only one of the three is caught.
    const results = report.data?.results ?? [];
    expect(
      totalIssues,
      `QA should find ≥3 issues (one per adversarial edit), got ${totalIssues}: ${JSON.stringify(results.map((r: any) => r.check_type))}`,
    ).toBeGreaterThanOrEqual(3);

    // Each check_type must appear at least once — assert by type, not by total.
    const checkTypes = new Set<string>(results.map((r: any) => r.check_type));
    expect(
      checkTypes.has("number_consistency"),
      `QA must catch number_consistency on seg 2 ("$49.99" removed). Got types: ${JSON.stringify([...checkTypes])}`,
    ).toBe(true);
    expect(
      checkTypes.has("punctuation"),
      `QA must catch punctuation on seg 3 (trailing period removed). Got types: ${JSON.stringify([...checkTypes])}`,
    ).toBe(true);
    expect(
      checkTypes.has("target_identical_to_source"),
      `QA must catch target_identical_to_source on seg 4 (empty confirm copied source). Got types: ${JSON.stringify([...checkTypes])}`,
    ).toBe(true);
  });

  test("C4: fix the source-copy segment + re-run QA — its finding clears", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Hello world/i).first()).toBeVisible({ timeout: 10000 });

    // Fix segment 4 — replace the copied source with a real translation
    const translationCells = page.locator("[data-translation-cell]");
    await translationCells.nth(3).click();
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });
    await editor.click();
    const isMac = process.platform === "darwin";
    await page.keyboard.press(isMac ? "ControlOrMeta+a" : "Control+a");
    await page.keyboard.type("Segmento vazio corrigido");

    const [confirmResp] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/translations/") && r.request().method() === "PUT" && !r.url().includes("/draft"),
        { timeout: 10000 },
      ),
      page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
    ]);
    expect(confirmResp.ok()).toBe(true);

    // Re-run QA, then read the stored report.
    await assertQARunSucceeded(page);
    await expect(page.getByText(/QA completed/i).first()).toBeVisible({ timeout: 5000 });

    // Segment 4 (position 3) no longer repeats its source. Other segments may
    // still carry findings, so only this segment's finding is checked.
    const report = await getQAReportViaAPI(page, jobId);
    const seg4Identical = report.results.filter(
      (r: any) => r.check_type === "target_identical_to_source" && r.segment_position === 3,
    );
    expect(
      seg4Identical,
      `target_identical_to_source must clear on seg 4 after the fix. Got: ${JSON.stringify(report.results.map((r: any) => [r.segment_position, r.check_type]))}`,
    ).toEqual([]);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D: Verify TM write-back happened (core concern repeated)
  // ════════════════════════════════════════════════════════════════════════

  test("D1: complete the job — TM should grow (pt-br locale, core concern)", async ({ page }) => {
    await login(page);

    // C1 confirmed seg 2 with "Preço sem número" for source "Price: $49.99"
    // (and other adversarial edits). Completion should write that pair to tmId.
    // The TM was imported with only "Hello world" → "Olá mundo" entries, so
    // the "Price: $49.99" entry is genuinely new — finding it proves completion wrote.
    //
    await assertCompletionPersistedToTM(page, jobId, {
      tmId,
      sourceText: "Price: $49.99",
      targetText: "Preço sem número",
    });
  });

  test("D2: verify TM has entries in TM browser", async ({ page }) => {
    await login(page);
    await page.goto(`/memories/${tmId}`);
    await expect(page.getByText(/QA Journey TM/i).first()).toBeVisible({ timeout: 10000 });
    await expect(page.getByText(/\d+ of \d+/).first()).toBeVisible({ timeout: 10000 });
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase E: Cleanup
  // ════════════════════════════════════════════════════════════════════════

  test("E1: cleanup", async ({ page }) => {
    await login(page);
    if (projectId) await deleteProjectViaAPI(page, projectId);
    if (tmId) await deleteTMViaAPI(page, tmId);
    if (qaProfileId) await deleteQAProfileViaAPI(page, qaProfileId);
    if (mockEngineId) await deleteMTEngineViaAPI(page, mockEngineId);
  });

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    try {
      await login(page);
      if (projectId) await deleteProjectViaAPI(page, projectId).catch(() => {});
      if (tmId) await deleteTMViaAPI(page, tmId).catch(() => {});
      if (qaProfileId) await deleteQAProfileViaAPI(page, qaProfileId).catch(() => {});
      if (mockEngineId) await deleteMTEngineViaAPI(page, mockEngineId).catch(() => {});
    } catch {} finally {
      await page.close();
    }
  });
});
