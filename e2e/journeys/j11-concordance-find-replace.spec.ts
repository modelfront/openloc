/**
 * J11 — Concordance search + Find & Replace (Adversarial)
 *
 * Core purpose: concordance searches TM for substring matches and returns
 * results. F&R replaces target text across all segments without errors.
 * Exercises QA findings #29 (F&R buttons) and #30 (F&R "Failed to load" error).
 *
 * NOT NEGOTIABLE: verify F&R actually CHANGED the text in affected segments.
 * "toast appeared" is not enough — check the translation API to confirm.
 * Uses its own project (not Demo) to avoid polluting seed data.
 */
import { test, expect } from "@playwright/test";
import * as fs from "fs";
import * as path from "path";
import {
  login,
  uploadFileViaAPI,
  pretranslateFileViaAPI,
  createTMViaAPI,
  importTMXViaAPI,
  createProjectViaAPI,
  setProjectTMsViaAPI,
  setProjectMTViaAPI,
  createMTEngineViaAPI,
  deleteProjectViaAPI,
  deleteTMViaAPI,
  deleteMTEngineViaAPI,
  selectWordInActiveSource,
  segmentEditor,
} from "./helpers";

const API = "";

// Source file with "password" in multiple segments — gives F&R something to replace
const SOURCE_FILE = [
  "Reset your password.",
  "Enter your email address.",
  "Your password has been updated.",
  "Click the forgot password link.",
].join("\n");

// TMX with concordance-searchable entries
const CONCORDANCE_TMX = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header creationtool="test" datatype="plaintext" segtype="sentence" adminlang="en" srclang="en"/>
  <body>
    <tu tuid="c-001"><tuv xml:lang="en"><seg>Reset your password.</seg></tuv><tuv xml:lang="es"><seg>Restablezca su contraseña.</seg></tuv></tu>
    <tu tuid="c-002"><tuv xml:lang="en"><seg>Enter your email address.</seg></tuv><tuv xml:lang="es"><seg>Introduzca su dirección de correo electrónico.</seg></tuv></tu>
    <tu tuid="c-003"><tuv xml:lang="en"><seg>Change your password now.</seg></tuv><tuv xml:lang="es"><seg>Cambie su contraseña ahora.</seg></tuv></tu>
  </body>
