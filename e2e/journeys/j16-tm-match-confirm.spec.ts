/**
 * J16 — TM Match Click Then Cmd+Enter (Adversarial)
 *
 * User-reported bug: clicking between segments, landing on one with multiple
 * TM matches in the sidebar, clicking one TM match, then another, then
 * pressing Cmd+Enter to confirm — "seemed broken" (segment didn't confirm).
 *
 * This journey reproduces the exact interaction sequence:
 *   1. Create a TM with two different translations for the same source text
 *      (multiple targets per source are allowed).
 *   2. Upload a file containing that source in multiple segments.
 *   3. Pre-translate (so TM matches populate and the sidebar shows both).
 *   4. Open CAT editor.
 *   5. Click around between segments (mimics user navigation).
 *   6. Land on the segment with 2 TM matches in the sidebar.
 *   7. Click TM match #1 — editor text replaced.
 *   8. Click TM match #2 — editor text replaced again.
 *   9. Press Cmd+Enter (Mod+Enter).
 *  10. Assert: segment status changed to "translated" AND human_text == TM match #2.
 *
 * The assertions are observable outcomes (API check on the translation record),
 * not just "no error thrown". The editText state is populated by
 * replaceAll() (from the sidebar click), not by typing in the TipTap editor.
 */
import { test, expect } from "@playwright/test";
import {
  login,
  uploadFileViaAPI,
  createTMViaAPI,
  importTMXViaAPI,
  createProjectViaAPI,
  setProjectTMsViaAPI,
  setProjectMTViaAPI,
  createMTEngineViaAPI,
  deleteProjectViaAPI,
  deleteTMViaAPI,
  deleteMTEngineViaAPI,
} from "./helpers";

const API = "";

// Source file: each line has the "magic" source we want multiple TM matches for.
// The magic source is the THIRD line so we have to navigate past some segments.
const SOURCE_FILE = [
  "Welcome to the system.",
  "Please sign in.",
  "Reset your password.",
  "Thank you for your patience.",
].join("\n");

// TMX with TWO different targets for "Reset your password." — multiple targets per source are allowed.
// Sidebar should show both as separate match cards.
const TMX = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header creationtool="J16-test" datatype="plaintext" segtype="sentence" adminlang="en" srclang="en"/>
  <body>
    <tu tuid="j16-001"><tuv xml:lang="en"><seg>Reset your password.</seg></tuv><tuv xml:lang="es-mx"><seg>Restablezca su contraseña.</seg></tuv></tu>
    <tu tuid="j16-002"><tuv xml:lang="en"><seg>Reset your password.</seg></tuv><tuv xml:lang="es-mx"><seg>Reinicie su contraseña ahora.</seg></tuv></tu>
    <tu tuid="j16-003"><tuv xml:lang="en"><seg>Welcome to the system.</seg></tuv><tuv xml:lang="es-mx"><seg>Bienvenido al sistema.</seg></tuv></tu>
  </body>
