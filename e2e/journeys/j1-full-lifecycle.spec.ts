import { test, expect, type Page } from "@playwright/test";
import * as fs from "fs";
import * as path from "path";
import {
  login,
  startFromScratch,
  startFromTemplate,
  segmentEditor,
  fillEditorExactly,
  selectLanguage,
  selectTargetLanguages,
  addAssignment,
  advanceToFinalStepViaAPI,
  assertCompletionPersistedToTM,
  getJobTranslationsViaAPI,
  updateHumanTranslationViaAPI,
  uploadFileViaAPI,
  pretranslateFileViaAPI,
  deleteFileViaAPI,
  createTMViaAPI,
  importTMXViaAPI,
  deleteTMViaAPI,
  createTermBaseViaAPI,
  addTermBaseTermViaAPI,
  deleteTermBaseViaAPI,
  createQAProfileViaAPI,
  deleteQAProfileViaAPI,
  deleteWorkflowViaAPI,
  deleteProjectViaAPI,
  verifyProjectSettings,
  setProjectTMsViaAPI,
  setProjectMTViaAPI,
  setProjectTermsViaAPI,
  setProjectWorkflowViaAPI,
  updateProjectSettingsViaAPI,
  pickComboboxOption,
  clickComboboxAction,
  type QARule,
  submitProjectWizard,
} from "./helpers";

const API = "";

/**
 * J1 — Full Project Lifecycle (Adversarial)
 *
 * The most comprehensive journey test. Exercises: project creation with all
 * assignment types, template pre-fill clobbering, editor with TM/term base/QA,
 * settings mutation mid-flight, multi-file cross-verification.
 */

// ─── Shared state across serial steps ──────────────────────────────────────
// These are set in Phase A and consumed/cleaned up in later phases.

let journeyTMId: number;
let journeyTM2Id: number;
let journeyTermBaseId: number;
let journeyQAProfileId: number;
let journeyWorkflowId: number | undefined;
const journeyWorkflowName = `Journey workflow ${Date.now()}`;
let projectId: number;
let mtRouterId: number | null;
let fileId: number;
let file2Id: number;
let jobId: number;

// The first segment ("Reset your password") as D14 leaves it: the TM match
// "Restablezca su contraseña" that D4 confirmed, plus a word, so the text
// matches neither the TM entry nor an MT output and can only be a human edit.
const FIRST_SEGMENT_TEXT = "Restablezca su contraseña ahora";

/**
 * The first segment, as the server holds it: D14's text and approved. A
 * pre-translation that overwrote the row would leave the text or the status
 * different, and the editor's text alone could still be the TM match.
 */
async function expectFirstSegmentApproved(page: Page): Promise<void> {
  const rows = await getJobTranslationsViaAPI(page, jobId);
  expect(rows[0].status, "first segment status").toBe("approved");
  expect(rows[0].active_text, "first segment text").toBe(FIRST_SEGMENT_TEXT);
}

// ─── The journey ───────────────────────────────────────────────────────────

