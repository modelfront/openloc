import { test, expect } from "@playwright/test";
import {
  login,
  segmentEditor,
  uploadFileViaAPI,
  createTMViaAPI,
  deleteTMViaAPI,
  createProjectViaAPI,
  deleteProjectViaAPI,
  createQAProfileViaAPI,
  deleteQAProfileViaAPI,
  updateProjectSettingsViaAPI,
  type QARule,
} from "./helpers";

/**
 * J29 — QA blocking on confirm
 *
 * `HandleUpdateHuman` has a server-side QA gate. When a confirm save would promote a
 * translation past `needs_review` AND any active QA rule with
 * `severity=error` fires against the proposed text, the handler
 * returns 422 with `error.code = "QAErrorsBlocked"` and the issue
 * list. The frontend (SegmentRow.tsx::confirmAndAdvance onError)
 * catches this, surfaces a toast, and leaves the segment in its
 * pre-confirm state.
 *
 * That contract is also pinned by backend integration tests and FE
 * unit tests. J29 is the keystroke-driven E2E that proves the WHOLE PATH works
 * — Cmd+Enter → mutation → 422 → toast → user fixes → Cmd+Enter →
 * 200 → status promoted.
 *
 * **Why `target_identical_to_source` instead of `tag_checks`:** both rules flow through the same
 * `enforceQAOnConfirm` gate, so the contract pin is identical.
 * `target_identical_to_source` works on plain TXT (no XLIFF parser
 * needed), keeping the test focused on the BLOCK behavior. Tag
 * mismatches as a QA-rule-fires-correctly diagnostic are covered by
 * j4-qa-workflow.
 *
 * Locale: ja-jp — distinct from j1 (es-mx), j2 (fr-ca), j4 (pt-br),
 * j6 (zh-hans), per the "repeat core concerns" rule.
 *
 * Anti-tautology proof for the regression guard:
 *   Mutation:    handler_qa.go::enforceQAOnConfirm — replace the
 *                `if !result.HasErrors() { return false }` early-out
 *                with `return false` so the gate never fires.
 *   Diagnostic:  Phase B's "Cmd+Enter is BLOCKED" assertion would
 *                fail because the API would return 200 + segment
 *                would advance past needs_review.
 *   Proven:      2026-05-07.
 */

const API = "";

let qaProfileId: number;
let tmId: number;
let projectId: number;
let fileId: number;
let jobId: number;
let translationId: number;

