/**
 * J14 — Large TM Scale Stress (Adversarial)
 *
 * Core purpose: large TM import completes WITHOUT truncation (QA finding #15
 * noted only 43K of 121K entries imported). Browse and search work at scale.
 * Pre-translate finds a match from deep in the TM.
 *
 * NOT NEGOTIABLE: the entry count after import MUST match the generated count
 * EXACTLY. "Import succeeded" is worthless if it silently dropped 60% of entries.
 *
 * Uses 10K entries (not 100K) for CI speed — the streaming parser
 * is constant-memory, so the batching/truncation bug manifests at any size.
 * The partial-import issue from QA #15 was a batch/timeout problem, not OOM.
 */
import { test, expect } from "@playwright/test";
import {
  login,
  createTMViaAPI,
  importTMXViaAPI,
  createProjectViaAPI,
  setProjectTMsViaAPI,
  createMTEngineViaAPI,
  setProjectMTViaAPI,
  uploadFileViaAPI,
  pretranslateFileViaAPI,
  deleteProjectViaAPI,
  deleteTMViaAPI,
  deleteMTEngineViaAPI,
} from "./helpers";

const API = "";
const TM_SIZE = 10_000; // 10K entries — enough to stress batching

// The "needle" entry buried at position 7500 — pre-translate should find it
const NEEDLE_SOURCE = "The quick brown fox jumps over the lazy dog.";
const NEEDLE_TARGET = "El rápido zorro marrón salta sobre el perro perezoso.";
const NEEDLE_POSITION = 7500;

/** Generate a TMX string with N entries + one known "needle" at a specific position. */
function generateLargeTMX(count: number): string {
  const lines: string[] = [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<tmx version="1.4">',
    '  <header creationtool="test" datatype="plaintext" segtype="sentence" adminlang="en" srclang="en"/>',
    '  <body>',
  ];

  for (let i = 0; i < count; i++) {
    const source = i === NEEDLE_POSITION
      ? NEEDLE_SOURCE
      : `Generated segment number ${i} for scale testing.`;
    const target = i === NEEDLE_POSITION
      ? NEEDLE_TARGET
      : `Segmento generado número ${i} para pruebas de escala.`;

    lines.push(`    <tu tuid="gen-${i}"><tuv xml:lang="en"><seg>${source}</seg></tuv><tuv xml:lang="es"><seg>${target}</seg></tuv></tu>`);
  }

  lines.push('  </body>', '</tmx>');
  return lines.join("\n");
}

let tmId: number;
let projectId: number;
let mtEngineId: number;
let fileId: number;
let jobId: number;

