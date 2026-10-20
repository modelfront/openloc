/**
 * J13 — File Format Gauntlet (Adversarial)
 *
 * Core purpose: txt, json, po, html, md, xlsx and pptx (binary) upload,
 * create segments and get pre-translated. Exports are verified for txt, json,
 * xlsx and pptx only. Exercises QA findings #3 (.xlsx bilingual), #28 (Excel
 * upload failure).
 *
 * NOT NEGOTIABLE: "upload succeeded" is worthless without verifying SEGMENT COUNT.
 * Each format must produce the expected number of segments. A verified export
 * must contain the translated text, not just be non-empty.
 *
 * Skipped: DOCX/PDF (require Python sidecar parser, tested manually).
 */
import { test, expect } from "@playwright/test";
import * as fs from "fs";
import * as path from "path";
import { execSync } from "child_process";
import {
  login,
  authHeaders,
  createProjectViaAPI,
  createMTEngineViaAPI,
  setProjectMTViaAPI,
  deleteProjectViaAPI,
  deleteMTEngineViaAPI,
  pretranslateFileViaAPI,
  updateHumanTranslationViaAPI,
} from "./helpers";

const API = "";

// ─── Inline fixtures (text-based formats) ──────────────────────────

const FIXTURES: { name: string; content: string | Buffer; mime: string; expectedSegments: number }[] = [
  {
    name: "gauntlet.txt",
    content: "Hello world.\nReset your password.\nThank you.\n",
    mime: "text/plain",
    expectedSegments: 3,
  },
  {
    name: "gauntlet.json",
    content: JSON.stringify({
      greeting: "Hello world",
      password_reset: "Reset your password",
      thanks: "Thank you",
    }, null, 2),
    mime: "application/json",
    expectedSegments: 3,
  },
  {
    name: "gauntlet.po",
    content: [
      'msgid ""',
      'msgstr ""',
      '"Content-Type: text/plain; charset=UTF-8\\n"',
      "",
      'msgid "Hello world"',
      'msgstr ""',
      "",
      'msgid "Reset your password"',
      'msgstr ""',
      "",
      'msgid "Thank you"',
      'msgstr ""',
    ].join("\n"),
    mime: "text/x-gettext-translation",
    expectedSegments: 3,
  },
  {
    name: "gauntlet.html",
    content: "<html><body><p>Hello <b>world</b>.</p><p>Reset your password.</p><p>Thank you.</p></body></html>",
    mime: "text/html",
    expectedSegments: 3,
  },
  {
    name: "gauntlet.md",
    content: "# Title\n\nHello world.\n\nReset your password.\n\nThank you.\n",
    mime: "text/markdown",
    expectedSegments: 4, // title + 3 paragraphs
  },
  {
    name: "gauntlet.xlsx",
    content: fs.readFileSync(path.join(__dirname, "../fixtures/journey-bilingual.xlsx")),
    mime: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    expectedSegments: 3, // 3 bilingual rows
  },
  {
    // Built by pptxtest.JourneyDeck (internal/parser/pptxtest/journey.go).
    name: "gauntlet.pptx",
    content: fs.readFileSync(path.join(__dirname, "../fixtures/journey-deck.pptx")),
    mime: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
    // slide 1: title + two-sentence subtitle (3); slide 2: two table cells,
    // alt text, notes (4); hidden slide 3: text box + two SmartArt nodes (3)
    expectedSegments: 10,
  },
];

let projectId: number;
let mtEngineId: number;
const fileIds: Map<string, { fileId: number; jobId: number }> = new Map();

