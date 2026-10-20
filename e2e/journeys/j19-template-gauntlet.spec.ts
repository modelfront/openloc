/**
 * J19 — Template CRUD Gauntlet (Adversarial)
 *
 * Goal: lock down the template CRUD validation surface AND the copy-semantics
 * invariant that distinguishes templates from references. Mirrors
 * the j17 phase structure (A setup / B validation parity / C copy semantics /
 * D entity lifecycle / E cleanup).
 *
 * Locales (deliberately heterogeneous — corner cases live at the edges):
 *   - Primary: da-DK  (Danish, Denmark)         — base-code fallback to `da`
 *   - Multi-locale fan-out trio (Phase C):
 *       da-DK + nb-NO + fi-FI                   — three concurrent fallbacks
 *       in one project, proves create-with-template's per-pair loop doesn't drop
 *       any. None of these are used by other journeys.
 *   - Casing probe (Phase B):  DA-dk             — normalization round-trip
 *
 * Why da-DK / nb-NO / fi-FI:
 *   The mock MT engine (internal/mt/languages.go::mockLanguages) supports
 *   `da`/`nb`/`fi` as base codes (same fallback shape as j17's sv-SE → sv).
 *   It does NOT support `ca` at all, so ValidateAssignment would reject
 *   en→ca-ES with 400. Add `ca` to mockLanguages before using ca-ES.
 *
 * Coordination with existing journeys:
 *   - j2 already covers: create template, apply to 2 projects, change one
 *     project's fuzzy → other unaffected (copy independence happy path).
 *     j19 explicitly does NOT redo that. j19 covers the INVERSE: edit the
 *     TEMPLATE after applying, verify projects are unchanged across EVERY
 *     scalar AND every assignment type — the full copy-semantics invariant
 *     surface. This is what would catch a future regression that quietly
 *     turns templates from copies into references.
 *   - j17 established the "validation parity with Create" Phase B pattern.
 *     j19 reuses it on PATCH /api/templates/:id, including the
 *     partial-PATCH contracts (absent = keep, null = clear).
 *   - j18 D1 established "delete-with-refs must 409" for QA profiles, j17
 *     D3/D4 confirmed the same for MT engines and MT routers. Templates are
 *     the OPPOSITE: templates are copies, so deleting a template
 *     with attached projects MUST succeed (projects own their own
 *     settings_profile post-apply). Phase D1 inverts the pattern and asserts 204.
 *
 * MT model: a settings profile points at ONE MT router (`mt_router_id`); the
 * router maps target languages to engines. Creating a project from a
 * template copies the POINTER, so template and project share the router by
 * reference — a router edit reaches both, but re-pointing the template does
 * NOT move projects already created from it (that is what Phase C pins).
 *
 * Honest scope notes:
 *   - "Pipeline running while the template is edited" is fundamentally
 *     race-prone in E2E. j19 instead asserts the COPY-SNAPSHOT invariant (Phase C), which
 *     makes that scenario safe by construction. If create-with-template is a true
 *     copy, then by definition no template edit can ever affect a running
 *     pipeline whose project already has its own settings_profile.
 *   - Orphaned settings_profile rows after a template delete are only
 *     visible with direct DB access — out of scope for E2E.
 *   - Cross-tenant template access has dedicated coverage in j9. j19 doesn't
 *     repeat it.
 */
import { test, expect, type APIRequestContext } from "@playwright/test";
import {
  login,
  uploadFileViaAPI,
  pretranslateFileViaAPI,
  createTMViaAPI,
  deleteTMViaAPI,
  createProjectViaAPI,
  deleteProjectViaAPI,
  createMTEngineViaAPI,
  deleteMTEngineViaAPI,
  createTemplateViaAPI,
  deleteTemplateViaAPI,
  setTemplateTMsViaAPI,
  setTemplateMTViaAPI,
  createMTRouterViaAPI,
  getMTRouterViaAPI,
  deleteMTRouterViaAPI,
  updateTemplateViaAPI,
} from "./helpers";

const API = "";
const RUN_ID = Date.now();

// State threaded through the serial describe
let templateId: number;
let templateProfileId: number;
let secondTemplateId: number; // template saved from project A's current settings
let projectAId: number;       // created-with-template, then template edited
let projectBId: number;       // created AFTER template mutation
let projectNordicId: number;  // multi-locale fan-out (C7-C9)
let projectA2Id: number;      // second project from the same template (C10)
let projectAFileId: number;
let projectAJobId: number;
let tmId: number;
let tm2Id: number;            // for the swap-TM-on-template test
let mtEngineId: number;
let router1Id: number | null;  // template's first router (da-DK → engine), set in C1
let router2Id: number;         // swapped in at C3, fanned out to the Nordic trio at C7
let thirdTemplateId: number;   // duplicate of T2 (D3b)

