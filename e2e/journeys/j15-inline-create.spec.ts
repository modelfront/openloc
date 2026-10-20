import { test, expect, Page } from "@playwright/test";
import {
  login,
  startFromScratch,
  startFromTemplate,
  createProjectViaAPI,
  deleteProjectViaAPI,
  deleteTMViaAPI,
  deleteTermBaseViaAPI,
  selectLanguage,
  selectTargetLanguages,
  pickComboboxOption,
  chooseTMFromScratch,
  deleteDraftFromURL,
  submitProjectWizard,
} from "./helpers";

/**
 * J15 — Inline Create Adversarial
 *
 * Covers both the create-mode (`/projects/new`) and edit-mode
 * (`/projects/:id/settings`) inline-create flows for TM and Term base.
 *
 * The adversarial hook: every dialog-within-a-form is a form-submission
 * boundary risk. A `type="submit"` button or Enter keypress inside the
 * inline-create dialog must NOT submit the parent project form. This
 * journey layers multiple inline-creates + scalar form edits together
 * and verifies nothing clobbers anything else.
 *
 * Structure:
 *   Phase A — baseline count snapshot
 *   Phase B — create-mode gauntlet (the big one, catches form-boundary bug)
 *   Phase D — cancel flows (rollback guards)
 *   Phase E — edit-mode inline create (works already — regression guard)
 *   Phase F — cross-surface verification
 *   Phase Z — cleanup
 */

const API = "";

// ─── Shared state across phases ────────────────────────────────────────

interface Baseline {
  projects: number;
  drafts: number;
  tms: number;
  tbs: number;
  engines: number;
}

let baseline: Baseline;
let gauntletProjectId: number | null = null;
let gauntletTMId: number | null = null;
let gauntletTBId: number | null = null;
let editProjectId: number;
let editTMId: number | null = null;
let editTBId: number | null = null;

// Unique suffix per test run so parallel invocations don't collide on names
const RUN_ID = Date.now();
const GAUNTLET_PROJECT = `J15 Gauntlet ${RUN_ID}`;
const GAUNTLET_TM = `J15 Gauntlet TM ${RUN_ID}`;
const GAUNTLET_TB = `J15 Gauntlet TB ${RUN_ID}`;
const EDIT_TM = `J15 Edit TM ${RUN_ID}`;
const EDIT_TB = `J15 Edit TB ${RUN_ID}`;

// ─── Helpers ────────────────────────────────────────────────────────────

async function authHeader(page: Page): Promise<Record<string, string>> {
  const token = await page.evaluate(() => localStorage.getItem("token"));
  return token ? { Authorization: `Bearer ${token}` } : {};
}

async function apiCount(page: Page, path: string): Promise<number> {
  const headers = await authHeader(page);
  const res = await page.request.get(`${API}${path}`, { headers });
  expect(res.ok(), `GET ${path}: ${res.status()}`).toBe(true);
  const body = await res.json();
  return (body.data ?? []).length;
}

// The wizard autosaves a draft project as soon as the form is touched,
// so a filled form legitimately adds a status='draft'
// row. The form boundary these tests guard is "nothing got published":
// drafts don't count. draftCount below pins the other half — a dialog must
// not produce a SECOND draft either.
async function publishedCount(page: Page): Promise<number> {
  return (await projectsByStatus(page)).published;
}

async function draftCount(page: Page): Promise<number> {
  return (await projectsByStatus(page)).drafts;
}

async function projectsByStatus(page: Page): Promise<{ published: number; drafts: number }> {
  const headers = await authHeader(page);
  const res = await page.request.get(`${API}/api/projects`, { headers });
  expect(res.ok(), `GET /api/projects: ${res.status()}`).toBe(true);
  const rows = ((await res.json()).data ?? []) as { status?: string }[];
  return {
    published: rows.filter(p => p.status !== "draft").length,
    drafts: rows.filter(p => p.status === "draft").length,
  };
}

async function apiFind<T = any>(
  page: Page,
  path: string,
  predicate: (item: any) => boolean,
): Promise<T | undefined> {
  const headers = await authHeader(page);
  const res = await page.request.get(`${API}${path}`, { headers });
  const body = await res.json();
  return (body.data ?? []).find(predicate);
}

async function projectFromAPI(page: Page, id: number): Promise<any> {
  const headers = await authHeader(page);
  const res = await page.request.get(`${API}/api/projects/${id}/settings`, { headers });
  expect(res.ok(), `GET project settings ${id}: ${res.status()}`).toBe(true);
  return (await res.json()).data;
}

// ─── Describe ───────────────────────────────────────────────────────────

