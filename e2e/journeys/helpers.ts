/**
 * Journey test helpers — API-first resource creation + cleanup.
 *
 * Re-exports everything from the page-test helpers so journey specs can
 * `import { login, createTMViaAPI, ... } from './helpers'` in one line.
 *
 * Setup resources are created via direct API calls (faster + no cmdk popover
 * battles). The actual exercise steps use real UI interactions.
 */
import { Page, expect } from "@playwright/test";

// Re-export all page-test helpers
export {
  login,
  authHeaders,
  inviteMember,
  sessionAs,
  segmentEditor,
  fillEditorExactly,
  waitForPipeline,
  uploadFileViaAPI,
  pretranslateFileViaAPI,
  selectLanguage,
  selectTargetLanguages,
  pickComboboxOption,
  pickComboboxValue,
  clickComboboxAction,
  startFromScratch,
  startFromTemplate,
  chooseTMFromScratch,
  addAssignment,
  getProjectSettingsViaAPI,
  deleteFileViaAPI,
  openBulkPretranslate,
  openJob,
  selectFirstTask,
  submitProjectWizard,
  deleteDraftFromURL,
  importTMXViaAPI,
  deleteViaAPI,
  deleteTMViaAPI,
} from "../tests/helpers";

import { deleteProjectViaAPI as deleteProjectRowViaAPI, deleteViaAPI } from "../tests/helpers";

const API = "";

// ─── Auth helper ───────────────────────────────────────────────────────────

/** Get the auth token from localStorage (must call login() first). */
async function authHeader(page: Page): Promise<Record<string, string>> {
  const token = await page.evaluate(() => localStorage.getItem("token"));
  return token ? { Authorization: `Bearer ${token}` } : {};
}

// ─── TM ────────────────────────────────────────────────────────────────────

// A TM needs a source + target languages, and TM assignments
// resolve by the TM's own declared languages. Journeys create TMs with name
// only, so default to source "en" + a broad set of BASE target codes. The
// pipeline lookup's base-language fallback matches every locale variant the
// journeys use (da-dk→da, fr-ca→fr, ja-jp→ja, es-mx→es, …). Pass explicit
// source/targets for the rare non-en-source case.
export async function createTMViaAPI(
  page: Page,
  name: string,
  source = "en",
  targets: string[] = ["es", "fr", "de", "da", "ja", "ko", "nb", "nl", "pt", "sv", "uk", "ar", "it", "ru", "zh"],
): Promise<number> {
  const headers = await authHeader(page);
  const res = await page.request.post(`${API}/api/tms`, {
    headers,
    data: { name, source_language: source, target_languages: targets },
  });
  expect(res.ok(), `createTM "${name}": ${res.status()}`).toBe(true);
  const body = await res.json();
  return body.data.id;
}

/** Import a TMX file from a raw Buffer (for binary-encoded files like UTF-16). */
export async function importTMXBufferViaAPI(
  page: Page,
  tmId: number,
  buffer: Buffer,
  filename = "import.tmx",
): Promise<void> {
  const headers = await authHeader(page);
  const res = await page.request.post(`${API}/api/tms/${tmId}/import`, {
    headers,
    multipart: {
      file: {
        name: filename,
        mimeType: "application/xml",
        buffer,
      },
    },
  });
  expect(res.ok(), `importTMXBuffer into TM ${tmId}: ${res.status()}`).toBe(true);
}

// ─── Term base / Terminology ────────────────────────────────────────────────

export async function createTermBaseViaAPI(
  page: Page,
  name: string,
  srcLang: string,
  tgtLang: string,
): Promise<number> {
  const headers = await authHeader(page);
  // tgtLang is unused on the base (target_languages derive from terms) but kept
  // in the signature so callers document the pair; terms are added with it.
  void tgtLang;
  const res = await page.request.post(`${API}/api/terminology`, {
    headers,
    data: { name, description: "", source_language: srcLang },
  });
  expect(res.ok(), `createTermBase "${name}": ${res.status()}`).toBe(true);
  const body = await res.json();
  return body.data.id;
}

// Adds a bilingual concept (source-language term + target-language term). Both
// languages are explicit — a term base has no single stored target.
export async function addTermBaseTermViaAPI(
  page: Page,
  terminologyId: number,
  srcLang: string,
  tgtLang: string,
  sourceTerm: string,
  targetTerm: string,
  status: "approved" | "forbidden" | "pending" = "approved",
): Promise<void> {
  const headers = await authHeader(page);
  const res = await page.request.post(`${API}/api/terminology/${terminologyId}/concepts`, {
    headers,
    data: {
      terms: [
        { language: srcLang, text: sourceTerm, status: "approved" },
        { language: tgtLang, text: targetTerm, status },
      ],
    },
  });
  expect(res.ok(), `addTerm "${sourceTerm}" to term base ${terminologyId}: ${res.status()}`).toBe(true);
}

