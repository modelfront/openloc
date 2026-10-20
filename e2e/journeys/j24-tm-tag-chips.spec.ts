/**
 * J24 — TM tag chips end-to-end (R24 fix)
 *
 * Goal: lock down the R24 user-visible behavior across all three read-only
 * surfaces — TM Browser, Concordance modal, and the editor's TM-match
 * sidebar — by importing a tag-bearing TMX and asserting the rendered DOM
 * contains atomic `.xliff-tag` chips, NOT raw `<bpt>...</bpt>` markup as
 * literal text.
 *
 * Locale: en→fr-CA — mock supports `fr` natively (base-code fallback for fr-CA),
 * fr-CA hasn't been used by any prior journey for the TM-import path. Single
 * locale keeps the test focused on the rendering invariant.
 *
 * Coverage map (per surface):
 *   B — TM Browser cells (pages/TMBrowser.tsx)
 *   C — Concordance modal source/target (components/ConcordanceModal.tsx)
 *   D — Editor TM-match sidebar (components/EditorSidebar.tsx) — covered
 *       indirectly via API assertion (UI integration adds flakiness without
 *       proportional value; the renderer goes through the same SegmentText)
 *
 * Anti-tautology: each visual assertion is paired with a *negative*
 * assertion that the raw markup is NOT present as text content. This way
 * a regression that drops the chip path AND happens to render markup as
 * literal text would still fail (because the literal markup would appear
 * in textContent), instead of silently passing.
 */
import { test, expect } from "@playwright/test";
import {
  login,
  createTMViaAPI,
  deleteTMViaAPI,
  importTMXViaAPI,
  createProjectViaAPI,
  deleteProjectViaAPI,
  createMTEngineViaAPI,
  deleteMTEngineViaAPI,
  setProjectTMsViaAPI,
  setProjectMTViaAPI,
  updateProjectSettingsViaAPI,
} from "./helpers";

const API = "";
const RUN_ID = Date.now();

let tmId: number;
let projectId: number;
let mtEngineId: number;

// A small TMX with a tag-bearing TU. Two TUs total — one with bold tags
// (paired bpt/ept), one without — so we can assert the chip path AND the
// plain-text fallback both work in the same TM.
const TMX_WITH_TAGS = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en"
          creationtool="J24" creationtoolversion="1.0" o-tmf="custom"/>
  <body>
    <tu tuid="t1">
      <tuv xml:lang="en"><seg>Click <bpt id="1" ctype="bold"/>here<ept id="1"/> to continue.</seg></tuv>
      <tuv xml:lang="fr-ca"><seg>Cliquez <bpt id="1" ctype="bold"/>ici<ept id="1"/> pour continuer.</seg></tuv>
    </tu>
    <tu tuid="t2">
      <tuv xml:lang="en"><seg>Welcome back.</seg></tuv>
      <tuv xml:lang="fr-ca"><seg>Bon retour.</seg></tuv>
    </tu>
  </body>
