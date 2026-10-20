/**
 * J22 — Multi-User Concurrent Edit Gauntlet (Adversarial)
 *
 * Goal: prove optimistic locking on translations and the TM write-back
 * race guard (partial unique index) end-to-end through the
 * HTTP API. Both invariants also have Go-level integration coverage — j22
 * adds the cross-process / cross-request E2E proof.
 *
 * Locale: uk-UA (Ukrainian, Ukraine) — never used by any other journey.
 * Mock supports `uk` natively (no `uk-ua`), so uk-UA exercises the
 * base-code fallback path the same way j17's sv-SE does.
 * Cyrillic source text gives us a visual sanity check on the prefix.
 *
 * Honest scope notes:
 *   - "Two browsers, same user" is simulated via two parallel HTTP requests
 *     using the same JWT — the journey doesn't spin up a real second browser
 *     context. The optimistic-locking semantics are server-side, so this
 *     captures the bug surface accurately. Frontend "ConfirmDialog" UX is
 *     not exercised here (out of scope for an API-level journey).
 *   - The partial-index dedupe only applies to `created_by = 'human'`
 *     rows by design. The human path is further narrowed to
 *     dedupe on SOURCE only (not source+target), so re-confirming a segment
 *     with a new target overwrites the live TM row + archives the prior
 *     target into translation_memory_history. Multiple targets per source
 * come only from TMX import. We assert the overwrite + archive
 *     contract here; j8 covers TMX import dedupe.
 *   - The "5 simultaneous PUTs" tests use `Promise.all` for client-side
 *     parallelism. The server processes them concurrently but ordering
 *     within the DB is up to Postgres. We assert outcomes that are
 *     deterministic regardless of order (exactly-1-winner, no-duplicates,
 *     no-lost-updates) — never "the second one wins" which would be flaky.
 */
import { test, expect, type APIRequestContext } from "@playwright/test";
import {
  login,
  uploadFileViaAPI,
  createProjectViaAPI,
  deleteProjectViaAPI,
  createMTEngineViaAPI,
  deleteMTEngineViaAPI,
  createTMViaAPI,
  deleteTMViaAPI,
  setProjectMTViaAPI,
  setProjectTMsViaAPI,
  updateProjectSettingsViaAPI,
} from "./helpers";

const API = "";
const RUN_ID = Date.now();

let projectId: number;
let fileId: number;
let jobId: number;
let tmId: number;
let mtEngineId: number;

// Translation IDs grouped by source_text (multiple may share a source for
// the dedupe phase D). Filled in Phase A.
const transBySource: Record<string, number[]> = {};

async function authHeader(page: any): Promise<Record<string, string>> {
  const token = await page.evaluate(() => localStorage.getItem("token"));
  return { Authorization: `Bearer ${token}` };
}

async function getTranslation(req: APIRequestContext, headers: Record<string, string>, fId: number, tId: number) {
  const res = await req.get(`${API}/api/files/${fId}/translations/${tId}`, { headers });
  expect(res.ok(), `getTranslation ${tId}: ${res.status()}`).toBe(true);
  return (await res.json()).data as { id: number; updated_at: string; human_text: string | null; mt_text: string | null };
}

async function putHuman(
  req: APIRequestContext,
  headers: Record<string, string>,
  fId: number,
  tId: number,
  humanText: string,
  expectedUpdatedAt?: string,
) {
  const data: any = { human_text: humanText, editing_time_ms: 0 };
  if (expectedUpdatedAt !== undefined) data.expected_updated_at = expectedUpdatedAt;
  return req.put(`${API}/api/files/${fId}/translations/${tId}`, { headers, data });
}