export async function deleteTermBaseViaAPI(page: Page, terminologyId: number): Promise<void> {
  await deleteViaAPI(page, `/api/terminology/${terminologyId}`, `term base ${terminologyId}`);
}

// ─── QA Profile ────────────────────────────────────────────────────────────

export interface QARule {
  check_type: string;
  enabled: boolean;
  severity: string;
  config?: Record<string, unknown>;
}

export async function createQAProfileViaAPI(
  page: Page,
  name: string,
  rules: QARule[] = [],
): Promise<number> {
  const headers = await authHeader(page);
  const res = await page.request.post(`${API}/api/qa-profiles`, {
    headers,
    data: { name, description: "" },
  });
  expect(res.ok(), `createQAProfile "${name}": ${res.status()}`).toBe(true);
  const body = await res.json();
  const profileId: number = body.data.id;

  // Upsert each rule
  for (const rule of rules) {
    const ruleRes = await page.request.post(`${API}/api/qa-profiles/${profileId}/rules`, {
      headers,
      data: rule,
    });
    expect(ruleRes.ok(), `upsertRule ${rule.check_type}: ${ruleRes.status()}`).toBe(true);
  }

  return profileId;
}

export async function deleteQAProfileViaAPI(page: Page, profileId: number): Promise<void> {
  await deleteViaAPI(page, `/api/qa-profiles/${profileId}`, `QA profile ${profileId}`);
}

// ─── Project ───────────────────────────────────────────────────────────────

export async function createProjectViaAPI(
  page: Page,
  name: string,
  srcLang: string,
  tgtLangs: string[],
  // Templates are create-time-only: settings + assignments are copied at
  // creation when template_id is passed.
  templateId?: number,
): Promise<number> {
  const headers = await authHeader(page);
  const res = await page.request.post(`${API}/api/projects`, {
    headers,
    data: { name, source_language: srcLang, target_languages: tgtLangs, template_id: templateId },
  });
  expect(res.ok(), `createProject "${name}": ${res.status()}`).toBe(true);
  const body = await res.json();
  return body.data.id;
}

/**
 * Delete a project, then retire the MT router setProjectMTViaAPI made for it
 * (the project was its only user). Routers with any other name — the seeded
 * "Default MT router", a spec's own — are left alone.
 */
export async function deleteProjectViaAPI(page: Page, projectId: number): Promise<void> {
  const headers = await authHeader(page);
  const res = await page.request.get(`${API}/api/projects/${projectId}/settings`, { headers }).catch(() => null);
  const routerId: number | null = res && res.ok() ? ((await res.json()).data?.project?.mt_router_id ?? null) : null;
  await deleteProjectRowViaAPI(page, projectId);
  if (routerId) await deleteJourneyRouterViaAPI(page, routerId);
}

/** Set project settings (scalars: fuzzy threshold, pre-translate, QA profile, MT router). */
export async function updateProjectSettingsViaAPI(
  page: Page,
  projectId: number,
  data: { fuzzy_threshold?: number; mt_on_upload?: boolean; qa_profile_id?: number | null; mt_router_id?: number | null },
): Promise<void> {
  const headers = await authHeader(page);
  const res = await page.request.patch(`${API}/api/projects/${projectId}/settings`, {
    headers,
    data,
  });
  expect(res.ok(), `updateProjectSettings ${projectId}: ${res.status()}`).toBe(true);
}

/** Set TM assignments for a project. */
export async function setProjectTMsViaAPI(
  page: Page,
  projectId: number,
  assignments: { tm_id: number; source_language: string; target_language: string; mode: string; priority: number }[],
): Promise<void> {
  const headers = await authHeader(page);
  const res = await page.request.put(`${API}/api/projects/${projectId}/tms`, {
    headers,
    data: { assignments },
  });
  expect(res.ok(), `setProjectTMs ${projectId}: ${res.status()}`).toBe(true);
}

/**
 * Point a project at machine translation for the given target languages.
 *
 * Builds one MT router (one rule per distinct target language → engine) and
 * sets it as the project's `mt_router_id`. Source languages are accepted for
 * caller compatibility but play no part: routing is by target language only.
 * An empty list clears the project's router (no machine translation).
 * Returns the router id (null when cleared).
 */