test.describe.serial("J13 — File Format Gauntlet", () => {
  test("A1: create project en→es-mx with mock MT", async ({ page }) => {
    await login(page);
    mtEngineId = await createMTEngineViaAPI(page, "Format Gauntlet MT", "mock", "");
    projectId = await createProjectViaAPI(page, "Format Gauntlet", "en", ["es-mx"]);
    await setProjectMTViaAPI(page, projectId, [
      { mt_engine_id: mtEngineId, source_language: "en", target_language: "es-mx" },
    ]);
  });

  // ─── Phase B: Upload each format ──────────────────────────────────

  for (const fixture of FIXTURES) {
    test(`B: upload ${fixture.name} — should create ${fixture.expectedSegments} segments`, async ({ page }) => {
      await login(page);
      const headers = await authHeaders(page);

      const buffer = typeof fixture.content === "string"
        ? Buffer.from(fixture.content)
        : fixture.content;

      const res = await page.request.post(`${API}/api/projects/${projectId}/files`, {
        headers,
        multipart: {
          file: {
            name: fixture.name,
            mimeType: fixture.mime,
            buffer,
          },
        },
      });
      expect(res.ok(), `upload ${fixture.name} failed: ${res.status()}`).toBe(true);
      const body = await res.json();
      const fileId = body.data.id;

      // Get the job for this file
      const jobsRes = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
      const allJobs = (await jobsRes.json()).data;
      const fileJob = allJobs.find((j: any) => j.file_id === fileId);
      expect(fileJob, `${fixture.name} should have a job`).toBeTruthy();

      // OBSERVABLE OUTCOME: verify exact segment count
      const transRes = await page.request.get(`${API}/api/jobs/${fileJob.id}/translations`, { headers });
      const translations = (await transRes.json()).data;
      expect(
        translations.length,
        `${fixture.name} should have ${fixture.expectedSegments} segments, got ${translations.length}`,
      ).toBe(fixture.expectedSegments);

      fileIds.set(fixture.name, { fileId, jobId: fileJob.id });
    });
  }

  test("B2: upload .ppt — refused with a message that says how to fix it", async ({ page }) => {
    await login(page);
    const headers = await authHeaders(page);
    // An OLE compound file, the container of pre-2007 PowerPoint files.
    const ole = Buffer.concat([Buffer.from([0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1]), Buffer.alloc(504)]);
    const res = await page.request.post(`${API}/api/projects/${projectId}/files`, {
      headers,
      multipart: { file: { name: "gauntlet.ppt", mimeType: "application/vnd.ms-powerpoint", buffer: ole } },
    });
    expect(res.status(), "a .ppt upload must be refused").toBe(400);
    expect(await res.text()).toContain("save it as .pptx");
  });

  // ─── Phase C: Pre-translate + verify ──────────────────────────────

  test("C1: pre-translate all files — mock MT fills targets", async ({ page }) => {
    await login(page);
    // Pre-translate each file via per-job phase endpoints
    for (const [, { fileId }] of fileIds) {
      await pretranslateFileViaAPI(page, projectId, fileId);
    }
    // pretranslate is synchronous (handler_pipeline.go runs TranslateFile inline,
    // returns 200 after commit) — no settle wait needed before the GET below.

    const headers = await authHeaders(page);

    // OBSERVABLE OUTCOME: every file's segments should have MT text
    for (const [name, { jobId }] of fileIds) {
      const transRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
      const translations = (await transRes.json()).data;
      const withMT = translations.filter((t: any) => t.active_text && t.active_text.length > 0);
      expect(
        withMT.length,
        `${name}: all segments should have MT text, got ${withMT.length}/${translations.length}`,
      ).toBe(translations.length);
    }
  });

  // ─── Phase D: Export + verify content ─────────────────────────────

  test("D1: export txt — should contain translated text", async ({ page }) => {
    await login(page);
    const headers = await authHeaders(page);

    const { fileId } = fileIds.get("gauntlet.txt")!;
    const res = await page.request.get(`${API}/api/files/${fileId}/export?lang=es-mx`, { headers });
    expect(res.ok(), `export txt failed: ${res.status()}`).toBe(true);
    const text = await res.text();

    // OBSERVABLE OUTCOME: exported text should contain mock MT output
    expect(text, "txt export should have [ES-MX] prefixed translations").toContain("[ES-MX]");
  });

  test("D2: export json — should be valid JSON with translated values", async ({ page }) => {
    await login(page);
    const headers = await authHeaders(page);

    const { fileId } = fileIds.get("gauntlet.json")!;
    const res = await page.request.get(`${API}/api/files/${fileId}/export?lang=es-mx`, { headers });
    expect(res.ok(), `export json failed: ${res.status()}`).toBe(true);
    const text = await res.text();

    // OBSERVABLE OUTCOME: must be valid JSON, not garbled
    let parsed: any;
    expect(() => { parsed = JSON.parse(text); }, "export should be valid JSON").not.toThrow();
    // At least one value should have mock MT text
    const values = Object.values(parsed as Record<string, string>);
    const hasMT = values.some((v: any) => typeof v === "string" && v.includes("[ES-MX]"));
    expect(hasMT, "json export should have [ES-MX] translated values").toBe(true);
  });

  test("D3: export xlsx — sharedStrings prove pipeline output reached the cells", async ({ page }) => {
    // Don't assert fixture strings like "Hola mundo": excelize can keep them
    // as orphaned sharedStrings entries, so they pass even if the export
    // returns the input verbatim. Assert a fresh marker instead.
    await login(page);
    const headers = await authHeaders(page);

    const { fileId, jobId } = fileIds.get("gauntlet.xlsx")!;

    // Find seg 0's translation ID and confirm it with a marker
    const transRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const translations = (await transRes.json()).data;
    expect(translations.length, "xlsx should have 3 segments").toBe(3);
    const marker = `__J13_XLSX_MARKER_${Date.now()}__`;
    await updateHumanTranslationViaAPI(page, fileId, translations[0].id, marker);

    const res = await page.request.get(`${API}/api/files/${fileId}/export?lang=es-mx`, { headers });
    expect(res.ok(), `export xlsx failed: ${res.status()}`).toBe(true);
    const body = await res.body();

    // OBSERVABLE OUTCOME 1: valid zip (xlsx IS a zip file)
    expect(body.length, "xlsx export should be non-trivial size").toBeGreaterThan(100);
    expect(body[0], "xlsx PK signature byte 1").toBe(0x50); // 'P'
    expect(body[1], "xlsx PK signature byte 2").toBe(0x4b); // 'K'

    // OBSERVABLE OUTCOME 2: the marker exists nowhere in the fixture, so
    // finding it proves the confirm → export → cell-write path. Bilingual-
    // imported segments skip mt-apply, so no [ES-MX] prefix here (D1/D2 cover it).
    const tmpFile = `/tmp/j13-export-${Date.now()}.xlsx`;
    fs.writeFileSync(tmpFile, body);
    try {
      const sharedStrings = execSync(`unzip -p "${tmpFile}" xl/sharedStrings.xml 2>/dev/null`).toString();
      expect(
        sharedStrings,
        `confirmed segment marker should be in xlsx (seg 0 was confirmed with "${marker}")`,
      ).toContain(marker);
    } finally {
      fs.unlinkSync(tmpFile);
    }
  });

  test("D4: export pptx — slides, notes and SmartArt translated, picture untouched", async ({ page }) => {
    await login(page);
    const headers = await authHeaders(page);

    const { fileId, jobId } = fileIds.get("gauntlet.pptx")!;
    const transRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const translations = (await transRes.json()).data;
    expect(translations.length, "pptx should have 10 segments").toBe(10);
    const marker = `__J13_PPTX_MARKER_${Date.now()}__`;
    await updateHumanTranslationViaAPI(page, fileId, translations[0].id, marker);

    const res = await page.request.get(`${API}/api/files/${fileId}/export?lang=es-mx`, { headers });
    expect(res.ok(), `export pptx failed: ${res.status()}`).toBe(true);
    const body = await res.body();
    expect(body[0], "pptx PK signature byte 1").toBe(0x50); // 'P'
    expect(body[1], "pptx PK signature byte 2").toBe(0x4b); // 'K'

    const tmpFile = `/tmp/j13-export-${Date.now()}.pptx`;
    const fixture = path.join(__dirname, "../fixtures/journey-deck.pptx");
    fs.writeFileSync(tmpFile, body);
    try {
      const part = (file: string, name: string) => execSync(`unzip -p "${file}" "${name}"`);

      // OBSERVABLE OUTCOME 1: the confirmed title reached slide 1, marked as
      // Spanish so PowerPoint spell-checks it in the target language.
      const slide1 = part(tmpFile, "ppt/slides/slide1.xml").toString();
      expect(slide1, `confirmed segment marker should be on slide 1 ("${marker}")`).toContain(marker);
      expect(slide1, "translated runs carry the target language").toContain('lang="es-MX"');

      // OBSERVABLE OUTCOME 2: MT output reached the notes, the SmartArt data
      // and the drawing viewers show it from.
      for (const name of ["ppt/notesSlides/notesSlide2.xml", "ppt/diagrams/data3.xml", "ppt/diagrams/drawing3.xml"]) {
        expect(part(tmpFile, name).toString(), `${name} should hold [ES-MX] translations`).toContain("[ES-MX]");
      }

      // OBSERVABLE OUTCOME 3: parts that hold no text come back byte for byte.
      expect(
        part(tmpFile, "ppt/media/image2.png").equals(part(fixture, "ppt/media/image2.png")),
        "the picture must be copied unchanged",
      ).toBe(true);
    } finally {
      fs.unlinkSync(tmpFile);
    }
  });

  // ─── Phase E: Cleanup ─────────────────────────────────────────────

  test("E1: cleanup all resources", async ({ page }) => {
    await login(page);
    await deleteProjectViaAPI(page, projectId);
    await deleteMTEngineViaAPI(page, mtEngineId);

    const headers = await authHeaders(page);
    const res = await page.request.get(`${API}/api/projects/${projectId}`, { headers });
    expect(res.status(), "project should be deleted").toBe(404);
  });

  test.afterAll(async ({ browser }) => {
    // Belt-and-suspenders cleanup if E1 was skipped due to a
    // mid-suite failure (e.g., xlsx export D3 fails). Seven file uploads' worth
    // of segments could leak into the seed DB without this.
    const page = await browser.newPage();
    try {
      await login(page);
      if (projectId) await deleteProjectViaAPI(page, projectId).catch(() => {});
      if (mtEngineId) await deleteMTEngineViaAPI(page, mtEngineId).catch(() => {});
    } catch {
      // best-effort
    } finally {
      await page.close();
    }
  });
});