test.describe.serial("J22 — Multi-User Concurrent Edit Gauntlet", () => {
  // ════════════════════════════════════════════════════════════════════════
  // Phase A — Setup
  //
  // File content: 3 unique sources for parallel-different tests + 3 copies
  // of the same source ("Hello, World!") for TM-dedupe race tests.
  // ════════════════════════════════════════════════════════════════════════

  test("A1: setup project en→uk-UA, mock MT engine, empty TM with read_write", async ({ page }) => {
    await login(page);
    tmId = await createTMViaAPI(page, `J22 TM ${RUN_ID}`);
    mtEngineId = await createMTEngineViaAPI(page, `J22 Mock ${RUN_ID}`, "mock");
    projectId = await createProjectViaAPI(page, `J22 Project ${RUN_ID}`, "en", ["uk-UA"]);

    await updateProjectSettingsViaAPI(page, projectId, { mt_on_upload: false });
    await setProjectTMsViaAPI(page, projectId, [
      { tm_id: tmId, source_language: "en", target_language: "uk-UA", mode: "read_write", priority: 1 },
    ]);
    await setProjectMTViaAPI(page, projectId, [
      { mt_engine_id: mtEngineId, source_language: "en", target_language: "uk-UA" },
    ]);
  });

  test("A2: upload file with 3 unique + 3 duplicate sources, NO pre-translate", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    // Adversarial layout:
    //   1. unique source — used for B (single-segment optimistic locking)
    //   2. unique source — used for C (5-parallel same-segment race)
    //   3-5. SAME source ("Hello, World!") × 3 — used for D (TM dedupe race)
    //   6-9. unique sources — used for E (parallel different-segment confirms)
    const content = [
      "Лінія один.",      // 1 — B target (Ukrainian unique)
      "Лінія два.",        // 2 — C target
      "Hello, World!",     // 3 — D dedupe race #1
      "Hello, World!",     // 4 — D dedupe race #2
      "Hello, World!",     // 5 — D dedupe race #3
      "Унікальний 6.",     // 6 — E target #1
      "Унікальний 7.",     // 7 — E target #2
      "Унікальний 8.",     // 8 — E target #3
      "Унікальний 9.",     // 9 — E target #4
    ].join("\n");

    const result = await uploadFileViaAPI(page, projectId, "j22.txt", content, "text/plain");
    fileId = result.data?.id ?? result.id;

    const jobsRes = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    const jobs = (await jobsRes.json()).data;
    expect(jobs.length, "A2: project should have 1 job (en→uk-UA)").toBe(1);
    jobId = jobs[0].id;

    // Map source_text → array of translation_ids (3 of them share "Hello, World!")
    const transRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const translations = (await transRes.json()).data as Array<{ id: number; segment_id: number }>;
    const segsRes = await page.request.get(`${API}/api/files/${fileId}/segments`, { headers });
    const segments = (await segsRes.json()).data as Array<{ id: number; source_text: string }>;
    for (const t of translations) {
      const seg = segments.find((s) => s.id === t.segment_id);
      if (!seg) continue;
      (transBySource[seg.source_text] = transBySource[seg.source_text] ?? []).push(t.id);
    }
    expect(transBySource["Hello, World!"]?.length,
      "A2: must have exactly 3 translation rows for the duplicate-source line — TM dedupe phase depends on it",
    ).toBe(3);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B — Optimistic Locking single-segment
  // ════════════════════════════════════════════════════════════════════════

  test("B1: PUT with matching expected_updated_at succeeds (200) and bumps updated_at", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const tId = transBySource["Лінія один."]![0];

    const before = await getTranslation(page.request, headers, fileId, tId);
    const baselineUpdatedAt = before.updated_at;

    const res = await putHuman(page.request, headers, fileId, tId, "Линия раз.", baselineUpdatedAt);
    expect(res.status(),
      "B1: PUT with matching expected_updated_at must 200. " +
      "If 409, the optimistic-lock comparison is broken — the value we just READ doesn't match itself.",
    ).toBe(200);

    const after = await getTranslation(page.request, headers, fileId, tId);
    expect(after.updated_at,
      "B1: updated_at must advance after a successful PUT (proves the row was actually written, " +
      "not just that the handler returned 200 with no DB effect).",
    ).not.toBe(baselineUpdatedAt);
    expect(after.active_text, "B1: human_text must reflect the PUT body").toBe("Линия раз.");
  });

  test("B2: PUT with STALE expected_updated_at returns 409", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const tId = transBySource["Лінія один."]![0];

    // Stash the CURRENT updated_at (from B1's write). Then write again to
    // bump it. Then try a PUT with the now-stale stash.
    const beforeBump = await getTranslation(page.request, headers, fileId, tId);
    const staleStamp = beforeBump.updated_at;

    // Bump via a fresh PUT
    const bumpRes = await putHuman(page.request, headers, fileId, tId, "Линия раз — оновлено.", staleStamp);
    expect(bumpRes.status(), "B2 setup: bump PUT must succeed").toBe(200);

    // Now staleStamp is no longer the row's updated_at. PUT with it → 409.
    const conflictRes = await putHuman(
      page.request, headers, fileId, tId, "Це не повинно зберегтися.", staleStamp,
    );
    expect(conflictRes.status(),
      "B2 (core invariant): PUT with stale expected_updated_at must 409. " +
      "If 200, the optimistic-lock check skipped — the OTHER user's edit just got silently overwritten.",
    ).toBe(409);

    // Defensive: the row must NOT have been modified by the rejected PUT.
    const after = await getTranslation(page.request, headers, fileId, tId);
    expect(after.active_text,
      "B2: rejected PUT must NOT have written. If human_text contains the rejected payload, " +
      "the 409 was returned AFTER the write — worst-case bug (returns conflict but did the work).",
    ).not.toBe("Це не повинно зберегтися.");
    expect(after.active_text,
      "B2: human_text must still be the bump value, not the rejected payload",
    ).toBe("Линия раз — оновлено.");
  });

  test("B3: PUT WITHOUT expected_updated_at (legacy mode) succeeds even on stale baseline (200)", async ({ page }) => {
    // Documents the existing escape hatch — clients that don't track
    // updated_at fall back to last-writer-wins. Removing this would be a
    // breaking API change; we assert the current contract.
    await login(page);
    const headers = await authHeader(page);
    const tId = transBySource["Лінія один."]![0];
    const res = await putHuman(page.request, headers, fileId, tId, "Без перевірки версії.");
    // No expected_updated_at field at all
    expect(res.status(),
      "B3: omitting expected_updated_at preserves the legacy last-writer-wins semantics — must 200.",
    ).toBe(200);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C — Parallel concurrent saves on the SAME segment
  //
  // The "5 simultaneous PUTs with the same stale baseline" test is the
  // most concrete proof of the lost-update guard. Without optimistic locking,
  // all 5 would 200 and the last one's text would win. With OL, exactly
  // one wins (200) and four lose (409).
  // ════════════════════════════════════════════════════════════════════════

  test("C1: 5 parallel PUTs sharing one stale baseline → EXACTLY 1 succeeds, 4 conflict", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const tId = transBySource["Лінія два."]![0];

    // All 5 clients GET first, capturing the same baseline.
    const before = await getTranslation(page.request, headers, fileId, tId);
    const sharedBaseline = before.updated_at;

    // Fire 5 PUTs in parallel, each with a distinct payload so we can later
    // tell which one won.
    const payloads = ["варіант_1", "варіант_2", "варіант_3", "варіант_4", "варіант_5"];
    const responses = await Promise.all(
      payloads.map((p) => putHuman(page.request, headers, fileId, tId, p, sharedBaseline)),
    );

    const statuses = responses.map((r) => r.status());
    const wins = statuses.filter((s) => s === 200).length;
    const conflicts = statuses.filter((s) => s === 409).length;

    expect(wins,
      `C1 (core invariant): EXACTLY 1 of 5 parallel PUTs with the same stale baseline must win. ` +
      `Got ${wins} winners. >1 means optimistic locking is broken (lost-update bug). ` +
      `0 means something else broke (not optimistic locking — investigate).`,
    ).toBe(1);
    expect(conflicts,
      `C1: the other 4 must 409. Got ${conflicts}. Statuses: ${statuses.join(", ")}.`,
    ).toBe(4);

    // Verify the winning value matches the row's final state.
    const after = await getTranslation(page.request, headers, fileId, tId);
    const winningIdx = statuses.indexOf(200);
    expect(after.active_text,
      `C1: row's final human_text must be the winner's payload (${payloads[winningIdx]}). ` +
      `If it's a different value, two writers BOTH succeeded and the second silently overwrote — ` +
      `the lost-update bug optimistic locking prevents.`,
    ).toBe(payloads[winningIdx]);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D — TM Write-Back Race
  //
  // 3 translations with IDENTICAL source ("Hello, World!") confirmed in
  // parallel with the SAME target. The partial unique index added in
  // migration 014 (tm_entries WHERE created_by='human') must collapse all
  // 3 INSERTs into a single TM row.
  // ════════════════════════════════════════════════════════════════════════

  test("D1: 3 parallel confirms of duplicate-source segments → TM gets EXACTLY 1 row (not 3)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    const dupTransIds = transBySource["Hello, World!"]!;
    expect(dupTransIds.length,
      "D1 setup: must have 3 translations with the same source for the dedupe race",
    ).toBe(3);

    // Snapshot TM size before
    const beforeRes = await page.request.get(`${API}/api/tms/${tmId}/entries?source_lang=en&target_lang=uk-UA&limit=200`, { headers });
    const beforeEntries = (await beforeRes.json()).data as Array<{ source_text: string; target_text: string }>;
    const beforeHello = beforeEntries.filter((e) => e.source_text === "Hello, World!").length;

    // Fire all 3 confirms in parallel with the SAME target text.
    const sameTarget = "Привіт, світ!";
    const responses = await Promise.all(
      dupTransIds.map((tId) => putHuman(page.request, headers, fileId, tId, sameTarget)),
    );
    for (const r of responses) {
      expect(r.status(), "D1: all 3 parallel confirms must 200").toBe(200);
    }

    // Wait briefly for the async TM write-back to complete (SaveSegmentToTM
    // is called from the translation handler, but TM write may be deferred
    // through the pipeline). Poll instead of fixed sleep.
    let helloCount = 0;
    for (let attempt = 0; attempt < 20; attempt++) {
      const res = await page.request.get(`${API}/api/tms/${tmId}/entries?source_lang=en&target_lang=uk-UA&limit=200`, { headers });
      const entries = (await res.json()).data as Array<{ source_text: string; target_text: string }>;
      helloCount = entries.filter((e) => e.source_text === "Hello, World!" && e.target_text === sameTarget).length;
      if (helloCount > 0) break;
      await page.waitForTimeout(250);
    }

    expect(helloCount - beforeHello,
      `D1 (core invariant): 3 parallel confirms of duplicate-source segments with the SAME target ` +
      `must produce EXACTLY 1 new TM row (delta=1). Got delta=${helloCount - beforeHello}. ` +
      `delta=3 means the partial unique index on (source,target) WHERE created_by='human' is missing or ineffective. ` +
      `delta=0 means TM write-back didn't happen at all (different bug).`,
    ).toBe(1);
  });

  test("D2: confirming the SAME 3 segments with the SAME target a SECOND time stays at 1 row (idempotent)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const dupTransIds = transBySource["Hello, World!"]!;

    // Re-confirm with the same text — should be a no-op for TM size.
    const sameTarget = "Привіт, світ!";
    await Promise.all(dupTransIds.map((tId) => putHuman(page.request, headers, fileId, tId, sameTarget)));

    const res = await page.request.get(`${API}/api/tms/${tmId}/entries?source_lang=en&target_lang=uk-UA&limit=200`, { headers });
    const entries = (await res.json()).data as Array<{ source_text: string; target_text: string }>;
    const helloCount = entries.filter((e) => e.source_text === "Hello, World!" && e.target_text === sameTarget).length;
    expect(helloCount,
      `D2: re-confirming the same source+target must NOT add new TM rows. Got ${helloCount} ` +
      `for 'Hello, World!' → '${sameTarget}'. Anything > 1 means the dedupe ON CONFLICT clause ` +
      `was bypassed on the second write.`,
    ).toBe(1);
  });

  test("D3: confirming a duplicate-source segment with a DIFFERENT target overwrites the live row + archives the prior target to history", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const dupTransIds = transBySource["Hello, World!"]!;

    // Pick the third segment, change its target to a DIFFERENT translation.
    const differentTarget = "Здоров, мир!";
    const res = await putHuman(page.request, headers, fileId, dupTransIds[2], differentTarget);
    expect(res.status()).toBe(200);

    const tmRes = await page.request.get(`${API}/api/tms/${tmId}/entries?source_lang=en&target_lang=uk-UA&limit=200`, { headers });
    const entries = (await tmRes.json()).data as Array<{ source_text: string; target_text: string; id: number }>;
    const helloEntries = entries.filter((e) => e.source_text === "Hello, World!");

    // Human path dedupes on source. Same source +
    // different target must overwrite the existing row in place. The prior
    // target is moved to translation_memory_history (asserted below).
    expect(helloEntries.length,
      `D3: same source with a new target must collapse to 1 TM row (overwrite). ` +
      `Got ${helloEntries.length}. Multiple targets per source come only from TMX import.`,
    ).toBe(1);
    expect(helloEntries[0].target_text,
      "D3: latest confirm wins — survivor target should be the most-recent put",
    ).toBe(differentTarget);

    // History viewer must show the prior target (D1's "Привіт, світ!") as
    // a snapshot row with change_reason 'cat_confirm'.
    const histRes = await page.request.get(`${API}/api/tms/${tmId}/entries/${helloEntries[0].id}/history`, { headers });
    expect(histRes.status()).toBe(200);
    const history = (await histRes.json()).data as Array<{ target_text: string; change_reason: string }>;
    const priorTargets = history.map((h) => h.target_text);
    expect(priorTargets,
      "D3: prior target must appear in the history audit log",
    ).toContain("Привіт, світ!");
    expect(history.find((h) => h.target_text === "Привіт, світ!")?.change_reason).toBe("cat_confirm");
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase E — Parallel different-segment confirms
  //
  // 4 unique segments confirmed in parallel — all should succeed with no
  // optimistic-lock interference (they're different rows). TM should grow
  // by exactly 4. This is the negative control for Phase C.
  // ════════════════════════════════════════════════════════════════════════

  test("E1: 4 parallel confirms on DIFFERENT segments → all 4 succeed, TM grows by 4", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    // Snapshot TM count before
    const beforeRes = await page.request.get(`${API}/api/tms/${tmId}/entries?source_lang=en&target_lang=uk-UA&limit=200`, { headers });
    const beforeCount = (await beforeRes.json()).data.length;

    const sources = ["Унікальний 6.", "Унікальний 7.", "Унікальний 8.", "Унікальний 9."];
    const targets = ["Шість", "Сім", "Вісім", "Дев'ять"];
    const tIds = sources.map((s) => transBySource[s]![0]);

    const responses = await Promise.all(
      tIds.map((tId, i) => putHuman(page.request, headers, fileId, tId, targets[i])),
    );
    for (let i = 0; i < responses.length; i++) {
      expect(responses[i].status(),
        `E1: parallel confirm of different segments — index ${i} must 200 (no inter-segment lock collisions). ` +
        `Got ${responses[i].status()}. If 409, optimistic locking is over-eager (locks rows it shouldn't).`,
      ).toBe(200);
    }

    // Poll TM until we see all 4 new entries (or timeout).
    let observedDelta = 0;
    for (let attempt = 0; attempt < 20; attempt++) {
      const res = await page.request.get(`${API}/api/tms/${tmId}/entries?source_lang=en&target_lang=uk-UA&limit=200`, { headers });
      observedDelta = (await res.json()).data.length - beforeCount;
      if (observedDelta >= 4) break;
      await page.waitForTimeout(250);
    }
    expect(observedDelta,
      `E1: TM must grow by exactly 4 new entries (one per unique source). Got delta=${observedDelta}. ` +
      `delta < 4: TM write-back lost some confirms (worse than duplicates — outright drops). ` +
      `delta > 4: phantom writes from a prior phase leaked in (test isolation issue OR real over-write bug).`,
    ).toBe(4);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase F — Cleanup
  // ════════════════════════════════════════════════════════════════════════

  test("F1: cleanup all resources", async ({ page }) => {
    await login(page);
    const cleanup = async (fn: () => Promise<void>) => { try { await fn(); } catch {} };
    if (projectId) await cleanup(() => deleteProjectViaAPI(page, projectId));
    if (tmId) await cleanup(() => deleteTMViaAPI(page, tmId));
    if (mtEngineId) await cleanup(() => deleteMTEngineViaAPI(page, mtEngineId));
  });

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    try {
      await login(page);
      if (projectId) await deleteProjectViaAPI(page, projectId).catch(() => {});
      if (tmId) await deleteTMViaAPI(page, tmId).catch(() => {});
      if (mtEngineId) await deleteMTEngineViaAPI(page, mtEngineId).catch(() => {});
    } catch {} finally {
      await page.close();
    }
  });
});