export async function setProjectMTViaAPI(
  page: Page,
  projectId: number,
  assignments: { mt_engine_id: number; source_language?: string; target_language: string }[],
): Promise<number | null> {
  if (assignments.length === 0) {
    await updateProjectSettingsViaAPI(page, projectId, { mt_router_id: null });
    return null;
  }
  const routerId = await createMTRouterViaAPI(page, {
    name: `${JOURNEY_ROUTER_PREFIX}p${projectId} ${Date.now()}`,
    rules: routerRulesFromAssignments(assignments),
  });
  await updateProjectSettingsViaAPI(page, projectId, { mt_router_id: routerId });
  return routerId;
}

/** Set terminology assignments for a project. */
export async function setProjectTermsViaAPI(
  page: Page,
  projectId: number,
  assignments: { term_base_id: number; source_language: string; target_language: string; mode: string; priority: number }[],
): Promise<void> {
  const headers = await authHeader(page);
  const res = await page.request.put(`${API}/api/projects/${projectId}/terminology`, {
    headers,
    data: { assignments },
  });
  expect(res.ok(), `setProjectTerms ${projectId}: ${res.status()}`).toBe(true);
}

/** Save human translation via API (PUT /api/files/{fileID}/translations/{translationID}). */
export async function updateHumanTranslationViaAPI(
  page: Page,
  fileId: number,
  translationId: number,
  humanText: string,
): Promise<void> {
  const headers = await authHeader(page);
  const res = await page.request.put(`${API}/api/files/${fileId}/translations/${translationId}`, {
    headers,
    data: { human_text: humanText, editing_time_ms: 0 },
  });
  expect(res.ok(), `updateHumanTranslation ${translationId}: ${res.status()}`).toBe(true);
}

/**
 * Save a draft translation via the draft endpoint
 * (PUT /api/files/{fileID}/translations/{translationID}/draft).
 *
 * Use this when a journey needs to PLANT bad-tag/QA-violating text for
 * subsequent run-qa scans. The regular human-confirm endpoint goes through
 * `enforceQAOnConfirm` and 422s on error-severity violations —
 * which is correct UX but defeats journeys whose whole point is to verify
 * that QA finds the violations *they* deliberately introduce. Drafts don't
 * enforce.
 *
 * `human_rich_text: ""` is passed so ListTranslationsForQAPage's COALESCE
 * returns '' as target_rich_text instead of falling back to the still-tagged
 * `active_rich_text` from MT pre-translate — without this the rich-text path
 * sees matching tag counts and misses a tag-removal violation.
 */
export async function saveTranslationDraftViaAPI(
  page: Page,
  fileId: number,
  translationId: number,
  humanText: string,
): Promise<void> {
  const headers = await authHeader(page);
  const res = await page.request.put(
    `${API}/api/files/${fileId}/translations/${translationId}/draft`,
    {
      headers,
      data: { human_text: humanText, human_rich_text: "", editing_time_ms: 0 },
    },
  );
  expect(res.ok(), `saveTranslationDraft ${translationId}: ${res.status()}`).toBe(true);
}

/** Upsert a QA rule on a profile (used to modify rules mid-flight in j18). */
export async function upsertQARuleViaAPI(
  page: Page,
  profileId: number,
  rule: QARule,
): Promise<void> {
  const headers = await authHeader(page);
  const res = await page.request.post(`${API}/api/qa-profiles/${profileId}/rules`, {
    headers,
    data: rule,
  });
  expect(res.ok(), `upsertQARule ${rule.check_type}: ${res.status()}`).toBe(true);
}

/** Run QA on a job and return the issue count. */
export async function runQAViaAPI(page: Page, jobId: number): Promise<{ status: number; body: any }> {
  const headers = await authHeader(page);
  const res = await page.request.post(`${API}/api/jobs/${jobId}/run-qa`, { headers });
  const body = res.ok() ? await res.json() : await res.text();
  return { status: res.status(), body };
}

/** Get the QA report for a job. */
export async function getQAReportViaAPI(page: Page, jobId: number): Promise<{ summary: { error: number; warning: number; info: number }; results: any[] }> {
  const headers = await authHeader(page);
  const res = await page.request.get(`${API}/api/jobs/${jobId}/qa-report`, { headers });
  expect(res.ok(), `getQAReport ${jobId}: ${res.status()}`).toBe(true);
  return (await res.json()).data;
}