test.describe.serial("J29 — QA blocking on confirm", () => {

  // ════════════════════════════════════════════════════════════════════════
  // Phase A: Setup — QA profile with target_identical_to_source as ERROR
  // ════════════════════════════════════════════════════════════════════════

  test("A1: create QA profile with target_identical_to_source severity=error", async ({ page }) => {
    await login(page);

    // The single load-bearing rule for J29: target_identical_to_source
    // pinned to error severity. Without `severity=error`, the gate
    // wouldn't fire — we'd be testing nothing.
    const rules: QARule[] = [
      { check_type: "target_identical_to_source", enabled: true, severity: "error" },
    ];
    qaProfileId = await createQAProfileViaAPI(page, "J29 QA Block Profile", rules);
    expect(qaProfileId, "QA profile must be created").toBeGreaterThan(0);
  });

  test("A2: create TM + project en→ja-jp + assign QA profile", async ({ page }) => {
    await login(page);

    tmId = await createTMViaAPI(page, "J29 TM");
    projectId = await createProjectViaAPI(page, "J29 QA Block Project", "en", ["ja-jp"]);
    await updateProjectSettingsViaAPI(page, projectId, { qa_profile_id: qaProfileId });
  });

  test("A3: upload file + pre-fill target identical to source (the trap)", async ({ page }) => {
    test.setTimeout(20_000);
    await login(page);

    // One-segment TXT. The segment text is what BOTH source and target
    // will hold — that's the trap that target_identical_to_source
    // detects. Single segment keeps the journey tight.
    const result = await uploadFileViaAPI(
      page,
      projectId,
      "j29-qa-block.txt",
      "Hello world.\n",
      "text/plain",
    );
    fileId = result.data?.id ?? result.id;
    expect(fileId, "file must upload").toBeGreaterThan(0);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Find the job for ja-jp.
    const jobsResp = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    const jobs = (await jobsResp.json()).data;
    const job = jobs.find((j: { target_language: string }) => j.target_language === "ja-jp");
    expect(job, "ja-jp job must exist").toBeDefined();
    jobId = job.id;

    // Find the translation for the segment.
    const translationsResp = await page.request.get(
      `${API}/api/jobs/${jobId}/translations`,
      { headers },
    );
    const translations = (await translationsResp.json()).data;
    expect(translations.length, "exactly 1 translation").toBe(1);
    translationId = translations[0].id;

    // Pre-fill the target with text identical to the source. We MUST
    // use the DRAFT endpoint (`PUT .../translations/:id/draft`) — the
    // confirm endpoint (`HandleUpdateHuman`) runs the same QA gate
    // we're trying to test, so it would 422 on us during setup
    // (which would itself be a meta-confirmation that the gate works,
    // but blocks the trap from being seeded). Drafts bypass QA per
    // the architecture spec §F: "auto-save during typing has different
    // semantics. Only the confirm path enforces."
    const draftResp = await page.request.put(
      `${API}/api/files/${fileId}/translations/${translationId}/draft`,
      {
        headers,
        data: { human_text: "Hello world.", editing_time_ms: 0 },
      },
    );
    expect(draftResp.ok(), `seed draft: ${draftResp.status()}`).toBe(true);

    // Verify the trap is set: status is still needs_review (not approved).
    const verifyResp = await page.request.get(
      `${API}/api/jobs/${jobId}/translations`,
      { headers },
    );
    const t = (await verifyResp.json()).data[0];
    expect(t.active_text, "target identical to source — the trap").toBe("Hello world.");
    expect(t.status, "status must NOT be approved yet").not.toBe("approved");
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B: Cmd+Enter is BLOCKED by the QA gate
  // ════════════════════════════════════════════════════════════════════════
  //
  // The headline behavior: a translator presses Cmd+Enter to confirm
  // a segment whose target equals its source. The backend's QA gate
  // returns 422 + QAErrorsBlocked code; the FE shows a toast; the
  // segment stays at needs_review.

  test("B: Cmd+Enter on identical-to-source target → toast + status NOT promoted", async ({ page }) => {
    test.setTimeout(20_000);
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);

    // Source visible = editor loaded. Use first() — the segment text
    // appears in BOTH source and target columns since they're equal.
    await expect(page.getByText(/Hello world/i).first()).toBeVisible({ timeout: 10000 });

    // Activate the segment by clicking the translation cell.
    const translationCells = page.locator("[data-translation-cell]");
    await translationCells.first().click();
    await expect(page.locator("[data-active-row]")).toBeVisible({ timeout: 3000 });

    // The editor should show "Hello world." (the API-pre-filled target).
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });

    // Capture API state BEFORE the keypress so we can compare.
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const beforeResp = await page.request.get(
      `${API}/api/jobs/${jobId}/translations`,
      { headers },
    );
    const beforeStatus = (await beforeResp.json()).data[0].status;

    // Click into the editor so the keymap is active. The editor's
    // confirmAndAdvance lives in the TipTap keymap — focus must be
    // inside the ProseMirror DOM for the binding to fire (j16 lesson).
    await editor.click();

    // The confirm keypress. The FE will:
    //   1. Save mutation kicked off with status promotion.
    //   2. Backend runs enforceQAOnConfirm → fires target_identical_to_source.
    //   3. Returns 422 + QAErrorsBlocked.
    //   4. FE catches via isQABlockError → toast.error(formatQAErrorToast).
    await page.keyboard.press("ControlOrMeta+Enter");

    // OBSERVABLE OUTCOME 1 — error toast from Sonner. Use the
    // data-sonner-toast pattern (matches j11). The toast text starts
    // with "Fix QA error" or "Fix N QA errors" — both match /Fix.*QA error/i.
    const errorToast = page.locator("[data-sonner-toast][data-type='error']");
    await expect(errorToast, "QA-block toast must appear").toBeVisible({ timeout: 5000 });
    await expect(errorToast).toContainText(/Fix.*QA error/i);

    // OBSERVABLE OUTCOME 2 — segment status did NOT advance. This is
    // the load-bearing assertion: even if the toast fires, a regression
    // that ALSO ran the UPDATE before short-circuiting would silently
    // promote the status. The integration test
    // "TestEnforceQAOnConfirm_NoUpdateWhenBlocked" pins this server-side;
    // here we pin it from the user's vantage point.
    const afterResp = await page.request.get(
      `${API}/api/jobs/${jobId}/translations`,
      { headers },
    );
    const afterStatus = (await afterResp.json()).data[0].status;
    expect(afterStatus, "status must NOT have advanced past pre-confirm state").toBe(beforeStatus);
    expect(afterStatus, "status must NOT be 'approved' (the block worked)").not.toBe("approved");
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C: Fix the trap → Cmd+Enter succeeds
  // ════════════════════════════════════════════════════════════════════════
  //
  // The translator changes the target to a real Japanese translation,
  // then presses Cmd+Enter again. With the rule no longer firing, the
  // backend returns 200 and the status advances.

  test("C: type a different target → Cmd+Enter succeeds + status advances", async ({ page }) => {
    test.setTimeout(20_000);
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);

    await expect(page.getByText(/Hello world/i).first()).toBeVisible({ timeout: 10000 });

    const translationCells = page.locator("[data-translation-cell]");
    await translationCells.first().click();
    await expect(page.locator("[data-active-row]")).toBeVisible({ timeout: 3000 });

    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });

    // Replace the editor contents with a real Japanese translation —
    // target ≠ source means target_identical_to_source no longer fires.
    await editor.click();
    const isMac = process.platform === "darwin";
    await page.keyboard.press(isMac ? "ControlOrMeta+a" : "Control+a");
    await page.keyboard.type("こんにちは世界。");

    // Confirm. Backend QA gate accepts (no error-severity issues fire);
    // status advances out of needs_review. The confirm is the PUT on the
    // translation itself (the autosave PUT ends in /draft and is excluded).
    const [confirmResp] = await Promise.all([
      page.waitForResponse(
        (r) =>
          r.request().method() === "PUT" &&
          /\/translations\/\d+$/.test(r.url().split("?")[0]),
        { timeout: 10_000 },
      ),
      page.keyboard.press("ControlOrMeta+Enter"),
    ]);
    expect(confirmResp.status(), "the confirm PUT must succeed — the QA gate no longer blocks").toBe(200);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const resp = await page.request.get(
      `${API}/api/jobs/${jobId}/translations`,
      { headers },
    );
    const t = (await resp.json()).data[0];

    // OBSERVABLE OUTCOME 1 — human_text was actually saved (not just toast).
    expect(t.active_text, "human_text must reflect the typed Japanese target").toBe("こんにちは世界。");

    // OBSERVABLE OUTCOME 2 — status advanced. With the QA error gone,
    // confirm-flow promotes from needs_review → approved (or similar
    // confirmed state). The exact target enum depends on workflow
    // rules, but the user's intent ("I confirmed this") MUST be
    // visible in the status.
    expect(t.status, "status must have advanced — confirm path completed").not.toBe("needs_review");
    expect(t.status, "status must NOT be untranslated").not.toBe("untranslated");

    // OBSERVABLE OUTCOME 3 — no error toast on this confirm. This test runs on
    // a fresh page, so no toast from Phase B can still be on screen.
    await expect(
      page.locator("[data-sonner-toast][data-type='error']"),
      "no error toast on the successful confirm",
    ).toHaveCount(0);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D: Cleanup
  // ════════════════════════════════════════════════════════════════════════

  test("D: cleanup", async ({ page }) => {
    await login(page);
    if (projectId) await deleteProjectViaAPI(page, projectId);
    if (tmId) await deleteTMViaAPI(page, tmId);
    if (qaProfileId) await deleteQAProfileViaAPI(page, qaProfileId);
  });

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    try {
      await login(page);
      if (projectId) await deleteProjectViaAPI(page, projectId).catch(() => {});
      if (tmId) await deleteTMViaAPI(page, tmId).catch(() => {});
      if (qaProfileId) await deleteQAProfileViaAPI(page, qaProfileId).catch(() => {});
    } catch {} finally {
      await page.close();
    }
  });
});