</tmx>`;

let tmId: number;
let mtEngineId: number;
let projectId: number;
let fileId: number;
let jobId: number;

test.describe.serial("J11 — Concordance + Find & Replace", () => {
  // ─── Phase A: Setup ───────────────────────────────────────────────

  test("A1: create TM with password-related entries", async ({ page }) => {
    await login(page);
    tmId = await createTMViaAPI(page, "Concordance F&R TM");
    await importTMXViaAPI(page, tmId, CONCORDANCE_TMX, "concordance.tmx");

    // Verify entries imported (the import lands asynchronously)
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    await expect
      .poll(
        async () => {
          const res = await page.request.get(`${API}/api/tms/${tmId}/entries?limit=100`, { headers });
          return res.ok() ? ((await res.json()).data ?? []).length : -1;
        },
        { message: "TM should have 3 entries", timeout: 10000 },
      )
      .toBe(3);
  });

  test("A2: create project en→es-mx with TM + mock MT", async ({ page }) => {
    await login(page);
    mtEngineId = await createMTEngineViaAPI(page, "Concordance MT", "mock", "");
    projectId = await createProjectViaAPI(page, "Concordance F&R Journey", "en", ["es-mx"]);

    await setProjectTMsViaAPI(page, projectId, [
      { tm_id: tmId, source_language: "en", target_language: "es-mx", mode: "read", priority: 1 },
    ]);
    await setProjectMTViaAPI(page, projectId, [
      { mt_engine_id: mtEngineId, source_language: "en", target_language: "es-mx" },
    ]);
  });

  test("A3: upload file + pre-translate", async ({ page }) => {
    await login(page);

    // Upload using shared helper
    const result = await uploadFileViaAPI(page, projectId, "concordance-test.txt", SOURCE_FILE, "text/plain");
    fileId = result.data?.id ?? result.id;

    // Get jobs via project endpoint
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const jobsRes = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    const jobsBody = await jobsRes.json();
    expect(jobsBody.data.length, "should have 1 job (en→es-mx)").toBe(1);
    jobId = jobsBody.data[0].id;

    // Pre-translate via per-job phase endpoints
    await pretranslateFileViaAPI(page, projectId, fileId);
  });

  // ─── Phase B: Concordance search ──────────────────────────────────

  test("B1: concordance API returns TM matches for 'password'", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Concordance search via API — searches all TMs assigned to the project
    const res = await page.request.get(
      `${API}/api/tms/concordance?project_id=${projectId}&source_language=en&target_language=es-mx&q=password`,
      { headers },
    );
    expect(res.ok(), `concordance search failed: ${res.status()}`).toBe(true);
    const body = await res.json();

    // Concordance returns { tm_matches: [...], term_matches: [...] }
    const tmMatches = body.tm_matches ?? [];
    const termMatches = body.term_matches ?? [];

    // OBSERVABLE OUTCOME: should find TM entries containing "password"
    // Our TMX has "Reset your password." and "Change your password now."
    expect(tmMatches.length, "concordance should find 2+ TM matches with 'password'").toBeGreaterThanOrEqual(2);

    // Verify actual source text matches contain "password"
    const sources: string[] = tmMatches.map((e: any) => e.source_text);
    expect(
      sources.every((s: string) => s.toLowerCase().includes("password")),
      `all TM results should contain 'password', got: ${JSON.stringify(sources)}`,
    ).toBe(true);

    // Verify targets are Spanish (not empty, not garbled)
    // The TMX has es targets with "contraseña"
    const esTargets = tmMatches.filter((e: any) => e.target_language === "es" || e.target_language === "es-mx");
    expect(esTargets.length, "should have es-language matches").toBeGreaterThanOrEqual(1);
    expect(
      esTargets.every((e: any) => e.target_text.includes("contraseña")),
      `es targets should contain 'contraseña', got: ${JSON.stringify(esTargets.map((e: any) => e.target_text))}`,
    ).toBe(true);

    // BONUS: term_matches should also find the term base term (if Default Term base assigned)
    // We didn't assign a term base, so term_matches may be empty — that's fine
    // The TM matches are what matter here
  });

  test("B2: concordance modal opens via Ctrl+K in editor", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // Click the first segment (should contain "Reset your password." source)
    const firstRow = page.getByText(/Reset your password/i).first();
    await firstRow.click();

    // Open concordance with Ctrl+K
    const isMac = process.platform === "darwin";
    await page.keyboard.press(isMac ? "ControlOrMeta+k" : "Control+k");

    // OBSERVABLE OUTCOME: concordance modal should appear
    const modal = page.getByRole("dialog");
    await expect(modal, "concordance modal should open").toBeVisible({ timeout: 5000 });

    // Verify it has a search input (shadcn Input renders as <input> without explicit type)
    const searchInput = modal.locator("input").first();
    await expect(searchInput, "concordance should have search input").toBeVisible();

    // Close it
    await page.keyboard.press("Escape");
  });

  // Pins that both the shortcut and the toolbar button prefill concordance from
  // the source selection and auto-search. A button's mousedown collapses the
  // document selection before the click handler can read it.
  test("B3: selected source phrase prefills concordance from BOTH the shortcut and the toolbar button", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // Activate segment 1 so its source renders in the outlined active box.
    // Two things land after activation and both collapse a selection made
    // before them: the editor's focus (a tick after it mounts) and the term
    // lookup, whose answer redraws the source text with term underlines.
    // Wait for both before selecting.
    const termLookup = page.waitForResponse(
      (r) => r.url().includes("/api/terminology/lookup") && r.request().method() === "GET",
      { timeout: 10000 },
    );
    await page.getByText(/Reset your password/i).first().click();
    const sourceBox = page.locator('[data-testid="active-source-box"]');
    await expect(sourceBox).toBeVisible({ timeout: 5000 });
    await expect(segmentEditor(page).first()).toBeFocused({ timeout: 5000 });
    await termLookup;
    await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => r(null))));

    const selectWordInSource = (word: string) => selectWordInActiveSource(page, word);

    const isMac = process.platform === "darwin";
    const modal = page.getByRole("dialog");
    const concordanceCall = () =>
      page.waitForResponse(
        (r) => r.url().includes("/api/tms/concordance") && r.status() === 200,
        { timeout: 10000 },
      );

    // The two paths deliberately select DIFFERENT words. The editor holds the
    // concordance query in state, so if path 2 reused path 1's word, a
    // button that opens the modal WITHOUT reading the selection would still
    // show the right query — the test would pass against the broken code.

    // ── Path 1: keyboard shortcut ──
    await selectWordInSource("Reset");
    let searchDone = concordanceCall();
    await page.keyboard.press(isMac ? "ControlOrMeta+k" : "Control+k");
    await searchDone;
    await expect(modal, "modal should open on the shortcut").toBeVisible();
    await expect(
      modal.locator("input").first(),
      "shortcut should prefill the query from the selection",
    ).toHaveValue("Reset");
    // Auto-searched, not just prefilled: the matching TMX entry must be on screen.
    await expect(
      modal.getByText(/Restablezca su contraseña/i),
      "selection should be searched immediately",
    ).toBeVisible({ timeout: 5000 });

    await page.keyboard.press("Escape");
    await expect(modal).toBeHidden({ timeout: 5000 });

    // ── Path 2: toolbar button (the reported bug) ──
    await selectWordInSource("password");
    searchDone = concordanceCall();
    await page.getByRole("button", { name: /^concordance$/i }).click();
    await searchDone;
    await expect(modal, "modal should open on the button").toBeVisible();
    await expect(
      modal.locator("input").first(),
      "toolbar button should prefill the query from the selection, not reuse the previous query",
    ).toHaveValue("password");
    await expect(
      modal.getByText(/Cambie su contraseña ahora/i),
      "button path should search the selection too",
    ).toBeVisible({ timeout: 5000 });

    await page.keyboard.press("Escape");
  });

  // ─── Phase C: Find & Replace ──────────────────────────────────────

  test("C1: F&R bar opens with all 4 buttons + Find shows a match badge", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // Open F&R with keyboard shortcut
    const isMac = process.platform === "darwin";
    await page.keyboard.press(isMac ? "ControlOrMeta+h" : "Control+h");

    // F&R is a top bar, not a dialog — check the Find input appeared
    await expect(page.locator("input[name='findText']")).toBeVisible({ timeout: 5000 });

    // OBSERVABLE OUTCOME: all 4 buttons must exist (QA Row 33)
    await expect(page.getByRole("button", { name: /^find$/i }), "Find button").toBeVisible();
    await expect(page.getByRole("button", { name: /^find all$/i }), "Find All button").toBeVisible();
    await expect(page.getByRole("button", { name: /^replace$/i }), "Replace button").toBeVisible();
    await expect(page.getByRole("button", { name: /^replace all$/i }), "Replace all button").toBeVisible();
    await expect(page.locator("input[name='findText']"), "Find input").toBeVisible();
    await expect(page.locator("input[name='replaceText']"), "Replace input").toBeVisible();

    // Exercise Find: type search → click Find → match counter + highlights
    await page.locator("input[name='findText']").fill("contraseña");
    await page.getByRole("button", { name: /^find$/i }).click();
    await page.waitForTimeout(1000);

    // OBSERVABLE OUTCOME: position badge should appear (e.g. "1/1")
    const matchBadge = page.locator("[data-testid='match-count']");
    await expect(matchBadge, "match position badge should appear").toBeVisible({ timeout: 5000 });
    const badgeText = await matchBadge.textContent();
    expect(badgeText, "badge should show position like 1/N").toMatch(/\d+\/\d+/);

    // Close with Escape
    await page.keyboard.press("Escape");
    // Bar should disappear — check the Find input is gone (more specific than the toolbar button text)
    await expect(page.locator("input[name='findText']")).not.toBeVisible({ timeout: 3000 });
  });

  test("C1a: Find advances to next match across the segment list", async ({ page }) => {
    // ADVERSARIAL: use Demo Project (31 segments — seed data) for enough
    // multi-segment matches. Hard-assert seed integrity rather than skipping
    // silently, otherwise a missing-seed regression would leave this test
    // green forever. See audit C-4 / C-5.
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    const projRes = await page.request.get(`${API}/api/projects`, { headers });
    const projects = (await projRes.json()).data;
    const demo = projects.find((p: any) => p.name === "Demo Project");
    expect(demo, "Demo Project seed missing — run `make seed`").toBeDefined();

    const jobsRes = await page.request.get(`${API}/api/projects/${demo.id}/jobs`, { headers });
    const jobs = (await jobsRes.json()).data;
    const esJob = jobs.find((j: any) => j.target_language === "es");
    expect(esJob, "Demo Project es job missing — run `make seed`").toBeDefined();

    await page.goto(`/projects/${demo.id}/jobs/${esJob.id}/edit`);
    await expect(
      page.getByText(/contraseña|password|reset/i).first(),
    ).toBeVisible({ timeout: 10000 });

    // Open F&R modal
    const isMac = process.platform === "darwin";
    await page.keyboard.press(isMac ? "ControlOrMeta+h" : "Control+h");
    await expect(page.locator("input[name='findText']")).toBeVisible({ timeout: 5000 });

    // Search for "contraseña" — Spanish translation of "password", present in
    // multiple Demo Project ES segments. This is the deterministic search term
    // for the seed; "password" was a fragile English-source-side fallback.
    await page.locator("input[name='findText']").fill("contraseña");
    await page.getByRole("button", { name: /^find$/i }).click();

    // Hard-assert at least 2 matches — anything less and the "advance to next"
    // semantic isn't testable. If seed data shifts so this is no longer true,
    // we want a loud failure, not a silent no-op.
    const badge = page.locator("[data-testid='match-count']");
    await expect(badge).toBeVisible({ timeout: 5000 });
    const firstBadge = await badge.textContent();
    const match = firstBadge?.match(/^(\d+)\s*\/\s*(\d+)/);
    expect(match, `expected match-count badge like "1/N", got "${firstBadge}"`).not.toBeNull();
    const total = parseInt(match![2], 10);
    expect(total, `C1a needs ≥2 matches in Demo Project ES; got ${total}`).toBeGreaterThanOrEqual(2);

    // Advance Find. Observable outcome: the match-count badge index changes
    // (1/N → 2/N or higher). This is the semantic "advance" — whether that
    // triggers a viewport scroll depends on viewport size + match positions,
    // which is too fragile to assert directly.
    await page.getByRole("button", { name: /^find$/i }).click();
    await expect(badge).not.toHaveText(firstBadge ?? "", { timeout: 3000 });

    await page.keyboard.press("Escape");
  });

  test("C1b: F&R via actual UI bar — no 'Failed to load translations' (QA #30)", async ({ page }) => {
    // *** QA #30: "Failed to load translations" when using F&R ***
    // HandleFindReplace returns "Failed to load translations" when
    // ListTranslationsByFile fails. This test drives the REAL F&R bar — type
    // in the fields, click Replace all — and asserts that toast never shows.
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 10000 });

    // Collect toast/error messages
    const toastErrors: string[] = [];
    page.on("console", (msg) => {
      if (msg.type() === "error") toastErrors.push(msg.text());
    });

    // Open F&R bar via keyboard
    const isMac = process.platform === "darwin";
    await page.keyboard.press(isMac ? "ControlOrMeta+h" : "Control+h");
    await expect(page.locator("input[name='findText']")).toBeVisible({ timeout: 5000 });

    // Type into the REAL form fields (not API)
    // Use "[ES-MX]" (mock MT prefix in segments 2-4) — different from "contraseña"
    // which C2-C3 tests via API. This avoids the two tests interfering.
    await page.locator("input[name='findText']").fill("[ES-MX]");
    await page.locator("input[name='replaceText']").fill("[MOCK]");

    // Click Replace all — this fires the actual frontend findReplace() call.
    // Await the real POST /jobs/:id/find-replace instead of a fixed sleep so the
    // toast/close assertions below run exactly when the operation has landed.
    const frResp = page.waitForResponse(
      (r) => /\/jobs\/\d+\/find-replace$/.test(r.url()) && r.request().method() === "POST",
    );
    await page.getByRole("button", { name: /^replace all$/i }).click();
    await frResp;

    // OBSERVABLE OUTCOME: no "Failed to load translations" error toast
    // Check for the success toast OR absence of error toast
    const errorToast = page.locator("[data-sonner-toast][data-type='error']");
    const errorCount = await errorToast.count();
    if (errorCount > 0) {
      const errorText = await errorToast.first().textContent();
      expect(
        errorText,
        "QA #30: F&R modal should NOT show 'Failed to load translations'",
      ).not.toContain("Failed to load");
    }

    // Bar should have closed (Replace all dismisses it)
    await expect(page.locator("input[name='findText']"), "bar should close on success").not.toBeVisible({ timeout: 3000 });
  });

  test("C2-C3: F&R replaces 'contraseña' with 'clave' in target text via API", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // First: check which segments have "contraseña" in their target text
    // The TM match for "Reset your password." should have produced "Restablezca su contraseña."
    // Other segments got mock MT text like "[ES-MX] Your password has been updated."
    const beforeRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const beforeBody = await beforeRes.json();
    const beforeTargets = beforeBody.data.map((t: any) => ({
      source: t.source_text,
      target: t.active_text || "",
    }));

    // Count how many targets contain "contraseña" before F&R
    const beforeCount = beforeTargets.filter((t: any) =>
      t.target.toLowerCase().includes("contraseña"),
    ).length;

    // F&R via API — replace "contraseña" with "clave secreta"
    // (QA finding #30: must NOT error "Failed to load translations")
    const frRes = await page.request.post(`${API}/api/jobs/${jobId}/find-replace`, {
      headers,
      data: {
        find: "contraseña",
        replace: "clave secreta",
        case_sensitive: false,
      },
    });
    expect(frRes.ok(), `F&R failed: ${frRes.status()}`).toBe(true);
    const frBody = await frRes.json();

    // OBSERVABLE OUTCOME: we KNOW beforeCount must be >= 1 because TM matched
    // "Reset your password." → "Restablezca su contraseña." (contains "contraseña")
    expect(beforeCount, "TM match should have put 'contraseña' in at least 1 segment").toBeGreaterThanOrEqual(1);
    expect(frBody.data.replaced, "F&R should have replaced at least 1 occurrence of 'contraseña'").toBeGreaterThanOrEqual(1);
  });

  test("C4: verify F&R actually changed segment text via translations API", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    const res = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    expect(res.ok()).toBe(true);
    const body = await res.json();

    // OBSERVABLE OUTCOME: if any segment had "contraseña" before, it should now have "clave secreta"
    // Collect all target text (human_text takes precedence if set, otherwise machine_text)
    const allTargets: string[] = body.data.map((t: any) => t.active_text || "");

    // No segment should still contain "contraseña" (it was replaced)
    const stillHasOld = allTargets.filter((t: string) => t.includes("contraseña"));
    expect(
      stillHasOld.length,
      `no segment should still have 'contraseña' after F&R, found: ${JSON.stringify(stillHasOld)}`,
    ).toBe(0);

    // F&R replaced "contraseña" → "clave secreta", so at least 1 segment must have the new text
    const hasNew = allTargets.filter((t: string) => t.includes("clave secreta"));
    expect(
      hasNew.length,
      `at least 1 segment should contain 'clave secreta' after F&R, got targets: ${JSON.stringify(allTargets)}`,
    ).toBeGreaterThanOrEqual(1);
  });

  // ─── Phase D: Cleanup ─────────────────────────────────────────────

  test("D1: cleanup all resources", async ({ page }) => {
    await login(page);
    await deleteProjectViaAPI(page, projectId);
    await deleteTMViaAPI(page, tmId);
    await deleteMTEngineViaAPI(page, mtEngineId);

    // Verify project gone
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const res = await page.request.get(`${API}/api/projects/${projectId}`, { headers });
    expect(res.status(), "project should be deleted").toBe(404);
  });

  test.afterAll(async ({ browser }) => {
    // Belt-and-suspenders cleanup if D1 was skipped due to a
    // mid-suite failure (concordance tests touch the editor + can flake).
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