/**
 * Complete a job through the API: advance every open workflow step, then
 * POST /api/jobs/{id}/complete, which marks the job completed and writes its
 * approved segments to the write TMs. Asserts each call returns 200.
 */
export async function completeJobViaAPI(page: Page, jobId: number): Promise<void> {
  const headers = await authHeader(page);

  const stepsRes = await page.request.get(`${API}/api/jobs/${jobId}/workflow/steps`, { headers });
  expect(stepsRes.ok(), `list steps job=${jobId}: ${stepsRes.status()}`).toBe(true);
  const steps: { status: string }[] = (await stepsRes.json()).data ?? [];
  const open = steps.filter((s) => s.status !== "completed").length;
  for (let i = 0; i < open; i++) {
    const adv = await page.request.post(`${API}/api/jobs/${jobId}/workflow/advance`, { headers });
    expect(adv.ok(), `advance ${i} job=${jobId}: ${adv.status()} ${await adv.text()}`).toBe(true);
  }

  const res = await page.request.post(`${API}/api/jobs/${jobId}/complete`, { headers });
  expect(res.status(), `complete job=${jobId}: ${await res.text()}`).toBe(200);
}

/**
 * Advance the job through every open workflow step but the last. Each step
 * confirms its own segments — a step advance takes the previous step's
 * approvals away, and job completion writes only the final step's
 * confirmations to TM — so a test that checks the completion write-back
 * confirms after this, in the final step.
 */
export async function advanceToFinalStepViaAPI(page: Page, jobId: number): Promise<void> {
  const headers = await authHeader(page);
  const stepsRes = await page.request.get(`${API}/api/jobs/${jobId}/workflow/steps`, { headers });
  expect(stepsRes.ok(), `list steps job=${jobId}: ${stepsRes.status()}`).toBe(true);
  const steps: { status: string }[] = (await stepsRes.json()).data ?? [];
  const open = steps.filter((s) => s.status !== "completed").length;
  for (let i = 0; i < open - 1; i++) {
    const adv = await page.request.post(`${API}/api/jobs/${jobId}/workflow/advance`, { headers });
    expect(adv.ok(), `advance ${i} job=${jobId}: ${adv.status()} ${await adv.text()}`).toBe(true);
  }
}

/** The job's translation rows in segment order (GET /api/jobs/{jobID}/translations). */
export async function getJobTranslationsViaAPI(
  page: Page,
  jobId: number,
): Promise<{ id: number; segment_id: number; status: string; active_text: string | null }[]> {
  const headers = await authHeader(page);
  const res = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
  expect(res.ok(), `list translations job=${jobId}: ${res.status()}`).toBe(true);
  return (await res.json()).data;
}

/**
 * Complete a job (see completeJobViaAPI) and verify its approved segments
 * landed in the target TM.
 *
 * A confirm into a read_write TM writes the same pair at once, so an entry
 * found after completion cannot by itself tell the confirm from the
 * completion write-back. `absentBefore` first asserts the entry is NOT in the
 * TM; finding it afterwards then proves the completion wrote it.
 */
export async function assertCompletionPersistedToTM(
  page: Page,
  jobId: number,
  verify: { tmId: number; sourceText: string; targetText?: string; absentBefore?: boolean },
): Promise<void> {
  if (verify.absentBefore) await assertTMLacksEntry(page, verify);
  await completeJobViaAPI(page, jobId);
  await assertTMHasEntry(page, verify);
}

type TMEntryText = { source_text: string; target_text: string };

/**
 * TM entries whose source contains `sourceText` — a substring search via the
 * TM-entries endpoint, the same one the TM browser uses. `entries` is the
 * unfiltered page, for failure messages.
 */
async function listTMEntriesForSource(
  page: Page,
  tmId: number,
  sourceText: string,
): Promise<{ entries: TMEntryText[]; matched: TMEntryText[] }> {
  const headers = await authHeader(page);
  const search = encodeURIComponent(sourceText);
  const entriesRes = await page.request.get(
    `${API}/api/tms/${tmId}/entries?q=${search}&limit=50`,
    { headers },
  );
  expect(entriesRes.ok(), `list TM ${tmId} entries: ${entriesRes.status()}`).toBe(true);
  const body = await entriesRes.json();
  const entries: TMEntryText[] = body.data ?? [];
  const matched = entries.filter((e) =>
    e.source_text.toLowerCase().includes(sourceText.toLowerCase()),
  );
  return { entries, matched };
}