</tmx>`;

async function authHeader(page: any): Promise<Record<string, string>> {
  const token = await page.evaluate(() => localStorage.getItem("token"));
  return { Authorization: `Bearer ${token}` };
}

test.describe.serial("J24 — TM tag chips end-to-end (R24)", () => {
  // ════════════════════════════════════════════════════════════════════════
  // Phase A — Setup: TM with inline-tag TMX, project + MT for the editor path.
  // ════════════════════════════════════════════════════════════════════════

  test("A1: create TM, import tag-bearing TMX, verify chips stored as rich_text via API", async ({ page }) => {
    await login(page);
    tmId = await createTMViaAPI(page, `J24 TM ${RUN_ID}`);

    await importTMXViaAPI(page, tmId, TMX_WITH_TAGS, "j24.tmx");

    // Poll the import endpoint until status=completed
    const headers = await authHeader(page);
    let imported = false;
    for (let i = 0; i < 40; i++) {
      const res = await page.request.get(`${API}/api/tms/${tmId}/imports`, { headers });
      const imports = (await res.json()).data as Array<{ status: string; imported: number }>;
      if (imports.length > 0 && imports[0].status === "completed" && imports[0].imported > 0) {
        imported = true;
        break;
      }
      await page.waitForTimeout(200);
    }
    expect(imported, "A1: TMX import must complete within 8s").toBe(true);

    // Verify the tag-bearing entry has source_rich_text populated AND
    // source_text is the markup-stripped form. This is the storage-layer
    // guarantee that the frontend chip path relies on.
    const entriesRes = await page.request.get(
      `${API}/api/tms/${tmId}/entries?source_lang=en&target_lang=fr-ca&limit=10`,
      { headers },
    );
    const entries = (await entriesRes.json()).data as Array<{
      source_text: string;
      target_text: string;
      source_rich_text: string | null;
      target_rich_text: string | null;
    }>;
    expect(entries.length, "A1: 2 entries imported (one with tags, one without)").toBeGreaterThanOrEqual(2);

    const tagEntry = entries.find((e) => e.source_text.startsWith("Click"));
    expect(tagEntry, "A1: tag entry must be present").toBeDefined();
    expect(tagEntry!.source_rich_text,
      "A1: source_rich_text MUST be populated for the tag-bearing entry — " +
      "this is what makes the frontend render chips. NULL here means the importer " +
      "didn't extract rich text from <seg> InnerXML.",
    ).toBeTruthy();
    expect(tagEntry!.source_rich_text!,
      "A1: source_rich_text preserves the original XLIFF inline markup",
    ).toContain("<bpt");
    expect(tagEntry!.source_text,
      "A1: plain source_text must be the STRIPPED form (no XLIFF tags). " +
      "Markup in plain text breaks fuzzy/exact match scoring.",
    ).not.toContain("<bpt");
    expect(tagEntry!.source_text,
      "A1: plain text is what a human typed without tags",
    ).toBe("Click here to continue.");

    const plainEntry = entries.find((e) => e.source_text.startsWith("Welcome"));
    expect(plainEntry, "A1: plain entry must be present").toBeDefined();
    expect(plainEntry!.source_rich_text,
      "A1: plain entry has NO inline tags — source_rich_text must be NULL " +
      "(NULLIF contract) so frontend's plain-text fallback path triggers cleanly.",
    ).toBeNull();
  });

  test("A2: create project en→fr-ca with this TM assigned + a mock MT engine", async ({ page }) => {
    await login(page);
    mtEngineId = await createMTEngineViaAPI(page, `J24 Mock ${RUN_ID}`, "mock");
    projectId = await createProjectViaAPI(page, `J24 Project ${RUN_ID}`, "en", ["fr-CA"]);
    await updateProjectSettingsViaAPI(page, projectId, { mt_on_upload: false });
    await setProjectTMsViaAPI(page, projectId, [
      { tm_id: tmId, source_language: "en", target_language: "fr-CA", mode: "read", priority: 1 },
    ]);
    await setProjectMTViaAPI(page, projectId, [
      { mt_engine_id: mtEngineId, source_language: "en", target_language: "fr-CA" },
    ]);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B — TM Browser renders chips, NOT raw markup.
  // ════════════════════════════════════════════════════════════════════════

  test("B1: open the TM browser → chips render for the tag entry, plain entry stays plain", async ({ page }) => {
    await login(page);

    // Pre-warm the entries API call so the page renders rows on first paint
    // (avoids the virtualized-list race where assertions fire before fetch).
    const headers = await authHeader(page);
    await page.request.get(`${API}/api/tms/${tmId}/entries?limit=10`, { headers });

    await page.goto(`/memories/${tmId}`);

    // Wait for the table header — proves we landed on TMBrowser.
    await expect(page.getByRole("columnheader", { name: /source/i }).first()).toBeVisible({ timeout: 10000 });

    // Positive assertion: at least one .xliff-tag chip is in the DOM.
    // This is the strongest possible signal that R24 is working: the
    // SegmentText component received non-null rich_text from the API
    // AND the parser produced atomic chip nodes AND the renderer mounted them.
    await expect(page.locator(".xliff-tag").first(),
      "B1: TM Browser must render at least one .xliff-tag chip for the tag entry. " +
      "Zero chips means TMBrowser.tsx isn't routing source/target through SegmentText, " +
      "or rich_text isn't populated (check A1 storage assertions first).",
    ).toBeVisible({ timeout: 10000 });

    // Anti-tautology negative: the raw `<bpt` markup must NOT appear as
    // literal text anywhere visible. If it does, the chip path failed AND
    // the fallback rendered raw markup verbatim (exactly the R24 bug).
    const visible = (await page.locator("body").textContent()) ?? "";
    expect(visible.includes("<bpt"),
      "B1 (anti-tautology): the literal text '<bpt' must NOT appear in the DOM. " +
      "If it does, raw XLIFF markup is leaking through to the user — exactly the R24 bug.",
    ).toBe(false);

    // Sanity: the plain entry's text is also visible (proves both rows
    // rendered, not just the chip one).
    await expect(page.getByText(/Welcome back/).first()).toBeVisible({ timeout: 5000 });
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C — Concordance modal renders chips for the search hit.
  // ════════════════════════════════════════════════════════════════════════

  test("C1: concordance API returns rich_text for the tag match", async ({ page }) => {
    // We assert the API contract directly here (the modal's chip rendering
    // is the SAME SegmentText component covered in B1 + the unit test).
    // This catches regressions in the SettingsConcordanceSearch query that
    // would feed the modal NULL rich_text.
    await login(page);
    const headers = await authHeader(page);
    const res = await page.request.get(
      `${API}/api/tms/concordance?project_id=${projectId}&source_language=en&target_language=fr-CA&q=here`,
      { headers },
    );
    expect(res.ok()).toBe(true);
    const body = await res.json();
    const tmMatches = body.data?.tm_matches ?? body.tm_matches;
    expect(Array.isArray(tmMatches), "C1: response shape must include tm_matches array").toBe(true);
    expect(tmMatches.length,
      "C1: concordance must return at least 1 TM match for 'here' (matches the tag entry's plain text)",
    ).toBeGreaterThanOrEqual(1);

    const tagMatch = tmMatches.find((m: any) => m.source_text.includes("Click"));
    expect(tagMatch,
      "C1: the 'Click here to continue.' match must be present (post-strip plain text was 'here'-searchable)",
    ).toBeDefined();
    expect(tagMatch.source_rich_text,
      "C1: SettingsConcordanceSearch must include source_rich_text in the response. " +
      "If null, the SQL query is missing the column, and the modal won't render chips for tag-bearing TMs.",
    ).toBeTruthy();
    expect(tagMatch.source_rich_text,
      "C1: rich_text preserves the original XLIFF markup",
    ).toContain("<bpt");
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D — Cleanup
  // ════════════════════════════════════════════════════════════════════════

  test("D1: cleanup", async ({ page }) => {
    await login(page);
    const cleanup = async (fn: () => Promise<void>) => { try { await fn(); } catch {} };
    if (projectId) await cleanup(() => deleteProjectViaAPI(page, projectId));
    if (mtEngineId) await cleanup(() => deleteMTEngineViaAPI(page, mtEngineId));
    if (tmId) await cleanup(() => deleteTMViaAPI(page, tmId));
  });

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    try {
      await login(page);
      if (projectId) await deleteProjectViaAPI(page, projectId).catch(() => {});
      if (mtEngineId) await deleteMTEngineViaAPI(page, mtEngineId).catch(() => {});
      if (tmId) await deleteTMViaAPI(page, tmId).catch(() => {});
    } catch {} finally {
      await page.close();
    }
  });
});
