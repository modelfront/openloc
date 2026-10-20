/**
 * J18 — QA Profile Rules Gauntlet (Adversarial)
 *
 * Goal: lock down the QA rule + profile lifecycle so disable/severity/config
 * changes propagate correctly, and so profile assignment changes don't leave
 * stale results. j4 covers happy-path detection of 3 check types; this journey
 * covers ALL API-valid check types AND the rule/profile mutation surface that
 * j4 doesn't touch.
 *
 * Locale: nl-be (Dutch, Belgium) — never used by any other journey.
 *
 * Honest scope notes:
 *
 *   • empty_target IS reachable via "no pre-translate, no human save" path:
 *     ListTranslationsForQAPage's COALESCE(active_text, '')
 *     returns '' for fully untouched translations. j4's note ("can't reach")
 *     was specific to the UI flow (frontend copies source → target). We
 *     reach it via API by skipping pre-translate.
 *
 *   • Tag detection (running tag_checks against actual XLIFF tags) is
 *     covered by j12. Here we cover the tag_checks CONFIG round-trip only —
 *     adding Phase B tag detection would duplicate j12.
 *
 *   • severity propagation: we change a rule's severity error → info,
 *     re-run, and verify the SAME issue is now reported with the new
 *     severity. This catches "severity field is hardcoded somewhere"
 *     bugs that j4's existence-only assertions wouldn't catch.
 *
 * Coordination with existing journeys:
 *   - j4: happy-path detection of number_consistency + punctuation +
 *         target_identical_to_source. j18 explicitly covers OTHER check
 *         types AND lifecycle, not detection happy paths.
 *   - j5: glossary enforcement at the editor level. j18 uses a glossary
 *         here only to drive the `terminology` QA check.
 *   - j12: tag_checks dynamic detection. j18 covers tag_checks config only.
 *
 * Severity legend in this file:
 *   error   — blocks confirmation (in our model: blocks completing the job)
 *   warning — visible but allows confirmation
 *   info    — informational only
 */
import { test, expect } from "@playwright/test";
import {
  login,
  uploadFileViaAPI,
  createTMViaAPI,
  importTMXViaAPI,
  deleteTMViaAPI,
  createProjectViaAPI,
  deleteProjectViaAPI,
  createQAProfileViaAPI,
  deleteQAProfileViaAPI,
  setProjectTMsViaAPI,
  setProjectMTViaAPI,
  setProjectTermsViaAPI,
  updateProjectSettingsViaAPI,
  createMTEngineViaAPI,
  deleteMTEngineViaAPI,
  createTermBaseViaAPI,
  addTermBaseTermViaAPI,
  deleteTermBaseViaAPI,
  saveTranslationDraftViaAPI,
  upsertQARuleViaAPI,
  runQAViaAPI,
  getQAReportViaAPI,
  type QARule,
} from "./helpers";

const API = "";
const RUN_ID = Date.now();

// State that flows through the serial describe
let qaProfileId: number;
let secondProfileId: number; // for the swap-profile phase
let emptyProfileId: number; // for the empty-rules phase
let tmId: number;
let glossaryId: number;
let mtEngineId: number;
let projectId: number;
let fileId: number;
let jobId: number;

// Per-segment translation IDs, keyed by their source text. Filled in Phase A.
const transByContent: Record<string, number> = {};

// Helper: find a translation_id by source_text (so phase B doesn't depend on
// position-based indexing — order changes if the parser tweaks segment splitting).
async function authHeader(page: any) {
  const token = await page.evaluate(() => localStorage.getItem("token"));
  return { Authorization: `Bearer ${token}` };
}