/**
 * Assert a TM holds an entry for `sourceText` (and, when given, with
 * `targetText` among its translations).
 *
 * @param verify.tmId — TM where the entry must be
 * @param verify.sourceText — known confirmed source text to look up
 * @param verify.targetText — optional: known confirmed translation to find
 */
export async function assertTMHasEntry(
  page: Page,
  verify: { tmId: number; sourceText: string; targetText?: string },
): Promise<void> {
  const { entries, matched } = await listTMEntriesForSource(page, verify.tmId, verify.sourceText);
  expect(
    matched.length,
    `TM ${verify.tmId} should contain entry with source "${verify.sourceText}" (found ${entries.length} total)`,
  ).toBeGreaterThan(0);

  if (verify.targetText) {
    const targetMatched = matched.find((e) =>
      e.target_text.toLowerCase().includes(verify.targetText!.toLowerCase()),
    );
    expect(
      targetMatched,
      `TM ${verify.tmId} should have target "${verify.targetText}" for source "${verify.sourceText}", got: ${JSON.stringify(matched.map((m) => m.target_text))}`,
    ).toBeDefined();
  }
}

/**
 * Assert a TM holds no entry for `sourceText` — or, when `targetText` is
 * given, none whose translation contains it. The "before" half of a
 * before/after proof that a write happened.
 */
export async function assertTMLacksEntry(
  page: Page,
  verify: { tmId: number; sourceText: string; targetText?: string },
): Promise<void> {
  const { matched } = await listTMEntriesForSource(page, verify.tmId, verify.sourceText);
  const offending = verify.targetText
    ? matched.filter((e) => e.target_text.toLowerCase().includes(verify.targetText!.toLowerCase()))
    : matched;
  expect(
    offending.map((e) => e.target_text),
    `TM ${verify.tmId} should not yet hold "${verify.sourceText}"${verify.targetText ? ` → "${verify.targetText}"` : ""}`,
  ).toEqual([]);
}

/**
 * Run QA on a job via the UI button and assert the response is exactly 200.
 *
 * Caller must already be on the CAT editor page for the job.
 */
export async function assertQARunSucceeded(page: Page): Promise<void> {
  // Select by testid, not label: after any autosave the button may already read
  // "QA (N)" (per-segment QA seeds the report), so a /run qa/ name
  // match would find nothing.
  const qaBtn = page.getByTestId("run-qa-btn");
  await expect(qaBtn).toBeVisible({ timeout: 5000 });
  const [qaResp] = await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes("/run-qa") && r.request().method() === "POST",
      { timeout: 15000 },
    ),
    qaBtn.click(),
  ]);
  expect(qaResp.status(), "QA run must return 200").toBe(200);
}

/** Resolve a library step definition by name, creating it when missing
 *  (steps come from the library, never free text). */
export async function ensureStepDefinitionViaAPI(
  page: Page,
  spec: { name: string; role_required?: string; ai_router_id?: number | null; step_type?: "human" | "hybrid" | "ai" },
): Promise<number> {
  const headers = await authHeader(page);
  const list = await page.request.get(`${API}/api/step-definitions`, { headers });
  expect(list.ok(), `list step definitions: ${list.status()}`).toBe(true);
  const existing = ((await list.json()).data as { id: number; name: string }[])
    .find((d) => d.name.toLowerCase() === spec.name.toLowerCase());
  if (existing) return existing.id;
  const res = await page.request.post(`${API}/api/step-definitions`, {
    headers,
    data: {
      name: spec.name,
      step_type: spec.step_type ?? (spec.ai_router_id ? "hybrid" : "human"),
      role_required: spec.role_required ?? "linguist",
      ai_router_id: spec.ai_router_id ?? null,
    },
  });
  expect(res.ok(), `create step definition ${spec.name}: ${res.status()} ${await res.text()}`).toBe(true);
  return (await res.json()).data.id;
}

/** Soft-delete a step definition by name (no-op when absent or still composed
 *  into a live workflow). Journeys that invent step names call this in cleanup
 *  so the library doesn't accumulate them. */
export async function deleteStepDefinitionByNameViaAPI(page: Page, name: string): Promise<void> {
  const headers = await authHeader(page);
  const list = await page.request.get(`${API}/api/step-definitions`, { headers });
  if (!list.ok()) return;
  const def = ((await list.json()).data as { id: number; name: string }[])
    .find((d) => d.name.toLowerCase() === name.toLowerCase());
  if (def) await page.request.delete(`${API}/api/step-definitions/${def.id}`, { headers });
}