test.describe.serial("J15 — Inline Create Adversarial", () => {
  // Tests that touched the wizard leave a draft behind.
  test.afterEach(async ({ page }) => { await deleteDraftFromURL(page); });

  // ═══════════════════════════════════════════════════════════════════════
  // Phase A — Baseline
  // ═══════════════════════════════════════════════════════════════════════

  test("A1: snapshot baseline counts (projects, TMs, TBs, engines)", async ({ page }) => {
    await login(page);
    baseline = {
      projects: await publishedCount(page),
      drafts: await draftCount(page),
      tms: await apiCount(page, "/api/tms"),
      tbs: await apiCount(page, "/api/terminology"),
      engines: await apiCount(page, "/api/mt-engines"),
    };
    expect(baseline.projects).toBeGreaterThanOrEqual(0);
  });

  // ═══════════════════════════════════════════════════════════════════════
  // Phase B — Create-mode adversarial gauntlet (ONE LONG TEST)
  //
  // Drives the /projects/new form through every inline-create path while
  // layering scalar edits on top. Every assertion marked [FORM-BOUNDARY]
  // catches the specific bug where a dialog's Create button accidentally
  // submits the parent project form.
  // ═══════════════════════════════════════════════════════════════════════

  test("B: /projects/new gauntlet — inline create TM + TB, layered with scalar edits, no premature submit", async ({ page }) => {
    test.setTimeout(60_000);
    await login(page);

    // Compute baseline inline so B is self-contained and can be run in isolation
    // via -g filter (bypasses the serial A1 setup)
    if (!baseline) {
      baseline = {
        projects: await publishedCount(page),
        drafts: await draftCount(page),
        tms: await apiCount(page, "/api/tms"),
        tbs: await apiCount(page, "/api/terminology"),
        engines: await apiCount(page, "/api/mt-engines"),
      };
    }

    // ── B2: navigate
    await page.goto("/projects/new");
    await startFromScratch(page);
    // The match scoring and the memory list only render once the TM source is
    // answered (a new project starts with no answer).
    await chooseTMFromScratch(page);
    await expect(page.getByRole("heading", { name: /new project/i })).toBeVisible({ timeout: 5000 });

    // ── B3: fill project name
    const nameInput = page.getByPlaceholder(/project name/i);
    await nameInput.fill(GAUNTLET_PROJECT);

    // ── B4: source language
    const srcLangTrigger = page.locator('[data-field="source_language"] button').first();
    await selectLanguage(page, srcLangTrigger, "en");

    // ── B5: target languages (multi) — regional codes
    const tgtLangTrigger = page.locator('[data-field="target_languages"] button').first();
    await selectTargetLanguages(page, tgtLangTrigger, ["de-at", "pt-br"]);

    // ── B6: fuzzy threshold 75 → 65
    const fuzzyThreshold = page.getByRole("spinbutton", { name: /fuzzy match threshold/i });
    await fuzzyThreshold.fill("65");
    await expect(fuzzyThreshold).toHaveValue("65");

    // ── B7: pre-translate — ensure it is ON (the new default). The switch
    // only unlocks once a router is picked, so pick one first.
    const preTranslateCheckbox = page.getByRole("checkbox", { name: /machine translation.*start or upload/i });
    await pickComboboxOption(page, page.getByTestId("mt-router-select"), "Default MT router");
    await expect(preTranslateCheckbox).toBeEnabled();
    if (!(await preTranslateCheckbox.isChecked())) {
      await preTranslateCheckbox.check();
    }
    await expect(preTranslateCheckbox).toBeChecked();

    // ── B8: QA profile — pick Standard QA
    await pickComboboxOption(page, page.getByTestId("qa-profile-select"), "Standard QA");

    // ═══ TM inline-create dialog ═══

    // ── B9: click Select TM
    const tmTrigger = page.getByRole("button", { name: /^add translation memory$/i }).first();
    await expect(tmTrigger).toBeVisible();
    await tmTrigger.click();

    // ── B10: click "+ Create new TM..."
    const tmCreateOption = page.getByRole("option", { name: /create new tm/i });
    await expect(tmCreateOption).toBeVisible({ timeout: 3000 });
    await tmCreateOption.click();

    // ── B11: [FORM-BOUNDARY] URL still /projects/new
    await expect(page).toHaveURL(/\/projects\/new(\?draft=\d+)?$/);
    // ── B12: [FORM-BOUNDARY] project count in DB still baseline
    expect(await publishedCount(page),
      "B12: opening TM dialog must not create a project").toBe(baseline.projects);

    // ── B13: TM dialog visible
    await expect(page.getByRole("heading", { name: /create translation memory/i })).toBeVisible({ timeout: 3000 });

    // ── B14: type TM name
    const tmNameInput = page.getByPlaceholder(/tm name/i);
    await tmNameInput.fill(GAUNTLET_TM);

    // ── B15: click dialog Create button
    const tmDialog = page.getByRole("dialog").filter({ hasText: /create translation memory/i });
    await tmDialog.getByRole("button", { name: /^create$/i }).click();

    // Wait for dialog to close
    await expect(page.getByRole("heading", { name: /create translation memory/i })).not.toBeVisible({ timeout: 5000 });

    // ── B16: [FORM-BOUNDARY] URL is STILL /projects/new (THE BUG)
    await expect(page,
      "B16: submitting the TM dialog must NOT navigate away from /projects/new").toHaveURL(/\/projects\/new(\?draft=\d+)?$/);

    // ── B17: [FORM-BOUNDARY] project counts STILL baseline. The form has been
    //         filled by now, so exactly one draft exists — the dialog must add
    //         neither a published project nor a second draft.
    const afterTMDialog = await projectsByStatus(page);
    expect(afterTMDialog.published,
      "B17: submitting the TM dialog must NOT create a project").toBe(baseline.projects);
    expect(afterTMDialog.drafts,
      "B17: submitting the TM dialog must NOT create a second draft").toBe(baseline.drafts + 1);

    // ── B18: TM was created
    expect(await apiCount(page, "/api/tms")).toBe(baseline.tms + 1);
    const tm = await apiFind<{ id: number }>(page, "/api/tms", (t) => t.name === GAUNTLET_TM);
    expect(tm).toBeDefined();
    gauntletTMId = tm!.id;

    // ── B19: the created TM was auto-added as a row (resource mode adds on
    //         create — no separate Add step). Its languages come from the TM.
    await expect(page.getByText(GAUNTLET_TM).first()).toBeVisible({ timeout: 3000 });

    // ═══ Form state survived the TM dialog ═══

    // ── B20: project name field still intact
    await expect(nameInput).toHaveValue(GAUNTLET_PROJECT);

    // ── B21: source lang trigger shows "en"
    await expect(page.locator('[data-field="source_language"] button').first()).toContainText(/en/i);

    // ── B22: target lang chips still show de-at + pt-br
    // The target MultiCombobox shows selected langs as chips — verify both are still visible
    await expect(page.locator('[data-field="target_languages"]').getByText(/de-at/i).first()).toBeVisible();
    await expect(page.locator('[data-field="target_languages"]').getByText(/pt-br/i).first()).toBeVisible();

    // ── B23: fuzzy threshold still 65%
    await expect(fuzzyThreshold).toHaveValue("65");

    // ── B24: pre-translate still checked
    await expect(preTranslateCheckbox).toBeChecked();

    // ── B25: QA profile still Standard QA
    await expect(page.getByTestId("qa-profile-select")).toContainText("Standard QA");

    // ═══ Layer more form edits ═══

    // ── B26: the TM is already assigned (auto-added on create — its languages
    // come from the TM itself, no per-row picker). Confirm the empty-state is
    // gone so B27/B28 still exercise "edit scalars AFTER an assignment exists".
    await expect(page.getByText(/no translation memory assigned/i)).not.toBeVisible();

    // ── B27: change fuzzy threshold again (AFTER an assignment)
    await fuzzyThreshold.fill("85");
    await expect(fuzzyThreshold).toHaveValue("85");

    // ── B28: toggle pre-translate OFF (scalar flip AFTER assignment)
    await preTranslateCheckbox.uncheck();
    await expect(preTranslateCheckbox).not.toBeChecked();

    // ═══ Term base inline-create dialog ═══

    // ── B29: click Select Term base
    const tbTrigger = page.getByRole("button", { name: /^add term base$/i }).first();
    await tbTrigger.click();

    // ── B30: click "+ Create new Term base..."
    const tbCreateOption = page.getByRole("option", { name: /create new term base/i });
    await expect(tbCreateOption).toBeVisible({ timeout: 3000 });
    await tbCreateOption.click();

    // ── B31: [FORM-BOUNDARY] URL still /projects/new
    await expect(page).toHaveURL(/\/projects\/new(\?draft=\d+)?$/);

    // ── B32: [FORM-BOUNDARY] project count still baseline
    expect(await publishedCount(page)).toBe(baseline.projects);

    // ── B33: TB dialog visible
    const tbDialog = page.getByRole("dialog").filter({ hasText: /create term base/i });
    await expect(tbDialog).toBeVisible({ timeout: 3000 });

    // ── B34: type TB name
    await page.getByPlaceholder(/term base name/i).fill(GAUNTLET_TB);

    // ── B35: pick the source language (the dialog is source-only now — targets
    //         derive from the base's terms).
    const tbDialogPopovers = tbDialog.locator('[data-slot="combobox-trigger"]');
    await selectLanguage(page, tbDialogPopovers.nth(0), "en");

    // ── B37: click dialog Create
    await tbDialog.getByRole("button", { name: /^create$/i }).click();

    // Wait for dialog close
    await expect(tbDialog).not.toBeVisible({ timeout: 5000 });

    // ── B38: [FORM-BOUNDARY] URL STILL /projects/new
    await expect(page,
      "B38: submitting the TB dialog must NOT navigate away from /projects/new").toHaveURL(/\/projects\/new(\?draft=\d+)?$/);

    // ── B39: [FORM-BOUNDARY] project count STILL baseline
    expect(await publishedCount(page),
      "B39: submitting the TB dialog must NOT create a project").toBe(baseline.projects);

    // ── B40: TB was created
    expect(await apiCount(page, "/api/terminology")).toBe(baseline.tbs + 1);
    const tb = await apiFind<{ id: number }>(page, "/api/terminology", (t) => t.name === GAUNTLET_TB);
    expect(tb).toBeDefined();
    gauntletTBId = tb!.id;

    // ── B41: the created TB was auto-added as a Terminology row (resource mode
    //         adds on create — there's no separate Add step).
    await expect(page.getByText(GAUNTLET_TB).first()).toBeVisible({ timeout: 3000 });

    // ── B42: the base has no terms yet, so it fans out to BOTH project targets
    //         (create-then-fill), shown read-only as en → de-at and en → pt-br.
    await expect(page.getByText(/en → de-at/i).first()).toBeVisible();
    await expect(page.getByText(/en → pt-br/i).first()).toBeVisible();

    // ═══ ALL previously-set state still survived the SECOND dialog ═══

    // ── B43: project name
    await expect(nameInput).toHaveValue(GAUNTLET_PROJECT);
    // ── B44: target langs
    await expect(page.locator('[data-field="target_languages"]').getByText(/de-at/i).first()).toBeVisible();
    await expect(page.locator('[data-field="target_languages"]').getByText(/pt-br/i).first()).toBeVisible();
    // ── B45: fuzzy threshold (85% — the latest edit from B27)
    await expect(fuzzyThreshold).toHaveValue("85");
    // ── B46: pre-translate (off — from B28)
    await expect(preTranslateCheckbox).not.toBeChecked();
    // ── B47: QA profile still set
    await expect(page.getByTestId("qa-profile-select")).toContainText("Standard QA");
    // ── B48: TM assignment row still in the list (from B26) — empty-state absent
    await expect(page.getByText(/no translation memory assigned/i)).not.toBeVisible();

    // ═══ TB is already assigned (auto-added on create — no Add-row step) ═══

    await expect(page.getByText(/no terminology assigned/i)).not.toBeVisible();

    // ═══ REAL project submit (this is when navigation is allowed) ═══

    // ── B59: click Create project — publishes the autosaved draft
    const { response: createResp } = await submitProjectWizard(page);
    expect(createResp.ok(),
      `B59: Create project publish failed: ${createResp.status()}`).toBe(true);

    // ── B60: navigate to /projects/:id — on Files while it has no files yet
    await expect(page).toHaveURL(/\/projects\/\d+(\/files)?$/, { timeout: 5000 });

    // Extract the project id from the URL
    const url = page.url();
    const match = url.match(/\/projects\/(\d+)/);
    expect(match).not.toBeNull();
    gauntletProjectId = Number(match![1]);

    // ── B61-B69: API verification of final persisted state
    const settings = await projectFromAPI(page, gauntletProjectId);

    // ── B62: exactly ONE new project — no duplicates from premature submits.
    //         Both halves matter: the gauntlet's draft became the published
    //         project, so drafts are back to baseline. A second draft here
    //         means an autosave raced itself.
    const after = await projectsByStatus(page);
    expect(after.published,
      "B62: exactly one project created (no duplicates)").toBe(baseline.projects + 1);
    expect(after.drafts,
      "B62: the gauntlet's draft was published, and no extra draft was created").toBe(baseline.drafts);

    // ── B63: TM assigned (per-TM — no per-assignment target_language;
    //         the TM covers its own declared targets).
    const hasTM = settings.tm_assignments?.some(
      (a: any) => a.tm_id === gauntletTMId,
    );
    expect(hasTM, `B63: project should have tm_id=${gauntletTMId}`).toBe(true);

    // ── B64: TB assigned — the empty base fanned out to BOTH project targets,
    //         so there's an en→de-at AND an en→pt-br assignment for it.
    const tbTargets = (settings.term_assignments ?? [])
      .filter((a: any) => a.term_base_id === gauntletTBId)
      .map((a: any) => a.target_language)
      .sort();
    expect(tbTargets, `B64: term_base_id=${gauntletTBId} should be assigned to both project targets`)
      .toEqual(["de-at", "pt-br"]);

    // ── B66: fuzzy threshold 0.85 (scalars live under settings.project)
    expect(settings.project.fuzzy_threshold).toBeCloseTo(0.85, 2);

    // ── B67: pre-translate off
    expect(settings.project.mt_on_upload).toBe(false);

    // ── B68: QA profile set
    expect(settings.project.qa_profile_id).not.toBeNull();
  });

  // ═══════════════════════════════════════════════════════════════════════
  // Phase E — Edit-mode inline create (regression guard — already worked)
  // Kept in the serial block because it depends on a project created once
  // and reused across the TM + TB steps.
  // ═══════════════════════════════════════════════════════════════════════

  test("E1-SER: create a fresh project for edit-mode testing (via API)", async ({ page }) => {
    await login(page);
    editProjectId = await createProjectViaAPI(page, `J15 Edit ${RUN_ID}`, "en", ["fr-CA"]);
    expect(editProjectId).toBeGreaterThan(0);
  });

  test("E2-SER: edit-mode — open TM dropdown on /projects/:id/settings, create TM inline", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${editProjectId}/files`);
    await page.getByRole("link", { name: /settings/i }).click();
    await expect(page.getByRole("heading", { name: /translation memory/i })).toBeVisible({ timeout: 5000 });

    const tmTrigger = page.getByRole("button", { name: /^add translation memory$/i }).first();
    await tmTrigger.click();
    await expect(page.getByRole("option", { name: /create new tm/i })).toBeVisible({ timeout: 3000 });
    await page.getByRole("option", { name: /create new tm/i }).click();
    await expect(page.getByRole("heading", { name: /create translation memory/i })).toBeVisible({ timeout: 3000 });

    await page.getByPlaceholder(/tm name/i).fill(EDIT_TM);
    await page.getByRole("dialog").getByRole("button", { name: /^create$/i }).click();

    // Dialog closes; the TM is auto-added as a row (its name shows as text).
    await expect(page.getByRole("heading", { name: /create translation memory/i })).not.toBeVisible({ timeout: 5000 });
    await expect(page.getByText(EDIT_TM).first()).toBeVisible({ timeout: 3000 });

    // API verification
    const tm = await apiFind<{ id: number }>(page, "/api/tms", (t) => t.name === EDIT_TM);
    expect(tm).toBeDefined();
    editTMId = tm!.id;
  });

  test("E3-SER: edit-mode — open TB dropdown, create TB inline with fr-CA", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${editProjectId}/files`);
    await page.getByRole("link", { name: /settings/i }).click();

    const tbTrigger = page.getByRole("button", { name: /^add term base$/i }).first();
    await tbTrigger.click();
    await page.getByRole("option", { name: /create new term base/i }).click();
    await expect(page.getByRole("heading", { name: /create term base/i })).toBeVisible({ timeout: 3000 });

    await page.getByPlaceholder(/term base name/i).fill(EDIT_TB);
    // Source-only dialog now (targets derive from terms; concept redesign).
    const dialog = page.getByRole("dialog");
    const popoverTriggers = dialog.locator('[data-slot="combobox-trigger"]');
    await selectLanguage(page, popoverTriggers.nth(0), "en");
    await dialog.getByRole("button", { name: /^create$/i }).click();

    await expect(page.getByRole("heading", { name: /create term base/i })).not.toBeVisible({ timeout: 5000 });
    await expect(page.getByText(EDIT_TB).first()).toBeVisible({ timeout: 3000 });

    const tb = await apiFind<{ id: number; source_language: string; target_languages: string[] }>(
      page, "/api/terminology", (t) => t.name === EDIT_TB,
    );
    expect(tb).toBeDefined();
    expect(tb!.source_language).toBe("en");
    // No terms yet → no derived target languages.
    expect(tb!.target_languages).toEqual([]);
    editTBId = tb!.id;
  });

  // (Z1 — cleanup — lives at the end of the non-serial describe so it runs
  // AFTER all BE/D/F tests, and queries by name prefix so it cleans up even
  // if B failed and the ID globals were never set.)
});

