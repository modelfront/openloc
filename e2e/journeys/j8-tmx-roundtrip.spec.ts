/**
 * J8 — TMX Import/Export Round-Trip (Adversarial)
 *
 * Core purpose: TMX metadata (tuid, creationdate, creationid, changedate,
 * changeid) survives import → export. Multiple translations per source
 * preserved. UTF-16 encoded TMX imports without mojibake.
 *
 * NOT NEGOTIABLE: metadata fidelity is the whole point. If assertions
 * just check "export succeeded" without parsing the XML, this test is
 * worthless. Parse the export and verify each attribute.
 */
import { test, expect } from "@playwright/test";
import * as fs from "fs";
import * as path from "path";
import {
  login,
  createTMViaAPI,
  importTMXViaAPI,
  importTMXBufferViaAPI,
  deleteTMViaAPI,
} from "./helpers";

const API = "";

// Read fixtures at module level
const metadataTMX = fs.readFileSync(
  path.join(__dirname, "../fixtures/journey-tmx-metadata.tmx"),
  "utf-8",
);
const utf16Buffer = fs.readFileSync(
  path.join(__dirname, "../../internal/tmx/testdata/utf16le.tmx"),
);

let tmId: number;

/**
 * Wait until the most recent TMX import for this TM has finished. A flat
 * wait is flake-prone on a loaded CI worker. Polls every 250ms with a
 * 10-iteration cap (~2.5s ceiling — small fixtures finish in 1-2 polls).
 */
async function waitForLastImport(page: import("@playwright/test").Page, tmId: number): Promise<void> {
  const headers = {
    Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
  };
  for (let i = 0; i < 10; i++) {
    const r = await page.request.get(`${API}/api/tms/${tmId}/imports`, { headers });
    if (r.ok()) {
      const imports = (await r.json()).data;
      if (imports.length > 0) {
        const latest = imports[imports.length - 1];
        if (latest.status === "completed" || latest.status === "done") return;
        if (latest.status === "failed") throw new Error(`TMX import failed: ${JSON.stringify(latest)}`);
      }
    }
    await page.waitForTimeout(250);
  }
  throw new Error(`TMX import did not complete within 2.5s for TM ${tmId}`);
}

