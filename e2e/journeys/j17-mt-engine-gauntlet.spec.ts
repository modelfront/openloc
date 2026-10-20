/**
 * J17 — MT Engine + MT Router Gauntlet (Adversarial)
 *
 * Goal: lock down MT engine CRUD validation, MT router validation, the
 * base-code locale-resolution path end-to-end through the pipeline, and the
 * cross-surface lifecycle (router swap, no-route skip, no-router skip,
 * delete guards on both engines and routers). Mirrors the j18 phase
 * structure (A setup / B per-op variants / C lifecycle / D entity lifecycle
 * / E cleanup) so future CRUD gauntlets have a stable template to follow.
 *
 * Model under test: a project's settings profile points at ONE MT router
 * (`mt_router_id`). A router maps target languages to engines (rules) and
 * may name a default engine for every other language. The pipeline resolves
 * router → engine by the job's target language: exact normalized match, then
 * same base language, then the default. Source languages play no part.
 *
 * Locale: sv-SE (Swedish, Sweden) — never used by any other journey.
 *
 * Why sv-SE specifically: the mock engine's language list (internal/mt/
 * languages.go::mockLanguages) supports `sv` but NOT `sv-SE` natively.
 * That's exactly the base-code-fallback path, so the journey can
 * assert end-to-end that the resolver swapped sv-SE → sv before calling
 * the mock — by inspecting the prefix the mock writes onto each segment
 * (`[SV] ` after fallback vs `[SV-SE] ` if no fallback). This is the kind
 * of unconditional assertion the "don't beat the purpose" rule demands.
 *
 * Coordination with existing journeys:
 *   - j1: full-lifecycle es-mx (mock supports es-mx natively → no fallback).
 *         j17 inverts that — picks a locale where fallback IS triggered.
 *   - j3: multi-language pt-br + zh-hans + es-mx, all native to mock. j17
 *         does NOT cover multi-job per-language, only single-job sv-SE.
 *   - j9: linguists get 403 on /api/mt-routers (RBAC lives there).
 *   - j19: templates share routers by reference; j17 stays on projects.
 *
 * Honest scope notes:
 *   - We do NOT exercise libretranslate / deepl / google live API calls —
 *     those need external services. A1 pins only the create contract: mock
 *     and libretranslate create without a key, deepl and google answer 400
 *     without one. C1 reads the language list of the mock engine only.
 *   - Routers do NOT validate that an engine supports a rule's language —
 *     that is resolved at run time. So "unsupported pair → 400"
 *     no longer exists as an API contract; the pipeline-level consequences
 *     (no route → skip with an actionable 400) are what C7/C8 pin instead.
 *   - AI-engine routes (ai_engine_id) need a connected provider; j17 pins
 *     only the "not both ids" validation for them.
 *   - The disabled CRUD round-trip is covered in Phase B; "disabled engine
 *     on a live pipeline run" is pinned by Go integration tests, not here.
 */
import { test, expect, type APIRequestContext } from "@playwright/test";
import {
  login,
  uploadFileViaAPI,
  pretranslateFileViaAPI,
  createProjectViaAPI,
  deleteProjectViaAPI,
  createMTEngineViaAPI,
  deleteMTEngineViaAPI,
  deleteMTRouterViaAPI,
  updateProjectSettingsViaAPI,
  type MTRouter,
} from "./helpers";

const API = "";
const RUN_ID = Date.now();

// State threaded through the serial describe
let mtEngineId: number; // primary engine, mock, used by Phase C/D
let secondEngineId: number; // for the swap test in D1
let routerId: number; // the project's MT router (created in C2)
let routerName: string;
let projectId: number;
let fileId: number;
let jobId: number;
const createdEngineIds: number[] = []; // belt-and-suspenders cleanup
const createdRouterIds: number[] = [];
// Second tenant for the cross-tenant probes in C3 (dev-login provisions it).
let tenantBToken: string;
let tenantBEngineId: number;
let tenantBRouterId: number;

async function authHeader(page: any): Promise<Record<string, string>> {
  const token = await page.evaluate(() => localStorage.getItem("token"));
  return { Authorization: `Bearer ${token}` };
}

async function postEngine(req: APIRequestContext, headers: Record<string, string>, body: any) {
  return req.post(`${API}/api/mt-engines`, { headers, data: body });
}