// ═══════════════════════════════════════════════════════════════════════
// Phase B-Enter + Phase D — independent tests (non-serial)
//
// Each of these tests fresh-navigates to /projects/new and does its own
// baseline / cleanup. Split into a non-serial describe so a failure in
// the big gauntlet test above doesn't skip these — we want to see ALL
// the form-boundary bugs at once when the feature is broken.
// ═══════════════════════════════════════════════════════════════════════

test.describe("J15 — Inline Create — Independent adversarial tests", () => {
  test.afterEach(async ({ page }) => { await deleteDraftFromURL(page); });


  test("BE1: Enter in project name field must not submit the form", async ({ page }) => {
    await login(page);
    const projectsBefore = await publishedCount(page);

    await page.goto("/projects/new");
    await startFromScratch(page);
    await chooseTMFromScratch(page);
    const nameInput = page.getByPlaceholder(/project name/i);
    await nameInput.fill(`J15 BE1 should-not-submit ${RUN_ID}`);
    await nameInput.press("Enter");

    // Give the page a moment to react to any submission
    await page.waitForTimeout(500);

    // [FORM-BOUNDARY] URL and project count must not change
    await expect(page).toHaveURL(/\/projects\/new(\?draft=\d+)?$/);
    expect(await publishedCount(page),
      "BE1: Enter in project name must not submit the form").toBe(projectsBefore);
  });

  test("BE2: Enter in TM dialog name input must not submit parent form", async ({ page }) => {
    await login(page);
    const projectsBefore = await publishedCount(page);
    const tmsBefore = await apiCount(page, "/api/tms");

    await page.goto("/projects/new");
    await startFromScratch(page);
    await chooseTMFromScratch(page);
    await page.getByPlaceholder(/project name/i).fill(`J15 BE2 host-project ${RUN_ID}`);
    // Set project source+target so the TM dialog inherits them as presets (TM
    // requires languages). A VALID parent form also makes the
    // form-boundary assertion stronger: a leak would actually create a project.
    await selectLanguage(page, page.locator('[data-field="source_language"] button').first(), "en");
    await selectTargetLanguages(page, page.locator('[data-field="target_languages"] button').first(), ["de"]);
    await page.getByRole("button", { name: /^add translation memory$/i }).first().click();
    await page.getByRole("option", { name: /create new tm/i }).click();
    await expect(page.getByRole("heading", { name: /create translation memory/i })).toBeVisible();

    const tmName = `J15 BE2 TM ${RUN_ID}`;
    await page.getByPlaceholder(/tm name/i).fill(tmName);
    // Press Enter instead of clicking Create
    await page.getByPlaceholder(/tm name/i).press("Enter");

    // Wait for the TM dialog to close
    await expect(page.getByRole("heading", { name: /create translation memory/i })).not.toBeVisible({ timeout: 5000 });

    // [FORM-BOUNDARY] TM should be created but project must NOT be
    expect(await apiCount(page, "/api/tms"),
      "BE2: Enter in TM name should create the TM").toBe(tmsBefore + 1);
    expect(await publishedCount(page),
      "BE2: Enter in TM name must NOT create a project").toBe(projectsBefore);
    await expect(page,
      "BE2: Enter in TM name must NOT navigate away from /projects/new").toHaveURL(/\/projects\/new(\?draft=\d+)?$/);

    // Clean up the stray TM so it doesn't leak
    const stray = await apiFind<{ id: number }>(page, "/api/tms", (t) => t.name === tmName);
    if (stray) {
      const headers = await authHeader(page);
      await page.request.delete(`${API}/api/tms/${stray.id}`, { headers });
    }
  });

  test("BE3: Enter in TM dropdown cmdk search input must not submit parent form", async ({ page }) => {
    await login(page);
    const projectsBefore = await publishedCount(page);

    await page.goto("/projects/new");
    await startFromScratch(page);
    await chooseTMFromScratch(page);
    await page.getByPlaceholder(/project name/i).fill(`J15 BE3 host-project ${RUN_ID}`);
    // Open TM dropdown (cmdk popover)
    await page.getByRole("button", { name: /^add translation memory$/i }).first().click();
    // The cmdk search input — type something that doesn't match any TM
    const cmdkSearch = page.locator('[cmdk-input]').first();
    await expect(cmdkSearch).toBeVisible();
    await cmdkSearch.fill("nonexistent");
    // Press Enter in the cmdk search — this SHOULD NOT submit the parent form
    await cmdkSearch.press("Enter");

    await page.waitForTimeout(500);

    // [FORM-BOUNDARY]
    await expect(page).toHaveURL(/\/projects\/new(\?draft=\d+)?$/);
    expect(await publishedCount(page),
      "BE3: Enter in cmdk search must not submit the parent form").toBe(projectsBefore);
  });

  test("BE4: Enter in TB dialog name input must not submit parent form", async ({ page }) => {
    await login(page);
    const projectsBefore = await publishedCount(page);
    const tbsBefore = await apiCount(page, "/api/terminology");

    await page.goto("/projects/new");
    await startFromScratch(page);
    await chooseTMFromScratch(page);
    await page.getByPlaceholder(/project name/i).fill(`J15 BE4 host-project ${RUN_ID}`);
    await page.getByRole("button", { name: /^add term base$/i }).first().click();
    await page.getByRole("option", { name: /create new term base/i }).click();
    await expect(page.getByRole("heading", { name: /create term base/i })).toBeVisible();

    const tbName = `J15 BE4 TB ${RUN_ID}`;
    await page.getByPlaceholder(/term base name/i).fill(tbName);

    // Pick the source language (source-only dialog; valid form state)
    const dialog = page.getByRole("dialog");
    const popoverTriggers = dialog.locator('[data-slot="combobox-trigger"]');
    await selectLanguage(page, popoverTriggers.nth(0), "en");

    // Press Enter in the name field
    await page.getByPlaceholder(/term base name/i).press("Enter");

    // Wait for dialog to close (success case)
    await expect(page.getByRole("heading", { name: /create term base/i })).not.toBeVisible({ timeout: 5000 });

    // [FORM-BOUNDARY]
    expect(await apiCount(page, "/api/terminology"),
      "BE4: Enter in TB name should create the TB").toBe(tbsBefore + 1);
    expect(await publishedCount(page),
      "BE4: Enter in TB name must NOT create a project").toBe(projectsBefore);
    await expect(page).toHaveURL(/\/projects\/new(\?draft=\d+)?$/);

    // Cleanup
    const stray = await apiFind<{ id: number }>(page, "/api/terminology", (t) => t.name === tbName);
    if (stray) {
      const headers = await authHeader(page);
      await page.request.delete(`${API}/api/terminology/${stray.id}`, { headers });
    }
  });

  test("BE5: Enter in TB dialog's lang-combobox search must not submit parent form", async ({ page }) => {
    await login(page);
    const projectsBefore = await publishedCount(page);

    await page.goto("/projects/new");
    await startFromScratch(page);
    await chooseTMFromScratch(page);
    await page.getByPlaceholder(/project name/i).fill(`J15 BE5 host-project ${RUN_ID}`);
    await page.getByRole("button", { name: /^add term base$/i }).first().click();
    await page.getByRole("option", { name: /create new term base/i }).click();
    await expect(page.getByRole("heading", { name: /create term base/i })).toBeVisible();

    // Open the Source lang combobox inside the dialog
    const dialog = page.getByRole("dialog");
    const popoverTriggers = dialog.locator('[data-slot="combobox-trigger"]');
    await popoverTriggers.nth(0).click();

    // Type in the cmdk search and press Enter
    const searchInput = page.getByPlaceholder(/search languages/i);
    await expect(searchInput).toBeVisible();
    await searchInput.fill("xyz");
    await searchInput.press("Enter");

    await page.waitForTimeout(500);

    // [FORM-BOUNDARY]
    await expect(page).toHaveURL(/\/projects\/new(\?draft=\d+)?$/);
    expect(await publishedCount(page),
      "BE5: Enter in lang cmdk search must not submit parent form").toBe(projectsBefore);

    // Close the popover and dialog
    await page.keyboard.press("Escape");
    await page.keyboard.press("Escape");
  });

  // ═══════════════════════════════════════════════════════════════════════
  // Phase D — Cancel flows (rollback guards)
  // ═══════════════════════════════════════════════════════════════════════

  test("D1: Cancel TM dialog on /projects/new creates nothing", async ({ page }) => {
    await login(page);
    const tmsBefore = await apiCount(page, "/api/tms");
    const projectsBefore = await publishedCount(page);

    await page.goto("/projects/new");
    await startFromScratch(page);
    await chooseTMFromScratch(page);
    await page.getByPlaceholder(/project name/i).fill(`J15 D1 should-not-persist ${RUN_ID}`);
    await page.getByRole("button", { name: /^add translation memory$/i }).first().click();
    await page.getByRole("option", { name: /create new tm/i }).click();
    await expect(page.getByRole("heading", { name: /create translation memory/i })).toBeVisible();

    // Cancel via button
    await page.getByRole("dialog").getByRole("button", { name: /cancel/i }).click();
    await expect(page.getByRole("heading", { name: /create translation memory/i })).not.toBeVisible({ timeout: 3000 });

    // Nothing was created — neither TM nor Project
    expect(await apiCount(page, "/api/tms")).toBe(tmsBefore);
    expect(await publishedCount(page)).toBe(projectsBefore);
    // URL still /projects/new
    await expect(page).toHaveURL(/\/projects\/new(\?draft=\d+)?$/);
    // Project name preserved
    await expect(page.getByPlaceholder(/project name/i)).toHaveValue(`J15 D1 should-not-persist ${RUN_ID}`);
  });

  test("D2: ESC closes TM dialog, creates nothing", async ({ page }) => {
    await login(page);
    const tmsBefore = await apiCount(page, "/api/tms");

    await page.goto("/projects/new");
    await startFromScratch(page);
    await chooseTMFromScratch(page);
    await page.getByRole("button", { name: /^add translation memory$/i }).first().click();
    await page.getByRole("option", { name: /create new tm/i }).click();
    await expect(page.getByRole("heading", { name: /create translation memory/i })).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(page.getByRole("heading", { name: /create translation memory/i })).not.toBeVisible({ timeout: 3000 });

    expect(await apiCount(page, "/api/tms")).toBe(tmsBefore);
    await expect(page).toHaveURL(/\/projects\/new(\?draft=\d+)?$/);
  });

  test("D3: typed-then-cancelled TM dialog creates nothing", async ({ page }) => {
    await login(page);
    const tmsBefore = await apiCount(page, "/api/tms");

    await page.goto("/projects/new");
    await startFromScratch(page);
    await chooseTMFromScratch(page);
    await page.getByRole("button", { name: /^add translation memory$/i }).first().click();
    await page.getByRole("option", { name: /create new tm/i }).click();
    await page.getByPlaceholder(/tm name/i).fill("J15 D3 discarded");
    await page.getByRole("dialog").getByRole("button", { name: /cancel/i }).click();

    expect(await apiCount(page, "/api/tms")).toBe(tmsBefore);
    const stray = await apiFind(page, "/api/tms", (t) => t.name === "J15 D3 discarded");
    expect(stray, "D3: cancelled TM name must NOT appear in DB").toBeUndefined();
  });

  test("D4: TM dialog Create with empty name shows validation error, creates NOTHING (not even the project)", async ({ page }) => {
    await login(page);
    const tmsBefore = await apiCount(page, "/api/tms");
    const projectsBefore = await publishedCount(page);

    await page.goto("/projects/new");
    await startFromScratch(page);
    await chooseTMFromScratch(page);
    // Fill project name so the parent form would validate successfully IF it ran
    await page.getByPlaceholder(/project name/i).fill(`J15 D4 would-submit-if-buggy ${RUN_ID}`);
    const srcLangTrigger = page.locator('[data-field="source_language"] button').first();
    await selectLanguage(page, srcLangTrigger, "en");
    const tgtLangTrigger = page.locator('[data-field="target_languages"] button').first();
    await selectTargetLanguages(page, tgtLangTrigger, ["de"]);

    // Now open the TM dialog and click Create with empty name
    await page.getByRole("button", { name: /^add translation memory$/i }).first().click();
    await page.getByRole("option", { name: /create new tm/i }).click();
    // Click Create without filling name
    await page.getByRole("dialog").getByRole("button", { name: /^create$/i }).click();

    // Dialog stays open — validation error shown
    await expect(page.getByRole("heading", { name: /create translation memory/i })).toBeVisible();
    await expect(page.getByText(/tm name is required/i)).toBeVisible();

    // [FORM-BOUNDARY] No TM created, NO PROJECT created, URL unchanged
    expect(await apiCount(page, "/api/tms")).toBe(tmsBefore);
    expect(await publishedCount(page),
      "D4: failed TM validation must NOT create a project").toBe(projectsBefore);
    await expect(page).toHaveURL(/\/projects\/new(\?draft=\d+)?$/);

    // Cleanup: close dialog
    await page.getByRole("dialog").getByRole("button", { name: /cancel/i }).click();
  });

  test("D5: Cancel TB dialog on /projects/new creates nothing", async ({ page }) => {
    await login(page);
    const tbsBefore = await apiCount(page, "/api/terminology");

    await page.goto("/projects/new");
    await startFromScratch(page);
    await chooseTMFromScratch(page);
    await page.getByRole("button", { name: /^add term base$/i }).first().click();
    await page.getByRole("option", { name: /create new term base/i }).click();
    await expect(page.getByRole("heading", { name: /create term base/i })).toBeVisible();

    // Type something, then Cancel
    await page.getByPlaceholder(/term base name/i).fill("J15 D5 discarded");
    await page.getByRole("dialog").getByRole("button", { name: /cancel/i }).click();
    await expect(page.getByRole("heading", { name: /create term base/i })).not.toBeVisible({ timeout: 3000 });

    expect(await apiCount(page, "/api/terminology")).toBe(tbsBefore);
    const stray = await apiFind(page, "/api/terminology", (t) => t.name === "J15 D5 discarded");
    expect(stray).toBeUndefined();
  });

  test("D6: re-opening TM dialog shows a fresh empty form (not stale from previous)", async ({ page }) => {
    await login(page);
    await page.goto("/projects/new");
    await startFromScratch(page);
    await chooseTMFromScratch(page);

    // Open, type, cancel
    await page.getByRole("button", { name: /^add translation memory$/i }).first().click();
    await page.getByRole("option", { name: /create new tm/i }).click();
    await page.getByPlaceholder(/tm name/i).fill("J15 D6 stale");
    await page.getByRole("dialog").getByRole("button", { name: /cancel/i }).click();
    await expect(page.getByRole("heading", { name: /create translation memory/i })).not.toBeVisible({ timeout: 3000 });

    // Re-open — the name field should be empty
    await page.getByRole("button", { name: /^add translation memory$/i }).first().click();
    await page.getByRole("option", { name: /create new tm/i }).click();
    await expect(page.getByPlaceholder(/tm name/i)).toHaveValue("");

    // Close
    await page.getByRole("dialog").getByRole("button", { name: /cancel/i }).click();
  });

  // ═══════════════════════════════════════════════════════════════════════
  // Phase G — Extra adversarial form-boundary + race tests
  //
  // Covers edge cases beyond the main gauntlet: rapid double-click, stacked
  // popover state, dialog-before-form-fill ordering, keyboard activation
  // (Space), backdrop close, alternating resource types, and "open dialog
  // on an incomplete form" poisoning.
  // ═══════════════════════════════════════════════════════════════════════

  test("G1: rapid double-click on TM Create button must not double-create or navigate", async ({ page }) => {
    await login(page);
    const projectsBefore = await publishedCount(page);

    // Count the create requests the double-click sends. The server could
    // de-duplicate by name and hide a second POST, so the count is taken on
    // the wire, before the name check below.
    const tmPosts: string[] = [];
    page.on("request", (req) => {
      if (req.method() === "POST" && new URL(req.url()).pathname === "/api/tms") {
        tmPosts.push(req.url());
      }
    });

    await page.goto("/projects/new");
    await startFromScratch(page);
    await chooseTMFromScratch(page);
    await page.getByPlaceholder(/project name/i).fill(`J15 G1 should-stay-here ${RUN_ID}`);
    await selectLanguage(page, page.locator('[data-field="source_language"] button').first(), "en");
    await selectTargetLanguages(page, page.locator('[data-field="target_languages"] button').first(), ["de"]);
    await page.getByRole("button", { name: /^add translation memory$/i }).first().click();
    await page.getByRole("option", { name: /create new tm/i }).click();

    const tmName = `J15 G1 double-click ${RUN_ID}`;
    await page.getByPlaceholder(/tm name/i).fill(tmName);

    // Rapid double-click — intentionally race the mutation
    const createBtn = page.getByRole("dialog").getByRole("button", { name: /^create$/i });
    await createBtn.click({ clickCount: 2, delay: 20 });

    // The dialog closing is the settled state
    await expect(page.getByRole("heading", { name: /create translation memory/i })).not.toBeVisible({ timeout: 5000 });
    expect(tmPosts.length,
      "G1: rapid double-click must send exactly ONE create request").toBe(1);

    // [FORM-BOUNDARY]
    await expect(page,
      "G1: double-click must not navigate away from /projects/new").toHaveURL(/\/projects\/new(\?draft=\d+)?$/);
    // No project should have been created
    expect(await publishedCount(page),
      "G1: double-click must not create a project").toBe(projectsBefore);
    // At most ONE TM should exist with this name (not two)
    const headers = await authHeader(page);
    const tmsRes = await page.request.get(`${API}/api/tms`, { headers });
    const tmsBody = await tmsRes.json();
    const matches = (tmsBody.data ?? []).filter((t: any) => t.name === tmName);
    expect(matches.length,
      "G1: rapid double-click must create exactly ONE TM, not duplicates").toBe(1);
  });

  test("G2: Space-key activation of Create button behaves like click (bug path)", async ({ page }) => {
    await login(page);
    const projectsBefore = await publishedCount(page);
    const tmsBefore = await apiCount(page, "/api/tms");

    await page.goto("/projects/new");
    await startFromScratch(page);
    await chooseTMFromScratch(page);
    await page.getByPlaceholder(/project name/i).fill(`J15 G2 space-key ${RUN_ID}`);
    await selectLanguage(page, page.locator('[data-field="source_language"] button').first(), "en");
    await selectTargetLanguages(page, page.locator('[data-field="target_languages"] button').first(), ["de"]);
    await page.getByRole("button", { name: /^add translation memory$/i }).first().click();
    await page.getByRole("option", { name: /create new tm/i }).click();

    const tmName = `J15 G2 space ${RUN_ID}`;
    await page.getByPlaceholder(/tm name/i).fill(tmName);

    // Focus the Create button via Tab and activate with Space
    const createBtn = page.getByRole("dialog").getByRole("button", { name: /^create$/i });
    await createBtn.focus();
    await page.keyboard.press("Space");

    // Wait for dialog to close
    await expect(page.getByRole("heading", { name: /create translation memory/i })).not.toBeVisible({ timeout: 5000 });

    // [FORM-BOUNDARY]
    await expect(page,
      "G2: Space on Create button must not navigate away").toHaveURL(/\/projects\/new(\?draft=\d+)?$/);
    expect(await publishedCount(page),
      "G2: Space on Create button must not create a project").toBe(projectsBefore);
    // The TM SHOULD have been created (Space is a valid button activation)
    expect(await apiCount(page, "/api/tms"),
      "G2: Space on Create button should create the TM").toBe(tmsBefore + 1);
  });

  test("G3: open TM dialog on an EMPTY project form (no name, no langs) — must not navigate", async ({ page }) => {
    await login(page);
    const projectsBefore = await publishedCount(page);
    const tmsBefore = await apiCount(page, "/api/tms");

    await page.goto("/projects/new");
    await startFromScratch(page);
    await chooseTMFromScratch(page);
    // DO NOT fill the project form at all
    await page.getByRole("button", { name: /^add translation memory$/i }).first().click();
    await page.getByRole("option", { name: /create new tm/i }).click();

    const tmName = `J15 G3 empty-form ${RUN_ID}`;
    await page.getByPlaceholder(/tm name/i).fill(tmName);
    // Project form is intentionally empty, so pick the TM's langs in the dialog
    // itself (TM requires source + ≥1 target).
    await selectLanguage(page, page.getByTestId("tm-create-source").locator("button").first(), "en");
    await selectTargetLanguages(page, page.getByTestId("tm-create-target").locator("button").first(), ["de"]);
    await page.getByRole("dialog").getByRole("button", { name: /^create$/i }).click();

    await page.waitForTimeout(1500);

    // [FORM-BOUNDARY]
    // The project form is empty / invalid — react-hook-form validation would fail
    // on submit — but the bug might still trigger a POST attempt or a validation flash
    await expect(page,
      "G3: empty project form + TM dialog Create must not navigate").toHaveURL(/\/projects\/new(\?draft=\d+)?$/);
    expect(await publishedCount(page),
      "G3: empty project form must not create a project").toBe(projectsBefore);
    // TM should still be created
    expect(await apiCount(page, "/api/tms"),
      "G3: TM should still be created via the dialog").toBe(tmsBefore + 1);
    // Fields that the user never touched must remain empty
    await expect(page.getByPlaceholder(/project name/i)).toHaveValue("");
  });

  test("G4: clicking the backdrop closes TM dialog without submitting parent form", async ({ page }) => {
    await login(page);
    const projectsBefore = await publishedCount(page);
    const tmsBefore = await apiCount(page, "/api/tms");

    await page.goto("/projects/new");
    await startFromScratch(page);
    await chooseTMFromScratch(page);
    await page.getByPlaceholder(/project name/i).fill(`J15 G4 backdrop-close ${RUN_ID}`);
    await page.getByRole("button", { name: /^add translation memory$/i }).first().click();
    await page.getByRole("option", { name: /create new tm/i }).click();
    await expect(page.getByRole("heading", { name: /create translation memory/i })).toBeVisible();

    // Type something so we'd know if cancel preserved or discarded
    await page.getByPlaceholder(/tm name/i).fill("J15 G4 should-not-exist");

    // Click the backdrop (near viewport edge, outside the dialog popup)
    const overlay = page.locator('[data-slot="dialog-overlay"]');
    await overlay.click({ position: { x: 10, y: 10 } });

    // Dialog should close
    await expect(page.getByRole("heading", { name: /create translation memory/i })).not.toBeVisible({ timeout: 3000 });

    // [FORM-BOUNDARY]
    await expect(page).toHaveURL(/\/projects\/new(\?draft=\d+)?$/);
    expect(await publishedCount(page)).toBe(projectsBefore);
    expect(await apiCount(page, "/api/tms")).toBe(tmsBefore);
    // Project name field still intact
    await expect(page.getByPlaceholder(/project name/i)).toHaveValue(`J15 G4 backdrop-close ${RUN_ID}`);
  });

  test("G5: alternating TM → TB → TM inline-creates in one session preserve form state throughout", async ({ page }) => {
    test.setTimeout(60_000);
    await login(page);
    const tmsBefore = await apiCount(page, "/api/tms");
    const tbsBefore = await apiCount(page, "/api/terminology");
    const projectsBefore = await publishedCount(page);

    await page.goto("/projects/new");
    await startFromScratch(page);
    await chooseTMFromScratch(page);
    await page.getByPlaceholder(/project name/i).fill(`J15 G5 alternating ${RUN_ID}`);
    await selectLanguage(page, page.locator('[data-field="source_language"] button').first(), "en");
    await selectTargetLanguages(page, page.locator('[data-field="target_languages"] button').first(), ["de"]);

    // ── Create TM 1 ──
    await page.getByRole("button", { name: /^add translation memory$/i }).first().click();
    await page.getByRole("option", { name: /create new tm/i }).click();
    const tm1 = `J15 G5 TM-1 ${RUN_ID}`;
    await page.getByPlaceholder(/tm name/i).fill(tm1);
    await page.getByRole("dialog").getByRole("button", { name: /^create$/i }).click();
    await expect(page.getByRole("heading", { name: /create translation memory/i })).not.toBeVisible({ timeout: 5000 });

    // [FORM-BOUNDARY after TM 1]
    await expect(page).toHaveURL(/\/projects\/new(\?draft=\d+)?$/);
    expect(await publishedCount(page)).toBe(projectsBefore);

    // ── Create TB ──
    await page.getByRole("button", { name: /^add term base$/i }).first().click();
    await page.getByRole("option", { name: /create new term base/i }).click();
    const tb = `J15 G5 TB ${RUN_ID}`;
    await page.getByPlaceholder(/term base name/i).fill(tb);
    const tbDialog = page.getByRole("dialog");
    const tbPopovers = tbDialog.locator('[data-slot="combobox-trigger"]');
    await selectLanguage(page, tbPopovers.nth(0), "en");
    await tbDialog.getByRole("button", { name: /^create$/i }).click();
    await expect(page.getByRole("heading", { name: /create term base/i })).not.toBeVisible({ timeout: 5000 });

    // [FORM-BOUNDARY after TB]
    await expect(page).toHaveURL(/\/projects\/new(\?draft=\d+)?$/);
    expect(await publishedCount(page)).toBe(projectsBefore);

    // ── Create TM 2 (second TM inline-create in the same session) ──
    // The add/create control is the title picker ("Add translation memory"); it
    // stays put after TM 1 was added as a row.
    await page.getByRole("button", { name: /^add translation memory$/i }).first().click();
    await page.getByRole("option", { name: /create new tm/i }).click();
    // Dialog form should be CLEAN (not "J15 G5 TM-1")
    await expect(page.getByPlaceholder(/tm name/i)).toHaveValue("");
    const tm2 = `J15 G5 TM-2 ${RUN_ID}`;
    await page.getByPlaceholder(/tm name/i).fill(tm2);
    await page.getByRole("dialog").getByRole("button", { name: /^create$/i }).click();
    await expect(page.getByRole("heading", { name: /create translation memory/i })).not.toBeVisible({ timeout: 5000 });

    // [FORM-BOUNDARY after TM 2]
    await expect(page).toHaveURL(/\/projects\/new(\?draft=\d+)?$/);
    expect(await publishedCount(page)).toBe(projectsBefore);

    // Project name still intact
    await expect(page.getByPlaceholder(/project name/i)).toHaveValue(`J15 G5 alternating ${RUN_ID}`);
    // TM count increased by 2, TB count by 1
    expect(await apiCount(page, "/api/tms")).toBe(tmsBefore + 2);
    expect(await apiCount(page, "/api/terminology")).toBe(tbsBefore + 1);
  });

  test("G6: clicking TM dropdown while cmdk dropdown is already open (stacked popover) doesn't leak events", async ({ page }) => {
    await login(page);
    const projectsBefore = await publishedCount(page);

    await page.goto("/projects/new");
    await startFromScratch(page);
    await chooseTMFromScratch(page);
    await page.getByPlaceholder(/project name/i).fill(`J15 G6 stacked ${RUN_ID}`);

    // Open the TM dropdown — shows existing TMs + Create new option
    await page.getByRole("button", { name: /^add translation memory$/i }).first().click();
    await expect(page.locator('[cmdk-list]')).toBeVisible();

    // While the dropdown is open, click "Create new TM..." — this closes
    // the dropdown and opens the dialog. The stacked popover transition
    // should NOT accidentally trigger the parent form.
    await page.getByRole("option", { name: /create new tm/i }).click();

    // Immediately (within the React render cycle) — URL should be unchanged
    await expect(page).toHaveURL(/\/projects\/new(\?draft=\d+)?$/);
    expect(await publishedCount(page)).toBe(projectsBefore);

    // Close the dialog to clean up
    await page.keyboard.press("Escape");
  });

  test("G7: opening TM dialog, then project form's tab key navigation does not accidentally submit", async ({ page }) => {
    await login(page);
    const projectsBefore = await publishedCount(page);

    await page.goto("/projects/new");
    await startFromScratch(page);
    await chooseTMFromScratch(page);
    await page.getByPlaceholder(/project name/i).fill(`J15 G7 tab-nav ${RUN_ID}`);
    await page.getByRole("button", { name: /^add translation memory$/i }).first().click();
    await page.getByRole("option", { name: /create new tm/i }).click();
    await expect(page.getByRole("heading", { name: /create translation memory/i })).toBeVisible();

    // Tab through the dialog — should reach Create button eventually
    // Don't activate it, just walk through
    for (let i = 0; i < 5; i++) {
      await page.keyboard.press("Tab");
    }

    // [FORM-BOUNDARY]
    await expect(page).toHaveURL(/\/projects\/new(\?draft=\d+)?$/);
    expect(await publishedCount(page)).toBe(projectsBefore);

    // Cleanup
    await page.keyboard.press("Escape");
  });

  // ═══════════════════════════════════════════════════════════════════════
  // Phase F — Cross-surface verification
  //
  // These tests are non-serial because they only READ state that was
  // written by the serial describe. If the gauntlet B failed, gauntlet*
  // IDs will be null and F1/F6 will fail meaningfully; F2-F5 query the
  // DB directly so they report independently.
  // ═══════════════════════════════════════════════════════════════════════

  test("F1: exactly one gauntlet project exists (no duplicates from premature submits)", async ({ page }) => {
    await login(page);
    const projects = await page.request.get(`${API}/api/projects`, { headers: await authHeader(page) });
    const body = await projects.json();
    const matches = body.data.filter((p: any) => p.name === GAUNTLET_PROJECT);
    expect(matches.length,
      `F1: exactly 1 project named "${GAUNTLET_PROJECT}" should exist`).toBe(1);
  });

  test("F2: project list UI shows the gauntlet project", async ({ page }) => {
    await login(page);
    await page.goto("/");
    await expect(page.getByText(GAUNTLET_PROJECT)).toBeVisible({ timeout: 5000 });
  });

  test("F3: TM list UI shows the gauntlet TM", async ({ page }) => {
    await login(page);
    await page.goto("/memories");
    await expect(page.getByText(GAUNTLET_TM)).toBeVisible({ timeout: 5000 });
  });

  test("F4: TB list UI shows the gauntlet TB", async ({ page }) => {
    await login(page);
    await page.goto("/terminology");
    await expect(page.getByText(GAUNTLET_TB)).toBeVisible({ timeout: 5000 });
  });

  test("F5: no J15-prefixed MT engine exists (inline-create must never touch MT)", async ({ page }) => {
    await login(page);
    // Stronger than a count: explicitly verify no engine was created by the journey
    const stray = await apiFind(page, "/api/mt-engines", (e: any) =>
      typeof e.name === "string" && e.name.startsWith("J15 "),
    );
    expect(stray,
      "F5: no MT engine should be named with a J15 prefix — inline-create must not touch MT").toBeUndefined();
  });

  test("F6: re-entering gauntlet project settings still shows the TM and TB assignments", async ({ page }) => {
    await login(page);
    // Audit L-5: include skip reason text so CI logs show why F6 didn't run
    // (instead of a bare `test.skip()` that's indistinguishable from "passed").
    test.skip(
      gauntletProjectId === null,
      "Phase B project creation failed — F6 depends on gauntletProjectId from B",
    );
    const settings = await projectFromAPI(page, gauntletProjectId!);
    expect(settings.tm_assignments.some((a: any) => a.tm_id === gauntletTMId)).toBe(true);
    expect(settings.term_assignments.some((a: any) => a.term_base_id === gauntletTBId)).toBe(true);
  });

  // ═══════════════════════════════════════════════════════════════════════
  // Phase Z — Cleanup (by name prefix — works even if B failed)
  //
  // Queries each resource type by the "J15" prefix + this run's RUN_ID and
  // deletes matches. Robust to partial-failure runs where ID globals are null.
  // ═══════════════════════════════════════════════════════════════════════

  test("Z1: cleanup — delete every resource prefixed with this run's ID", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    const doCleanup = async (listPath: string, delPath: (id: number) => string) => {
      const res = await page.request.get(`${API}${listPath}`, { headers });
      const body = await res.json();
      const matches = (body.data ?? []).filter((item: any) =>
        typeof item.name === "string" && item.name.startsWith("J15 ") && item.name.includes(String(RUN_ID)),
      );
      for (const item of matches) {
        try {
          await page.request.delete(`${API}${delPath(item.id)}`, { headers });
        } catch { /* best effort */ }
      }
      return matches.length;
    };

    // Projects must go first (assignments reference TMs/TBs)
    const projectsDeleted = await doCleanup("/api/projects", (id) => `/api/projects/${id}`);
    const tmsDeleted = await doCleanup("/api/tms", (id) => `/api/tms/${id}`);
    const tbsDeleted = await doCleanup("/api/terminology", (id) => `/api/terminology/${id}`);
    // eslint-disable-next-line no-console
    console.log(`[J15 cleanup] projects=${projectsDeleted} tms=${tmsDeleted} tbs=${tbsDeleted}`);

    // Adversarial: verify nothing J15-prefixed remains after cleanup
    const remaining = async (listPath: string) => {
      const res = await page.request.get(`${API}${listPath}`, { headers });
      const body = await res.json();
      return (body.data ?? []).filter((item: any) =>
        typeof item.name === "string" && item.name.startsWith("J15 "),
      ).length;
    };
    expect(await remaining("/api/projects"),
      "Z1: no stray J15-prefixed projects must remain").toBe(0);
    expect(await remaining("/api/tms"),
      "Z1: no stray J15-prefixed TMs must remain").toBe(0);
    expect(await remaining("/api/terminology"),
      "Z1: no stray J15-prefixed Term bases must remain").toBe(0);
  });
});