// The Nordic trio: 3 regional locales whose base codes are all in mock.
// Each forces a base-code fallback (da-DK→da, nb-NO→nb, fi-FI→fi), so the
// resolver must do real work for each pair independently.
const NORDIC_PAIRS = [
  { target: "da-DK", expectedPrefix: "[DA] " },
  { target: "nb-NO", expectedPrefix: "[NB] " },
  { target: "fi-FI", expectedPrefix: "[FI] " },
];

async function authHeader(page: any): Promise<Record<string, string>> {
  const token = await page.evaluate(() => localStorage.getItem("token"));
  return { Authorization: `Bearer ${token}` };
}

async function getTemplate(req: APIRequestContext, headers: Record<string, string>, id: number) {
  const res = await req.get(`${API}/api/templates/${id}`, { headers });
  expect(res.ok(), `getTemplate ${id}: ${res.status()}`).toBe(true);
  return (await res.json()).data as {
    template: {
      id: number; name: string; fuzzy_threshold: number; mt_on_upload: boolean; qa_profile_id: number | null;
      mt_router_id: number | null; mt_router_name: string | null;
    };
    tm_assignments: any[];
    term_assignments: any[];
  };
}

async function getProjectSettings(req: APIRequestContext, headers: Record<string, string>, id: number) {
  const res = await req.get(`${API}/api/projects/${id}/settings`, { headers });
  expect(res.ok(), `getProjectSettings ${id}: ${res.status()}`).toBe(true);
  return (await res.json()).data as {
    project: {
      id: number; fuzzy_threshold: number; mt_on_upload: boolean; qa_profile_id: number | null;
      mt_router_id: number | null; mt_router_name: string | null;
    };
    tm_assignments: any[];
    term_assignments: any[];
  };
}