/** Delete a library workflow. Tolerates an already-deleted one so cleanup
 *  stays best-effort. */
export async function deleteWorkflowViaAPI(page: Page, id: number): Promise<void> {
  const res = await page.request.delete(`${API}/api/workflows/${id}`, { headers: await authHeader(page) });
  expect([204, 404]).toContain(res.status());
}

/** Set workflow steps for a project from name-based specs. */
export async function setProjectWorkflowViaAPI(
  page: Page,
  projectId: number,
  steps: { name: string; role_required?: string; default_assignee?: number | null; deadline?: string | null; ai_router_id?: number | null }[],
): Promise<void> {
  const headers = await authHeader(page);
  const items = [];
  for (const s of steps) {
    items.push({
      step_definition_id: await ensureStepDefinitionViaAPI(page, s),
      default_assignee: s.default_assignee ?? null,
      deadline: s.deadline ?? null,
    });
  }
  const res = await page.request.put(`${API}/api/projects/${projectId}/workflow-steps`, {
    headers,
    data: { steps: items },
  });
  expect(res.ok(), `setProjectWorkflow ${projectId}: ${res.status()} ${await res.text()}`).toBe(true);
}

// ─── MT Engine ─────────────────────────────────────────────────────────────

export async function createMTEngineViaAPI(
  page: Page,
  name: string,
  engineType: string = "mock",
): Promise<number> {
  const headers = await authHeader(page);
  const res = await page.request.post(`${API}/api/mt-engines`, {
    headers,
    data: { name, engine_type: engineType },
  });
  expect(res.ok(), `createMTEngine "${name}": ${res.status()}`).toBe(true);
  const body = await res.json();
  return body.data.id;
}

/**
 * Delete an MT engine. An engine a live router uses cannot be deleted (409),
 * so the routers that route to it go first — they belong to the same test,
 * since only the test that created the engine can reference it. Best-effort:
 * a router still pinned by a live project stays, and so does the engine.
 */
export async function deleteMTEngineViaAPI(page: Page, engineId: number): Promise<void> {
  const headers = await authHeader(page);
  const routers = await listMTRoutersViaAPI(page).catch(() => [] as MTRouter[]);
  for (const r of routers) {
    const usesEngine = r.default_mt_engine_id === engineId || r.rules.some((rule) => rule.mt_engine_id === engineId);
    if (usesEngine) await deleteMTRouterViaAPI(page, r.id);
  }
  await page.request.delete(`${API}/api/mt-engines/${engineId}`, { headers }).catch(() => {});
}

// ─── MT Router ─────────────────────────────────────────────────────────────

/** One engine reference: exactly one of the two ids is set. */
export interface MTRouteRef {
  mt_engine_id?: number | null;
  ai_engine_id?: number | null;
}

export interface MTRouterInput {
  name: string;
  default?: MTRouteRef;
  rules?: ({ target_lang: string } & MTRouteRef)[];
}

/** A router as GET /api/mt-routers returns it. */
export interface MTRouter {
  id: number;
  name: string;
  default_mt_engine_id: number | null;
  default_ai_engine_id: number | null;
  rules: { id: number; target_lang: string; mt_engine_id: number | null; ai_engine_id: number | null }[];
}

/** Name prefix of routers the set*MTViaAPI helpers create; cleanup only retires these. */
const JOURNEY_ROUTER_PREFIX = "Journey router ";

/** Retire a router if (and only if) a set*MTViaAPI helper created it. */
async function deleteJourneyRouterViaAPI(page: Page, routerId: number): Promise<void> {
  const headers = await authHeader(page);
  const res = await page.request.get(`${API}/api/mt-routers/${routerId}`, { headers }).catch(() => null);
  if (!res || !res.ok()) return;
  const router = (await res.json()).data as MTRouter;
  if (router.name.startsWith(JOURNEY_ROUTER_PREFIX)) await deleteMTRouterViaAPI(page, routerId);
}

/** One rule per distinct target language (last engine wins for a repeated language). */
function routerRulesFromAssignments(
  assignments: { mt_engine_id: number; target_language: string }[],
): ({ target_lang: string } & MTRouteRef)[] {
  const byLang = new Map<string, number>();
  for (const a of assignments) byLang.set(a.target_language.toLowerCase(), a.mt_engine_id);
  return [...byLang.entries()].map(([target_lang, mt_engine_id]) => ({ target_lang, mt_engine_id }));
}