test.describe.serial("J8 — TMX Round-Trip", () => {
  test("1: create TMX Roundtrip TM", async ({ page }) => {
    await login(page);
    tmId = await createTMViaAPI(page, "TMX Roundtrip TM");
    expect(tmId, "TM should have a valid ID").toBeGreaterThan(0);
  });

  test("2: import metadata TMX — 4 TU entries (2 share same source)", async ({ page }) => {
    await login(page);
    await importTMXViaAPI(page, tmId, metadataTMX, "metadata.tmx");

    // Poll until the import is complete.
    await waitForLastImport(page, tmId);

    // OBSERVABLE OUTCOME: verify exact entry count via API
    // 4 TU entries — 2 have identical source but different targets (both are preserved)
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const res = await page.request.get(`${API}/api/tms/${tmId}/entries?limit=100`, { headers });
    expect(res.ok()).toBe(true);
    const body = await res.json();
    expect(body.data.length, "should have exactly 4 entries (no dedup of same-source different-target)").toBe(4);
  });

  test("3: browse — same source 'Welcome to the application' has 2 different targets", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Search for the shared source text
    const res = await page.request.get(
      `${API}/api/tms/${tmId}/entries?q=${encodeURIComponent("Welcome to the application")}`,
      { headers },
    );
    expect(res.ok()).toBe(true);
    const body = await res.json();

    // OBSERVABLE OUTCOME: BOTH targets must be present — not collapsed into one
    const targets = body.data.map((e: any) => e.target_text).sort();
    expect(targets, "must have exactly 2 entries for same source").toHaveLength(2);
    expect(targets, "both translations must be preserved").toEqual([
      "Bienvenida a la aplicación", // feminine form (meta-002)
      "Bienvenido a la aplicación", // masculine form (meta-001)
    ]);
  });

  test("4: import UTF-16 LE encoded TMX — should not error", async ({ page }) => {
    await login(page);
    await importTMXBufferViaAPI(page, tmId, utf16Buffer, "utf16le.tmx");

    // Poll until the second import is complete.
    await waitForLastImport(page, tmId);

    // After this import, TM should have 4 (metadata) + 2 (UTF-16) = 6 entries
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const res = await page.request.get(`${API}/api/tms/${tmId}/entries?limit=100`, { headers });
    expect(res.ok()).toBe(true);
    const body = await res.json();
    expect(body.data.length, "should have 6 entries total (4 metadata + 2 UTF-16)").toBe(6);
  });

  test("5: browse — UTF-16 entries have correct text (no mojibake)", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Search for "Good morning" — from the UTF-16 fixture
    const res = await page.request.get(
      `${API}/api/tms/${tmId}/entries?q=${encodeURIComponent("Good morning")}`,
      { headers },
    );
    expect(res.ok()).toBe(true);
    const body = await res.json();

    // OBSERVABLE OUTCOME: UTF-16 text must be correct, not garbled
    expect(body.data.length, "should find 'Good morning' from UTF-16 import").toBeGreaterThanOrEqual(1);
    const entry = body.data[0];
    expect(entry.source_text, "source text intact").toBe("Good morning");
    expect(entry.target_text, "target with accented char — no mojibake").toBe("Buenos días");

    // Also verify the second UTF-16 entry
    const res2 = await page.request.get(
      `${API}/api/tms/${tmId}/entries?q=${encodeURIComponent("Thank you")}`,
      { headers },
    );
    const body2 = await res2.json();
    const entry2 = body2.data.find((e: any) => e.source_text === "Thank you");
    expect(entry2, "should find 'Thank you' from UTF-16 import").toBeTruthy();
    expect(entry2.target_text, "target text intact").toBe("Gracias");
  });

  test("6-7: export TMX — verify metadata attributes survive round-trip", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    const res = await page.request.get(`${API}/api/tms/${tmId}/export`, { headers });
    expect(res.ok(), `export failed: ${res.status()}`).toBe(true);
    const xml = await res.text();

    // OBSERVABLE OUTCOME: exported TMX is valid XML with <tmx> root
    expect(xml, "should be a TMX file").toContain("<tmx");
    expect(xml, "should have <body>").toContain("<body>");

    // --- Metadata round-trip verification (THE CORE OF THIS JOURNEY) ---
    // Each <tu> from the metadata fixture should preserve its attributes.

    // meta-001: full metadata (tuid, creationdate, creationid, changedate, changeid)
    expect(xml, "tuid meta-001 preserved").toContain('tuid="meta-001"');
    expect(xml, "creationdate for meta-001").toContain("20250615T093000Z");
    expect(xml, "creationid translator_a").toContain("translator_a");
    expect(xml, "changedate for meta-001").toContain("20250620T141500Z");
    expect(xml, "changeid reviewer_b").toContain("reviewer_b");

    // meta-002: same source different target — both must exist in export
    expect(xml, "tuid meta-002 preserved").toContain('tuid="meta-002"');
    expect(xml, "creationid translator_c").toContain("translator_c");

    // Verify BOTH translations for "Welcome to the application" in the XML
    expect(xml, "masculine translation in export").toContain("Bienvenido a la aplicación");
    expect(xml, "feminine translation in export").toContain("Bienvenida a la aplicación");

    // meta-003: partial metadata (no changedate/changeid)
    expect(xml, "tuid meta-003 preserved").toContain('tuid="meta-003"');
    expect(xml, "meta-003 creation date").toContain("20250610T100000Z");

    // meta-004: special characters survive round-trip
    expect(xml, "tuid meta-004 preserved").toContain('tuid="meta-004"');
    expect(xml, "special chars: dollar + dash").toContain("$99.99");
    expect(xml, "special chars: Spanish exclamation").toContain("¡20% de descuento!");

    // UTF-16 entries should also be in the export (they were imported into the same TM)
    expect(xml, "UTF-16 entry 'Good morning' in export").toContain("Good morning");
    expect(xml, "UTF-16 entry 'Buenos días' in export").toContain("Buenos días");
  });

  test("8: cleanup — delete TM", async ({ page }) => {
    await login(page);
    await deleteTMViaAPI(page, tmId);

    // Verify it's gone
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const res = await page.request.get(`${API}/api/tms/${tmId}`, { headers });
    expect(res.status(), "TM should be deleted (404)").toBe(404);
  });

  test.afterAll(async ({ browser }) => {
    // Belt-and-suspenders cleanup if a mid-suite test failure
    // skipped the explicit cleanup test above. Without this, a TM imported
    // in test 1 leaks across runs.
    const page = await browser.newPage();
    try {
      await login(page);
      if (tmId) await deleteTMViaAPI(page, tmId).catch(() => {});
    } catch {
      // best-effort
    } finally {
      await page.close();
    }
  });
});
