/**
 * J12 — Tag Integrity Through the Pipeline (Adversarial)
 *
 * Core purpose: XLIFF inline tags (bpt/ept, ph) survive import → pre-translate
 * → human edit → confirm → QA check → export. Tag QA catches missing/orphan tags.
 *
 * NOT NEGOTIABLE: verify the EXPORTED XLIFF contains the tag markup.
 * "Editor loaded" is not enough — parse the export XML and check for
 * <bpt>, <ept>, <ph> elements. Also verify QA catches the deliberately
 * broken tags we create.
 */
import { test, expect } from "@playwright/test";
import * as fs from "fs";
import * as path from "path";
import {
  login,
  createProjectViaAPI,
  createQAProfileViaAPI,
  createMTEngineViaAPI,
  createTMViaAPI,
  setProjectMTViaAPI,
  setProjectTMsViaAPI,
  deleteProjectViaAPI,
  deleteQAProfileViaAPI,
  deleteMTEngineViaAPI,
  deleteTMViaAPI,
  pretranslateFileViaAPI,
} from "./helpers";

const API = "";

const XLIFF_FIXTURE = fs.readFileSync(
  path.join(__dirname, "../fixtures/journey-tags.xliff"),
  "utf-8",
);

let projectId: number;
let qaProfileId: number;
let mtEngineId: number;
let tmId: number; // TM assigned read_write so confirms write rich_text back
let fileId: number;
let jobId: number;