test.describe.serial("J1 — Full Project Lifecycle", () => {
  test.beforeAll(async ({ browser }) => {
    // Login once for the setup phase
    const page = await browser.newPage();
    await login(page);
    await page.close();
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase A: Setup — create the resources the project will depend on
  // ════════════════════════════════════════════════════════════════════════

  test("A1: create Journey TM", async ({ page }) => {
    await login(page);
    journeyTMId = await createTMViaAPI(page, "Journey TM");
    expect(journeyTMId).toBeGreaterThan(0);
  });

  test("A2: import TMX into Journey TM (5 en→es entries)", async ({ page }) => {
    await login(page);
    const tmxPath = path.resolve(__dirname, "../fixtures/journey-tm.tmx");
    const tmxContent = fs.readFileSync(tmxPath, "utf-8");
    await importTMXViaAPI(page, journeyTMId, tmxContent, "journey-tm.tmx");

    // Verify: navigate to TM browser and check entry count
    await page.goto(`/memories/${journeyTMId}`);
    await expect(page.getByText(/\d+ of \d+/).first()).toBeVisible({ timeout: 10000 });
  });

  test("A3: create Journey TM 2 (empty — for multi-TM priority test)", async ({ page }) => {
    await login(page);
    journeyTM2Id = await createTMViaAPI(page, "Journey TM 2");
    expect(journeyTM2Id).toBeGreaterThan(0);
  });

  test("A4: create Journey Term base (en→es — regional code!)", async ({ page }) => {
    await login(page);
    journeyTermBaseId = await createTermBaseViaAPI(page, "Journey Term base", "en", "es-mx");
    expect(journeyTermBaseId).toBeGreaterThan(0);
  });

  test("A5: add term base terms (password, account, settings)", async ({ page }) => {
    await login(page);
    await addTermBaseTermViaAPI(page, journeyTermBaseId, "en", "es-mx", "password", "contraseña");
    await addTermBaseTermViaAPI(page, journeyTermBaseId, "en", "es-mx", "account", "cuenta");
    await addTermBaseTermViaAPI(page, journeyTermBaseId, "en", "es-mx", "settings", "configuración");

    // Verify: navigate to term base browser. The view opens filtered to the
    // source language, so assert on a source term ("password") — the Spanish
    // target term is hidden under the default filter.
    await page.goto(`/terminology/${journeyTermBaseId}`);
    await expect(page.getByText(/password/i).first()).toBeVisible({ timeout: 5000 });
  });

  test("A6: create Journey QA profile (empty_target, tags, numbers, punctuation, terminology)", async ({ page }) => {
    await login(page);
    // Valid check_types per qa/handler.go allowlist:
    // tag_checks, number_consistency, empty_target, terminology, consistency, punctuation
    const rules: QARule[] = [
      { check_type: "empty_target", enabled: true, severity: "error" },
      { check_type: "tag_checks", enabled: true, severity: "warning" },
      { check_type: "number_consistency", enabled: true, severity: "warning" },
      { check_type: "punctuation", enabled: true, severity: "warning" },
      { check_type: "terminology", enabled: true, severity: "error" },
    ];
    journeyQAProfileId = await createQAProfileViaAPI(page, "Journey QA", rules);
    expect(journeyQAProfileId).toBeGreaterThan(0);
  });

  test("A7: verify LibreTranslate MT engine exists and is enabled", async ({ page }) => {
    await login(page);
    // Engines live under MT → Engines (the MT nav entry opens the Routers tab).
    await page.getByRole("link", { name: /^MT$/ }).click();
    await page.getByRole("link", { name: /^Engines$/ }).click();
    await expect(page).toHaveURL(/\/mt\/engines/);
    const libreRow = page
      .locator('[data-testid^="mt-engine-row-"]')
      .filter({ hasText: "LibreTranslate" });
    await expect(libreRow).toBeVisible({ timeout: 5000 });
    // Engine state is the Enabled switch INSIDE the engine's dialog,
    // not a badge and not a control on the row. Opening
    // that engine is what proves THIS engine is on, rather than that the word
    // appears somewhere on the page.
    await libreRow.getByTestId(/^mt-engine-open-/).click();
    await expect(
      page.getByTestId("mt-engine-form").getByTestId("mt-engine-enabled"),
    ).toBeChecked();
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B: Project creation — the adversarial clobbering gauntlet
  //
  // Template pre-fill → scalar change → TM add → TM2 add → MT add →
  // term base add → workflow steps → QA profile → scalar change again →
  // submit → API verify EVERYTHING survived.
  // ════════════════════════════════════════════════════════════════════════

  test("B1: navigate to New project form", async ({ page }) => {
    await login(page);
    await page.getByRole("link", { name: /new project/i }).click();
    await expect(page.getByRole("heading", { name: /new project/i })).toBeVisible();
  });

  test("B3-B13: template pre-fill + adversarial settings + submit + API verify", async ({ page }) => {
    test.setTimeout(60_000);
    await login(page);
    await page.getByRole("link", { name: /new project/i }).click();
    await expect(page.getByRole("heading", { name: /new project/i })).toBeVisible();

    // ── B1: Fill name ──

    // ── B3: Select Default Template FIRST ──
    // Template pre-fill overwrites target_languages, so we must select the
    // template BEFORE setting our custom targets. Otherwise the template's
    // setValue('target_languages', [...]) clobbers our es-mx with de/es/fr.
    await startFromTemplate(page, "Default Template");
    await page.getByPlaceholder(/project name/i).fill("Journey Test Project");

    // Wait for template pre-fill to ACTUALLY land.
    await expect(page.getByRole("spinbutton", { name: /fuzzy match threshold/i })).toBeVisible({ timeout: 8000 });
    // Wait for target-language chips from the template to appear
    await expect(
      page.locator('[data-field="target_languages"] span').filter({ hasText: /^(ES|FR|DE)×?$/ }).first(),
    ).toBeVisible({ timeout: 5000 });

    // ── B2: Now override source + add our regional target ──
    // Source stays en (template default). Remove template targets, add es-mx.
    // First remove the template-provided targets by clicking their × buttons.
    // Chip remove controls are role=button spans (aria-label "Remove …"). Only
    // visible chips render one, so clear repeatedly until none remain (handles a
    // "+N" overflow re-slicing more into view).
    const removeButtons = page.locator('[data-field="target_languages"] [aria-label^="Remove"]');
    while (await removeButtons.count() > 0) {
      await removeButtons.first().click();
    }
    // Now add our regional target
    const targetTrigger = page.getByRole("button", { name: /add target languages/i });
    await selectTargetLanguages(page, targetTrigger, ["es-mx"]);

    // ── B4: Change fuzzy threshold to 80% AFTER pre-fill ──
    // This tests that a scalar change doesn't clobber the template's TM/MT.
    // Use Playwright's .fill() which triggers React's synthetic onChange.
    const fuzzyThreshold = page.getByRole("spinbutton", { name: /fuzzy match threshold/i });
    await fuzzyThreshold.scrollIntoViewIfNeeded();
    await fuzzyThreshold.fill("80");
    await expect(fuzzyThreshold).toHaveValue("80");

    // ── B5-B8: Add assignments via API after project creation ──
    // In create mode, the assignment add-row uses the cmdk language Combobox for langs
    // which is hostile to Playwright (documented in testing.md). The existing
    // `addAssignment` helper works in EDIT mode (project settings page) but
    // struggles in CREATE mode because:
    //   1. The form is very long — scroll state is unreliable
    //   2. Template pre-fill renders existing rows as inline-editable comboboxes
    //   3. The add-row's select may be below the fold
    //
    // Strategy: create the project with template settings ONLY (which is already
    // adversarial — B3-B4's clobbering test). Then immediately after creation,
    // add our Journey TM/MT/Term base via API. This tests:
    //   - Template pre-fill survives scalar changes (B4 + B12)
    //   - API-based assignment mutation works (the same path the settings page uses)
    //   - The project's settings contain BOTH template data AND our additions
    //
    // We'll exercise the UI-based assignment flow in Phase E (settings change
    // mid-flight) where the form is in EDIT mode and `addAssignment` works.

    // ── B9-B10: Build the workflow in the wizard (the
    //    wizard's steps always come from the library, and "New workflow"
    //    creates one from the steps on screen and uses it right away). The
    //    dialog opens seeded with the template's two steps; two more go on top.
    await clickComboboxAction(page, "use-workflow-select", "create");
    const wfDialog = page.getByTestId("workflow-create-dialog");
    await expect(wfDialog).toBeVisible();
    const addStepBtn = wfDialog.getByRole("button", { name: /add step/i });
    await addStepBtn.click();
    await pickComboboxOption(page, wfDialog.getByTestId("workflow-step-picker-2"), /^Translation\b/);
    await addStepBtn.click();
    await pickComboboxOption(page, wfDialog.getByTestId("workflow-step-picker-3"), /^Review\b/);
    await wfDialog.getByTestId("workflow-create-name").fill(journeyWorkflowName);
    const [wfCreated] = await Promise.all([
      page.waitForResponse((r) => r.url().includes("/api/workflows") && r.request().method() === "POST"),
      wfDialog.getByTestId("workflow-create-save").click(),
    ]);
    expect(wfCreated.status(), "create workflow from the wizard").toBe(201);
    journeyWorkflowId = (await wfCreated.json()).data.id;
    await expect(wfDialog).toHaveCount(0);
    // The new workflow is the project's choice, with its four steps.
    await expect(page.getByTestId("use-workflow-select")).toContainText(journeyWorkflowName);
    await expect(page.getByTestId("workflow-step-badge-3")).toBeVisible();

    // ── B11: Select Journey QA profile ──
    await pickComboboxOption(page, page.getByTestId("qa-profile-select"), "Journey QA");

    // ── B12: Toggle pre-translate OFF ──
    const preTranslateCheckbox = page.getByRole("checkbox", { name: /machine translation.*start or upload/i });
    await preTranslateCheckbox.scrollIntoViewIfNeeded();
    if (await preTranslateCheckbox.isChecked()) {
      await preTranslateCheckbox.click();
    }

    // ── B13: Submit ──
    const { response: createResp, id: createdId } = await submitProjectWizard(page);
    expect(createResp.ok(), `Create project failed: ${createResp.status()}`).toBe(true);
    projectId = createdId;
    expect(projectId).toBeGreaterThan(0);

    // Wait for redirect to project detail
    await expect(page.getByRole("link", { name: /^files$/i })).toBeVisible({ timeout: 10000 });

    // ── B14a: Verify template + UI scalar changes survived creation ──
    // Workflow steps: Default Template has 2 (Translation + Review) and we added
    // 2 more in the UI (Translation + Review again) = 4 total. This is correct —
    // the template pre-fill populates workflow steps, and we added on top.
    await verifyProjectSettings(page, projectId, {
      fuzzy_threshold: 0.8,
      mt_on_upload: false,
      qa_profile_id: journeyQAProfileId,
      workflow_step_count: 4, // 2 from template + 2 from UI
    });

    // ── B14b: Add Journey TM/MT/Term base via API ──
    // (Create mode's cmdk add-rows are hostile to Playwright — documented in
    // testing.md. The API path is the same one the settings page uses.)
    await setProjectTMsViaAPI(page, projectId, [
      { tm_id: journeyTMId, source_language: "en", target_language: "es", mode: "read_write", priority: 1 },
      { tm_id: journeyTM2Id, source_language: "en", target_language: "es", mode: "read", priority: 2 },
    ]);
    // LibreTranslate supports base codes only (es, not es-mx). The pipeline's
    // ResolveLocale (internal/mt/locale.go) maps es-mx → es before calling
    // the engine.
    mtRouterId = await setProjectMTViaAPI(page, projectId, [
      { mt_engine_id: 1, source_language: "en", target_language: "es-mx" },
    ]);
    await setProjectTermsViaAPI(page, projectId, [
      { term_base_id: journeyTermBaseId, source_language: "en", target_language: "es", mode: "read", priority: 1 },
    ]);

    // ── B14c: Final verify — EVERYTHING is in place ──
    await verifyProjectSettings(page, projectId, {
      fuzzy_threshold: 0.8,
      mt_on_upload: false,
      qa_profile_id: journeyQAProfileId,
      tm_count: 2,
      mt_router_id: mtRouterId,
      term_count: 1,
      workflow_step_count: 4,
    });
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C: Upload file + pre-translate
  // ════════════════════════════════════════════════════════════════════════

  test("C1: upload txt file with adversarial segments", async ({ page }) => {
    await login(page);
    // Upload the journey source file — 5 segments chosen to trigger TM, term base, QA
    const content = fs.readFileSync(
      path.resolve(__dirname, "../fixtures/journey-source.txt"),
      "utf-8",
    );
    const result = await uploadFileViaAPI(page, projectId, "journey-source.txt", content, "text/plain");
    fileId = result.data?.id ?? result.id;
    expect(fileId).toBeGreaterThan(0);

    // Get the job ID for the es-mx target
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const jobsResp = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    const jobs = (await jobsResp.json()).data;
    expect(jobs.length).toBeGreaterThan(0);
    jobId = jobs[0].id;
  });

  test("C2-C3: open editor + pre-translate", async ({ page }) => {
    test.setTimeout(30_000);
    await login(page);

    // Navigate to the CAT editor for this job
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // Trigger pre-translate by looping production phase-split endpoints
    // (tm-apply + mt-apply per job). Mirrors the CAT editor's manual buttons.
    await pretranslateFileViaAPI(page, projectId, fileId);

    // Reload the editor to pick up the pre-translated content
    await page.reload();
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D: Editor exercise — confirm, draft, lock/unlock, TM write-back
  // ════════════════════════════════════════════════════════════════════════

  test("D1: click segment, verify term base + TM panel auto-populate", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // Click the "Reset your password" segment — it contains "password" which is
    // in both Journey TM and Journey Term base
    await page.getByText(/Reset your password/i).first().click();

    // Editor should open
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });

    // Terminology panel should auto-populate with "contraseña" from Journey Term base
    // (QA finding #20: auto-search TM/TB on segment click). Scoped to the
    // panel so a TM match or the editor text cannot satisfy it.
    await expect(
      page.getByTestId("terminology-section").getByText(/contraseña/i),
    ).toBeVisible({ timeout: 10000 });
  });

  test("D2-D3: concordance search + insert result", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // Click the first segment to activate
    await page.getByText(/Reset your password/i).first().click();
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });

    // D2: Open concordance with Ctrl+K.
    const isMacD2 = process.platform === "darwin";
    await page.keyboard.press(isMacD2 ? "ControlOrMeta+k" : "Control+k");

    // Concordance modal should appear
    await expect(page.getByText(/concordance/i).first()).toBeVisible({ timeout: 5000 });

    // Type "password" in the search field and search
    const searchInput = page.locator('input[placeholder*="earch"]').last();
    await searchInput.fill("password");
    await page.keyboard.press("Enter");

    // D3: Results should include our Journey TM entry "contraseña"
    // Scope to the concordance dialog to avoid matching the editor or term panel
    const concordanceDialog = page.locator('[role="dialog"]').filter({ hasText: /Concordance search/ });
    await expect(concordanceDialog.getByText(/contraseña/i).first()).toBeVisible({ timeout: 10000 });

    // Click the "password" → "contraseña" entry by its exact target text (not
    // the longer "Restablezca su contraseña" entry). Insert replaces the whole
    // segment text and closes the dialog.
    await concordanceDialog.getByText("contraseña", { exact: true }).click();
    await expect(concordanceDialog).toHaveCount(0);
    await expect(editor).toHaveText("contraseña");
  });

  test("D4: edit and confirm a segment (Ctrl+Enter)", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // Click the first segment
    await page.locator("[data-translation-cell]").first().click();
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });

    // Replace whatever the segment holds — D2-D3 left a concordance insert in
    // it, and `keyboard.type` would splice into that at the caret. `fill` is
    // the atomic select-all-then-type (see testing.md, "CAT/TM segment-editor
    // test types via keyboard.type").
    await editor.click();
    await editor.fill("Restablezca su contraseña");

    const isMac = process.platform === "darwin";
    const [updateResp] = await Promise.all([
      page.waitForResponse(
        (resp) => resp.url().includes("/translations/") && resp.request().method() === "PUT" && !resp.url().includes("/draft"),
        { timeout: 10000 },
      ),
      page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
    ]);
    expect(updateResp.ok(), `Confirm failed: ${updateResp.status()}`).toBe(true);
  });

  test("D5: segment is NOT locked after confirm (QA finding #18)", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // Click the first segment (which we confirmed in D4)
    await page.locator("[data-translation-cell]").first().click();

    // Should be editable — NOT locked (QA finding #18)
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });

    // Verify no lock icon is shown (🔒)
    // The lock button should say "Lock" not "Unlock" — meaning it's currently unlocked
    await expect(page.getByText(/🔓 Lock/i).first()).toBeVisible({ timeout: 3000 });
  });

  test("D6: confirm a pre-translated segment WITHOUT editing (QA finding #16)", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // Click the SECOND segment (should have pre-translated MT text)
    const translationCells = page.locator("[data-translation-cell]");
    await translationCells.nth(1).click();
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });

    // Confirm WITHOUT editing — just Ctrl+Enter on the pre-translated text
    // QA finding #16: status must change to approved even without edits
    const isMac = process.platform === "darwin";
    const [updateResp] = await Promise.all([
      page.waitForResponse(
        (resp) => resp.url().includes("/translations/") && resp.request().method() === "PUT" && !resp.url().includes("/draft"),
        { timeout: 10000 },
      ),
      page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
    ]);
    expect(updateResp.ok(), `Confirm-without-edit failed: ${updateResp.status()}`).toBe(true);
  });

  test("D9: confirm segment with number $49.99 preserved", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // Click the FIFTH segment ("Price: $49.99")
    const translationCells = page.locator("[data-translation-cell]");
    await translationCells.nth(4).click();
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });

    // Select all + type translation preserving the number
    await editor.click();
    const isMacD9 = process.platform === "darwin";
    await page.keyboard.press(isMacD9 ? "ControlOrMeta+a" : "Control+a");
    await page.keyboard.type("Precio: $49.99");

    const [resp] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/translations/") && r.request().method() === "PUT" && !r.url().includes("/draft"),
        { timeout: 10000 },
      ),
      page.keyboard.press(isMacD9 ? "ControlOrMeta+Enter" : "Control+Enter"),
    ]);
    expect(resp.ok(), `Confirm with number failed: ${resp.status()}`).toBe(true);
  });

  test("D8: confirm remaining segments (3 + 4) to complete the job", async ({ page }) => {
    test.setTimeout(30_000);
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // Confirmed so far: seg 1 (D4), seg 2 (D6), seg 5 (D9). Segs 3 and 4
    // ("TODO: fix this later", "Enter your email address") are still open.
    // Confirm seg 3 (index 2) and seg 4 (index 3).
    const isMacD8 = process.platform === "darwin";
    const translationCells = page.locator("[data-translation-cell]");

    for (const idx of [2, 3]) {
      await translationCells.nth(idx).click();
      const editor = segmentEditor(page).first();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.click();

      const [resp] = await Promise.all([
        page.waitForResponse(
          (r) => r.url().includes("/translations/") && r.request().method() === "PUT" && !r.url().includes("/draft"),
          { timeout: 10000 },
        ),
        page.keyboard.press(isMacD8 ? "ControlOrMeta+Enter" : "Control+Enter"),
      ]);
      expect(resp.ok(), `Confirm segment ${idx + 1} failed: ${resp.status()}`).toBe(true);
    }
    // All 5 segments should now be confirmed
  });

  // ── D11-D14: Lock/unlock/edit/re-confirm (QA finding #17) ──
  // Lock the segment D4 confirmed, unlock it, edit it and re-confirm. The
  // cycle must leave the segment editable and confirmable; the edit makes the
  // confirm send a PUT (an unchanged approved segment may send none). TM
  // write-back after a confirm is pinned by j30.

  test("D11-D14: lock, unlock, edit, re-confirm", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // Click the first segment (confirmed in D4 with "Restablezca su contraseña")
    await page.locator("[data-translation-cell]").first().click();
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });

    // D12: Lock the segment
    const lockBtn = page.getByText(/🔓 Lock/i).first();
    await expect(lockBtn).toBeVisible({ timeout: 3000 });
    await lockBtn.click();
    // Should now show "Unlock"
    await expect(page.getByText(/🔒 Unlock/i).first()).toBeVisible({ timeout: 3000 });

    // D13: Unlock the segment
    await page.getByText(/🔒 Unlock/i).first().click();
    await expect(page.getByText(/🔓 Lock/i).first()).toBeVisible({ timeout: 3000 });

    // D14: edit, then re-confirm. The lock and unlock each refetch the
    // translations, and a refetch landing inside a single fill splices the
    // text into the old one; fillEditorExactly fills until the editor holds it.
    await editor.click();
    await fillEditorExactly(editor, FIRST_SEGMENT_TEXT);
    const isMacD14 = process.platform === "darwin";
    const [confirmResp] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/translations/") && r.request().method() === "PUT" && !r.url().includes("/draft"),
        { timeout: 10000 },
      ),
      page.keyboard.press(isMacD14 ? "ControlOrMeta+Enter" : "Control+Enter"),
    ]);
    expect(confirmResp.ok(), `Re-confirm after lock/unlock failed: ${confirmResp.status()}`).toBe(true);
    await expectFirstSegmentApproved(page);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase E: Mid-flight settings change
  // The adversarial part: mutate settings AFTER translations exist, then
  // verify the mutations stuck AND didn't corrupt existing translations.
  // ════════════════════════════════════════════════════════════════════════

  test("E1-E3: change fuzzy, remove TM 2, change QA profile via API + verify", async ({ page }) => {
    await login(page);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // E1: Change fuzzy threshold 80% → 60%
    await updateProjectSettingsViaAPI(page, projectId, { fuzzy_threshold: 0.6 });

    // E2: Remove Journey TM 2 — keep only Journey TM
    await setProjectTMsViaAPI(page, projectId, [
      { tm_id: journeyTMId, source_language: "en", target_language: "es-mx", mode: "read_write", priority: 1 },
    ]);

    // E3: Change QA profile from Journey QA → Standard QA (ID 1 from seed)
    await updateProjectSettingsViaAPI(page, projectId, { qa_profile_id: 1 });

    // Verify ALL settings survived the mutations
    await verifyProjectSettings(page, projectId, {
      fuzzy_threshold: 0.6,
      mt_on_upload: false,
      qa_profile_id: 1,           // Standard QA (not Journey QA)
      tm_count: 1,                // Journey TM only (TM 2 removed)
      mt_router_id: mtRouterId,   // LibreTranslate router still there
      term_count: 1,              // Journey Term base still there
      workflow_step_count: 4,     // unchanged
    });
  });

  test("E4: verify QA profile change reflected in settings UI", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/files`);
    await page.getByRole("link", { name: /settings/i }).click();

    // The QA profile combobox should show "Standard QA" selected (changed in E3).
    await expect(page.getByText(/fuzzy match threshold/i).first()).toBeVisible({ timeout: 5000 });
    await expect(page.getByTestId("qa-profile-select")).toContainText("Standard QA", { timeout: 3000 });
  });

  test("E5: verify first file's translations survived settings change", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // The confirmed segments from Phase D should still be there
    // Click the first segment — it should show the translation confirmed in D14
    await page.locator("[data-translation-cell]").first().click();
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });
    await expect(editor).toHaveText(FIRST_SEGMENT_TEXT);
    await expectFirstSegmentApproved(page);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase F: Second file upload after settings change
  // Critical: verify the pipeline uses the UPDATED settings (fuzzy=60%,
  // only Journey TM 1, Standard QA profile).
  // ════════════════════════════════════════════════════════════════════════

  test("F1-F2: upload second file after settings change", async ({ page }) => {
    await login(page);

    // Upload a second .txt file with different content
    const content2 = "Welcome to the app\nPlease confirm your email\nThank you for signing up\n";
    const result = await uploadFileViaAPI(page, projectId, "journey-source-2.txt", content2, "text/plain");
    file2Id = result.data?.id ?? result.id;
    expect(file2Id).toBeGreaterThan(0);
  });

  test("F3-F4: pre-translate second file with updated settings", async ({ page }) => {
    test.setTimeout(30_000);
    await login(page);

    // Trigger pre-translate via the per-job phase endpoints — picks up the
    // updated settings (fuzzy=60%, Journey TM only, Standard QA profile).
    await pretranslateFileViaAPI(page, projectId, file2Id);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Navigate to the second file's job in the editor
    // Get the job ID for the es-mx target on the second file
    const jobsResp = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    const jobs = (await jobsResp.json()).data;
    // Find the job for file2
    const file2Job = jobs.find((j: any) => j.file_id === file2Id);
    expect(file2Job, "second file should have a job").toBeDefined();

    await page.goto(`/projects/${projectId}/jobs/${file2Job.id}/edit`);
    await expect(page.getByText(/Welcome to the app/i).first()).toBeVisible({ timeout: 10000 });

    // Verify segments have MT text (LibreTranslate should have translated them
    // using the locale resolver: es-mx → es)
    const translationCells = page.locator("[data-translation-cell]");
    const firstCellText = await translationCells.first().textContent();
    expect(firstCellText?.length, "second file's segments should have pre-translated text").toBeGreaterThan(0);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase G: Cross-file verification
  // Go back to the first file and verify nothing was corrupted by the
  // second file's pipeline run.
  // ════════════════════════════════════════════════════════════════════════

  test("G1-G2: first file's translations intact after second file pipeline", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // All 5 segments should still be approved from Phase D
    // Click the first segment to verify its text is still the D14 translation
    await page.locator("[data-translation-cell]").first().click();
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });
    await expect(editor).toHaveText(FIRST_SEGMENT_TEXT);
    await expectFirstSegmentApproved(page);
  });

  test("G3: complete the job — TM write-back with current settings", async ({ page }) => {
    await login(page);

    // Complete the first file's job. Only Journey TM is assigned (TM 2 went in
    // Phase E). D9's confirm already wrote "Price: $49.99" → "Precio: $49.99"
    // to it, so that entry cannot tell a confirm from the completion
    // write-back. Re-confirm the segment with a new target while the TM is
    // read-only — a confirm writes nothing then — restore read_write, and
    // complete: the new target can only have come from the completion.
    // Each step confirms its own segments: confirm in the final step, the
    // one whose confirmations the completion writes to TM.
    await advanceToFinalStepViaAPI(page, jobId);
    const tm = { tm_id: journeyTMId, source_language: "en", target_language: "es-mx", priority: 1 };
    await setProjectTMsViaAPI(page, projectId, [{ ...tm, mode: "read" }]);
    const rows = await getJobTranslationsViaAPI(page, jobId);
    await updateHumanTranslationViaAPI(page, fileId, rows[4].id, "Precio final: $49.99");
    await setProjectTMsViaAPI(page, projectId, [{ ...tm, mode: "read_write" }]);

    await assertCompletionPersistedToTM(page, jobId, {
      tmId: journeyTMId,
      sourceText: "Price: $49.99",
      targetText: "Precio final: $49.99",
      absentBefore: true,
    });
  });

  test("G5: export first file's job", async ({ page }) => {
    await login(page);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Export via API — verify it returns a file (200 with content)
    const exportResp = await page.request.get(`${API}/api/files/${fileId}/export?lang=es-mx`, { headers });
    expect(exportResp.ok(), `Export failed: ${exportResp.status()}`).toBe(true);
    const body = await exportResp.body();
    expect(body.length, "exported file should have content").toBeGreaterThan(0);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase H: Cleanup
  // ════════════════════════════════════════════════════════════════════════

  test("H1-H4: cleanup all journey resources", async ({ page }) => {
    await login(page);

    // Delete files first (FK), then project, then resources
    if (file2Id && projectId) await deleteFileViaAPI(page, projectId, file2Id).catch(() => {});
    if (fileId && projectId) await deleteFileViaAPI(page, projectId, fileId).catch(() => {});
    if (projectId) await deleteProjectViaAPI(page, projectId);
    if (journeyTMId) await deleteTMViaAPI(page, journeyTMId);
    if (journeyTM2Id) await deleteTMViaAPI(page, journeyTM2Id);
    if (journeyTermBaseId) await deleteTermBaseViaAPI(page, journeyTermBaseId);
    if (journeyQAProfileId) await deleteQAProfileViaAPI(page, journeyQAProfileId);
    if (journeyWorkflowId) await deleteWorkflowViaAPI(page, journeyWorkflowId);

    // Verify project is gone
    await page.goto("/");
    await expect(page.getByText(/Journey Test Project/)).not.toBeVisible({ timeout: 3000 });
  });

  // ════════════════════════════════════════════════════════════════════════
  // Cleanup fallback — if the test suite is interrupted, try to clean up.
  // Phase H handles the normal case; this catches crashes.
  // ════════════════════════════════════════════════════════════════════════

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    try {
      await login(page);
      // Delete files first (FK constraint), then project, then resources
      if (file2Id && projectId) await deleteFileViaAPI(page, projectId, file2Id).catch(() => {});
      if (fileId && projectId) await deleteFileViaAPI(page, projectId, fileId).catch(() => {});
      if (projectId) await deleteProjectViaAPI(page, projectId);
      if (journeyTMId) await deleteTMViaAPI(page, journeyTMId);
      if (journeyTM2Id) await deleteTMViaAPI(page, journeyTM2Id);
      if (journeyTermBaseId) await deleteTermBaseViaAPI(page, journeyTermBaseId);
      if (journeyQAProfileId) await deleteQAProfileViaAPI(page, journeyQAProfileId);
      if (journeyWorkflowId) await deleteWorkflowViaAPI(page, journeyWorkflowId);
    } catch {
      // swallow — cleanup is best-effort
    } finally {
      await page.close();
    }
  });
});