export async function createMTRouterViaAPI(page: Page, input: MTRouterInput): Promise<number> {
  const headers = await authHeader(page);
  const res = await page.request.post(`${API}/api/mt-routers`, {
    headers,
    data: { name: input.name, default: input.default ?? {}, rules: input.rules ?? [] },
  });
  expect(res.ok(), `createMTRouter "${input.name}": ${res.status()} ${(await res.text()).slice(0, 200)}`).toBe(true);
  return (await res.json()).data.id;
}

export async function getMTRouterViaAPI(page: Page, routerId: number): Promise<MTRouter> {
  const headers = await authHeader(page);
  const res = await page.request.get(`${API}/api/mt-routers/${routerId}`, { headers });
  expect(res.ok(), `getMTRouter ${routerId}: ${res.status()}`).toBe(true);
  return (await res.json()).data;
}

export async function listMTRoutersViaAPI(page: Page): Promise<MTRouter[]> {
  const headers = await authHeader(page);
  const res = await page.request.get(`${API}/api/mt-routers`, { headers });
  expect(res.ok(), `listMTRouters: ${res.status()}`).toBe(true);
  return (await res.json()).data;
}

/** Best-effort delete; a router a live project/template still uses answers 409 and stays. */
export async function deleteMTRouterViaAPI(page: Page, routerId: number): Promise<void> {
  const headers = await authHeader(page);
  await page.request.delete(`${API}/api/mt-routers/${routerId}`, { headers }).catch(() => {});
}

// ─── Template ──────────────────────────────────────────────────────────────

/** Update template scalars (name, fuzzy, pre_translate, qa_profile). Name is required by the API. */
export async function updateTemplateViaAPI(
  page: Page,
  templateId: number,
  data: { name: string; fuzzy_threshold?: number; mt_on_upload?: boolean; qa_profile_id?: number | null; mt_router_id?: number | null },
): Promise<void> {
  const headers = await authHeader(page);
  const res = await page.request.patch(`${API}/api/templates/${templateId}`, { headers, data });
  expect(res.ok(), `updateTemplate ${templateId}: ${res.status()}`).toBe(true);
}

export async function createTemplateViaAPI(page: Page, name: string): Promise<{ id: number; settings_profile_id: number }> {
  const headers = await authHeader(page);
  const res = await page.request.post(`${API}/api/templates`, {
    headers,
    data: { name },
  });
  expect(res.ok(), `createTemplate "${name}": ${res.status()}`).toBe(true);
  const body = await res.json();
  return { id: body.data.id, settings_profile_id: body.data.settings_profile_id };
}

/** Delete a template, then retire the MT router setTemplateMTViaAPI made for it. */
export async function deleteTemplateViaAPI(page: Page, templateId: number): Promise<void> {
  const headers = await authHeader(page);
  const res = await page.request.get(`${API}/api/templates/${templateId}`, { headers }).catch(() => null);
  const routerId: number | null = res && res.ok() ? ((await res.json()).data?.template?.mt_router_id ?? null) : null;
  await page.request.delete(`${API}/api/templates/${templateId}`, { headers }).catch(() => {});
  if (routerId) await deleteJourneyRouterViaAPI(page, routerId);
}

/** Set TM assignments for a template. */
export async function setTemplateTMsViaAPI(
  page: Page,
  templateId: number,
  assignments: { tm_id: number; source_language: string; target_language: string; mode: string; priority: number }[],
): Promise<void> {
  const headers = await authHeader(page);
  const res = await page.request.put(`${API}/api/templates/${templateId}/tms`, {
    headers,
    data: { assignments },
  });
  expect(res.ok(), `setTemplateTMs ${templateId}: ${res.status()}`).toBe(true);
}

/**
 * Point a template at machine translation for the given target languages —
 * same shape as setProjectMTViaAPI: one router, one rule per target language,
 * set as the template's `mt_router_id`. Projects created from the template
 * share the router by reference. Returns the router id (null when cleared).
 */
export async function setTemplateMTViaAPI(
  page: Page,
  templateId: number,
  assignments: { mt_engine_id: number; source_language?: string; target_language: string }[],
): Promise<number | null> {
  const headers = await authHeader(page);
  // PATCH /api/templates/{id} requires the name, so read it back first.
  const getRes = await page.request.get(`${API}/api/templates/${templateId}`, { headers });
  expect(getRes.ok(), `setTemplateMT ${templateId}: get template: ${getRes.status()}`).toBe(true);
  const name = (await getRes.json()).data.template.name as string;

  let routerId: number | null = null;
  if (assignments.length > 0) {
    routerId = await createMTRouterViaAPI(page, {
      name: `${JOURNEY_ROUTER_PREFIX}t${templateId} ${Date.now()}`,
      rules: routerRulesFromAssignments(assignments),
    });
  }
  await updateTemplateViaAPI(page, templateId, { name, mt_router_id: routerId });
  return routerId;
}

