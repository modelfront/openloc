/**
 * J21 — Pipeline Cancel + Retry + Concurrent Jobs (Adversarial)
 *
 * Goal: lock down the pipeline lifecycle surface end-to-end through HTTP:
 *   - Concurrent pre-translate on 3 files in parallel — each independent
 *   - DELETE-after-complete cleans up translations + cancels asynq task
 *   - DELETE on a never-translated job is a clean no-op (no zombie state)
 *   - Engine-level failure (an engine that cannot write the job's target)
 *     surfaces as `pipeline_status='failed'` + `error_message` populated
 *     (a failure is never silent)
 *
 * Locale: ar-EG (Arabic, Egypt) — never used by any other journey.
 *
 * Why ar-EG: the mock MT engine (internal/mt/languages.go::mockLanguages)
 *   advertises `ar` as a base code, so ar-EG falls back to ar via base-code fallback,
 *   and Arabic gives RTL coverage. The mock does NOT support `he`, so
 *   ValidateAssignment would reject en→he-IL with 400. Add `he` to
 *   mockLanguages before using he-IL.
 *
 * Honest scope notes (be deliberate about what we DON'T test):
 *   - **True mid-flight cancellation** is NOT exercised. The mock MT engine
 *     processes each chunk synchronously and instantly; a 200-segment file
 *     completes in ~50ms before any HTTP DELETE could fire. The cooperative
 *     cancellation logic at chunk boundaries (internal/pipeline/translate.go
 *     line 310 area) is covered by the Go integration test
 *     `internal/pipeline/translate_test.go::TestPipeline_CancellationAborts
 *     MidChunk` which uses a controllable progress callback to fire cancel
 *     between chunks deterministically. j21's "cancel via DELETE" tests
 *     instead exercise the post-completion delete path, which is what users
 *     actually hit (the UI's "Stop & Delete" button on a finished job).
 *   - **asynq retry storm reproduction** is also not E2E here. The
 *     MaxRetry(3) cap is configured at enqueue time and only observable by
 *     watching asynq Inspector for retried-task counts. j21's failure-path
 *     test instead asserts the user-visible outcome: job ends in 'failed'
 *     status with a non-empty error_message. The retry
 *     count itself is asynq-internal and not part of the API surface.
 *   - asynq Inspector instance availability in the test environment is
 *     assumed (TaskCanceller is wired via deps.TaskCanceller in api.go).
 *     If TaskCanceller is nil, DELETE still succeeds at the row level —
 *     the cancel call is best-effort by design.
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
  setProjectMTViaAPI,
  updateProjectSettingsViaAPI,
  createMTRouterViaAPI,
} from "./helpers";

const API = "";
const RUN_ID = Date.now();

let projectId: number;
let mtEngineId: number;
let badEngineId: number; // libretranslate, which writes no Arabic — used in Phase D
const fileIds: number[] = [];
const jobIds: number[] = [];

async function authHeader(page: any): Promise<Record<string, string>> {
  const token = await page.evaluate(() => localStorage.getItem("token"));
  return { Authorization: `Bearer ${token}` };
}

async function getJob(req: APIRequestContext, headers: Record<string, string>, jobId: number) {
  return req.get(`${API}/api/jobs/${jobId}`, { headers });
}

async function pollJob(
  req: APIRequestContext,
  headers: Record<string, string>,
  jobId: number,
  predicate: (j: any) => boolean,
  timeoutMs: number,
): Promise<any> {
  const deadline = Date.now() + timeoutMs;
  let last: any = null;
  while (Date.now() < deadline) {
    const res = await getJob(req, headers, jobId);
    if (res.ok()) {
      last = (await res.json()).data;
      if (predicate(last)) return last;
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(`pollJob ${jobId} timed out after ${timeoutMs}ms — last state: ${JSON.stringify(last)}`);
}

test.describe.serial("J21 — Pipeline Cancel + Retry + Concurrent Jobs", () => {
  // ════════════════════════════════════════════════════════════════════════
  // Phase A — Setup project + 3 files for concurrent pre-translate
  // ════════════════════════════════════════════════════════════════════════

  test("A1: setup project en→ar-EG with mock MT engine, no auto pre-translate", async ({ page }) => {
    await login(page);
    mtEngineId = await createMTEngineViaAPI(page, `J21 Mock ${RUN_ID}`, "mock");
    projectId = await createProjectViaAPI(page, `J21 Project ${RUN_ID}`, "en", ["ar-EG"]);
    await updateProjectSettingsViaAPI(page, projectId, { mt_on_upload: false });
    await setProjectMTViaAPI(page, projectId, [
      { mt_engine_id: mtEngineId, source_language: "en", target_language: "ar-EG" },
    ]);
  });

  test("A2: upload 3 files with distinct content for the concurrent run", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    // 3 small files, distinct content per file so we can verify each job's
    // mt_text is independent (no cross-file contamination).
    const files = [
      { name: "j21-file1.txt", content: "Hello one\nGoodbye one" },
      { name: "j21-file2.txt", content: "Hello two\nGoodbye two" },
      { name: "j21-file3.txt", content: "Hello three\nGoodbye three" },
    ];
    for (const f of files) {
      const result = await uploadFileViaAPI(page, projectId, f.name, f.content, "text/plain");
      const fileId = result.data?.id ?? result.id;
      fileIds.push(fileId);
    }

    // Map files → jobs (1 job per file since project has 1 target language)
    for (const fileId of fileIds) {
      const jobsRes = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
      const jobs = (await jobsRes.json()).data as Array<{ id: number; file_id: number }>;
      const job = jobs.find((j) => j.file_id === fileId);
      expect(job, `A2: must find job for file ${fileId}`).toBeDefined();
      if (!jobIds.includes(job!.id)) jobIds.push(job!.id);
    }
    expect(jobIds.length, "A2: 3 files → 3 jobs").toBe(3);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B — Concurrent pre-translate on 3 files in parallel
  // ════════════════════════════════════════════════════════════════════════

  test("B1: fire /translate on all 3 files in parallel — every job must complete independently", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    // Fire all 3 translate calls truly in parallel with Promise.all.
    // Each pretranslateFileViaAPI internally sequences tm-apply + mt-apply
    // per job, but the per-file calls run in parallel — the cross-file
    // concurrency property B1 cares about is preserved.
    await Promise.all(
      fileIds.map((fileId) => pretranslateFileViaAPI(page, projectId, fileId)),
    );

    // Wait for ALL 3 to reach the "all segments touched" state. NOTE: the
    // phase-split endpoints (tm-apply, mt-apply) run synchronously and don't
    // transition the job's `pipeline_status` field (that's tracked by the
    // async worker path). The user-visible "translation done" signal is
    // counts.untranslated === 0 + total > 0, which is what we poll on.
    for (const jobId of jobIds) {
      const final = await pollJob(page.request, headers, jobId,
        (j) => j.counts.total > 0 && j.counts.untranslated === 0,
        15_000,
      );
      expect(final.error_message,
        `B1: job ${jobId} must NOT have an error_message — mock engine never errors. ` +
        `Got error_message=${JSON.stringify(final.error_message)}, pipeline_status=${final.pipeline_status}.`,
      ).toBeFalsy();
      expect(final.counts.pre_translated + final.counts.auto_approved + final.counts.needs_review + final.counts.approved,
        `B1: job ${jobId} — every segment must be in some translated bucket (pre_translated + auto_approved + needs_review + approved). ` +
        `Got counts=${JSON.stringify(final.counts)}.`,
      ).toBe(final.counts.total);
    }
  });

  test("B2: every job's translations have the [AR] prefix (proves resolver fallback ar-EG → ar in the pipeline path)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    for (const jobId of jobIds) {
      const transRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
      const translations = (await transRes.json()).data as Array<{ mt_text: string | null }>;
      expect(translations.length,
        `B2: job ${jobId} must have at least 1 translation`,
      ).toBeGreaterThanOrEqual(1);
      for (const t of translations) {
        expect(t.active_text,
          `B2: job ${jobId} segment with null mt_text — pre-translate dropped this segment ` +
          `(would have manifested as 'translated_count != total_count' too).`,
        ).toBeTruthy();
        expect(t.active_text!.startsWith("[AR] "),
          `B2: mt_text must start with "[AR] " (proving ar-EG → ar fallback ran in the pipeline). ` +
          `Got: ${JSON.stringify(t.active_text!.slice(0, 30))}.`,
        ).toBe(true);
      }
    }
  });

  test("B3: every segment in every job has an mt_text (no silent drops, chunking-correctness)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    for (const jobId of jobIds) {
      const transRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
      const translations = (await transRes.json()).data as Array<{ mt_text: string | null }>;
      const withMT = translations.filter((t) => t.active_text && t.active_text.length > 0);
      expect(withMT.length,
        `B3: job ${jobId} — every segment must have non-empty mt_text. Got ${withMT.length}/${translations.length}. ` +
        `A mismatch indicates the chunked-MT loop in pipeline/translate.go silently dropped a chunk.`,
      ).toBe(translations.length);
    }
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C — DELETE lifecycle: cancel-then-cleanup
  //
  // True mid-flight cancellation isn't reachable with the mock engine
  // (see scope notes in the file header). C tests the post-completion
  // DELETE path that the UI's "Stop & Delete" button uses — the user-
  // visible behavior must be: 204, translations gone, job 404.
  // ════════════════════════════════════════════════════════════════════════

  test("C1: DELETE a completed job → 204, translations gone, GET → 404", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const victimJobId = jobIds[0];
    const victimFileId = fileIds[0];

    // Snapshot translation count before DELETE
    const beforeRes = await page.request.get(`${API}/api/jobs/${victimJobId}/translations`, { headers });
    const beforeCount = (await beforeRes.json()).data.length;
    expect(beforeCount, "C1 setup: completed job must have translations to delete").toBeGreaterThan(0);

    const delRes = await page.request.delete(`${API}/api/jobs/${victimJobId}`, { headers });
    expect(delRes.status(),
      "C1: DELETE on a completed job must 204. " +
      "If 5xx, the cancel-asynq-task call (best-effort) is incorrectly surfacing failures " +
      "instead of swallowing them — see job/handler.go::HandleDeleteJob comment.",
    ).toBe(204);

    // GET the job → 404
    const getRes = await getJob(page.request, headers, victimJobId);
    expect(getRes.status(), "C1: GET deleted job must 404").toBe(404);

    // Translations for that job must be gone (verify by hitting the file's translations endpoint
    // filtered by the deleted job's target_language — there should be zero)
    const transRes = await page.request.get(`${API}/api/jobs/${victimJobId}/translations`, { headers });
    // The route checks job ownership first, so this returns 404 too — that's fine.
    // The defense-in-depth assertion: query the file's translations for ar-EG
    // (the endpoint requires `lang`) and check none remain.
    const fileTranRes = await page.request.get(
      `${API}/api/files/${victimFileId}/translations?lang=ar-EG`,
      { headers },
    );
    expect(fileTranRes.ok(), `C1: file translations list failed: ${fileTranRes.status()}`).toBe(true);
    const all = (await fileTranRes.json()).data as Array<{ target_language: string }>;
    const arRows = all.filter((t) => t.target_language.toLowerCase() === "ar-eg");
    expect(arRows.length,
      "C1: every translation for the deleted job's (file, target_language) must be gone. " +
      "Lingering rows would create orphan data referenced by no job — pollutes the file's downstream operations.",
    ).toBe(0);

    // Remove from our tracking
    jobIds.shift();
    fileIds.shift();
  });

  test("C2: DELETE again (same id) → 404, not 5xx", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    // Use the deleted ID from C1 — playwright doesn't preserve it across tests cleanly,
    // so we DELETE a fresh victim then DELETE-again.
    const victimJobId = jobIds[0];
    const victimFileId = fileIds[0];

    let res = await page.request.delete(`${API}/api/jobs/${victimJobId}`, { headers });
    expect(res.status(), "C2 setup: first DELETE 204").toBe(204);
    res = await page.request.delete(`${API}/api/jobs/${victimJobId}`, { headers });
    expect(res.status(),
      "C2: re-DELETE on a gone job must 404 (tenant-scoped GetJobTenant returns ErrNoRows → DBError → 404). " +
      "5xx here would indicate the handler doesn't gracefully handle the already-deleted case.",
    ).toBe(404);

    jobIds.shift();
    fileIds.shift();
    void victimFileId;
  });

  test("C3: DELETE a never-translated job (untouched, status='queued' equivalent) → 204, no zombie state", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    // Upload a fresh file we DON'T pre-translate. The job exists but has
    // zero MT writes — DELETE must clean up cleanly even with no asynq task.
    const result = await uploadFileViaAPI(page, projectId, "j21-untouched.txt", "Untouched line", "text/plain");
    const fileId = result.data?.id ?? result.id;
    const jobsRes = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    const jobs = (await jobsRes.json()).data as Array<{ id: number; file_id: number; asynq_task_id?: string | null }>;
    const job = jobs.find((j) => j.file_id === fileId)!;

    const delRes = await page.request.delete(`${API}/api/jobs/${job.id}`, { headers });
    expect(delRes.status(),
      "C3: DELETE on a never-translated job must 204 — the cancel-asynq path needs to handle " +
      "the asynq_task_id=NULL case gracefully (skip the cancel call rather than dereference nil).",
    ).toBe(204);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D — Engine failure surface (user-visible invariant)
  //
  // A failure must never be silent — no job stuck "queued" with no error.
  // When translate raises an error, the job ends in pipeline_status='failed'
  // with error_message populated. We trigger it by sending the job a target
  // language the engine does not write — the MT phase refuses the pair before
  // it sends anything, propagates an error, and the job ends 'failed'.
  // ════════════════════════════════════════════════════════════════════════

  test("D1: setup a libretranslate engine, which cannot write Arabic", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    // The endpoint is whatever this deployment supplies: `api_url` is not a
    // request field for an MT engine, so an engine cannot be pointed at a host
    // of the test's choosing. The engine below is therefore a WORKING one, and
    // the failure D2 wants comes from the language, not the address.
    const res = await page.request.post(`${API}/api/mt-engines`, {
      headers,
      data: {
        name: `J21 NoArabic ${RUN_ID}`,
        engine_type: "libretranslate",
      },
    });
    expect(res.ok(), `D1: create libretranslate engine: ${res.status()}`).toBe(true);
    badEngineId = (await res.json()).data.id;
  });

  test("D2: route the project through an engine that cannot write its target, upload a fresh file, trigger translate → job ends 'failed' with non-empty error_message", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    // Through the router's DEFAULT, not a rule. A rule names one target
    // language and one engine, both known at save time, so saving a rule for a
    // language the engine cannot write is refused outright.
    // The default answers for every language no rule names, so there is nothing
    // to check it against until a project supplies its targets — which is how a
    // job still reaches an engine that cannot write its target, the failure
    // this section is about.
    // "Journey router " prefix on purpose: cleanup retires only routers named
    // that way, so a hand-built one needs it too or it outlives the run.
    const badRouterId = await createMTRouterViaAPI(page, {
      name: `Journey router p${projectId} default ${Date.now()}`,
      default: { mt_engine_id: badEngineId },
    });
    await updateProjectSettingsViaAPI(page, projectId, { mt_router_id: badRouterId });
    const settingsRes = await page.request.get(`${API}/api/projects/${projectId}/settings`, { headers });
    expect((await settingsRes.json()).data.project.mt_router_id,
      "D2 setup: project must now point at that engine's router",
    ).toBe(badRouterId);

    // Trigger the WORKER pipeline via the production upload-hook path —
    // failJob + error_message live in the asynq worker handler.
    // The legacy /jobs/{id}/pipeline endpoint was deleted (no FE consumer);
    // the upload hook is the user-facing trigger for the async path.
    await updateProjectSettingsViaAPI(page, projectId, { mt_on_upload: true });

    const result = await uploadFileViaAPI(page, projectId, "j21-failpath.txt", "This will fail to translate", "text/plain");
    const fileId = result.data?.id ?? result.id;
    const jobsRes = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    const jobs = (await jobsRes.json()).data as Array<{ id: number; file_id: number }>;
    const failJob = jobs.find((j) => j.file_id === fileId)!;

    // Wait for failure. A refused pair fails the same way on every attempt, so
    // the worker fails the job on the first one instead of retrying through
    // asynq's 1m/2m/4m backoff. Generous 60s budget.
    const final = await pollJob(page.request, headers, failJob.id,
      (j) => j.pipeline_status === "failed" || j.pipeline_status === "completed",
      60_000,
    );

    expect(final.pipeline_status,
      `D2: job whose engine cannot write its target must end 'failed', not 'completed' or stuck 'queued'. ` +
      `Got pipeline_status=${final.pipeline_status}. A 'completed' status here means the engine error was swallowed; ` +
      `a 'queued' status with a "Retrying" step means the refusal is being retried instead of failing at once.`,
    ).toBe("failed");

    expect(final.error_message,
      `D2: error_message MUST be populated on failure. ` +
      `Empty/null error_message is a silent failure with no UI signal. ` +
      `Got error_message=${JSON.stringify(final.error_message)}.`,
    ).toBeTruthy();
    expect(typeof final.error_message,
      "D2: error_message must be a string, not an object",
    ).toBe("string");
    expect(final.error_message.length,
      "D2: error_message string must be non-empty after stringification",
    ).toBeGreaterThan(0);
    // The editor and the task matrix offer Re-run only when the job says
    // which automation failed.
    expect(final.failed_phase,
      "D2: a failed pre-translation must carry failed_phase so Re-run is offered",
    ).toBe("pretranslation");

    // Cleanup tracking
    fileIds.push(fileId);
    jobIds.push(failJob.id);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase E — Cleanup
  // ════════════════════════════════════════════════════════════════════════

  test("E1: cleanup all resources", async ({ page }) => {
    await login(page);
    const cleanup = async (fn: () => Promise<void>) => { try { await fn(); } catch {} };
    if (projectId) await cleanup(() => deleteProjectViaAPI(page, projectId));
    if (mtEngineId) await cleanup(() => deleteMTEngineViaAPI(page, mtEngineId));
    if (badEngineId) await cleanup(() => deleteMTEngineViaAPI(page, badEngineId));
  });

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    try {
      await login(page);
      if (projectId) await deleteProjectViaAPI(page, projectId).catch(() => {});
      if (mtEngineId) await deleteMTEngineViaAPI(page, mtEngineId).catch(() => {});
      if (badEngineId) await deleteMTEngineViaAPI(page, badEngineId).catch(() => {});
    } catch {} finally {
      await page.close();
    }
  });
});
