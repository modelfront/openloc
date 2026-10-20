import { test, expect } from "@playwright/test";
import {
  login,
  segmentEditor,
  assertTMHasEntry,
  uploadFileViaAPI,
  pretranslateFileViaAPI,
  createProjectViaAPI,
  deleteProjectViaAPI,
  createTMViaAPI,
  deleteTMViaAPI,
  setProjectMTViaAPI,
  setProjectTMsViaAPI,
  setProjectWorkflowViaAPI,
  createMTEngineViaAPI,
  deleteMTEngineViaAPI,
  verifyProjectSettings,
  deleteStepDefinitionByNameViaAPI,
} from "./helpers";

/**
 * J6 — Workflow Stages (Adversarial)
 *
 * Full lifecycle: translate → advance → review → reject → re-translate →
 * advance → advance → complete. Uses zh-hans locale (CJK script — tests
 * SPLIT_PART('zh-hans', '-', 1) = 'zh' fallback shape, different from
 * country-code locales like es-mx or fr-ca).
 */

const API = "";

let mockEngineId: number;
let mtRouterId: number | null;
let tmId: number;
let projectId: number;
let fileId: number;
let jobId: number;

test.describe.serial("J6 — Workflow Stages", () => {

  // ════════════════════════════════════════════════════════════════════════
  // Phase A: Setup — project with 3-step workflow + zh-hans
  // ════════════════════════════════════════════════════════════════════════

  test("A1: create project with zh-hans + mock MT + write-back TM", async ({ page }) => {
    await login(page);

    // Mock engine supports zh-hans natively
    mockEngineId = await createMTEngineViaAPI(page, "Workflow Journey Mock MT", "mock");

    // Empty TM for write-back. Pre-translate falls through to MT (no entries
    // → no matches), but completing the job in D4 lands confirmed segments here so
    // we can verify TM write-back works end-to-end with zh-hans. Tests the SPLIT_PART('zh-hans', '-', 1) = 'zh' base-code
    // fallback path for TM write-back specifically.
    tmId = await createTMViaAPI(page, "Workflow Journey TM");

    projectId = await createProjectViaAPI(page, "Workflow Journey", "en", ["zh-hans"]);

    mtRouterId = await setProjectMTViaAPI(page, projectId, [
      { mt_engine_id: mockEngineId, source_language: "en", target_language: "zh-hans" },
    ]);
    await setProjectTMsViaAPI(page, projectId, [
      { tm_id: tmId, source_language: "en", target_language: "zh-hans", mode: "read_write", priority: 1 },
    ]);
  });

  test("A2: set up 3-step workflow (translate → review → proofread)", async ({ page }) => {
    await login(page);

    await setProjectWorkflowViaAPI(page, projectId, [
      { name: "translate", role_required: "linguist" },
      { name: "review", role_required: "linguist" },
      { name: "proofread", role_required: "admin" },
    ]);

    await verifyProjectSettings(page, projectId, {
      workflow_step_count: 3,
      mt_router_id: mtRouterId,
      tm_count: 1,
    });
  });

  test("A3: upload file + pre-translate", async ({ page }) => {
    test.setTimeout(30_000);
    await login(page);

    const content = "Welcome to the application\nPlease enter your password\nThank you for registering\n";
    const result = await uploadFileViaAPI(page, projectId, "workflow-journey.txt", content, "text/plain");
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
  // Phase B: Translate step — confirm all + advance
  // ════════════════════════════════════════════════════════════════════════

  test("B1: open editor — workflow banner shows 'translate' step", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Welcome to the application/i).first()).toBeVisible({ timeout: 10000 });

    // The breadcrumb names the current step.
    await expect(page.getByTestId("cat-current-step")).toHaveText(/translate/i, { timeout: 5000 });

    // "Complete step" button visible (translate step is active)
    await expect(page.getByRole("button", { name: /complete step/i })).toBeVisible({ timeout: 5000 });

    // Note: Pre-translate button only shows when there are untranslated segments.
    // Since A3 already pre-translated, the button is correctly hidden here.
    // The toolbar adaptation test (hidden on review) is still valid in B5.
  });

  test("B2-B3: confirm all 3 segments", async ({ page }) => {
    test.setTimeout(30_000);
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Welcome to the application/i).first()).toBeVisible({ timeout: 10000 });

    const isMac = process.platform === "darwin";
    const translationCells = page.locator("[data-translation-cell]");

    for (const idx of [0, 1, 2]) {
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

  test("B4-B5: advance from Translate → Review", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Welcome to the application/i).first()).toBeVisible({ timeout: 10000 });

    // B2-B3 approved every segment: the dialog opens for the hand-off note,
    // with no unapproved warning.
    const completeBtn = page.getByRole("button", { name: /complete step/i });
    await expect(completeBtn).toBeVisible({ timeout: 5000 });
    await completeBtn.click();
    const dialog = page.locator('[role="dialog"]');
    await expect(dialog).toBeVisible({ timeout: 3000 });
    await expect(dialog.getByTestId("complete-unapproved")).toHaveCount(0);
    const advanced = page.waitForResponse(
      (r) => /\/jobs\/\d+\/workflow\/advance/.test(r.url()) && r.request().method() === "POST",
      { timeout: 10000 },
    );
    await dialog.getByRole("button", { name: /^Complete (final )?step$/ }).click();
    expect((await advanced).ok(), "advance should succeed").toBe(true);

    // Navigate back to editor (advance may have navigated away or dialog may have changed URL)
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Welcome to the application/i).first()).toBeVisible({ timeout: 10000 });

    // B5: Banner should now show "Review" as the active step
    await expect(page.getByText(/review/i).first()).toBeVisible({ timeout: 5000 });

    // "← Reject" button should now be visible (only on non-first steps)
    await expect(page.getByRole("button", { name: /reject/i })).toBeVisible({ timeout: 3000 });
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C: Review step — reject back to translate
  // ════════════════════════════════════════════════════════════════════════

  test("C1-C3: reject workflow — sends back to translate", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Welcome to the application/i).first()).toBeVisible({ timeout: 10000 });

    // C2: Click "← Reject"
    const rejectBtn = page.getByRole("button", { name: /reject/i });
    await expect(rejectBtn).toBeVisible({ timeout: 5000 });
    await rejectBtn.click();

    // Reject dialog with reason field
    const dialog = page.locator('[role="dialog"], [role="alertdialog"]');
    await expect(dialog).toBeVisible({ timeout: 3000 });

    // C3: Type reason and confirm
    const reasonInput = dialog.locator('textarea, input[type="text"]').first();
    await reasonInput.fill("Translation is too literal");

    // The dialog picks the nearest earlier step, translate.
    await expect(dialog.getByTestId("reject-confirm")).toHaveText(/send back to translate/i);
    const rejected = page.waitForResponse(
      (r) => /\/jobs\/\d+\/workflow\/reject/.test(r.url()) && r.request().method() === "POST",
    );
    await dialog.getByTestId("reject-confirm").click();
    expect((await rejected).ok(), "reject should succeed").toBe(true);

    // Navigate back to editor (rejection may have changed page state)
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Welcome to the application/i).first()).toBeVisible({ timeout: 10000 });

    // Should be back at "translate" step
    await expect(page.getByText(/translate/i).first()).toBeVisible({ timeout: 5000 });
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D: Fix + re-advance through all steps
  // ════════════════════════════════════════════════════════════════════════

  test("D1: re-confirm a segment after rejection", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Welcome to the application/i).first()).toBeVisible({ timeout: 10000 });

    // The rejection reason banner should be visible — the translator needs to
    // see WHY the reviewer sent this back. This is a core workflow UX: without
    // the reason, the translator has no context on what to fix.
    await expect(page.getByText(/too literal/i).first()).toBeVisible({ timeout: 5000 });

    // Re-confirm segment 1
    const translationCells = page.locator("[data-translation-cell]");
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

  test("D2: advance Translate → Review (second time)", async ({ page }) => {
    test.setTimeout(30_000);
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.locator("[data-translation-cell]").first()).toBeVisible({ timeout: 15000 });

    // "Complete step →" should be visible (translate step is active after
    // rejection).
    const completeBtn = page.getByRole("button", { name: /complete step/i });
    await expect(completeBtn).toBeVisible({ timeout: 5000 });
    await completeBtn.click();
    const dialog = page.locator('[role="dialog"]');
    await expect(dialog).toBeVisible({ timeout: 3000 });
    await dialog.getByRole("button", { name: /^Complete (final )?step$/ }).click();

    // After advancing, the app navigates to project detail (established behavior)
    await expect(page).toHaveURL(/\/projects\/\d+$/, { timeout: 10000 });
    await expect(page.getByText(/step completed|advanced/i).first()).toBeVisible({ timeout: 5000 });
  });

  test("D3: advance Review → Proofread", async ({ page }) => {
    test.setTimeout(30_000);
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.locator("[data-translation-cell]").first()).toBeVisible({ timeout: 15000 });

    // At review step — the button may say "Complete step →" or "Complete final step ✓"
    // depending on the workflow step resolution. Use a broader matcher.
    const completeBtn = page.getByRole("button", { name: /complete.*step/i });
    await expect(completeBtn).toBeVisible({ timeout: 5000 });
    await completeBtn.click();

    const dialog = page.locator('[role="dialog"]');
    await expect(dialog).toBeVisible({ timeout: 3000 });
    await dialog.getByRole("button", { name: /^Complete (final )?step$/ }).click();

    await expect(page).toHaveURL(/\/projects\/\d+$/, { timeout: 10000 });
  });

  test("D4: complete final step Proofread — all workflow done", async ({ page }) => {
    test.setTimeout(30_000);
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.locator("[data-translation-cell]").first()).toBeVisible({ timeout: 15000 });

    // Each step takes away the previous step's approvals and TM takes only the last
    // step's confirmations, so the proofreader confirms segment 1 here.
    await page.locator("[data-translation-cell]").nth(0).click();
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });
    await editor.click();
    const [confirmed] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/translations/") && r.request().method() === "PUT" && !r.url().includes("/draft"),
        { timeout: 10000 },
      ),
      page.keyboard.press(process.platform === "darwin" ? "ControlOrMeta+Enter" : "Control+Enter"),
    ]);
    expect(confirmed.ok()).toBe(true);

    // Final step — button says "Complete final step ✓"
    const completeBtn = page.getByRole("button", { name: /complete.*step/i });
    await expect(completeBtn).toBeVisible({ timeout: 5000 });
    await completeBtn.click();

    const dialog = page.locator('[role="dialog"]');
    await expect(dialog).toBeVisible({ timeout: 3000 });

    // The automation bullets are built from the job payload
    // (lib/completion-copy.ts ← job.counts.approved): completion and the TM
    // save are the only things the server does on the final step.
    await expect(dialog.getByText("Mark the job as completed")).toBeVisible();
    await expect(dialog.getByText(/Save \d+ human-approved segments? to Translation memory/)).toBeVisible();
    await expect(dialog.getByText(/feedback/i)).toHaveCount(0);

    // Anchor on the POST /workflow/advance that COMMITS the final-step
    // completion (proofread → completed, current_step → null). Text alone is
    // no signal: the confirm dialog and the success toast both contain
    // "completed" before the server has advanced.
    const advanced = page.waitForResponse(
      (r) =>
        /\/jobs\/\d+\/workflow\/advance/.test(r.url()) &&
        r.request().method() === "POST",
      { timeout: 15000 },
    );
    // The final advance chains into POST /complete (CATEditor's advanceMut
    // fires completeMut on workflow_completed), which is what closes the job.
    const completed = page.waitForResponse(
      (r) =>
        /\/jobs\/\d+\/complete$/.test(r.url()) && r.request().method() === "POST",
      { timeout: 15000 },
    );
    await dialog.getByRole("button", { name: /^Complete (final )?step$/ }).click();
    const advanceResp = await advanced;
    expect(advanceResp.ok(), "final-step advance should succeed").toBe(true);
    // httputil.Data wraps the payload in `{ data: ... }`.
    expect((await advanceResp.json()).data.status).toBe("workflow_completed");

    // One completion, one toast. completeMut alone owns the success message;
    // the advance chain's callback only navigates. Asserted after the
    // navigation so every toast the chain queues has already been dispatched.
    await completed;
    await page.waitForURL(/\/projects\/\d+$/, { timeout: 10000 });
    const completedToast = page
      .locator("[data-sonner-toast]")
      .filter({ hasText: /Job completed/i });
    // Wait for it to exist, THEN read the count once: toasts auto-dismiss
    // after 4s, so a polling toHaveCount would report the post-expiry 0
    // instead of the duplicate it actually saw.
    await expect(completedToast.first()).toBeVisible({ timeout: 10000 });
    expect(
      await completedToast.count(),
      "completing the final step must announce itself once",
    ).toBe(1);

    // Now the workflow is committed server-side; the reload's steps GET is
    // guaranteed to see no in_progress step → the editor shows the last task
    // as completed. The journey's user is a manager, so it offers the reopen.
    const stepsLoaded = page.waitForResponse(
      (r) => /\/jobs\/\d+\/workflow\/steps/.test(r.url()) && r.request().method() === "GET",
      { timeout: 20000 },
    );
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await stepsLoaded;
    await expect(page.getByTestId("task-actions")).toHaveAttribute("data-mode", "reopenable", { timeout: 10000 });
    await expect(page.getByText(/Edit a segment to reopen this task/i)).toBeVisible();
  });

  // ── TM write-back (core concern — repeated in every journey) ──

  test("D5: TM write-back from D4's completion lands confirmed zh-hans translation", async ({ page }) => {
    // D4 confirmed segment 1 in the last step with the pre-translated MT
    // output. Mock engine prefixes with `[<UPPER_LOCALE>] `
    // (internal/mt/mock.go), so target = "[ZH-HANS] Welcome to the application".
    // D4 completed the job, which persists that pair to the project's
    // read_write TM.
    // Audit M-3: tightened from `expect(status).toBeLessThan(500)` to a full
    // round-trip check that also exercises zh-hans locale handling end-to-end.
    await login(page);
    await assertTMHasEntry(page, {
      tmId,
      sourceText: "Welcome to the application",
      targetText: "[ZH-HANS] Welcome to the application",
    });
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase E: Cleanup
  // ════════════════════════════════════════════════════════════════════════

  test("E1: cleanup", async ({ page }) => {
    await login(page);
    if (projectId) await deleteProjectViaAPI(page, projectId);
    if (tmId) await deleteTMViaAPI(page, tmId);
    if (mockEngineId) await deleteMTEngineViaAPI(page, mockEngineId);
    // The step definitions A2 invented ("review" resolves to the tenant default).
    for (const name of ["translate", "proofread"]) await deleteStepDefinitionByNameViaAPI(page, name);
  });

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    try {
      await login(page);
      if (projectId) await deleteProjectViaAPI(page, projectId).catch(() => {});
      if (tmId) await deleteTMViaAPI(page, tmId).catch(() => {});
      if (mockEngineId) await deleteMTEngineViaAPI(page, mockEngineId).catch(() => {});
      for (const name of ["translate", "proofread"]) await deleteStepDefinitionByNameViaAPI(page, name).catch(() => {});
    } catch {} finally {
      await page.close();
    }
  });
});