// ─── Verification ──────────────────────────────────────────────────────────

/**
 * Deep-verify project settings via API. Throws on any mismatch.
 * Pass only the fields you want to check — undefined fields are skipped.
 */
export async function verifyProjectSettings(
  page: Page,
  projectId: number,
  expected: {
    fuzzy_threshold?: number;
    mt_on_upload?: boolean;
    qa_profile_id?: number | null;
    tm_count?: number;
    /** The router the project points at; null asserts "no machine translation". */
    mt_router_id?: number | null;
    term_count?: number;
    workflow_step_count?: number;
  },
): Promise<void> {
  const headers = await authHeader(page);
  const res = await page.request.get(`${API}/api/projects/${projectId}/settings`, { headers });
  expect(res.ok(), `getProjectSettings ${projectId}: ${res.status()}`).toBe(true);
  const body = await res.json();
  const s = body.data;

  if (expected.fuzzy_threshold !== undefined)
    expect(s.project?.fuzzy_threshold, "fuzzy_threshold").toBeCloseTo(expected.fuzzy_threshold, 2);
  if (expected.mt_on_upload !== undefined)
    expect(s.project?.mt_on_upload, "mt_on_upload").toBe(expected.mt_on_upload);
  if (expected.qa_profile_id !== undefined)
    expect(s.project?.qa_profile_id, "qa_profile_id").toBe(expected.qa_profile_id);
  if (expected.tm_count !== undefined)
    expect(s.tm_assignments?.length ?? 0, "tm_assignment count").toBe(expected.tm_count);
  if (expected.mt_router_id !== undefined)
    expect(s.project?.mt_router_id ?? null, "mt_router_id").toBe(expected.mt_router_id);
  if (expected.term_count !== undefined)
    expect(s.term_assignments?.length ?? 0, "term_assignment count").toBe(expected.term_count);
  if (expected.workflow_step_count !== undefined)
    expect(s.workflow_steps?.length ?? 0, "workflow_step count").toBe(expected.workflow_step_count);
}

/**
 * Select one word inside the active segment's source cell by dragging the
 * mouse across it, as a user does, and assert the browser agrees.
 *
 * A drag between the word's own first and last characters rather than a
 * dblclick: the word is rarely at the element's centre, so a click lands on
 * the wrong token. A drag rather than a scripted Range: the press moves focus
 * off the segment editor, as it does for a user. ProseMirror re-asserts its
 * own caret 20 ms after the editor takes focus while it keeps it, so a Range
 * set with the editor still focused can be undone before the next key press.
 * Used by the concordance specs, whose whole subject is "does the tool act on
 * my selection".
 */
export async function selectWordInActiveSource(page: Page, word: string): Promise<void> {
  const ends = await page.evaluate((w) => {
    const box = document.querySelector('[data-testid="active-source-box"]');
    if (!box) throw new Error("active source box missing — is a segment active?");
    const walker = document.createTreeWalker(box, NodeFilter.SHOW_TEXT);
    while (walker.nextNode()) {
      const node = walker.currentNode as Text;
      const i = node.data.indexOf(w);
      if (i === -1) continue;
      const charRect = (at: number) => {
        const r = document.createRange();
        r.setStart(node, at);
        r.setEnd(node, at + 1);
        return r.getBoundingClientRect();
      };
      const first = charRect(i), last = charRect(i + w.length - 1);
      // Inside the first character's left half and the last one's right half,
      // so the caret lands before and after the word.
      return {
        from: { x: first.left + 1, y: first.top + first.height / 2 },
        to: { x: last.right - 1, y: last.top + last.height / 2 },
      };
    }
    throw new Error(`"${w}" not found in the active source box`);
  }, word);
  await page.mouse.move(ends.from.x, ends.from.y);
  await page.mouse.down();
  await page.mouse.move(ends.to.x, ends.to.y, { steps: 4 });
  await page.mouse.up();
  const selected = await page.evaluate(() => window.getSelection()?.toString() ?? "");
  expect(selected, "browser should report the word as selected").toBe(word);
}
