import { test, expect } from "@playwright/test";
import * as fs from "fs";
import * as path from "path";
import {
  login,
  segmentEditor,
  advanceToFinalStepViaAPI,
  assertCompletionPersistedToTM,
  uploadFileViaAPI,
  pretranslateFileViaAPI,
  createTMViaAPI,
  importTMXViaAPI,
  deleteTMViaAPI,
  createProjectViaAPI,
  deleteProjectViaAPI,
  createTemplateViaAPI,
  updateTemplateViaAPI,
  deleteTemplateViaAPI,
  setTemplateTMsViaAPI,
  setTemplateMTViaAPI,
  setProjectTMsViaAPI,
  getJobTranslationsViaAPI,
  updateHumanTranslationViaAPI,
  verifyProjectSettings,
} from "./helpers";

/**
 * J2 — Template + TM Round-Trip (Adversarial)
 *
 * Verifies: template → project → TM match → confirm → complete → TM grew.
 * Also: apply same template to a SECOND project, verify the copy is independent.
 */

const API = "";

let roundtripTMId: number;
let templateId: number;
let templateRouterId: number | null;
let project1Id: number;
let project2Id: number;
let fileId: number;

test.describe.serial("J2 — Template + TM Round-Trip", () => {

  // ════════════════════════════════════════════════════════════════════════
  // Phase A: Setup
  // ════════════════════════════════════════════════════════════════════════

  test("A1: create Roundtrip TM", async ({ page }) => {
    await login(page);
    roundtripTMId = await createTMViaAPI(page, "Roundtrip TM");
    expect(roundtripTMId).toBeGreaterThan(0);
  });

  test("A2: import TMX (en→fr: Hello/Goodbye/Thank you)", async ({ page }) => {
    await login(page);
    // TMX uses base code 'fr' entries, but the project uses 'fr-ca'.
    // This exercises locale-aware TM matching AGAIN (same as J1 with es-mx).
    // Core TMS concerns like locale handling must be tested in EVERY journey.
    const tmx = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header creationtool="test" srclang="en" datatype="plaintext" segtype="sentence"/>
  <body>
    <tu tuid="rt-001"><tuv xml:lang="en"><seg>Hello</seg></tuv><tuv xml:lang="fr"><seg>Bonjour</seg></tuv></tu>
    <tu tuid="rt-002"><tuv xml:lang="en"><seg>Goodbye</seg></tuv><tuv xml:lang="fr"><seg>Au revoir</seg></tuv></tu>
    <tu tuid="rt-003"><tuv xml:lang="en"><seg>Thank you</seg></tuv><tuv xml:lang="fr"><seg>Merci</seg></tuv></tu>
  </body>
</tmx>`;
    await importTMXViaAPI(page, roundtripTMId, tmx, "roundtrip.tmx");
  });

  test("A3-A4: create template + assign TM and MT", async ({ page }) => {
    await login(page);
    const tmpl = await createTemplateViaAPI(page, "Roundtrip Template");
    templateId = tmpl.id;

    // Assign TM: en→fr, Search+Update (read_write), priority 1
    await setTemplateTMsViaAPI(page, templateId, [
      { tm_id: roundtripTMId, source_language: "en", target_language: "fr-ca", mode: "read_write", priority: 1 },
    ]);

    // Assign MT: LibreTranslate en→fr-ca
    templateRouterId = await setTemplateMTViaAPI(page, templateId, [
      { mt_engine_id: 1, source_language: "en", target_language: "fr-ca" },
    ]);

    // Set template scalars: fuzzy=75%, pre-translate ON (as spec requires)
    await updateTemplateViaAPI(page, templateId, {
      name: "Roundtrip Template",
      fuzzy_threshold: 0.75,
      mt_on_upload: true,
    });
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B: First project from template
  // ════════════════════════════════════════════════════════════════════════

  test("B1-B3: create project from template + verify settings", async ({ page }) => {
    await login(page);

    // Create project WITH the template — copies TM + MT assignments into the
    // project's own profile at creation (templates are create-time-only).
    project1Id = await createProjectViaAPI(page, "Roundtrip Project 1", "en", ["fr-ca"], templateId);

    // B3: Verify settings match template — the project points at the
    // template's own MT router (shared by reference).
    await verifyProjectSettings(page, project1Id, {
      tm_count: 1,
      mt_router_id: templateRouterId,
    });
  });

  test("B4-B5: upload file + pre-translate (setup)", async ({ page }) => {
    test.setTimeout(30_000);
    await login(page);

    // Upload: 3 segments chosen to trigger different match types
    const content = "Hello world\nThank you for your patience\nA brand new sentence\n";
    const result = await uploadFileViaAPI(page, project1Id, "roundtrip.txt", content, "text/plain");
    fileId = result.data?.id ?? result.id;

    // Pre-translate via per-job phase endpoints (production manual-button path)
    await pretranslateFileViaAPI(page, project1Id, fileId);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Get the job
    const jobsResp = await page.request.get(`${API}/api/projects/${project1Id}/jobs`, { headers });
    const jobs = (await jobsResp.json()).data;
    expect(jobs.length).toBeGreaterThan(0);
    const jobId = jobs[0].id;

    // Navigate to editor and verify segments loaded
    await page.goto(`/projects/${project1Id}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Hello world/i).first()).toBeVisible({ timeout: 10000 });
  });

  test("B6: edit the fuzzy match + confirm", async ({ page }) => {
    await login(page);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const jobsResp = await page.request.get(`${API}/api/projects/${project1Id}/jobs`, { headers });
    const jobId = (await jobsResp.json()).data[0].id;

    await page.goto(`/projects/${project1Id}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Hello world/i).first()).toBeVisible({ timeout: 10000 });

    // Click the second segment ("Thank you for your patience" — fuzzy match to "Thank you")
    const translationCells = page.locator("[data-translation-cell]");
    await translationCells.nth(1).click();
    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });

    // Edit: type our own translation
    await editor.click();
    const isMac = process.platform === "darwin";
    await page.keyboard.press(isMac ? "ControlOrMeta+a" : "Control+a");
    await page.keyboard.type("Merci pour votre patience");

    // Confirm
    const [resp] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/translations/") && r.request().method() === "PUT" && !r.url().includes("/draft"),
        { timeout: 10000 },
      ),
      page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
    ]);
    expect(resp.ok()).toBe(true);
  });

  test("B7-B8: confirm exact match (no edit) + MT segment (no edit)", async ({ page }) => {
    await login(page);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const jobsResp = await page.request.get(`${API}/api/projects/${project1Id}/jobs`, { headers });
    const jobId = (await jobsResp.json()).data[0].id;

    await page.goto(`/projects/${project1Id}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Hello world/i).first()).toBeVisible({ timeout: 10000 });

    const isMac = process.platform === "darwin";
    const translationCells = page.locator("[data-translation-cell]");

    // Confirm segments 1 and 3 (indices 0 and 2) without editing
    for (const idx of [0, 2]) {
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

  test("B9: complete the job — TM write-back", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const jobsResp = await page.request.get(`${API}/api/projects/${project1Id}/jobs`, { headers });
    const jobId = (await jobsResp.json()).data[0].id;

    // B6's confirm already wrote its pair to the read_write TM, so that entry
    // cannot tell a confirm from the completion write-back. Re-confirm the
    // second segment with a new target while the TM is read-only — a confirm
    // writes nothing then — restore read_write, and complete: the new target
    // can only have come from the completion.
    // Each step confirms its own segments: confirm in the final step, the
    // one whose confirmations the completion writes to TM.
    await advanceToFinalStepViaAPI(page, jobId);
    const tm = { tm_id: roundtripTMId, source_language: "en", target_language: "fr-ca", priority: 1 };
    await setProjectTMsViaAPI(page, project1Id, [{ ...tm, mode: "read" }]);
    const rows = await getJobTranslationsViaAPI(page, jobId);
    await updateHumanTranslationViaAPI(page, fileId, rows[1].id, "Merci de votre patience");
    await setProjectTMsViaAPI(page, project1Id, [{ ...tm, mode: "read_write" }]);

    await assertCompletionPersistedToTM(page, jobId, {
      tmId: roundtripTMId,
      sourceText: "Thank you for your patience",
      targetText: "Merci de votre patience",
      absentBefore: true,
    });
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C: Second project — verify template copy is independent
  // ════════════════════════════════════════════════════════════════════════

  test("C1-C3: create second project from same template + change fuzzy", async ({ page }) => {
    await login(page);

    // C1: Create project 2 from the same template
    project2Id = await createProjectViaAPI(page, "Roundtrip Project 2", "en", ["fr-ca"], templateId);

    // C2: Change fuzzy threshold to 50% on project 2
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const patchResp = await page.request.patch(`${API}/api/projects/${project2Id}/settings`, {
      headers,
      data: { fuzzy_threshold: 0.5 },
    });
    expect(patchResp.ok()).toBe(true);
  });

  test("C4-C5: verify template copy independence", async ({ page }) => {
    await login(page);

    // C4: Project 2 should have fuzzy=50% (we changed it) but still have TM + MT + pre_translate from template
    await verifyProjectSettings(page, project2Id, {
      fuzzy_threshold: 0.5,
      mt_on_upload: true, // inherited from template, not changed
      tm_count: 1,
      mt_router_id: templateRouterId,
    });

    // C5: Project 1 should STILL have the original settings from the template.
    // This is the critical independence check — project 2's fuzzy change must NOT affect project 1.
    await verifyProjectSettings(page, project1Id, {
      fuzzy_threshold: 0.75,
      mt_on_upload: true, // inherited from template
      tm_count: 1,
      mt_router_id: templateRouterId,
    });
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D: Cleanup
  // ════════════════════════════════════════════════════════════════════════

  test("D1: cleanup all resources", async ({ page }) => {
    await login(page);
    if (project2Id) await deleteProjectViaAPI(page, project2Id);
    if (project1Id) await deleteProjectViaAPI(page, project1Id);
    if (templateId) await deleteTemplateViaAPI(page, templateId);
    if (roundtripTMId) await deleteTMViaAPI(page, roundtripTMId);
  });

  // Fallback cleanup
  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    try {
      await login(page);
      if (project2Id) await deleteProjectViaAPI(page, project2Id).catch(() => {});
      if (project1Id) await deleteProjectViaAPI(page, project1Id).catch(() => {});
      if (templateId) await deleteTemplateViaAPI(page, templateId).catch(() => {});
      if (roundtripTMId) await deleteTMViaAPI(page, roundtripTMId).catch(() => {});
    } catch {} finally {
      await page.close();
    }
  });
});