async function patchEngine(req: APIRequestContext, headers: Record<string, string>, id: number, body: any) {
  return req.patch(`${API}/api/mt-engines/${id}`, { headers, data: body });
}

async function getEngine(req: APIRequestContext, headers: Record<string, string>, id: number) {
  return req.get(`${API}/api/mt-engines/${id}`); // engines are listed; per-id GET may not exist
}

async function listEngines(req: APIRequestContext, headers: Record<string, string>) {
  return (await (await req.get(`${API}/api/mt-engines`, { headers })).json()).data as Array<{
    id: number;
    name: string;
    engine_type: string;
    enabled: boolean;
  }>;
}

test.describe.serial("J17 — MT Engine CRUD Gauntlet", () => {
  // ════════════════════════════════════════════════════════════════════════
  // Phase A — Create-side validation gauntlet
  // ════════════════════════════════════════════════════════════════════════

  test("A1: create accepts the keyless types and requires a key for the provider ones", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    // The deployment types authenticate from the environment, so they create
    // with nothing else supplied.
    for (const t of ["mock", "libretranslate"] as const) {
      const res = await postEngine(page.request, headers, {
        name: `J17 probe ${t} ${RUN_ID}`,
        engine_type: t,
      });
      expect(res.status(),
        `A1: engine_type=${t} must be accepted by the API whitelist (mtengine.ValidType)`,
      ).toBeLessThan(300);
      const body = await res.json();
      createdEngineIds.push(body.data.id);
    }
    // DeepL and Google spend a key from AI settings, so a
    // create without one is a 400 rather than an engine that cannot translate.
    for (const t of ["deepl", "google"] as const) {
      const res = await postEngine(page.request, headers, {
        name: `J17 probe ${t} ${RUN_ID}`,
        engine_type: t,
      });
      expect(res.status(),
        `A1: engine_type=${t} must require a key — an engine with no credential fails at translate time instead`,
      ).toBe(400);
    }
  });

  test("A2: create with bogus engine_type returns 400", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await postEngine(page.request, headers, {
      name: `J17 bogus ${RUN_ID}`,
      engine_type: "totally_made_up_engine",
    });
    expect(res.status(),
      "A2: Create whitelist must reject unknown engine_type with 400",
    ).toBe(400);
  });

  test("A3: create with empty name returns 400", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await postEngine(page.request, headers, { name: "", engine_type: "mock" });
    expect(res.status(), "A3: empty name must 400").toBe(400);
  });

  test("A4: create with whitespace-only name returns 400 (handler trims before checking empty)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await postEngine(page.request, headers, { name: "   \t\n  ", engine_type: "mock" });
    expect(res.status(),
      "A4: whitespace-only name must 400. handler.go::HandleCreate trims then checks empty.",
    ).toBe(400);
  });

  test("A5: create with empty engine_type returns 400", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await postEngine(page.request, headers, { name: `J17 noType ${RUN_ID}`, engine_type: "" });
    expect(res.status(), "A5: empty engine_type must 400").toBe(400);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B — Update-side validation parity
  //
  // PATCH must enforce the SAME invariants as POST. Asymmetric validation is
  // a classic security/data-integrity hole — Create gates an invariant,
  // Update silently bypasses it.
  // ════════════════════════════════════════════════════════════════════════

  test("B0: setup — create the primary mock engine that Phases B/C/D will mutate", async ({ page }) => {
    await login(page);
    mtEngineId = await createMTEngineViaAPI(page, `J17 Primary Mock ${RUN_ID}`, "mock");
    createdEngineIds.push(mtEngineId);
    expect(mtEngineId).toBeGreaterThan(0);
  });

  test("B1: PATCH with bogus engine_type must 400 (validation parity with Create)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    // Send the full body with the original valid name + bogus type.
    const res = await patchEngine(page.request, headers, mtEngineId, {
      name: `J17 Primary Mock ${RUN_ID}`,
      engine_type: "totally_bogus_engine_after_update",
      enabled: true,
    });
    expect(res.status(),
      "B1: HandleUpdate must reject unknown engine_type just like HandleCreate.",
    ).toBe(400);

    // Defensive: even if status was wrong, ensure the engine still has a valid type.
    const list = await listEngines(page.request, headers);
    const engine = list.find((e) => e.id === mtEngineId);
    expect(engine, "B1: primary engine still in list").toBeDefined();
    expect(["mock", "libretranslate", "deepl", "google"]).toContain(engine!.engine_type);
  });

  test("B2: PATCH with empty name must 400 (validation parity with Create)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    const res = await patchEngine(page.request, headers, mtEngineId, {
      name: "",
      engine_type: "mock",
      enabled: true,
    });
    expect(res.status(),
      "B2: HandleUpdate must reject an empty name, like HandleCreate's trim+empty guard.",
    ).toBe(400);

    // Defensive: engine name was not wiped
    const list = await listEngines(page.request, headers);
    const engine = list.find((e) => e.id === mtEngineId);
    expect(engine!.name.length,
      "B2: primary engine name must NOT have been wiped to empty string by the rejected PATCH",
    ).toBeGreaterThan(0);
  });

  test("B3: partial PATCH that omits `enabled` must NOT silently disable an enabled engine", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    // Step 1: ensure engine is enabled (full PATCH)
    const enableRes = await patchEngine(page.request, headers, mtEngineId, {
      name: `J17 Primary Mock ${RUN_ID}`,
      engine_type: "mock",
      enabled: true,
    });
    expect(enableRes.ok(), "B3 setup: enable PATCH must succeed").toBe(true);

    // Step 2: send a partial PATCH that ONLY changes the name — no `enabled` field.
    // This is what a UI form sending "rename" without re-sending the whole record looks like.
    const renamedName = `J17 Primary Mock RENAMED ${RUN_ID}`;
    const partialRes = await patchEngine(page.request, headers, mtEngineId, {
      name: renamedName,
      engine_type: "mock",
      // enabled intentionally omitted
    });
    expect(partialRes.ok(), `B3: partial PATCH should succeed: ${partialRes.status()}`).toBe(true);

    // Step 3: verify enabled=true survived the partial PATCH
    const list = await listEngines(page.request, headers);
    const engine = list.find((e) => e.id === mtEngineId);
    expect(engine, "B3: engine must still exist").toBeDefined();
    expect(engine!.name, "B3: rename did persist (positive control)").toBe(renamedName);
    expect(engine!.enabled,
      "B3: partial PATCH omitting `enabled` must keep the current value " +
      "(absent field = 'keep current', the qa_profile_id tri-state pattern).",
    ).toBe(true);
  });

  test("B4: well-formed PATCH that toggles enabled false→true→false round-trips correctly (positive control)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const baseName = `J17 Primary Mock RENAMED ${RUN_ID}`;

    // Disable
    let res = await patchEngine(page.request, headers, mtEngineId, {
      name: baseName, engine_type: "mock", enabled: false,
    });
    expect(res.ok()).toBe(true);
    let list = await listEngines(page.request, headers);
    expect(list.find((e) => e.id === mtEngineId)!.enabled, "B4: disable round-trip").toBe(false);

    // Re-enable
    res = await patchEngine(page.request, headers, mtEngineId, {
      name: baseName, engine_type: "mock", enabled: true,
    });
    expect(res.ok()).toBe(true);
    list = await listEngines(page.request, headers);
    expect(list.find((e) => e.id === mtEngineId)!.enabled, "B4: re-enable round-trip").toBe(true);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C — Router validation + base-code locale resolution through the pipeline
  //
  // This is the heart of j17. The mock engine supports `sv` but NOT `sv-SE`.
  // The router rule is for sv-SE; the resolver should fall back base-code,
  // the mock should receive `sv`, and the `[SV] ` prefix on translated
  // segments proves it. If anyone ever adds `sv-SE` to mockLanguages, C1
  // catches it.
  // ════════════════════════════════════════════════════════════════════════

  async function getProjectSettings(req: APIRequestContext, headers: Record<string, string>) {
    const res = await req.get(`${API}/api/projects/${projectId}/settings`, { headers });
    expect(res.ok(), `get project settings: ${res.status()}`).toBe(true);
    return (await res.json()).data as { project: { mt_router_id: number | null; mt_router_name: string | null } };
  }

  async function postRouter(req: APIRequestContext, headers: Record<string, string>, body: any) {
    return req.post(`${API}/api/mt-routers`, { headers, data: body });
  }

  async function putRouter(req: APIRequestContext, headers: Record<string, string>, id: number, body: any) {
    const res = await req.put(`${API}/api/mt-routers/${id}`, { headers, data: body });
    expect(res.ok(), `PUT router ${id}: ${res.status()} ${(await res.text()).slice(0, 200)}`).toBe(true);
    return (await res.json()).data as MTRouter;
  }

  async function listRouters(req: APIRequestContext, headers: Record<string, string>) {
    const res = await req.get(`${API}/api/mt-routers`, { headers });
    expect(res.ok(), `list routers: ${res.status()}`).toBe(true);
    return (await res.json()).data as MTRouter[];
  }

  /** POST mt-apply raw, so the skip paths' 400 body is visible (the helper swallows it). */
  async function mtApply(req: APIRequestContext, headers: Record<string, string>) {
    const res = await req.post(`${API}/api/jobs/${jobId}/mt-apply`, { headers });
    const body = await res.json().catch(() => ({}));
    return { status: res.status(), message: (body?.error?.message ?? body?.message ?? "") as string };
  }

  async function assertPrefix(req: APIRequestContext, headers: Record<string, string>, prefix: string, why: string) {
    const transRes = await req.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const translations = (await transRes.json()).data as Array<{ active_text: string | null; segment_id: number }>;
    expect(translations.length, "job must have segments").toBeGreaterThan(0);
    for (const t of translations) {
      expect(t.active_text?.startsWith(prefix),
        `segment ${t.segment_id}: expected prefix ${JSON.stringify(prefix)}, got ${JSON.stringify(t.active_text?.slice(0, 30))}. ${why}`,
      ).toBe(true);
    }
  }

  test("C1: HandleLanguages confirms mock supports `sv` but NOT `sv-SE` (regression guard for the base-code fallback test)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await page.request.get(`${API}/api/mt-engines/${mtEngineId}/languages`, { headers });
    expect(res.ok(), `C1: get languages: ${res.status()}`).toBe(true);
    const cat = (await res.json()).data as { sources: { code: string }[]; targets: { code: string }[] };
    const allTargets = new Set(cat.targets.map(t => t.code));

    expect(allTargets.has("sv"),
      "C1: mock MUST advertise `sv` as a target — it's the fallback code j17 relies on.",
    ).toBe(true);
    expect(allTargets.has("sv-SE"),
      "C1: mock MUST NOT natively advertise `sv-SE`. If this test fails, someone added sv-SE to " +
      "internal/mt/languages.go::mockLanguages — j17's locale-resolution test (C5) is no longer testing fallback. " +
      "Either remove sv-SE from mock OR pick a different unsupported locale for j17.",
    ).toBe(false);
  });

  test("C2: create router (rule sv-SE → mock), point the project at it, settings + history echo it", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    routerName = `J17 Router ${RUN_ID}`;

    const res = await postRouter(page.request, headers, {
      name: routerName,
      default: {},
      rules: [{ target_lang: "sv-SE", mt_engine_id: mtEngineId }],
    });
    expect(res.status(), `C2: create router: ${res.status()} ${(await res.text()).slice(0, 200)}`).toBe(201);
    const router = (await res.json()).data as MTRouter;
    routerId = router.id;
    createdRouterIds.push(routerId);
    expect(router.default_mt_engine_id, "C2: no default engine was asked for").toBeNull();
    expect(router.rules.length).toBe(1);
    expect(router.rules[0].target_lang,
      "C2: rule language is stored normalized (langcode.Normalize lowercases)",
    ).toBe("sv-se");
    expect(router.rules[0].mt_engine_id).toBe(mtEngineId);

    projectId = await createProjectViaAPI(page, `J17 Project ${RUN_ID}`, "en", ["sv-SE"]);
    await updateProjectSettingsViaAPI(page, projectId, { mt_on_upload: false });
    const before = await getProjectSettings(page.request, headers);
    expect(before.project.mt_router_id, "C2: a fresh project has no MT router").toBeNull();

    await updateProjectSettingsViaAPI(page, projectId, { mt_router_id: routerId });
    const after = await getProjectSettings(page.request, headers);
    expect(after.project.mt_router_id, "C2: settings echo the router id").toBe(routerId);
    expect(after.project.mt_router_name, "C2: settings resolve the router name").toBe(routerName);

    // Project history records the change by NAME, not just id.
    const evRes = await page.request.get(`${API}/api/projects/${projectId}/events`, { headers });
    expect(evRes.ok(), `C2: list events: ${evRes.status()}`).toBe(true);
    const events = (await evRes.json()).data as Array<{ event_type: string; details: any }>;
    const settingsEvents = events.filter((e) => e.event_type === "project.settings_updated");
    expect(settingsEvents.length, "C2: pointing the project at a router is a settings_updated event").toBeGreaterThan(0);
    const named = settingsEvents.some((e) => JSON.stringify(e.details).includes(routerName));
    expect(named, `C2: a settings_updated event must carry mt_router_name=${JSON.stringify(routerName)}`).toBe(true);
  });

  test("C3: router validation gauntlet — every malformed router is rejected and nothing is written", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const reject = async (label: string, body: any, status = 400, msgRe?: RegExp) => {
      const res = await postRouter(page.request, headers, body);
      const text = await res.text();
      expect(res.status(), `C3 ${label}: expected ${status}, got ${res.status()} ${text.slice(0, 200)}`).toBe(status);
      if (msgRe) expect(text, `C3 ${label}: message`).toMatch(msgRe);
    };

    await reject("empty name", { name: "   ", default: { mt_engine_id: mtEngineId }, rules: [] }, 400, /name is required/i);
    await reject("no route at all", { name: `J17 empty ${RUN_ID}`, default: {}, rules: [] }, 400, /at least one route/i);
    await reject("rule without language", { name: `J17 nolang ${RUN_ID}`, default: {}, rules: [{ target_lang: "", mt_engine_id: mtEngineId }] }, 400, /target language is required/i);
    await reject("wildcard language", { name: `J17 wild ${RUN_ID}`, default: {}, rules: [{ target_lang: "*", mt_engine_id: mtEngineId }] }, 400, /target language is required/i);
    await reject("same language twice (differs only by case)", {
      name: `J17 dup ${RUN_ID}`, default: {},
      rules: [{ target_lang: "sv-SE", mt_engine_id: mtEngineId }, { target_lang: "SV-se", mt_engine_id: mtEngineId }],
    }, 400, /listed twice/i);
    await reject("both engine kinds on one rule", {
      name: `J17 both ${RUN_ID}`, default: {}, rules: [{ target_lang: "sv", mt_engine_id: mtEngineId, ai_engine_id: 1 }],
    }, 400, /not both/i);
    await reject("unknown engine id", { name: `J17 ghost ${RUN_ID}`, default: {}, rules: [{ target_lang: "sv", mt_engine_id: 999999999 }] }, 400, /not found/i);
    await reject("unknown default engine id", { name: `J17 ghostdef ${RUN_ID}`, default: { mt_engine_id: 999999999 }, rules: [] }, 400, /default engine.*not found/i);
    await reject("duplicate router name (case-insensitive)", {
      name: routerName.toUpperCase(), default: {}, rules: [{ target_lang: "sv", mt_engine_id: mtEngineId }],
    }, 409, /already exists/i);

    // Cross-tenant: an engine that exists but belongs to another org.
    const provision = await page.request.post(`${API}/api/auth/dev-login`, {
      data: { email: `j17-tenant-b-${RUN_ID}@test.com`, tenant_name: `J17 Tenant B ${RUN_ID}` },
    });
    expect(provision.ok(), `C3: provision tenant B: ${provision.status()}`).toBe(true);
    tenantBToken = (await provision.json()).token;
    const bHeaders = { Authorization: `Bearer ${tenantBToken}` };
    const bEngine = await page.request.post(`${API}/api/mt-engines`, { headers: bHeaders, data: { name: `J17 B engine ${RUN_ID}`, engine_type: "mock" } });
    expect(bEngine.ok(), `C3: tenant B engine: ${bEngine.status()}`).toBe(true);
    tenantBEngineId = (await bEngine.json()).data.id;
    await reject("engine from another tenant in a rule", {
      name: `J17 foreign ${RUN_ID}`, default: {}, rules: [{ target_lang: "sv", mt_engine_id: tenantBEngineId }],
    }, 400, /not found/i);
    await reject("engine from another tenant as default", {
      name: `J17 foreigndef ${RUN_ID}`, default: { mt_engine_id: tenantBEngineId }, rules: [],
    }, 400, /not found/i);

    // Tenant B's own router is fine for B — but A cannot point a project at it.
    const bRouter = await page.request.post(`${API}/api/mt-routers`, {
      headers: bHeaders, data: { name: `J17 B router ${RUN_ID}`, default: { mt_engine_id: tenantBEngineId }, rules: [] },
    });
    expect(bRouter.status(), `C3: tenant B router: ${bRouter.status()}`).toBe(201);
    tenantBRouterId = (await bRouter.json()).data.id;
    const foreignPatch = await page.request.patch(`${API}/api/projects/${projectId}/settings`, { headers, data: { mt_router_id: tenantBRouterId } });
    expect(foreignPatch.status(), "C3: PATCH mt_router_id to another tenant's router must 400").toBe(400);
    const ghostPatch = await page.request.patch(`${API}/api/projects/${projectId}/settings`, { headers, data: { mt_router_id: 999999999 } });
    expect(ghostPatch.status(), "C3: PATCH mt_router_id to an unknown router must 400").toBe(400);
    const badTypePatch = await page.request.patch(`${API}/api/projects/${projectId}/settings`, { headers, data: { mt_router_id: "not-a-number" } });
    expect(badTypePatch.status(), "C3: PATCH mt_router_id with a non-numeric value must 400").toBe(400);
    // Tenant A cannot read, edit or delete B's router either.
    expect((await page.request.get(`${API}/api/mt-routers/${tenantBRouterId}`, { headers })).status(), "C3: GET foreign router → 404").toBe(404);
    expect((await page.request.delete(`${API}/api/mt-routers/${tenantBRouterId}`, { headers })).status(), "C3: DELETE foreign router → 404").toBe(404);

    // Nothing leaked: the project still points at the C2 router, and the only
    // router carrying this run's id is the C2 one.
    const settings = await getProjectSettings(page.request, headers);
    expect(settings.project.mt_router_id, "C3: rejected PATCHes must not move the project's router").toBe(routerId);
    const mine = (await listRouters(page.request, headers)).filter((r) => r.name.includes(String(RUN_ID)));
    expect(mine.map((r) => r.id), "C3: every rejected POST must leave no router behind").toEqual([routerId]);
    const theirs = await page.request.get(`${API}/api/mt-routers`, { headers: bHeaders });
    expect(((await theirs.json()).data as MTRouter[]).map((r) => r.id),
      "C3: tenant B's list must not contain tenant A's router",
    ).not.toContain(routerId);
  });

  test("C4: upload file with adversarial content for sv-SE", async ({ page }) => {
    await login(page);
    const content = [
      "Hello world",
      "Welcome back",
      "Click here",
    ].join("\n");
    const result = await uploadFileViaAPI(page, projectId, "j17.txt", content, "text/plain");
    fileId = result.data?.id ?? result.id;

    const headers = await authHeader(page);
    const jobsRes = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    const jobs = (await jobsRes.json()).data;
    expect(jobs.length, "C4: should have 1 job (en→sv-SE)").toBe(1);
    jobId = jobs[0].id;
  });

  test("C5: pre-translate → mock prefix is `[SV] ` not `[SV-SE] ` (proves base-code fallback fired in pipeline)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    await pretranslateFileViaAPI(page, projectId, fileId);
    // No buffer needed: mt-apply runs TranslateFile synchronously and returns
    // 200 only after the DB writes commit (internal/job/handler_pipeline.go).

    // THE adversarial assertion: mock prefixes [TARGET_LANG_UPPER]. After base-code
    // fallback resolves sv-SE → sv, mock receives "sv" → prefix is "[SV] ". If resolution
    // failed and the raw "sv-SE" reached mock, the prefix would be "[SV-SE] ".
    await assertPrefix(page.request, headers, "[SV] ",
      'If prefix is "[SV-SE] ", the pipeline did NOT call ResolveLocale before Translate — see ' +
      "internal/pipeline/translate.go and internal/mt/locale.go::ResolveLocale.");
  });

  test("C6: router rule for base `sv` still routes an sv-SE job (same-base-language match)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const updated = await putRouter(page.request, headers, routerId, {
      name: routerName, default: {}, rules: [{ target_lang: "sv", mt_engine_id: mtEngineId }],
    });
    expect(updated.rules.map((r) => r.target_lang), "C6: rules replaced wholesale").toEqual(["sv"]);

    const apply = await mtApply(page.request, headers);
    expect(apply.status, `C6: mt-apply via a base-language rule must run: ${apply.status} ${apply.message}`).toBe(200);
    await assertPrefix(page.request, headers, "[SV] ",
      "C6: an sv-SE job must match the router's `sv` rule (langcode.BestMatch base fallback).");
  });

  test("C7: router with a rule for another language and no default → MT skipped with an actionable 400", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    await putRouter(page.request, headers, routerId, {
      name: routerName, default: {}, rules: [{ target_lang: "de", mt_engine_id: mtEngineId }],
    });

    const apply = await mtApply(page.request, headers);
    expect(apply.status,
      "C7: a router with no route for sv-SE must make mt-apply a 400 (the pipeline skipped with no_route), " +
      `not silently 200. Got ${apply.status} ${apply.message}`,
    ).toBe(400);
    expect(apply.message, "C7: the message must name the router and the fix").toMatch(/MT router has no engine for sv-se.*route.*default engine/i);

    // A default engine rescues every other language.
    await putRouter(page.request, headers, routerId, {
      name: routerName, default: { mt_engine_id: mtEngineId }, rules: [{ target_lang: "de", mt_engine_id: mtEngineId }],
    });
    const rescued = await mtApply(page.request, headers);
    expect(rescued.status, `C7: with a default engine the same job must translate: ${rescued.status} ${rescued.message}`).toBe(200);
    await assertPrefix(page.request, headers, "[SV] ", "C7: the default engine still goes through locale resolution.");
  });

  test("C8: project with no router (mt_router_id: null) → MT skipped; settings show no router", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    await updateProjectSettingsViaAPI(page, projectId, { mt_router_id: null });
    const cleared = await getProjectSettings(page.request, headers);
    expect(cleared.project.mt_router_id, "C8: null clears the router").toBeNull();
    expect(cleared.project.mt_router_name, "C8: no name without a router").toBeNull();

    const apply = await mtApply(page.request, headers);
    expect(apply.status, `C8: no router → mt-apply 400 (skip reason no_assignment). Got ${apply.status} ${apply.message}`).toBe(400);
    expect(apply.message).toMatch(/no mt engine configured/i);

    // Absent field = keep current (null stays null), then restore the router.
    await updateProjectSettingsViaAPI(page, projectId, { mt_on_upload: false });
    expect((await getProjectSettings(page.request, headers)).project.mt_router_id, "C8: a PATCH without mt_router_id keeps it").toBeNull();
    await updateProjectSettingsViaAPI(page, projectId, { mt_router_id: routerId });
    expect((await getProjectSettings(page.request, headers)).project.mt_router_id).toBe(routerId);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D — Cross-surface lifecycle: swap, delete-with-refs on engine + router
  // ════════════════════════════════════════════════════════════════════════

  test("D1: create a SECOND mock engine, re-route sv-SE to it, re-pre-translate still works", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    secondEngineId = await createMTEngineViaAPI(page, `J17 Secondary Mock ${RUN_ID}`, "mock");
    createdEngineIds.push(secondEngineId);

    // Editing the router is how a manager swaps engines — the project keeps
    // pointing at the same router and picks the change up on the next run.
    const updated = await putRouter(page.request, headers, routerId, {
      name: routerName, default: {}, rules: [{ target_lang: "sv-SE", mt_engine_id: secondEngineId }],
    });
    expect(updated.rules[0].mt_engine_id).toBe(secondEngineId);
    expect((await getProjectSettings(page.request, headers)).project.mt_router_id, "D1: project untouched by the router edit").toBe(routerId);

    const apply = await mtApply(page.request, headers);
    expect(apply.status, `D1: mt-apply after swap: ${apply.status} ${apply.message}`).toBe(200);
    await assertPrefix(page.request, headers, "[SV] ",
      "D1: prefix must still be [SV] after engine swap — locale resolution must run for the NEW engine too.");
  });

  test("D2: deleting the FIRST engine (no live router routes to it after D1) succeeds with 204", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await page.request.delete(`${API}/api/mt-engines/${mtEngineId}`, { headers });
    expect(res.status(),
      "D2: unreferenced engine deletes cleanly (204). The D1 swap left this engine out of every live router.",
    ).toBe(204);

    const list = await listEngines(page.request, headers);
    expect(list.find((e) => e.id === mtEngineId), "D2: deleted engine must not appear in the list").toBeUndefined();
    mtEngineId = 0;
  });

  test("D3: deleting the SECOND engine (a live router routes to it) must return 409", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await page.request.delete(`${API}/api/mt-engines/${secondEngineId}`, { headers });
    expect(res.status(), "D3: deleting an engine a live router uses must 409 — silently dropping the route is the bug").toBe(409);
    expect(await res.text(), "D3: the message names the router count").toMatch(/1 MT router\(s\) still use/i);

    // Sanity: engine still listed AND the router's rule still points at it (no silent partial damage)
    const list = await listEngines(page.request, headers);
    expect(list.find((e) => e.id === secondEngineId), "D3: 409 must NOT delete the engine").toBeDefined();
    const routerRes = await page.request.get(`${API}/api/mt-routers/${routerId}`, { headers });
    const router = (await routerRes.json()).data as MTRouter;
    expect(router.rules[0]?.mt_engine_id, "D3: 409 must NOT touch the router's rule").toBe(secondEngineId);
  });

  test("D4: deleting the router while the project points at it must return 409", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await page.request.delete(`${API}/api/mt-routers/${routerId}`, { headers });
    expect(res.status(), "D4: a router a project uses must not delete").toBe(409);
    expect(await res.text()).toMatch(/1 project\(s\)\/template\(s\) still use/i);

    expect((await listRouters(page.request, headers)).find((r) => r.id === routerId), "D4: router still listed").toBeDefined();
    expect((await getProjectSettings(page.request, headers)).project.mt_router_id, "D4: project still points at it").toBe(routerId);
  });

  test("D5: unpoint the project → router deletes (204, soft) → engine deletes (204)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    await updateProjectSettingsViaAPI(page, projectId, { mt_router_id: null });

    const delRouter = await page.request.delete(`${API}/api/mt-routers/${routerId}`, { headers });
    expect(delRouter.status(), "D5: unreferenced router deletes (proves D4's 409 was a precondition, not a permanent block)").toBe(204);
    expect((await listRouters(page.request, headers)).find((r) => r.id === routerId), "D5: deleted router leaves the list").toBeUndefined();
    expect((await page.request.get(`${API}/api/mt-routers/${routerId}`, { headers })).status(), "D5: GET on a deleted router → 404").toBe(404);
    // A retired router's id is not re-usable as a profile target.
    const stale = await page.request.patch(`${API}/api/projects/${projectId}/settings`, { headers, data: { mt_router_id: routerId } });
    expect(stale.status(), "D5: pointing a project at a deleted router must 400").toBe(400);
    routerId = 0;

    const delEngine = await page.request.delete(`${API}/api/mt-engines/${secondEngineId}`, { headers });
    expect(delEngine.status(), "D5: once no live router routes to it, the engine deletes").toBe(204);
    secondEngineId = 0;
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase E — Cleanup
  // ════════════════════════════════════════════════════════════════════════

  async function cleanupAll(page: any) {
    const cleanup = async (fn: () => Promise<void>) => { try { await fn(); } catch {} };
    if (projectId) await cleanup(() => deleteProjectViaAPI(page, projectId));
    for (const id of createdRouterIds) await cleanup(() => deleteMTRouterViaAPI(page, id));
    if (mtEngineId) await cleanup(() => deleteMTEngineViaAPI(page, mtEngineId));
    if (secondEngineId) await cleanup(() => deleteMTEngineViaAPI(page, secondEngineId));
    // Probe engines from A1 (libretranslate/deepl/google/mock)
    for (const id of createdEngineIds) await cleanup(() => deleteMTEngineViaAPI(page, id));
    // Tenant B (C3): its own owner removes it; the cascade takes engine + router.
    if (tenantBToken) {
      await cleanup(async () => {
        await page.request.delete(`${API}/api/tenants/current`, { headers: { Authorization: `Bearer ${tenantBToken}` } });
      });
    }
  }

  test("E1: cleanup project, routers, engines, tenant B", async ({ page }) => {
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