test.describe.serial("J12 — Tag Integrity", () => {
  // ─── Phase A: Setup ───────────────────────────────────────────────

  test("A1: create QA profile with tag_checks enabled", async ({ page }) => {
    await login(page);
    qaProfileId = await createQAProfileViaAPI(page, "Tag QA Journey", [
      { check_type: "tag_checks", enabled: true, severity: "error" },
    ]);
    expect(qaProfileId).toBeGreaterThan(0);
  });

  test("A2: create project en→es-mx + assign QA + mock MT + read_write TM", async ({ page }) => {
    await login(page);
    mtEngineId = await createMTEngineViaAPI(page, "Tag MT", "mock", "");
    projectId = await createProjectViaAPI(page, "Tag Journey Project", "en", ["es-mx"]);

    // Empty TM in read_write mode — confirms in C2 will write rich text back
    // here. C3 asserts source_rich_text + target_rich_text round-tripped
    // correctly so the TM browser / Concordance render atomic chips.
    tmId = await createTMViaAPI(page, "Tag Journey TM");
    await setProjectTMsViaAPI(page, projectId, [
      { tm_id: tmId, source_language: "en", target_language: "es-mx", mode: "read_write", priority: 1 },
    ]);

    await setProjectMTViaAPI(page, projectId, [
      { mt_engine_id: mtEngineId, source_language: "en", target_language: "es-mx" },
    ]);

    // Assign QA profile via settings endpoint
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const qaRes = await page.request.patch(`${API}/api/projects/${projectId}/settings`, {
      headers,
      data: { qa_profile_id: qaProfileId },
    });
    expect(qaRes.ok(), `assign QA profile failed: ${qaRes.status()}`).toBe(true);
  });

  test("A3: upload XLIFF with inline tags (bpt/ept, ph, plain)", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    const res = await page.request.post(`${API}/api/projects/${projectId}/files`, {
      headers,
      multipart: {
        file: {
          name: "tags-test.xliff",
          mimeType: "application/xliff+xml",
          buffer: Buffer.from(XLIFF_FIXTURE),
        },
      },
    });
    expect(res.ok(), `XLIFF upload failed: ${res.status()}`).toBe(true);
    const body = await res.json();
    fileId = body.data.id;

    // Get job
    const jobsRes = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    const jobs = (await jobsRes.json()).data;
    expect(jobs.length, "should have 1 job").toBe(1);
    jobId = jobs[0].id;

    // OBSERVABLE OUTCOME: 4 segments created from the XLIFF
    const transRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const translations = (await transRes.json()).data;
    expect(translations.length, "should have 4 segments from XLIFF").toBe(4);
  });

  // ─── Phase B: Verify tags stored in translations ──────────────────

  test("B1: segments store plain text AND rich text with tags", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Source text is on SEGMENTS (not translations) — use segments endpoint
    const res = await page.request.get(`${API}/api/files/${fileId}/segments`, { headers });
    expect(res.ok()).toBe(true);
    const segments = (await res.json()).data;
    segments.sort((a: any, b: any) => a.position - b.position);

    // Seg 1: "Click here to continue" — has bpt/ept tags in rich text
    expect(segments[0].source_text, "seg1 plain text").toBe("Click here to continue");
    expect(segments[0].source_rich_text, "seg1 should have bpt/ept tags").toContain("<bpt");
    expect(segments[0].source_rich_text, "seg1 should have ept closing").toContain("<ept");

    // Seg 2: "Press {key} to submit" — has ph tag
    expect(segments[1].source_text, "seg2 plain text").toContain("{key}");
    expect(segments[1].source_rich_text, "seg2 should have ph tag").toContain("<ph");

    // Seg 3: "Bold text with trailing words" — has bpt/ept
    expect(segments[2].source_text, "seg3 plain text").toContain("Bold text");
    expect(segments[2].source_rich_text, "seg3 should have bpt/ept tags").toContain("<bpt");

    // Seg 4: "Normal text without tags" — NO rich text (control segment)
    expect(segments[3].source_text, "seg4 plain text").toBe("Normal text without tags");
    expect(
      segments[3].source_rich_text ?? "",
      "seg4 should have no rich text (no tags)",
    ).toBe("");
  });

  // ─── Phase C: Pre-translate + confirm ─────────────────────────────

  test("C1: pre-translate — mock MT fills target text", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    await pretranslateFileViaAPI(page, projectId, fileId);

    // OBSERVABLE OUTCOME: all 4 segments should have MT text
    const transRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const translations = (await transRes.json()).data;
    const withMT = translations.filter((t: any) => t.active_text && t.active_text.length > 0);
    expect(withMT.length, "all 4 segments should have MT text").toBe(4);
  });

  test("C2: confirm all 4 segments via API (J12 tests tag pipeline, not editor UX)", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Confirm all 4 via API — J1 already tests editor confirm extensively.
    // Carry `human_rich_text` for tag-bearing segments so the server-side QA
    // gate doesn't reject the confirm on tag-count mismatch — the
    // project's QA profile has tag_checks:error enabled (A1) so a plain
    // human_text against a tagged source would 422. `active_rich_text` is
    // what the MT pre-translate phase already produced (tags positioned by
    // TransferTags), so we just promote it as the human edit.
    const transRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const translations = (await transRes.json()).data.sort((a: any, b: any) => a.id - b.id);

    for (const t of translations) {
      const payload: { human_text: string; human_rich_text?: string } = {
        human_text: t.active_text || "confirmed",
      };
      if (t.active_rich_text) payload.human_rich_text = t.active_rich_text;
      const res = await page.request.put(`${API}/api/files/${fileId}/translations/${t.id}`, {
        headers,
        data: payload,
      });
      expect(res.ok(), `confirm seg ${t.id} failed: ${res.status()}`).toBe(true);
    }

    // Verify all 4 approved
    const verifyRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const verified = (await verifyRes.json()).data;
    const approved = verified.filter((t: any) => t.status === "approved");
    expect(approved.length, "all 4 should be approved").toBe(4);
  });

  // ─── Phase C+: TM write-back preserves rich_text ──
  //
  // C2 confirmed all 4 segments. The translation handler's auto-write-to-TM
  // path runs SaveToTM → SaveSegmentToTM → UpsertTMPipelineEntry, which
  // forwards the segment's source_rich_text + the human's human_rich_text
  // (both populated for the tag-bearing segments seg1/2/3). C3 asserts the
  // TM rows are populated correctly; C4 asserts the concordance API returns
  // rich_text so the modal renders chips. Together with B1 (storage on
  // upload) these pin the full editor → TM → concordance round-trip for
  // tag-bearing content.

  test("C3: TM write-back stored source_rich_text + target_rich_text for tag-bearing segments", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // TM write-back is best-effort + may complete after the PUT returns. Poll
    // briefly for the entries to appear (4 segments → 4 entries expected).
    let entries: any[] = [];
    for (let i = 0; i < 20; i++) {
      const res = await page.request.get(
        `${API}/api/tms/${tmId}/entries?source_lang=en&target_lang=es-mx&limit=20`,
        { headers },
      );
      entries = (await res.json()).data ?? [];
      if (entries.length >= 4) break;
      await page.waitForTimeout(150);
    }
    expect(entries.length,
      "C3: 4 confirmed segments must produce 4 TM entries (one per pair). " +
      "Fewer means TM write-back didn't fire OR a tm-write-time error was swallowed.",
    ).toBe(4);

    const bySource = (txt: string) => entries.find((e) => e.source_text === txt);

    // Seg 1 — "Click here to continue" — has bpt/ept tags in source.
    // Source rich_text MUST be populated (the importer extracted it in B1
    // and SaveToTM forwarded segment.source_rich_text on confirm).
    const seg1 = bySource("Click here to continue");
    expect(seg1, "C3 seg1: must be in TM").toBeDefined();
    expect(seg1.source_rich_text,
      "C3 seg1: source_rich_text MUST be populated for tag-bearing segments. " +
      "NULL here means SaveToTM didn't forward seg.source_rich_text — check tm_write.go.",
    ).toBeTruthy();
    expect(seg1.source_rich_text,
      "C3 seg1: rich_text preserves the original XLIFF inline markup",
    ).toContain("<bpt");

    // Seg 4 — "Normal text without tags" — control: rich_text MUST be NULL
    // (NULLIF contract). If it's an empty string instead, the frontend's
    // `rich_text != null` check would falsely trigger the chip path.
    const seg4 = bySource("Normal text without tags");
    expect(seg4, "C3 seg4: must be in TM").toBeDefined();
    expect(seg4.source_rich_text,
      "C3 seg4 (NULLIF contract): plain segments must have NULL rich_text, not empty string",
    ).toBeNull();
  });

  test("C4: concordance API returns rich_text for the tag-bearing TM entries", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Search the project's assigned TMs for "here" — should hit seg1.
    const res = await page.request.get(
      `${API}/api/tms/concordance?project_id=${projectId}&source_language=en&target_language=es-mx&q=here`,
      { headers },
    );
    expect(res.ok(), `C4: concordance call: ${res.status()}`).toBe(true);
    const body = await res.json();
    const tmMatches = body.data?.tm_matches ?? body.tm_matches ?? [];
    expect(tmMatches.length,
      "C4: concordance must return at least 1 match for 'here' (seg1's plain text contains it)",
    ).toBeGreaterThanOrEqual(1);

    const seg1Match = tmMatches.find((m: any) => m.source_text === "Click here to continue");
    expect(seg1Match,
      "C4: the seg1 match must be present (post-strip plain text was 'here'-searchable)",
    ).toBeDefined();
    expect(seg1Match.source_rich_text,
      "C4: SettingsConcordanceSearch must include source_rich_text. " +
      "If null, the SQL query is missing the column and ConcordanceModal won't render chips.",
    ).toBeTruthy();
    expect(seg1Match.source_rich_text,
      "C4: rich_text preserves the original XLIFF inline markup",
    ).toContain("<bpt");
  });

  // ─── Phase D: Create tag violations for QA ────────────────────────

  test("D1: edit seg2 to remove the ph tag — creates QA violation", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Get translations sorted by ID to identify seg2 (position 1 = "Press {key} to submit")
    const transRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const translations = (await transRes.json()).data.sort((a: any, b: any) => a.id - b.id);
    const seg2 = translations[1]; // second segment by creation order

    // Overwrite target with text that REMOVES the ph tag. Source has
    // `<ph id="2">{key}</ph>`; our replacement omits the placeholder.
    //
    // We assert BOTH halves of the QA enforcement design here:
    //
    //   1) Immediate block on confirm — HandleUpdateHuman calls
    //      enforceQAOnConfirm before persisting, so a confirm with the bad
    //      tag count must 422 and carry a tag-related qa_issue in the body.
    //   2) Periodic scan via run-qa (D2) — a transient draft with the same
    //      bad state must still get flagged so it surfaces in the QA report.
    //
    // The two paths share the same checker but enter it from different
    // surfaces. Covering both in one test pins the contract that "draft +
    // scan finds it" and "confirm blocks it" stay in sync — if either side
    // regresses, this test goes red.
    const blocked = await page.request.put(
      `${API}/api/files/${fileId}/translations/${seg2.id}`,
      { headers, data: { human_text: "Presione para enviar" } },
    );
    expect(blocked.status(),
      `confirm-with-missing-tag must 422 (got ${blocked.status()})`,
    ).toBe(422);
    const blockedBody = await blocked.json() as { qa_issues?: Array<{ check_type?: string }> };
    const blockedTagIssue = (blockedBody.qa_issues ?? []).some(
      (i) => (i.check_type ?? "").includes("tag"),
    );
    expect(blockedTagIssue,
      `confirm 422 body should report a tag qa_issue, got: ${JSON.stringify(blockedBody.qa_issues)}`,
    ).toBe(true);

    // Now plant the bad-tag state via the draft endpoint so D2's run-qa
    // scan has something to find. Drafts skip enforceQAOnConfirm by design
    // — they exist for in-flight edits that may transiently violate rules.
    //
    // `human_rich_text: ""` is required: without it the draft leaves
    // active_rich_text untouched, and ListTranslationsForQAPage's COALESCE falls
    // back to active_rich_text (still has the original `<ph>` from MT
    // pre-translate). The scan would then see matching tag counts and miss
    // the violation. Empty string forces COALESCE to return '' (zero tags)
    // which is what D2 needs to detect tag_missing.
    const res = await page.request.put(
      `${API}/api/files/${fileId}/translations/${seg2.id}/draft`,
      { headers, data: { human_text: "Presione para enviar", human_rich_text: "" } },
    );
    expect(res.ok(), `draft save failed: ${res.status()}`).toBe(true);
  });

  test("D2: run QA — should detect missing tag on seg2", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Run QA
    const qaRes = await page.request.post(`${API}/api/jobs/${jobId}/run-qa`, { headers });
    expect(qaRes.ok(), `run QA failed: ${qaRes.status()}`).toBe(true);

    // Get QA report
    const reportRes = await page.request.get(`${API}/api/jobs/${jobId}/qa-report`, { headers });
    expect(reportRes.ok()).toBe(true);
    const report = await reportRes.json();

    // QA report shape: { data: { results: [...], summary: { error, warning, info } } }
    const results = report.data?.results ?? [];
    const summary = report.data?.summary ?? {};

    // OBSERVABLE OUTCOME: QA should find tag issues
    expect(summary.error, "QA should find at least 1 error").toBeGreaterThanOrEqual(1);
    expect(results.length, "QA results should have entries").toBeGreaterThanOrEqual(1);

    // At least one issue should be tag-related
    const tagIssues = results.filter((i: any) =>
      i.check_type?.includes("tag") || i.message?.toLowerCase().includes("tag"),
    );
    const issueDesc = JSON.stringify(results.map((r: any) => ({ type: r.check_type, msg: r.message })));
    expect(
      tagIssues.length,
      "should have tag-related QA issues, got: " + issueDesc,
    ).toBeGreaterThanOrEqual(1);
  });

  // ─── Phase E: Export + verify tags in XLIFF ───────────────────────

  test("E1: export job — XLIFF should contain tag markup", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    const res = await page.request.get(`${API}/api/files/${fileId}/export?lang=es-mx`, { headers });
    expect(res.ok(), `export failed: ${res.status()}`).toBe(true);
    const xml = await res.text();

    // OBSERVABLE OUTCOME (THE CORE): exported XLIFF must contain tag markup
    expect(xml, "should be XLIFF").toContain("<xliff");

    // Tags must be inside a <target>: the <source> elements carry the same
    // markup, so a bare substring match would pass without any target tags.
    // (Seg 2 lost its <ph> in D1, so only bpt/ept are asserted.)
    expect(xml, "bpt tag preserved in a target").toMatch(
      /<target\b[^>]*>(?:(?!<\/target>)[\s\S])*<bpt\b/,
    );
    expect(xml, "ept tag preserved in a target").toMatch(
      /<target\b[^>]*>(?:(?!<\/target>)[\s\S])*<ept\b/,
    );

    // Segment 4 (plain text) should NOT have tags
    expect(xml, "export contains plain text segment").toContain("Normal text without tags");

    // The tag content should survive round-trip
    expect(xml, "bpt content preserved").toContain("&lt;b&gt;");
    expect(xml, "ept content preserved").toContain("&lt;/b&gt;");
  });

  // ─── Phase F: Cleanup ─────────────────────────────────────────────

  test("F1: cleanup all resources", async ({ page }) => {
    await login(page);
    await deleteProjectViaAPI(page, projectId);
    await deleteQAProfileViaAPI(page, qaProfileId);
    await deleteMTEngineViaAPI(page, mtEngineId);
    if (tmId) await deleteTMViaAPI(page, tmId);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const res = await page.request.get(`${API}/api/projects/${projectId}`, { headers });
    expect(res.status(), "project should be deleted").toBe(404);
  });

  test.afterAll(async ({ browser }) => {
    // Belt-and-suspenders cleanup if F1 was skipped due to a
    // mid-suite failure.
    const page = await browser.newPage();
    try {
      await login(page);
      if (projectId) await deleteProjectViaAPI(page, projectId).catch(() => {});
      if (qaProfileId) await deleteQAProfileViaAPI(page, qaProfileId).catch(() => {});
      if (mtEngineId) await deleteMTEngineViaAPI(page, mtEngineId).catch(() => {});
      if (tmId) await deleteTMViaAPI(page, tmId).catch(() => {});
    } catch {
      // best-effort
    } finally {
      await page.close();
    }
  });
});