test.describe.serial("J14 — Large TM Scale", () => {
  test.setTimeout(120_000); // 2 minutes — import takes time

  test("1: create TM", async ({ page }) => {
    await login(page);
    tmId = await createTMViaAPI(page, "Large TM Journey");
    expect(tmId).toBeGreaterThan(0);
  });

  test("2: import 10K-entry TMX — ALL entries must be imported (QA #15)", async ({ page }) => {
    test.setTimeout(120_000);
    await login(page);

    const tmxContent = generateLargeTMX(TM_SIZE);
    await importTMXViaAPI(page, tmId, tmxContent, "large-10k.tmx");

    // Poll for import completion — 10K entries in 500-row batches ≈ 1s
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    let importStatus = "";
    let importedCount = 0;
    for (let attempt = 0; attempt < 30; attempt++) {
      await page.waitForTimeout(1000);
      const importsRes = await page.request.get(`${API}/api/tms/${tmId}/imports`, { headers });
      const imports = (await importsRes.json()).data;
      if (imports.length > 0) {
        const latest = imports[imports.length - 1];
        importStatus = latest.status;
        importedCount = latest.imported;
        if (latest.status === "completed" || latest.status === "done") break;
        if (latest.status === "failed") {
          throw new Error(`Import failed: ${JSON.stringify(latest)}`);
        }
      }
    }

    // Verify import completed (not stuck in "processing")
    expect(importStatus, "import should complete").toBe("completed");
    expect(importedCount, "import should report all entries").toBe(TM_SIZE);

    // OBSERVABLE OUTCOME (THE CORE): all 10K entries must be imported
    // QA #15 reported 43K of 121K — the partial import bug.
    // We verify the EXACT count to catch any truncation.
    // entries response: { data: [...], total: N, cursor: N, has_more: bool }
    const entriesRes = await page.request.get(`${API}/api/tms/${tmId}/entries?limit=1`, { headers });
    const entriesBody = await entriesRes.json();
    const totalEntries = entriesBody.total ?? 0;

    expect(
      totalEntries,
      `ALL ${TM_SIZE} entries must be imported, got ${totalEntries} (QA #15: partial import bug)`,
    ).toBe(TM_SIZE);
  });

  test("3: search for 'needle' entry at position 7500", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Search for the needle buried deep in the TM
    const res = await page.request.get(
      `${API}/api/tms/${tmId}/entries?q=${encodeURIComponent("quick brown fox")}`,
      { headers },
    );
    expect(res.ok()).toBe(true);
    const body = await res.json();

    // OBSERVABLE OUTCOME: should find the needle entry
    expect(body.data.length, "should find the needle entry").toBeGreaterThanOrEqual(1);
    const needle = body.data.find((e: any) => e.source_text === NEEDLE_SOURCE);
    expect(needle, "exact needle source text should match").toBeTruthy();
    expect(needle.target_text, "needle target text").toBe(NEEDLE_TARGET);
  });

  test("4: search for entry NOT on first page (deep pagination)", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Search for a generic entry that exists deep in the TM
    const res = await page.request.get(
      `${API}/api/tms/${tmId}/entries?q=${encodeURIComponent("segment number 9500")}`,
      { headers },
    );
    expect(res.ok()).toBe(true);
    const body = await res.json();

    // OBSERVABLE OUTCOME: should find entries with "9500" in source
    expect(body.data.length, "should find entries matching 'segment number 9500'").toBeGreaterThanOrEqual(1);
    const match = body.data[0];
    expect(match.source_text, "should contain 9500").toContain("9500");
  });

  test("5: create project + assign large TM + pre-translate with needle match", async ({ page }) => {
    test.setTimeout(60_000);
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Setup
    mtEngineId = await createMTEngineViaAPI(page, "Large TM MT", "mock", "");
    projectId = await createProjectViaAPI(page, "Large TM Project", "en", ["es-mx"]);
    await setProjectTMsViaAPI(page, projectId, [
      { tm_id: tmId, source_language: "en", target_language: "es-mx", mode: "read", priority: 1 },
    ]);
    await setProjectMTViaAPI(page, projectId, [
      { mt_engine_id: mtEngineId, source_language: "en", target_language: "es-mx" },
    ]);

    // Upload file with the needle source text + 2 non-matching segments
    const sourceFile = [
      NEEDLE_SOURCE, // should match TM entry at position 7500
      "This segment has no TM match whatsoever.",
      "Another unmatched segment for MT fallback.",
    ].join("\n");

    const result = await uploadFileViaAPI(page, projectId, "needle-test.txt", sourceFile, "text/plain");
    fileId = result.data?.id ?? result.id;

    // Get job
    const jobsRes = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    jobId = (await jobsRes.json()).data[0].id;

    // Pre-translate via per-job phase endpoints
    await pretranslateFileViaAPI(page, projectId, fileId);
    // synchronous pretranslate — GET below sees committed rows immediately.

    // OBSERVABLE OUTCOME: the needle segment should have a TM match
    const transRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const translations = (await transRes.json()).data;
    expect(translations.length, "should have 3 segments").toBe(3);

    // Find the needle's translation (sort by ID for stable order)
    translations.sort((a: any, b: any) => a.id - b.id);
    const needleTrans = translations[0]; // first segment = needle

    // The TM should have matched "The quick brown fox..." → "El rápido zorro marrón..."
    // TM has 'es' entries, project uses 'es-mx' — SPLIT_PART locale fallback bridges them.
    // If this assertion fails, the locale-aware TM lookup is broken (regression).
    // Origin is 'tm'; match strength is tm_match_percent.
    const hasTMMatch = needleTrans.origin?.translation_origin === "tm"
      || needleTrans.tm_match_percent !== null;

    expect(hasTMMatch, "needle segment MUST have TM match (es→es-mx SPLIT_PART fallback)").toBe(true);

    // Verify the Spanish text from TM
    const needleText = needleTrans.active_text ?? "";
    expect(needleText, "needle should have TM-matched Spanish text").toContain("rápido zorro");

    // The other 2 segments should have mock MT (no TM match for them)
    const mtSegments = translations.filter((t: any) => t.active_text?.includes("[ES-MX]"));
    expect(mtSegments.length, "2 non-matching segments should get mock MT").toBe(2);
  });

  test("6: cleanup", async ({ page }) => {
    await login(page);
    await deleteProjectViaAPI(page, projectId);
    await deleteTMViaAPI(page, tmId);
    if (mtEngineId) await deleteMTEngineViaAPI(page, mtEngineId);
  });

  test.afterAll(async ({ browser }) => {
    // Belt-and-suspenders cleanup — J14 imports a 10K-entry TMX. If a mid-suite failure skips test 6, the
    // 10K entries stay in the DB and slow subsequent TM lookups.
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