test.describe.serial("J19 — Template CRUD Gauntlet", () => {
  // ════════════════════════════════════════════════════════════════════════
  // Phase A — Create-side validation gauntlet
  // ════════════════════════════════════════════════════════════════════════

  test("A1: create accepts a well-formed template (positive control + happy-path persistence)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await page.request.post(`${API}/api/templates`, {
      headers,
      data: { name: `J19 Primary ${RUN_ID}`, mt_on_upload: true, fuzzy_threshold: 0.85 },
    });
    expect(res.status(), "A1: well-formed template create must succeed").toBe(201);
    const body = await res.json();
    templateId = body.data.id;
    templateProfileId = body.data.settings_profile_id;
    expect(templateId).toBeGreaterThan(0);
    expect(templateProfileId).toBeGreaterThan(0);
    expect(body.data.fuzzy_threshold,
      "A1: fuzzy_threshold must round-trip from create to response",
    ).toBeCloseTo(0.85, 5);
    expect(body.data.mt_on_upload,
      "A1: mt_on_upload must round-trip true",
    ).toBe(true);
  });

  test("A2: create with empty name returns 400", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await page.request.post(`${API}/api/templates`, { headers, data: { name: "" } });
    expect(res.status(), "A2: empty name must 400").toBe(400);
  });

  test("A3: create with whitespace-only name returns 400 (parity with mtengine A4)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await page.request.post(`${API}/api/templates`, { headers, data: { name: "   \t\n  " } });
    expect(res.status(),
      "A3: whitespace-only name must 400 (HandleCreate trims, like j17 mtengine A4).",
    ).toBe(400);
  });

  test("A4: defaults — fuzzy threshold and upload phases default correctly", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await page.request.post(`${API}/api/templates`, {
      headers,
      data: { name: `J19 Default-Probe ${RUN_ID}` },
    });
    expect(res.ok()).toBe(true);
    const body = await res.json();
    try {
      expect(body.data.fuzzy_threshold,
        "A4: fuzzy_threshold default is 0.75 per HandleCreate",
      ).toBeCloseTo(0.75, 5);
      expect(body.data.mt_on_upload, "A4: MT on upload defaults on").toBe(true);
      expect(body.data.tm_on_upload, "A4: TM on upload defaults on").toBe(true);
    } finally {
      // Delete the probe even when an assertion above failed.
      await page.request.delete(`${API}/api/templates/${body.data.id}`, { headers }).catch(() => {});
    }
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B — Update-side validation parity (j17 pattern reused)
  //
  // PATCH /api/templates/:id must enforce the same invariants as POST and
  // must follow the tri-state contract for nullable fields.
  // ════════════════════════════════════════════════════════════════════════

  test("B1: PATCH with empty name returns 400 (positive control — Update DOES check)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await page.request.patch(`${API}/api/templates/${templateId}`, {
      headers, data: { name: "" },
    });
    expect(res.status(),
      "B1: HandleUpdate has `req.Name == \"\"` guard — empty name must 400",
    ).toBe(400);
  });

  test("B2: PATCH with whitespace-only name must 400 (validation parity with Create)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await page.request.patch(`${API}/api/templates/${templateId}`, {
      headers, data: { name: "   " },
    });
    expect(res.status(),
      "B2: HandleUpdate must trim+reject whitespace-only names, same as A3.",
    ).toBe(400);
  });

  test("B3: partial PATCH that omits `mt_on_upload` must NOT silently disable it", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    // Step 1: ensure pre_translate is currently true (set in A1)
    const before = await getTemplate(page.request, headers, templateId);
    expect(before.template.mt_on_upload,
      "B3 setup: pre_translate must start as true (set in A1)",
    ).toBe(true);

    // Step 2: send a partial PATCH that ONLY changes the name. No mt_on_upload field.
    // This is what a UI rename form looks like.
    const renamed = `J19 Primary RENAMED ${RUN_ID}`;
    const res = await page.request.patch(`${API}/api/templates/${templateId}`, {
      headers, data: { name: renamed },
    });
    expect(res.ok(), `B3: partial PATCH must succeed: ${res.status()}`).toBe(true);

    // Step 3: verify pre_translate=true survived
    const after = await getTemplate(page.request, headers, templateId);
    expect(after.template.name, "B3: rename did persist (positive control)").toBe(renamed);
    expect(after.template.mt_on_upload,
      "B3: partial PATCH omitting mt_on_upload must keep the current value.",
    ).toBe(true);
  });

  test("B4: PATCH `qa_profile_id: null` must clear (REST convention, tri-state contract)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    // First set a real QA profile on the template so we can clear it.
    const profileRes = await page.request.post(`${API}/api/qa-profiles`, {
      headers, data: { name: `J19 QA Probe ${RUN_ID}` },
    });
    expect(profileRes.ok(), `B4 setup: create QA profile: ${profileRes.status()}`).toBe(true);
    const qaProfileId = (await profileRes.json()).data.id as number;

    try {
      // Assign the QA profile via PATCH (this should work — set positive)
      const setRes = await page.request.patch(`${API}/api/templates/${templateId}`, {
        headers,
        data: { name: `J19 Primary RENAMED ${RUN_ID}`, qa_profile_id: qaProfileId, mt_on_upload: true },
      });
      expect(setRes.ok(), "B4 setup: setting qa_profile_id must succeed").toBe(true);

      const beforeClear = await getTemplate(page.request, headers, templateId);
      expect(beforeClear.template.qa_profile_id, "B4 setup: QA profile must be assigned").toBe(qaProfileId);

      // Now clear with null — REST convention, what a UI's "None" sends.
      const clearRes = await page.request.patch(`${API}/api/templates/${templateId}`, {
        headers,
        data: { name: `J19 Primary RENAMED ${RUN_ID}`, qa_profile_id: null, mt_on_upload: true },
      });
      expect(clearRes.ok(), "B4: PATCH with qa_profile_id=null must succeed").toBe(true);

      const after = await getTemplate(page.request, headers, templateId);
      expect(after.template.qa_profile_id,
        "B4 (tri-state contract): PATCH qa_profile_id=null on a TEMPLATE must clear the field, " +
        "the same tri-state contract projects use (absent = keep, null = clear).",
      ).toBeNull();
    } finally {
      await page.request.delete(`${API}/api/qa-profiles/${qaProfileId}`, { headers }).catch(() => {});
    }
  });

  test("B5: PATCH with negative fuzzy_threshold — backend should bound or reject", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await page.request.patch(`${API}/api/templates/${templateId}`, {
      headers, data: { name: `J19 Primary RENAMED ${RUN_ID}`, fuzzy_threshold: -0.5, mt_on_upload: true },
    });
    // Two acceptable behaviors: 400 (reject) or 200-then-clamp. Either is
    // defensible; what is NOT defensible is silently persisting -0.5.
    if (res.ok()) {
      const after = await getTemplate(page.request, headers, templateId);
      expect(after.template.fuzzy_threshold,
        "B5: if backend accepts negative fuzzy, it MUST clamp to a sane value (>=0). " +
        "Persisting -0.5 verbatim corrupts every project that later applies this template — " +
        "fuzzy match comparisons would behave unpredictably.",
      ).toBeGreaterThanOrEqual(0);
    } else {
      expect(res.status(),
        "B5: rejection path — must be 400 (validation), not 500 (unhandled crash)",
      ).toBe(400);
    }
  });

  test("B6: PATCH with absurd fuzzy_threshold (5.0) — backend should bound or reject", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await page.request.patch(`${API}/api/templates/${templateId}`, {
      headers, data: { name: `J19 Primary RENAMED ${RUN_ID}`, fuzzy_threshold: 5.0, mt_on_upload: true },
    });
    if (res.ok()) {
      const after = await getTemplate(page.request, headers, templateId);
      expect(after.template.fuzzy_threshold,
        "B6: if backend accepts >1.0 fuzzy, it MUST clamp to <=1.0. " +
        "Persisting 5.0 means no TM match will ever clear the threshold — pre-translate becomes a no-op.",
      ).toBeLessThanOrEqual(1.0);
    } else {
      expect(res.status(), "B6: rejection path — 400 not 500").toBe(400);
    }
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C — Copy-semantics invariant (the heart of j19)
  //
  // The contract: applying a template at create time is a one-time COPY. Projects own their
  // own settings_profile post-create. Editing the template afterward must
  // NOT propagate to existing projects. j2 covers the project→project
  // independence after fuzzy-edit on one project. j19 covers the inverse:
  // template→project independence across EVERY scalar and EVERY assignment
  // type — including which MT router the profile points at. If anyone ever
  // quietly switches templates to references, the C tests catch it.
  // ════════════════════════════════════════════════════════════════════════

  test("C0: setup — TM, second TM, MT engine to populate the template's assignments", async ({ page }) => {
    await login(page);
    tmId = await createTMViaAPI(page, `J19 TM A ${RUN_ID}`);
    tm2Id = await createTMViaAPI(page, `J19 TM B ${RUN_ID}`);
    mtEngineId = await createMTEngineViaAPI(page, `J19 Mock MT ${RUN_ID}`, "mock");
    expect(tmId).toBeGreaterThan(0);
    expect(tm2Id).toBeGreaterThan(0);
    expect(mtEngineId).toBeGreaterThan(0);
  });

  test("C1: populate template T1 with TM A and an MT router (da-DK → engine)", async ({ page }) => {
    await login(page);
    await setTemplateTMsViaAPI(page, templateId, [
      { tm_id: tmId, source_language: "en", target_language: "da-DK", mode: "read_write", priority: 1 },
    ]);
    router1Id = await setTemplateMTViaAPI(page, templateId, [
      { mt_engine_id: mtEngineId, source_language: "en", target_language: "da-DK" },
    ]);
    expect(router1Id, "C1: helper must return the router it created").toBeTruthy();

    const t = await getTemplate(page.request, await authHeader(page), templateId);
    expect(t.tm_assignments.length, "C1: template should have 1 TM assignment").toBe(1);
    expect(t.template.mt_router_id, "C1: template points at router 1").toBe(router1Id);
    expect(t.template.mt_router_name, "C1: template resolves the router name").toBeTruthy();
    const r1 = await getMTRouterViaAPI(page, router1Id!);
    expect(r1.rules.map((r) => [r.target_lang, r.mt_engine_id]), "C1: router 1 has the one da-dk rule").toEqual([["da-dk", mtEngineId]]);
  });

  test("C2: create project A WITH the template, snapshot project A's settings", async ({ page }) => {
    await login(page);
    projectAId = await createProjectViaAPI(page, `J19 Project A ${RUN_ID}`, "en", ["da-DK"], templateId);

    const headers = await authHeader(page);
    const projA = await getProjectSettings(page.request, headers, projectAId);

    // Verify create-with-template COPIED everything from template to project
    expect(projA.tm_assignments.length, "C2: project A inherits TM A from template").toBe(1);
    expect(projA.tm_assignments[0].tm_id).toBe(tmId);
    expect(projA.project.mt_router_id, "C2: project A inherits the template's MT router (shared by reference)").toBe(router1Id);
    expect(projA.project.mt_router_name).toBeTruthy();
    expect(projA.project.fuzzy_threshold,
      "C2: project A inherits fuzzy_threshold=0.85 from template (set in A1)",
    ).toBeCloseTo(0.85, 5);
    expect(projA.project.mt_on_upload,
      "C2: project A inherits pre_translate=true from template",
    ).toBe(true);
  });

  test("C3: MUTATE the template AFTER apply — change scalars, swap TM, swap MT router, change name", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    // Change template scalars
    const patchRes = await page.request.patch(`${API}/api/templates/${templateId}`, {
      headers,
      data: {
        name: `J19 Primary MUTATED ${RUN_ID}`,
        fuzzy_threshold: 0.55, // was 0.85
        mt_on_upload: false, // was true
      },
    });
    expect(patchRes.ok(), `C3: scalar PATCH must succeed: ${patchRes.status()}`).toBe(true);

    // Swap the template's TM (A → B)
    await setTemplateTMsViaAPI(page, templateId, [
      { tm_id: tm2Id, source_language: "en", target_language: "da-DK", mode: "read_write", priority: 1 },
    ]);

    // Swap the template's MT router (1 → 2). Same engine, so the pipeline
    // outcome is identical — only the pointer moves, which is exactly what
    // C4 must show did NOT reach project A.
    router2Id = await createMTRouterViaAPI(page, {
      name: `J19 Router 2 ${RUN_ID}`,
      rules: [{ target_lang: "da-DK", mt_engine_id: mtEngineId }],
    });
    await updateTemplateViaAPI(page, templateId, { name: `J19 Primary MUTATED ${RUN_ID}`, mt_router_id: router2Id });

    // Verify template state changed
    const t = await getTemplate(page.request, headers, templateId);
    expect(t.template.fuzzy_threshold).toBeCloseTo(0.55, 5);
    expect(t.template.mt_on_upload).toBe(false);
    expect(t.tm_assignments.length).toBe(1);
    expect(t.tm_assignments[0].tm_id, "C3: template now points at TM B").toBe(tm2Id);
    expect(t.template.mt_router_id, "C3: template now points at router 2").toBe(router2Id);
    expect(t.template.mt_router_name).toBe(`J19 Router 2 ${RUN_ID}`);
  });

  test("C4: project A settings MUST be unchanged across EVERY mutation (copy-semantics invariant)", async ({ page }) => {
    await login(page);
    const projA = await getProjectSettings(page.request, await authHeader(page), projectAId);

    expect(projA.project.fuzzy_threshold,
      "C4 (CRITICAL): project A's fuzzy_threshold must STILL be 0.85 (its snapshot from C2). " +
      "If this is 0.55, create-with-template is no longer a copy — it became a reference.",
    ).toBeCloseTo(0.85, 5);

    expect(projA.project.mt_on_upload,
      "C4 (CRITICAL): project A's mt_on_upload must STILL be true. " +
      "Template flipped it to false — that must NOT propagate.",
    ).toBe(true);

    expect(projA.tm_assignments.length, "C4: project A still has exactly 1 TM assignment").toBe(1);
    expect(projA.tm_assignments[0].tm_id,
      "C4 (CRITICAL): project A's TM must STILL point at TM A (snapshot from C2). " +
      "If it now points at TM B, the assignments are referencing the template's profile " +
      "instead of being copied to the project's own profile.",
    ).toBe(tmId);

    expect(projA.project.mt_router_id,
      "C4 (CRITICAL): project A must STILL point at router 1. The template moved to router 2 — " +
      "the pointer is copied per profile, so that must NOT propagate.",
    ).toBe(router1Id);
  });

  test("C5: pipeline still works on project A (proves the snapshot is fully self-contained)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    // Upload a tiny file, pre-translate, verify mock MT runs end-to-end against project A's snapshot.
    const result = await uploadFileViaAPI(page, projectAId, "j19a.txt", "Hello world", "text/plain");
    projectAFileId = result.data?.id ?? result.id;

    const jobsRes = await page.request.get(`${API}/api/projects/${projectAId}/jobs`, { headers });
    const jobs = (await jobsRes.json()).data;
    expect(jobs.length, "C5: project A should have 1 job (en→da-DK)").toBe(1);
    projectAJobId = jobs[0].id;

    // Project A had pre_translate=true at apply time, but template now has it false.
    // Important: regardless of which value was in effect at upload, we explicitly
    // trigger pre-translate via per-job phase endpoints to remove that ambiguity.
    await pretranslateFileViaAPI(page, projectAId, projectAFileId);
    // synchronous pretranslate — no settle wait needed before the GET.

    const transRes = await page.request.get(`${API}/api/jobs/${projectAJobId}/translations`, { headers });
    const translations = (await transRes.json()).data as Array<{ active_text: string | null }>;
    expect(translations.length, "C5: 1 segment from 'Hello world'").toBeGreaterThanOrEqual(1);
    for (const t of translations) {
      expect(t.active_text,
        "C5: project A's pipeline must still produce mt_text through its own router, " +
        "even after the template was mutated. If null, the snapshot is incomplete and the " +
        "template mutation broke the project — copy semantics violated.",
      ).toBeTruthy();
      expect(t.active_text!.startsWith("[DA] "),
        `C5: mt_text must start with "[DA] " — proves base-code fallback resolved da-DK → da. ` +
        `Got: ${JSON.stringify(t.active_text!.slice(0, 30))}`,
      ).toBe(true);
    }
  });

  test("C6: creating fresh project B from the MUTATED template copies the NEW values (proves create re-reads template each call)", async ({ page }) => {
    await login(page);
    projectBId = await createProjectViaAPI(page, `J19 Project B ${RUN_ID}`, "en", ["da-DK"], templateId);

    const headers = await authHeader(page);
    const projB = await getProjectSettings(page.request, headers, projectBId);

    expect(projB.project.fuzzy_threshold,
      "C6: project B (fresh create-with-template) must get the CURRENT template fuzzy=0.55, not project A's stale 0.85. " +
      "Otherwise create-with-template is reading from a cache instead of the live template.",
    ).toBeCloseTo(0.55, 5);
    expect(projB.project.mt_on_upload, "C6: project B picks up pre_translate=false").toBe(false);
    expect(projB.tm_assignments[0].tm_id,
      "C6: project B picks up TM B (the post-mutation template state)",
    ).toBe(tm2Id);
    expect(projB.project.mt_router_id, "C6: project B picks up router 2 (the post-mutation pointer)").toBe(router2Id);
  });

  test("C7: multi-locale fan-out — template TM for ALL Nordic pairs; router 2 gets one rule per Nordic locale", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    // Replace the template's single-locale TM assignments with the Nordic trio.
    // (Template was mutated to TM B in C3; we now also fan it across 3 locales.)
    await setTemplateTMsViaAPI(page, templateId, NORDIC_PAIRS.map((p) => ({
      tm_id: tm2Id, source_language: "en", target_language: p.target,
      mode: "read_write", priority: 1,
    })));
    // MT fan-out lives on the router the template points at: one rule per
    // target language. Editing the router in place keeps the pointer stable.
    const putRes = await page.request.put(`${API}/api/mt-routers/${router2Id}`, {
      headers,
      data: {
        name: `J19 Router 2 ${RUN_ID}`, default: {},
        rules: NORDIC_PAIRS.map((p) => ({ target_lang: p.target, mt_engine_id: mtEngineId })),
      },
    });
    expect(putRes.ok(), `C7: PUT router 2 rules: ${putRes.status()} ${(await putRes.text()).slice(0, 200)}`).toBe(true);

    const t = await getTemplate(page.request, headers, templateId);
    // TM assignments are per-TM — the 3-locale fan-out collapses to ONE
    // TM row (the TM's own declared targets cover the locales).
    expect(t.tm_assignments.length, "C7: per-TM — one TM row regardless of locale fan-out").toBe(1);
    expect(t.template.mt_router_id, "C7: template still points at router 2 after the rule edit").toBe(router2Id);

    const r2 = await getMTRouterViaAPI(page, router2Id);
    expect(r2.rules.length, "C7: router 2 now carries 3 rules").toBe(3);
    // Each Nordic pair must be present (order-independent assertion — the API doesn't
    // promise insertion order, and assuming it would couple j19 to an SQL implementation detail).
    const targets = new Set(r2.rules.map((r) => r.target_lang));
    for (const p of NORDIC_PAIRS) {
      expect(targets.has(p.target.toLowerCase()),
        `C7: router 2 MUST have a rule for ${p.target} (stored normalized). Missing means the rule ` +
        `replace dropped a language — would silently lose locales on every project using the router.`,
      ).toBe(true);
      expect(r2.rules.find((r) => r.target_lang === p.target.toLowerCase())!.mt_engine_id).toBe(mtEngineId);
    }
  });

  test("C8: create a fresh project from the multi-locale template — it points at router 2, which covers all 3 locales", async ({ page }) => {
    await login(page);
    projectNordicId = await createProjectViaAPI(
      page, `J19 Project Nordic ${RUN_ID}`, "en",
      NORDIC_PAIRS.map((p) => p.target),
      templateId,
    );

    const headers = await authHeader(page);
    const projN = await getProjectSettings(page.request, headers, projectNordicId);

    // TM is per-TM — apply copies the single TM row (its declared
    // targets cover the locales).
    expect(projN.tm_assignments.length,
      "C8: create-with-template copies the one per-TM assignment onto the project.",
    ).toBe(1);
    expect(projN.project.mt_router_id, "C8: project Nordic shares router 2 with the template").toBe(router2Id);
    // The router the project resolves through must route every one of ITS languages.
    const r2 = await getMTRouterViaAPI(page, projN.project.mt_router_id!);
    const routed = new Set(r2.rules.map((r) => r.target_lang));
    for (const p of NORDIC_PAIRS) {
      expect(routed.has(p.target.toLowerCase()), `C8: project Nordic's router must route ${p.target}`).toBe(true);
    }

    // (Jobs are created per-file × target-language pair on upload, not on
    //  project creation. The "3 jobs" invariant is asserted in C9 after upload.)
  });

  test("C9: pre-translate ALL 3 Nordic jobs — each job gets its OWN locale prefix (resolver runs per-pair, not once)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    // Upload a single file. The pipeline should create translations under each job
    // (one per target language), and the resolver must produce a different prefix per job.
    const result = await uploadFileViaAPI(page, projectNordicId, "j19n.txt", "Hello Nordic", "text/plain");
    const fileId = result.data?.id ?? result.id;

    await pretranslateFileViaAPI(page, projectNordicId, fileId);
    // synchronous pretranslate — rows for all 3 Nordic jobs are committed on return.

    const jobsRes = await page.request.get(`${API}/api/projects/${projectNordicId}/jobs`, { headers });
    const jobs = (await jobsRes.json()).data as Array<{ id: number; target_language: string }>;

    // For each Nordic pair, locate its job and verify its translation has the right prefix.
    for (const p of NORDIC_PAIRS) {
      const job = jobs.find((j) => j.target_language.toLowerCase() === p.target.toLowerCase());
      expect(job, `C9: missing job for ${p.target}`).toBeDefined();

      const tRes = await page.request.get(`${API}/api/jobs/${job!.id}/translations`, { headers });
      const translations = (await tRes.json()).data as Array<{ active_text: string | null }>;
      expect(translations.length, `C9: job for ${p.target} must have at least 1 translation`).toBeGreaterThanOrEqual(1);

      for (const t of translations) {
        expect(t.active_text,
          `C9 (${p.target}): mt_text must NOT be null. Pipeline must run for every locale, ` +
          `not just the first one. Null here would mean the multi-locale loop short-circuited.`,
        ).toBeTruthy();
        expect(t.active_text!.startsWith(p.expectedPrefix),
          `C9 (${p.target}): mt_text MUST start with ${JSON.stringify(p.expectedPrefix)} ` +
          `(proves the router picked THIS job's rule, not the previous job's locale). ` +
          `Got: ${JSON.stringify(t.active_text!.slice(0, 40))}.`,
        ).toBe(true);
      }
    }
  });

  test("C10: a SECOND project from the same template gets the SAME pointer (no cross-project bleed)", async ({ page }) => {
    // Templates are create-time-only, so the copy invariant lives on the
    // create path: every project created from a template must get exactly
    // the template's current lists and pointer — never another project's.
    await login(page);
    const headers = await authHeader(page);

    // Template is in its Nordic-trio state (1 TM + router 2, set in C7).
    projectA2Id = await createProjectViaAPI(
      page, `J19 Project A2 ${RUN_ID}`, "en",
      NORDIC_PAIRS.map((p) => p.target),
      templateId,
    );
    const projA2 = await getProjectSettings(page.request, headers, projectA2Id);
    expect(projA2.tm_assignments.length, "C10: exact TM copy (template has 1)").toBe(1);
    expect(projA2.project.mt_router_id, "C10: A2 gets the template's CURRENT router (2)").toBe(router2Id);

    // Sibling projects created earlier from the same template are untouched.
    const projA = await getProjectSettings(page.request, headers, projectAId);
    expect(projA.project.mt_router_id,
      "C10: project A (created from the pre-mutation template state) still points at router 1",
    ).toBe(router1Id);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D — Lifecycle: delete-with-projects, template from current settings
  //
  // INVERSE of j17 D3/D4 / j18 D1: deleting a template with projects attached
  // MUST succeed, because projects own their own settings_profile post-apply.
  // ════════════════════════════════════════════════════════════════════════

  test("D1: delete template T1 with projects A AND B attached — MUST succeed (copy semantics)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await page.request.delete(`${API}/api/templates/${templateId}`, { headers });
    expect(res.status(),
      "D1 (inverse of j17/j18 patterns): deleting a template with attached projects must 204. " +
      "Projects do NOT reference the template post-apply — they own their own settings_profile copy. " +
      "If this returns 409, someone added a CountTemplateReferences check that violates copy semantics.",
    ).toBe(204);

    // Verify template is gone from the list
    const listRes = await page.request.get(`${API}/api/templates`, { headers });
    const list = (await listRes.json()).data as Array<{ id: number }>;
    expect(list.find((t) => t.id === templateId), "D1: deleted template must not appear in list").toBeUndefined();
    templateId = 0; // signal cleanup not needed

    // The template's router outlives the template — projects still use it.
    const r2 = await getMTRouterViaAPI(page, router2Id);
    expect(r2.rules.length, "D1: router 2 survives the template deletion untouched").toBe(3);
  });

  test("D2: project A is still fully functional after template deletion (no cascading damage)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    // Settings still readable + intact. Project A's fuzzy is its C2 creation
    // snapshot (0.85 — the template's pre-mutation value). The point of D2 is
    // "settings survive template deletion" — whatever they were BEFORE the
    // delete must STILL BE there AFTER the delete (no shared FK).
    const projA = await getProjectSettings(page.request, headers, projectAId);
    expect(projA.project.fuzzy_threshold,
      "D2: project A's fuzzy_threshold (= its create-time snapshot 0.85) must survive template deletion. " +
      "If the template were a reference, the FK would now point to a deleted row and either crash or null this out.",
    ).toBeCloseTo(0.85, 5);
    expect(projA.tm_assignments.length, "D2: TM assignment survives template deletion").toBeGreaterThanOrEqual(1);
    expect(projA.project.mt_router_id, "D2: MT router pointer survives template deletion").toBe(router1Id);
    expect(projA.project.mt_router_name, "D2: and still resolves to a name").toBeTruthy();

    // Pipeline still works — re-translate the file from C5
    await pretranslateFileViaAPI(page, projectAId, projectAFileId);
    // synchronous pretranslate — no settle wait needed before the GET.
    const transRes = await page.request.get(`${API}/api/jobs/${projectAJobId}/translations`, { headers });
    const translations = (await transRes.json()).data as Array<{ active_text: string | null }>;
    for (const t of translations) {
      expect(t.active_text!.startsWith("[DA] "),
        "D2: mock MT still runs through project A's router after template deletion",
      ).toBe(true);
    }
  });

  test("D3: a template saved with project A's CURRENT settings carries them all — including the router", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    // The New project page sends the form's settings + lists to POST
    // /api/templates; here the same payload is built from project A's
    // current settings (its C2 creation snapshot — fuzzy=0.85, TM A, router 1),
    // so the template reflects them regardless of the (now deleted) template
    // they originally came from.
    const sRes = await page.request.get(`${API}/api/projects/${projectAId}/settings`, { headers });
    expect(sRes.ok(), `D3: project settings: ${sRes.status()}`).toBe(true);
    const s = (await sRes.json()).data;
    const res = await page.request.post(`${API}/api/templates`, {
      headers, data: {
        name: `J19 SavedFromA ${RUN_ID}`,
        settings: {
          fuzzy_threshold: s.project.fuzzy_threshold, mt_on_upload: s.project.mt_on_upload, tm_on_upload: s.project.tm_on_upload,
          mt_router_id: s.project.mt_router_id,
        },
        tm_assignments: s.tm_assignments.map((a: any) => ({ tm_id: a.tm_id, mode: a.mode, priority: a.priority })),
      },
    });
    expect(res.ok(), `D3: create template from settings: ${res.status()}`).toBe(true);
    const body = await res.json();
    secondTemplateId = body.data.id;
    expect(secondTemplateId).toBeGreaterThan(0);

    const t2 = await getTemplate(page.request, headers, secondTemplateId);
    expect(t2.template.fuzzy_threshold,
      "D3: the template carries project A's CURRENT fuzzy (0.85 from its create-time snapshot)",
    ).toBeCloseTo(0.85, 5);
    expect(t2.tm_assignments.length, "D3: TM assignment carried into new template").toBeGreaterThanOrEqual(1);
    expect(t2.template.mt_router_id, "D3: MT router pointer carried").toBe(router1Id);
    // And that router still routes to the one engine created in C0.
    const r1 = await getMTRouterViaAPI(page, t2.template.mt_router_id!);
    expect(r1.rules[0].mt_engine_id).toBe(mtEngineId);
  });

  test("D3b: duplicating a template shares its router by reference; a foreign router id on create is rejected", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    const dupRes = await page.request.post(`${API}/api/templates/${secondTemplateId}/duplicate`, { headers });
    expect(dupRes.status(), `D3b: duplicate: ${dupRes.status()} ${(await dupRes.text()).slice(0, 200)}`).toBe(201);
    const dup = (await dupRes.json()).data as { id: number; mt_router_id: number | null; mt_router_name: string | null };
    thirdTemplateId = dup.id;
    expect(dup.mt_router_id, "D3b: the copy points at the same router").toBe(router1Id);
    expect(dup.mt_router_name).toBeTruthy();
    const t3 = await getTemplate(page.request, headers, thirdTemplateId);
    expect(t3.template.mt_router_id, "D3b: GET agrees with the duplicate response").toBe(router1Id);

    // PATCH parity with projects: unknown router → 400, and the pointer stays.
    const ghost = await page.request.patch(`${API}/api/templates/${thirdTemplateId}`, {
      headers, data: { name: `J19 Dup ${RUN_ID}`, mt_router_id: 999999999 },
    });
    expect(ghost.status(), "D3b: PATCH template mt_router_id to an unknown router must 400").toBe(400);
    expect((await getTemplate(page.request, headers, thirdTemplateId)).template.mt_router_id, "D3b: rejected PATCH leaves the pointer").toBe(router1Id);

    // null clears it.
    await updateTemplateViaAPI(page, thirdTemplateId, { name: `J19 Dup ${RUN_ID}`, mt_router_id: null });
    expect((await getTemplate(page.request, headers, thirdTemplateId)).template.mt_router_id, "D3b: null clears the template's router").toBeNull();
  });

  test("D4: a template with an empty name returns 400", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await page.request.post(`${API}/api/templates`, {
      headers, data: { name: "", settings: { fuzzy_threshold: 0.85 } },
    });
    expect(res.status(), "D4: empty name must 400").toBe(400);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase E — Cleanup
  // ════════════════════════════════════════════════════════════════════════

  async function cleanupAll(page: any) {
    const cleanup = async (fn: () => Promise<void>) => { try { await fn(); } catch {} };

    // Projects (own their own settings_profile copies; clean these first)
    if (projectAId) await cleanup(() => deleteProjectViaAPI(page, projectAId));
    if (projectA2Id) await cleanup(() => deleteProjectViaAPI(page, projectA2Id));
    if (projectBId) await cleanup(() => deleteProjectViaAPI(page, projectBId));
    if (projectNordicId) await cleanup(() => deleteProjectViaAPI(page, projectNordicId));

    // Templates (T1 already deleted in D1; T2 from save-as + T3 duplicate still here)
    if (templateId) await cleanup(() => deleteTemplateViaAPI(page, templateId));
    if (secondTemplateId) await cleanup(() => deleteTemplateViaAPI(page, secondTemplateId));
    if (thirdTemplateId) await cleanup(() => deleteTemplateViaAPI(page, thirdTemplateId));

    // Routers (nothing points at them once the profiles above are gone), then resources
    if (router1Id) await cleanup(() => deleteMTRouterViaAPI(page, router1Id!));
    if (router2Id) await cleanup(() => deleteMTRouterViaAPI(page, router2Id));
    if (tmId) await cleanup(() => deleteTMViaAPI(page, tmId));
    if (tm2Id) await cleanup(() => deleteTMViaAPI(page, tm2Id));
    if (mtEngineId) await cleanup(() => deleteMTEngineViaAPI(page, mtEngineId));
  }

  test("E1: cleanup all resources", async ({ page }) => {
    await login(page);
    await cleanupAll(page);
  });

  test.afterAll(async ({ browser }) => {
    // Belt-and-suspenders: try to clean up even if E1 was skipped via failure.
    const page = await browser.newPage();
    try {
      await login(page);
      await cleanupAll(page);
    } catch {} finally {
      await page.close();
    }
  });
});