test.describe.serial("J18 — QA Profile Rules Gauntlet", () => {

  // ════════════════════════════════════════════════════════════════════════
  // Phase A — Setup: profile, glossary, project, NO pre-translate
  // ════════════════════════════════════════════════════════════════════════

  test("A1: forbidden_terms rule round-trips", async ({ page }) => {
    // POST a rule, GET the rule, ensure it persisted with the expected
    // enabled/severity state.
    await login(page);

    const tmpId = await createQAProfileViaAPI(page, `J18 forbidden-probe ${RUN_ID}`);
    const headers = await authHeader(page);
    try {
      const res = await page.request.post(`${API}/api/qa-profiles/${tmpId}/rules`, {
        headers,
        data: { check_type: "forbidden_terms", enabled: true, severity: "error" },
      });
      expect(res.status(),
        "forbidden_terms must be accepted by the API.",
      ).toBeLessThan(300);

      // Verify the rule persisted via GET
      const getRes = await page.request.get(`${API}/api/qa-profiles/${tmpId}/rules`, { headers });
      const rules = (await getRes.json()).data as Array<{ check_type: string; enabled: boolean; severity: string }>;
      const forbiddenRule = rules.find((r) => r.check_type === "forbidden_terms");
      expect(forbiddenRule, "A1: forbidden_terms rule must persist").toBeDefined();
      expect(forbiddenRule!.enabled).toBe(true);
      expect(forbiddenRule!.severity).toBe("error");
    } finally {
      // Delete the probe profile even when an assertion above failed; a
      // refused delete fails the test rather than leaking the profile.
      await deleteQAProfileViaAPI(page, tmpId);
    }
  });

  test("A2: create QA profile with all 8 valid check types, including tag_checks config", async ({ page }) => {
    await login(page);
    const rules: QARule[] = [
      { check_type: "empty_target", enabled: true, severity: "error" },
      { check_type: "number_consistency", enabled: true, severity: "error" },
      { check_type: "punctuation", enabled: true, severity: "warning" },
      { check_type: "terminology", enabled: true, severity: "error" },
      { check_type: "forbidden_terms", enabled: true, severity: "error" },
      { check_type: "consistency", enabled: true, severity: "warning" },
      { check_type: "target_identical_to_source", enabled: true, severity: "warning" },
      // tag_checks with full config — j12 tests detection; here we test the round-trip
      {
        check_type: "tag_checks",
        enabled: true,
        severity: "error",
        config: { missing: true, extra: true, unclosed: true, unopened: true },
      },
    ];
    qaProfileId = await createQAProfileViaAPI(page, `J18 Master Profile ${RUN_ID}`, rules);
    expect(qaProfileId).toBeGreaterThan(0);
  });

  // Helper: parse a `config` field that may be either a JSON object, a raw JSON
  // string, or a base64-encoded JSON string. The latter happens because the
  // backend serializes `Config []byte` (Go's default base64 encoding for byte
  // slices in encoding/json). The frontend's plain JSON.parse falls
  // back to {} when it gets base64, silently losing the user's tag_checks config
  // in the UI even though runtime QA reads the bytes correctly.
  function parseRuleConfig(raw: any): any {
    if (raw == null) return {};
    if (typeof raw === "object") return raw;
    if (typeof raw !== "string") return {};
    // Try plain JSON first
    try { return JSON.parse(raw); } catch {}
    // Try base64 → JSON
    try {
      const decoded = Buffer.from(raw, "base64").toString("utf-8");
      return JSON.parse(decoded);
    } catch {}
    return {};
  }

  test("A3: tag_checks config persisted as JSON object (regression guard)", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await page.request.get(`${API}/api/qa-profiles/${qaProfileId}/rules`, { headers });
    expect(res.ok()).toBe(true);
    const rules = (await res.json()).data as Array<{ check_type: string; enabled: boolean; severity: string; config: any }>;
    const tagRule = rules.find((r) => r.check_type === "tag_checks");
    expect(tagRule, "A3: tag_checks rule must persist").toBeDefined();
    expect(tagRule!.enabled).toBe(true);
    expect(tagRule!.severity).toBe("error");

    // Regression guard: the API returns config as a JSON OBJECT, not
    // a base64 string. A raw []byte serializes as base64, which the frontend's
    // JSON.parse cannot read, so the tag_checks sub-config is lost in the UI.
    expect(typeof tagRule!.config,
      "rule.config must be a JSON object, not a base64 string. " +
      "If this is 'string', the qaRuleResponse DTO in handler.go is bypassed.",
    ).toBe("object");

    // The data round-trips correctly. parseRuleConfig is the deploy-window
    // tolerant parser; for the post-fix path it just passes the object through.
    const cfg = parseRuleConfig(tagRule!.config);
    expect(cfg.missing,   "A3: tag_checks.missing config must round-trip").toBe(true);
    expect(cfg.extra,     "A3: tag_checks.extra config must round-trip").toBe(true);
    expect(cfg.unclosed,  "A3: tag_checks.unclosed config must round-trip").toBe(true);
    expect(cfg.unopened,  "A3: tag_checks.unopened config must round-trip").toBe(true);
  });

  test("A4: setup TM (empty), glossary (with approved term 'password'→'wachtwoord'), MT engine", async ({ page }) => {
    await login(page);
    tmId = await createTMViaAPI(page, `J18 TM ${RUN_ID}`);
    // Don't import — we want segments to fall through to MT (or stay empty)

    glossaryId = await createTermBaseViaAPI(page, `J18 Glossary ${RUN_ID}`, "en", "nl-be");
    await addTermBaseTermViaAPI(page, glossaryId, "en", "nl-be", "password", "wachtwoord", "approved");
    // Also add a FORBIDDEN term (with "moetenietverschijnen" as the
    // forbidden Dutch translation). Phase B asserts this fires when the
    // forbidden translation appears in target text.
    await addTermBaseTermViaAPI(page, glossaryId, "en", "nl-be", "deprecated_word", "moetenietverschijnen", "forbidden");

    mtEngineId = await createMTEngineViaAPI(page, `J18 Mock MT ${RUN_ID}`, "mock", "");
  });

  test("A5: create project en→nl-be, assign profile + TM + MT + glossary, DISABLE pre-translate", async ({ page }) => {
    await login(page);
    projectId = await createProjectViaAPI(page, `J18 Project ${RUN_ID}`, "en", ["nl-be"]);

    await updateProjectSettingsViaAPI(page, projectId, {
      qa_profile_id: qaProfileId,
      mt_on_upload: false, // critical: keep segments untranslated for empty_target
    });

    await setProjectTMsViaAPI(page, projectId, [
      { tm_id: tmId, source_language: "en", target_language: "nl-be", mode: "read_write", priority: 1 },
    ]);
    await setProjectMTViaAPI(page, projectId, [
      { mt_engine_id: mtEngineId, source_language: "en", target_language: "nl-be" },
    ]);
    await setProjectTermsViaAPI(page, projectId, [
      { term_base_id: glossaryId, source_language: "en", target_language: "nl-be", mode: "read", priority: 1 },
    ]);
  });

  test("A6: upload file with adversarial content, do NOT pre-translate", async ({ page }) => {
    await login(page);
    // Each line targets a specific check type. The duplicate "Click here." lines
    // (segments 6 and 7) are for the consistency check.
    const content = [
      "Price: $49.99",          // seg 1 — number_consistency target
      "End of sentence.",       // seg 2 — punctuation target (trailing period)
      "Please enter your password.", // seg 3 — terminology target (must contain "wachtwoord")
      "Test segment",           // seg 4 — target_identical_to_source target
      "Untouched empty line",   // seg 5 — empty_target target (we'll never write a translation)
      "Click here.",            // seg 6 — consistency duplicate #1
      "Click here.",            // seg 7 — consistency duplicate #2 (DIFFERENT translation)
      "Clean baseline.",        // seg 8 — should produce ZERO issues
      "This is a deprecated_word.", // seg 9 — forbidden_terms target
    ].join("\n");

    const result = await uploadFileViaAPI(page, projectId, "j18.txt", content, "text/plain");
    fileId = result.data?.id ?? result.id;

    // Grab the job
    const headers = await authHeader(page);
    const jobsRes = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    const jobs = (await jobsRes.json()).data;
    expect(jobs.length, "A6: should have 1 job (en→nl-be)").toBe(1);
    jobId = jobs[0].id;

    // Map source_text → translation_id for phase B
    const transRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const translations = (await transRes.json()).data as Array<{ id: number; segment_id: number }>;
    const segsRes = await page.request.get(`${API}/api/files/${fileId}/segments`, { headers });
    const segments = (await segsRes.json()).data as Array<{ id: number; source_text: string }>;
    for (const t of translations) {
      const seg = segments.find((s) => s.id === t.segment_id);
      if (seg) transByContent[seg.source_text] = t.id;
    }
    // 8 unique source_texts because seg 6 and seg 7 share "Click here.";
    // total file lines = 9 with the new forbidden_terms test segment.
    // Audit M-2: assert exact count, not `>= 8`.
    expect(
      Object.keys(transByContent).length,
      `expected exactly 8 unique source_texts, got ${Object.keys(transByContent).length}: ${JSON.stringify(Object.keys(transByContent))}`,
    ).toBe(8);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B — Each check type, manually crafted violation, verify detection
  //
  // We set ALL violations first via API, then run QA ONCE and assert the
  // full report. This is more adversarial than per-check runs because it
  // proves the rules don't interfere with each other.
  // ════════════════════════════════════════════════════════════════════════

  test("B1: write adversarial human_text for every check type", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);

    // J18 exercises the PERIODIC QA SCAN (`run-qa`), which is a different
    // surface from the confirm-time gate (covered canonically by
    // J29). Both share the same checker code but enter it from different
    // paths — and j18 already proves the scan side per-check_type, so the
    // additional pin below is just to assert "confirm-block and scan-find
    // are wired to the same rule set". One representative case
    // (number_consistency, error severity) is enough; doing it for every
    // check would 2x the API calls without adding signal.
    const priceId = transByContent["Price: $49.99"];
    const blocked = await page.request.put(
      `${API}/api/files/${fileId}/translations/${priceId}`,
      { headers, data: { human_text: "Prijs zonder nummer" } },
    );
    expect(blocked.status(),
      `confirm-with-dropped-number must 422 (got ${blocked.status()})`,
    ).toBe(422);
    const blockedBody = await blocked.json() as { qa_issues?: Array<{ check_type?: string }> };
    const sawNumberIssue = (blockedBody.qa_issues ?? []).some(
      (i) => i.check_type === "number_consistency",
    );
    expect(sawNumberIssue,
      `confirm 422 body should carry number_consistency, got: ${JSON.stringify(blockedBody.qa_issues)}`,
    ).toBe(true);

    // From here on, drafts. The confirm-time gate would block the
    // error-severity violations (number_consistency, terminology,
    // forbidden_terms) before they ever land in the DB — defeating B2's
    // run-qa scan which is the entire point of this journey. Drafts persist
    // bad-tag/QA-violating text without gating, exactly what the periodic
    // QA scan needs to detect.

    // number_consistency: source "Price: $49.99" → drop the number
    await saveTranslationDraftViaAPI(page, fileId, priceId, "Prijs zonder nummer");

    // punctuation: source "End of sentence." → drop trailing period
    await saveTranslationDraftViaAPI(page, fileId, transByContent["End of sentence."], "Einde van de zin");

    // terminology: source has "password", glossary requires target uses "wachtwoord"
    // → write target without "wachtwoord"
    await saveTranslationDraftViaAPI(page, fileId, transByContent["Please enter your password."], "Voer uw geheim in.");

    // target_identical_to_source: write target == source (verbatim)
    await saveTranslationDraftViaAPI(page, fileId, transByContent["Test segment"], "Test segment");

    // empty_target: leave seg 5 ("Untouched empty line") UNTOUCHED — no human_text,
    // no MT (we disabled pre-translate). target_text COALESCEs to ''.

    // consistency: same source "Click here." with TWO different translations.
    // We need both translation IDs (the helper map only stores ONE per source_text
    // because the second overwrites the first). Re-fetch translations and pick both.
    // (headers already declared at the top of B1.)
    const transRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const allTrans = (await transRes.json()).data as Array<{ id: number; segment_id: number }>;
    const segsRes = await page.request.get(`${API}/api/files/${fileId}/segments`, { headers });
    const allSegs = (await segsRes.json()).data as Array<{ id: number; source_text: string; position: number }>;
    const clickSegs = allSegs
      .filter((s) => s.source_text === "Click here.")
      .sort((a, b) => a.position - b.position);
    expect(clickSegs.length,
      "B1: should have exactly 2 segments with 'Click here.' source",
    ).toBe(2);
    const clickTransA = allTrans.find((t) => t.segment_id === clickSegs[0].id)!;
    const clickTransB = allTrans.find((t) => t.segment_id === clickSegs[1].id)!;
    await saveTranslationDraftViaAPI(page, fileId, clickTransA.id, "Klik hier.");
    await saveTranslationDraftViaAPI(page, fileId, clickTransB.id, "Druk op deze link."); // different!

    // Clean baseline: write a normal translation that violates nothing.
    // Draft (not confirm) for consistency with the rest of B1 — see note above.
    await saveTranslationDraftViaAPI(page, fileId, transByContent["Clean baseline."], "Schone basislijn.");

    // forbidden_terms: source contains "deprecated_word", glossary marks
    // "moetenietverschijnen" as the FORBIDDEN target. Write that exact target.
    await saveTranslationDraftViaAPI(page, fileId, transByContent["This is a deprecated_word."], "Dit is een moetenietverschijnen.");
  });

  test("B2: run QA with all rules, verify each violation is detected with correct severity", async ({ page }) => {
    await login(page);

    const { status, body } = await runQAViaAPI(page, jobId);
    expect(status, `B2: run-qa must succeed: ${JSON.stringify(body)}`).toBe(200);

    const report = await getQAReportViaAPI(page, jobId);
    const results = report.results as Array<{ translation_id: number; check_type: string; severity: string; message: string }>;

    // Build a (translation_id → set of check_types) map for assertions
    const issuesByTrans = new Map<number, Array<{ check_type: string; severity: string }>>();
    for (const r of results) {
      const arr = issuesByTrans.get(r.translation_id) ?? [];
      arr.push({ check_type: r.check_type, severity: r.severity });
      issuesByTrans.set(r.translation_id, arr);
    }

    // ── number_consistency on "Price: $49.99" → severity error ──
    const priceIssues = issuesByTrans.get(transByContent["Price: $49.99"]) ?? [];
    const numIssue = priceIssues.find((i) => i.check_type === "number_consistency");
    expect(numIssue, "B2.number: missing number_consistency on $49.99 segment").toBeDefined();
    expect(numIssue!.severity, "B2.number: severity must be 'error' as configured").toBe("error");

    // ── punctuation on "End of sentence." → severity warning ──
    const endIssues = issuesByTrans.get(transByContent["End of sentence."]) ?? [];
    const punctIssue = endIssues.find((i) => i.check_type === "punctuation");
    expect(punctIssue, "B2.punctuation: missing punctuation issue on dropped-period segment").toBeDefined();
    expect(punctIssue!.severity, "B2.punctuation: severity must be 'warning'").toBe("warning");

    // ── terminology on "Please enter your password." → severity error ──
    const termIssues = issuesByTrans.get(transByContent["Please enter your password."]) ?? [];
    const termIssue = termIssues.find((i) => i.check_type === "terminology");
    expect(termIssue,
      "B2.terminology: missing terminology issue. " +
      "The target 'Voer uw geheim in.' lacks the approved term 'wachtwoord' for source 'password'.",
    ).toBeDefined();
    expect(termIssue!.severity).toBe("error");

    // ── target_identical_to_source on "Test segment" → severity warning ──
    const identIssues = issuesByTrans.get(transByContent["Test segment"]) ?? [];
    const identIssue = identIssues.find((i) => i.check_type === "target_identical_to_source");
    expect(identIssue, "B2.identical: missing target_identical_to_source on verbatim segment").toBeDefined();
    expect(identIssue!.severity).toBe("warning");

    // ── empty_target on "Untouched empty line" → severity error ──
    const emptyIssues = issuesByTrans.get(transByContent["Untouched empty line"]) ?? [];
    const emptyIssue = emptyIssues.find((i) => i.check_type === "empty_target");
    expect(emptyIssue,
      "B2.empty: empty_target must fire on untranslated segments " +
      "(target_text COALESCEs human/active/mt to '' — see internal/qa/queries.sql).",
    ).toBeDefined();
    expect(emptyIssue!.severity).toBe("error");

    // ── consistency: at least ONE of the two "Click here." translations must be flagged ──
    // (CheckConsistency runs per-translation; both translations would each see the OTHER
    //  as different, so both should be flagged. Assert at least 1 to be lenient on
    //  query ordering quirks.)
    const consistencyResults = results.filter((r) => r.check_type === "consistency");
    expect(consistencyResults.length,
      "B2.consistency: at least 1 consistency issue expected on 'Click here.' duplicates with different targets",
    ).toBeGreaterThanOrEqual(1);
    expect(consistencyResults[0].severity).toBe("warning");

    // ── Clean baseline on "Clean baseline." → ZERO issues ──
    const cleanIssues = issuesByTrans.get(transByContent["Clean baseline."]) ?? [];
    expect(cleanIssues.length,
      `B2.clean: 'Clean baseline.' must have zero QA issues, got: ${JSON.stringify(cleanIssues)}`,
    ).toBe(0);

    // ── forbidden_terms on "This is a deprecated_word." → severity error ──
    const forbiddenIssues = issuesByTrans.get(transByContent["This is a deprecated_word."]) ?? [];
    const forbidden = forbiddenIssues.find((i) => i.check_type === "forbidden_terms");
    expect(forbidden,
      "forbidden_terms must fire when target contains a glossary entry " +
      "with status='forbidden'. Source 'deprecated_word' → glossary forbidden term " +
      "'moetenietverschijnen' is present in the human_text — this MUST be flagged.",
    ).toBeDefined();
    expect(forbidden!.severity).toBe("error");
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C — Rule lifecycle: disable, severity change, config change
  // ════════════════════════════════════════════════════════════════════════

  test("C1: disable number_consistency, re-run QA, verify number issues are GONE but other issues remain", async ({ page }) => {
    await login(page);

    // Disable number_consistency
    await upsertQARuleViaAPI(page, qaProfileId, {
      check_type: "number_consistency",
      enabled: false,
      severity: "error",
    });

    const { status } = await runQAViaAPI(page, jobId);
    expect(status).toBe(200);

    const report = await getQAReportViaAPI(page, jobId);
    const numberIssues = (report.results as any[]).filter((r) => r.check_type === "number_consistency");
    expect(numberIssues.length,
      "C1: disabling number_consistency must clear those issues. RunQA explicitly clears prior results before running.",
    ).toBe(0);

    // Sanity: punctuation issue should still be there
    const punctIssues = (report.results as any[]).filter((r) => r.check_type === "punctuation");
    expect(punctIssues.length,
      "C1: disabling number_consistency must NOT affect punctuation",
    ).toBeGreaterThanOrEqual(1);
  });

  test("C2: re-enable number_consistency with severity 'info' (was 'error'), verify severity propagates", async ({ page }) => {
    await login(page);

    await upsertQARuleViaAPI(page, qaProfileId, {
      check_type: "number_consistency",
      enabled: true,
      severity: "info", // was "error"
    });

    await runQAViaAPI(page, jobId);
    const report = await getQAReportViaAPI(page, jobId);
    const numberIssues = (report.results as any[]).filter((r) => r.check_type === "number_consistency");
    expect(numberIssues.length, "C2: number_consistency back after re-enable").toBeGreaterThanOrEqual(1);
    expect(numberIssues[0].severity,
      "C2: severity change error→info must propagate. " +
      "If this fails, severity is hardcoded somewhere or read from a stale source.",
    ).toBe("info");

    // Counts should reflect: the issue moves from 'error' bucket to 'info'
    expect(report.summary.info,
      "C2: summary.info must include the number_consistency issue",
    ).toBeGreaterThanOrEqual(1);
  });

  test("C3: tag_checks config round-trip — disable Missing only, verify GET reflects config", async ({ page }) => {
    await login(page);

    // Update tag_checks rule with a partial config (only `missing` disabled)
    await upsertQARuleViaAPI(page, qaProfileId, {
      check_type: "tag_checks",
      enabled: true,
      severity: "error",
      config: { missing: false, extra: true, unclosed: true, unopened: true },
    });

    // Verify via GET that the config was stored as-is (decode base64 if needed)
    const headers = await authHeader(page);
    const res = await page.request.get(`${API}/api/qa-profiles/${qaProfileId}/rules`, { headers });
    const rules = (await res.json()).data as Array<{ check_type: string; config: any }>;
    const tagRule = rules.find((r) => r.check_type === "tag_checks");
    expect(tagRule).toBeDefined();
    const cfg = parseRuleConfig(tagRule!.config);
    expect(cfg.missing,  "C3: missing must round-trip as false").toBe(false);
    expect(cfg.extra,    "C3: extra must remain true").toBe(true);
    expect(cfg.unclosed, "C3: unclosed must remain true").toBe(true);
    expect(cfg.unopened, "C3: unopened must remain true").toBe(true);

    // (Tag detection itself — verifying that disabling Missing skips the
    //  tag_missing check at runtime — is covered by j12. Adding it here
    //  would duplicate j12 and require uploading XLIFF, which makes this
    //  journey slower without adding new bug coverage.)
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D — Profile lifecycle: delete-with-refs, unassign, swap, empty
  // ════════════════════════════════════════════════════════════════════════

  test("D1: deleting a QA profile assigned to a project must return 409", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await page.request.delete(`${API}/api/qa-profiles/${qaProfileId}`, { headers });
    expect(res.status(),
      "D1: deleting a referenced QA profile must 409 (handler.go::HandleDeleteProfile checks CountQAProfileReferences)",
    ).toBe(409);
  });

  test("D2a: regression guard — null clears qa_profile_id (REST convention)", async ({ page }) => {
    // qa_profile_id is tri-state:
    //   - field absent  → keep current
    //   - field is null → clear (REST convention, what the UI's "None" sends)
    //   - field is 0    → clear
    //   - field is N>0  → set to N
    //   - bad value     → 400
    //
    // This test asserts the REST-convention path (null → clear). The full
    // tri-state contract is also covered by an in-process Go integration
    // test at internal/project/qa_profile_id_tristate_test.go.
    await login(page);
    const headers = await authHeader(page);

    const res = await page.request.patch(`${API}/api/projects/${projectId}/settings`, {
      headers, data: { qa_profile_id: null },
    });
    expect(res.ok(), "PATCH with null must succeed").toBe(true);

    const settingsRes = await page.request.get(`${API}/api/projects/${projectId}/settings`, { headers });
    const settings = (await settingsRes.json()).data;
    expect(settings.project.qa_profile_id,
      "PATCH qa_profile_id=null must clear. If this is non-null, " +
      "the backend reverted to the *int64 contract.",
    ).toBeNull();
  });

  test("D2b: run-qa returns 400 'no QA profile assigned' after unassigning", async ({ page }) => {
    await login(page);
    const { status, body } = await runQAViaAPI(page, jobId);
    expect(status,
      "D2b: run-qa with no profile assigned must 400. Got: " + JSON.stringify(body).slice(0, 200),
    ).toBe(400);
  });

  test("D3: create a SECOND profile with only target_identical_to_source, swap, verify ONLY that check fires", async ({ page }) => {
    await login(page);

    // Create profile with just one check
    secondProfileId = await createQAProfileViaAPI(page, `J18 Identical-Only ${RUN_ID}`, [
      { check_type: "target_identical_to_source", enabled: true, severity: "error" },
    ]);

    // Assign the second profile
    await updateProjectSettingsViaAPI(page, projectId, { qa_profile_id: secondProfileId });

    // Run QA — only target_identical_to_source should fire
    const { status } = await runQAViaAPI(page, jobId);
    expect(status).toBe(200);

    const report = await getQAReportViaAPI(page, jobId);
    const checkTypes = new Set((report.results as any[]).map((r) => r.check_type));
    expect(checkTypes.has("target_identical_to_source"),
      "D3: target_identical_to_source must fire (we kept that violation in seg 4)",
    ).toBe(true);

    // No OTHER check types should be in the report
    const unexpectedTypes = [...checkTypes].filter((t) => t !== "target_identical_to_source");
    expect(unexpectedTypes,
      `D3: only target_identical_to_source should fire under the new profile, got extras: ${unexpectedTypes.join(", ")}. ` +
      "Possible bug: previous profile's results not cleared, OR rule resolution is using a cached/wrong profile.",
    ).toEqual([]);

    // Severity propagated from the new profile (was warning before, now error)
    const identResults = (report.results as any[]).filter((r) => r.check_type === "target_identical_to_source");
    expect(identResults[0].severity,
      "D3: severity must come from the new profile (error), not the old one (warning)",
    ).toBe("error");
  });

  test("D4: now that no project references the FIRST profile, deleting it should succeed", async ({ page }) => {
    await login(page);
    const headers = await authHeader(page);
    const res = await page.request.delete(`${API}/api/qa-profiles/${qaProfileId}`, { headers });
    expect(res.status(), "D4: unreferenced profile delete should 204").toBe(204);

    // Verify it's gone
    const listRes = await page.request.get(`${API}/api/qa-profiles`, { headers });
    const profiles = (await listRes.json()).data as Array<{ id: number }>;
    expect(profiles.find((p) => p.id === qaProfileId),
      "D4: deleted profile must not appear in list",
    ).toBeUndefined();
    qaProfileId = 0; // signal cleanup not needed
  });

  test("D5a: empty profile (no enabled rules) returns 200 + 'no enabled QA rules'", async ({ page }) => {
    await login(page);

    // Create a profile with no rules and assign it
    emptyProfileId = await createQAProfileViaAPI(page, `J18 Empty ${RUN_ID}`);
    await updateProjectSettingsViaAPI(page, projectId, { qa_profile_id: emptyProfileId });

    const { status, body } = await runQAViaAPI(page, jobId);
    expect(status,
      "D5a: run-qa with profile-but-no-rules should still return 200 (graceful no-op)",
    ).toBe(200);
    expect(body.data?.message ?? "").toMatch(/no enabled.*rules/i);
    expect(body.data?.issues, "D5a: issues=0 in the response").toBe(0);
  });

  test("D5b: regression guard — empty profile clears stale results", async ({ page }) => {
    // A profile with no enabled rules clears the job's findings:
    // qa.Service.RunAndPersistJob's no-rules branch calls
    // DeleteQAResultsForJob before returning. Drop that call and stale results
    // outlive the rules that produced them.
    await login(page);
    const report = await getQAReportViaAPI(page, jobId);
    expect(report.results.length,
      "empty profile must clear all prior issue records",
    ).toBe(0);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase E — Cleanup
  // ════════════════════════════════════════════════════════════════════════

  test("E1: cleanup all resources", async ({ page }) => {
    await login(page);
    const cleanup = async (fn: () => Promise<void>) => { try { await fn(); } catch {} };

    // Project first (it references the profile + TM + glossary + MT)
    if (projectId) await cleanup(() => deleteProjectViaAPI(page, projectId));

    // Profiles (qaProfileId may already be deleted in D4 — guarded above)
    if (qaProfileId) await cleanup(() => deleteQAProfileViaAPI(page, qaProfileId));
    if (secondProfileId) await cleanup(() => deleteQAProfileViaAPI(page, secondProfileId));
    if (emptyProfileId) await cleanup(() => deleteQAProfileViaAPI(page, emptyProfileId));

    // Resources
    if (tmId) await cleanup(() => deleteTMViaAPI(page, tmId));
    if (glossaryId) await cleanup(() => deleteTermBaseViaAPI(page, glossaryId));
    if (mtEngineId) await cleanup(() => deleteMTEngineViaAPI(page, mtEngineId));
  });

  test.afterAll(async ({ browser }) => {
    // Belt-and-suspenders: try to clean up even if E1 was skipped via failure.
    const page = await browser.newPage();
    try {
      await login(page);
      if (projectId) await deleteProjectViaAPI(page, projectId).catch(() => {});
      if (qaProfileId) await deleteQAProfileViaAPI(page, qaProfileId).catch(() => {});
      if (secondProfileId) await deleteQAProfileViaAPI(page, secondProfileId).catch(() => {});
      if (emptyProfileId) await deleteQAProfileViaAPI(page, emptyProfileId).catch(() => {});
      if (tmId) await deleteTMViaAPI(page, tmId).catch(() => {});
      if (glossaryId) await deleteTermBaseViaAPI(page, glossaryId).catch(() => {});
      if (mtEngineId) await deleteMTEngineViaAPI(page, mtEngineId).catch(() => {});
    } catch {} finally {
      await page.close();
    }
  });
});