</tmx>`;

// Unique suffix so parallel runs don't collide
const RUN_ID = Date.now();
const TM_NAME = `J16 MultiMatch TM ${RUN_ID}`;
const MT_NAME = `J16 Mock MT ${RUN_ID}`;
const PROJECT_NAME = `J16 TM Match Confirm ${RUN_ID}`;

let tmId: number;
let mtEngineId: number;
let projectId: number;
let fileId: number;
let jobId: number;

async function authHeader(page) {
  const token = await page.evaluate(() => localStorage.getItem("token"));
  return { Authorization: `Bearer ${token}` };
}

test.describe.serial("J16 — TM Match Click Then Cmd+Enter", () => {
  // ─── Phase A: Setup ─────────────────────────────────────────────────────

  test("A1: create TM with two different targets for the same source", async ({ page }) => {
    await login(page);
    tmId = await createTMViaAPI(page, TM_NAME);
    await importTMXViaAPI(page, tmId, TMX, "j16.tmx");
    // Give the async import a moment to settle
    await page.waitForTimeout(500);

    // Verify the TM actually has 3 entries (2 for "Reset your password.", 1 for "Welcome to the system.")
    const headers = await authHeader(page);
    const res = await page.request.get(`${API}/api/tms/${tmId}/entries?limit=100`, { headers });
    const body = await res.json();
    expect(body.data.length,
      "A1: TM should have 3 entries (2 same-source + 1 other)").toBe(3);
    const passwordEntries = body.data.filter((e: any) => e.source_text === "Reset your password.");
    expect(passwordEntries.length,
      "A1: TM should have 2 entries for 'Reset your password.' (multi-target)").toBe(2);
  });

  test("A2: create project en→es-mx + assign TM + mock MT", async ({ page }) => {
    await login(page);
    mtEngineId = await createMTEngineViaAPI(page, MT_NAME, "mock", "");
    projectId = await createProjectViaAPI(page, PROJECT_NAME, "en", ["es-mx"]);

    await setProjectTMsViaAPI(page, projectId, [
      { tm_id: tmId, source_language: "en", target_language: "es-mx", mode: "read_write", priority: 1 },
    ]);
    await setProjectMTViaAPI(page, projectId, [
      { mt_engine_id: mtEngineId, source_language: "en", target_language: "es-mx" },
    ]);
  });

  test("A3: upload file — NO pre-translate (we want untranslated segments so Cmd+Enter has real work to do)", async ({ page }) => {
    // IMPORTANT: we deliberately do NOT pre-translate. When the pipeline runs
    // with a TM that exact-matches, it auto-approves the segment — both
    // active_text and human_text get set and status becomes "approved",
    // which means Cmd+Enter would be a no-op. We want the segment to start
    // in "untranslated" state so the user's interaction (click TM matches,
    // Cmd+Enter) is meaningful.
    await login(page);
    // Disable BOTH mt_on_upload AND tm_on_upload so the upload doesn't trigger
    // any pipeline (tm_on_upload and mt_on_upload are independent flags).
    // With tm_on_upload on, the TM (which has an exact match for "Reset your password.") would auto-fill
    // the segment on upload and the test below would fail its "untranslated"
    // starting-state assertion.
    await page.request.patch(`${API}/api/projects/${projectId}/settings`, {
      headers: await authHeader(page),
      data: { mt_on_upload: false, tm_on_upload: false },
    });

    const result = await uploadFileViaAPI(page, projectId, "j16.txt", SOURCE_FILE, "text/plain");
    fileId = result.data?.id ?? result.id;

    const headers = await authHeader(page);
    const jobsRes = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    const jobsBody = await jobsRes.json();
    expect(jobsBody.data.length).toBe(1);
    jobId = jobsBody.data[0].id;
  });

  // ─── Phase B: The bug reproduction ──────────────────────────────────────

  test("B: click around, click two TM matches, Cmd+Enter — segment must confirm", async ({ page }) => {
    test.setTimeout(45_000);
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);

    // Wait for editor + segments to render
    await expect(page.locator("[data-translation-cell]").first()).toBeVisible({ timeout: 10_000 });

    // Fetch segments to find the "Reset your password." segment_id, then
    // fetch translations and find the one whose segment_id matches.
    // (The translations endpoint doesn't include source_text — that's on segments.)
    const headers = await authHeader(page);
    const segRes = await page.request.get(`${API}/api/files/${fileId}/segments`, { headers });
    const segBody = await segRes.json();
    const passwordSegment = (segBody.data as any[]).find(
      (s) => s.source_text === "Reset your password.",
    );
    expect(passwordSegment,
      "B: should find the 'Reset your password.' segment in file").toBeDefined();
    const passwordSegmentId = passwordSegment.id;

    const transRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const transBody = await transRes.json();
    // eslint-disable-next-line no-console
    console.log(`[J16] translations (${transBody.data?.length ?? 0}):`,
      JSON.stringify((transBody.data as any[])?.map(t => ({
        id: t.id, seg: t.segment_id, status: t.status, active: t.active_text, human: t.active_text,
      })) ?? []));

    const passwordTranslation = (transBody.data as any[]).find(
      (t) => t.segment_id === passwordSegmentId,
    );
    expect(passwordTranslation,
      "B: should find translation for the 'Reset your password.' segment").toBeDefined();
    const passwordTranslationId = passwordTranslation.id;

    // Capture the initial state — should be untranslated (no pre-translate ran)
    const humanTextBefore = passwordTranslation.active_text ?? "";
    const statusBefore = passwordTranslation.status;
    // eslint-disable-next-line no-console
    console.log(`[J16] initial state — status=${statusBefore}, human_text="${humanTextBefore}", active="${passwordTranslation.active_text}"`);
    // 'untranslated' is derived — raw status is 'needs_review'
    // with empty active_text (asserted below).
    expect(statusBefore,
      "B: segment should start at needs_review (no pre-translate)").toBe("needs_review");
    expect(humanTextBefore,
      "B: active_text should be empty initially").toBe("");

    // ═══ Mimic the user's actions ═══

    // Click segment #1 (Welcome to the system.) — user navigation
    const cells = page.locator("[data-translation-cell]");
    await cells.nth(0).click();
    await page.waitForTimeout(300);
    // Click segment #2 (Please sign in.)
    await cells.nth(1).click();
    await page.waitForTimeout(300);
    // Click segment #3 (Reset your password.) — this is our target
    await cells.nth(2).click();
    await page.waitForTimeout(500);

    // The active row should now be showing "Reset your password."
    const activeRow = page.locator("[data-active-row]");
    await expect(activeRow).toBeVisible({ timeout: 3000 });

    // Sidebar should show 2 TM matches
    const sidebar = page.locator('[data-testid="editor-sidebar"]');
    // Each TM match card renders its target_text; look for both.
    await expect(sidebar.getByText("Restablezca su contraseña.", { exact: false })).toBeVisible({ timeout: 5000 });
    await expect(sidebar.getByText("Reinicie su contraseña ahora.", { exact: false })).toBeVisible();

    // ═══ Click TM match #1 ═══
    // Address the card by its testid, not its tag: a `div`/`button` swap must
    // not silently re-point these clicks at a wrapper that holds both cards.
    const match1 = sidebar
      .getByTestId("tm-match-card")
      .filter({ hasText: "Restablezca su contraseña." });
    await expect(match1).toHaveCount(1);
    await match1.click();
    await page.waitForTimeout(200);

    // The editor's text should now equal TM match #1
    const editor = activeRow.locator(".ProseMirror");
    await expect(editor).toContainText("Restablezca su contraseña.", { timeout: 2000 });

    // ═══ Click TM match #2 ═══
    const match2 = sidebar
      .getByTestId("tm-match-card")
      .filter({ hasText: "Reinicie su contraseña ahora." });
    await expect(match2).toHaveCount(1);
    await match2.click();
    await page.waitForTimeout(200);

    // Editor should now show TM match #2
    await expect(editor).toContainText("Reinicie su contraseña ahora.", { timeout: 2000 });

    // ═══ Cmd+Enter (Mod+Enter) to confirm ═══
    //
    // IMPORTANT: don't focus the editor manually here. After clicking a TM
    // match button, focus stays on that SIDEBAR BUTTON — not the editor — and
    // the confirm shortcut must still fire. Press Cmd+Enter from wherever focus
    // ended up after the last click, like a real user.
    // The confirm is the final (non-draft) PUT for this translation; wait for it.
    const [confirmResp] = await Promise.all([
      page.waitForResponse(
        (r) =>
          new RegExp(`/translations/${passwordTranslationId}(\\?|$)`).test(r.url()) &&
          r.request().method() === "PUT" &&
          !r.url().includes("/draft"),
        { timeout: 10000 },
      ),
      page.keyboard.press("ControlOrMeta+Enter"),
    ]);
    expect(confirmResp.ok(), `B: confirm PUT failed: ${confirmResp.status()}`).toBe(true);

    // ═══ The real assertions: verify the save happened via API ═══
    //
    //   1. active_text must equal TM match #2 (the last clicked — proves the
    //      save used the LATEST editor state, not a stale one).
    //   2. status must be "approved" (proves the confirm, not a draft, saved).
    const afterRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const afterBody = await afterRes.json();
    const afterTranslation = (afterBody.data as any[]).find(
      (t) => t.id === passwordTranslationId,
    );
    expect(afterTranslation,
      "B: translation must still exist after confirm").toBeDefined();

    // eslint-disable-next-line no-console
    console.log(`[J16] after Cmd+Enter — status=${afterTranslation.status}, human_text="${afterTranslation.active_text}", active="${afterTranslation.active_text}"`);

    // THE BUG ASSERTION: human_text must equal TM match #2
    expect(afterTranslation.active_text,
      "B: human_text must equal TM match #2 (the last clicked). If this fails, the save either didn't happen or used a stale editor value.",
    ).toBe("Reinicie su contraseña ahora.");

    // Status must be approved after the confirm
    expect(afterTranslation.status,
      "B: status must be 'approved' after Cmd+Enter (the confirm didn't fire)",
    ).toBe("approved");
  });

  // ─── Phase C: Mod+Shift+S (copy source) — same bug class ───────────────
  //
  // The same focus-trap bug affects every segment-level keyboard shortcut
  // that lives inside TipTap's keymap (see SegmentEditor.tsx → createSegmentKeymap).
  // Cmd+Shift+S is "copy source to target" — if the user clicks a sidebar
  // button (focus moves to button) and then presses Cmd+Shift+S, the
  // ProseMirror keymap never fires. This Phase reproduces that.
  //
  // Setup: pick a fresh segment (one we haven't touched in Phase B — segment #4
  // "Thank you for your patience." is untouched).

  test("C: click a sidebar button, then Cmd+Shift+S — editor must contain source text", async ({ page }) => {
    test.setTimeout(30_000);
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.locator("[data-translation-cell]").first()).toBeVisible({ timeout: 10_000 });

    // Fetch segments + translations
    const headers = await authHeader(page);
    const segRes = await page.request.get(`${API}/api/files/${fileId}/segments`, { headers });
    const segBody = await segRes.json();
    // Segment #4 — "Thank you for your patience." — TM has no entry, but we'll
    // still open the TM-matches fetch which returns [] — no sidebar match clicks
    // needed for THIS test. We just need focus to leave the editor via ANY
    // sidebar click (we'll click the sidebar close button or a collapsed section
    // header — something clickable that won't mutate state).
    const thankSegment = (segBody.data as any[]).find((s) => s.source_text === "Thank you for your patience.");
    expect(thankSegment,
      "C: should find 'Thank you for your patience.' segment").toBeDefined();

    const transRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const transBody = await transRes.json();
    const thankTranslation = (transBody.data as any[]).find((t) => t.segment_id === thankSegment.id);
    // 'untranslated' is derived — fresh segment is raw
    // 'needs_review' with no active_text.
    expect(thankTranslation?.status,
      "C: segment should start at needs_review (untranslated)").toBe("needs_review");
    expect(thankTranslation?.active_text,
      "C: untranslated segment has no active_text").toBeFalsy();

    // Find the segment's cell and click it to activate
    const cells = page.locator("[data-translation-cell]");
    await cells.nth(3).click();
    await page.waitForTimeout(500);
    const activeRow = page.locator("[data-active-row]");
    await expect(activeRow).toBeVisible({ timeout: 3000 });

    // Editor should be empty or contain placeholder (no active_text)
    const editor = activeRow.locator(".ProseMirror");
    await expect(editor).toBeVisible();

    // Move focus OUT of the editor by clicking a button that isn't the editor.
    // The sidebar always has the "TM Matches" / "Terminology" / "Comments"
    // section headers as clickable buttons. Click one to focus it.
    const sidebar = page.locator('[data-testid="editor-sidebar"]');
    const tmMatchesHeader = sidebar.getByRole("button", { name: /TM Matches/i }).first();
    await tmMatchesHeader.click();
    await page.waitForTimeout(150);

    // Sanity: focus should now be on the TM Matches button, NOT in .ProseMirror
    const focusInEditor = await page.evaluate(() => {
      const active = document.activeElement;
      return !!(active && active.closest('.ProseMirror'));
    });
    expect(focusInEditor,
      "C: precondition — focus must be OUTSIDE the editor for this test to be meaningful").toBe(false);

    // ═══ Press Cmd+Shift+S (Mod+Shift+S) — should copy source to target ═══
    await page.keyboard.press("ControlOrMeta+Shift+KeyS");
    await page.waitForTimeout(300);

    // OBSERVABLE OUTCOME: the editor should now contain the source text
    // ("Thank you for your patience.") because that's what copySource does.
    // If the bug exists, the editor is still empty.
    const editorText = (await editor.textContent())?.trim() ?? "";
    // eslint-disable-next-line no-console
    console.log(`[J16] after Cmd+Shift+S (focus outside editor) — editor text: "${editorText}"`);

    expect(editorText,
      "C: Cmd+Shift+S must copy source to target even when focus is outside the editor (on sidebar button). If editor is empty, the keymap never fired.",
    ).toBe("Thank you for your patience.");
  });

  // ─── Phase Z: Cleanup ───────────────────────────────────────────────────

  test("Z1: cleanup project + TM + MT engine", async ({ page }) => {
    await login(page);
    const cleanup = async (fn: () => Promise<void>) => { try { await fn(); } catch {} };
    if (projectId) await cleanup(() => deleteProjectViaAPI(page, projectId));
    if (tmId) await cleanup(() => deleteTMViaAPI(page, tmId));
    if (mtEngineId) await cleanup(() => deleteMTEngineViaAPI(page, mtEngineId));
  });

  test.afterAll(async ({ browser }) => {
    // Belt-and-suspenders cleanup if Z1 was skipped due to a
    // mid-suite failure.
    const page = await browser.newPage();
    try {
      await login(page);
      if (projectId) await deleteProjectViaAPI(page, projectId).catch(() => {});
      if (tmId) await deleteTMViaAPI(page, tmId).catch(() => {});
      if (mtEngineId) await deleteMTEngineViaAPI(page, mtEngineId).catch(() => {});
    } catch {
      // best-effort
    } finally {
      await page.close();
    }
  });
});
