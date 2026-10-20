import { test, expect } from "@playwright/test";
import { readFileSync } from "fs";
import { join } from "path";
import { createIsolatedCATJob, login } from "./helpers";

/**
 * Generic XML source files.
 *
 * The fixture is a trimmed SCHEMA ST4 (Quanos) export with invented content.
 * It deliberately exercises every extraction rule at once:
 * <sup>/<sub>/<br>/<meta-ref> inline chips, a sentence-split paragraph, a <b>
 * wrapper that gets unwrapped, an &lt; entity, and a DOCTYPE + file-hash that
 * must survive export untouched.
 */

const FIXTURE = readFileSync(
  join(__dirname, "..", "fixtures", "journey-st4.xml"),
  "utf-8"
);

/** Segment count the parser produces for the fixture. */
const EXPECTED_SEGMENTS = 17;

interface ApiSegment {
  id: number;
  source_text: string;
  source_rich_text: string | null;
  segment_key: string | null;
  context: string;
}

interface ApiTranslation {
  id: number;
  segment_id: number;
}

async function fetchSegments(
  page: import("@playwright/test").Page,
  fileId: number,
  token: string | null
): Promise<ApiSegment[]> {
  const res = await page.request.get(`/api/files/${fileId}/segments`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  expect(res.ok(), `list segments: ${res.status()}`).toBe(true);
  return (await res.json()).data;
}

test.describe("Generic XML upload", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("parses an ST4 export into segments with inline tag chips", async ({
    page,
  }) => {
    const job = await createIsolatedCATJob(page, {
      fileName: `st4-${Date.now()}.xml`,
      content: FIXTURE,
      contentType: "application/xml",
      targetLanguages: ["de"],
    });

    try {
      const segments = await fetchSegments(page, job.fileId, job.token);
      expect(segments).toHaveLength(EXPECTED_SEGMENTS);

      const texts = segments.map((s) => s.source_text);

      // Wrapper <b> unwrapped rather than becoming a lone chip.
      expect(texts).toContain("General specifications");
      // Entity decoded — the translator must never see "&lt;5 seconds".
      expect(texts).toContain("<5 seconds");
      // Paragraph split into two sentences.
      expect(texts).toContain(
        "Please store this addendum with your pump documentation."
      );
      expect(texts).toContain("Keep it for future reference.");

      // Inline elements arrive as XLIFF chips in source_rich_text.
      const sup = segments.find((s) =>
        s.source_text.startsWith("The F7m Flow Sensor")
      );
      expect(sup?.source_rich_text).toContain('ctype="x-html-sup"');

      const metaRef = segments.find((s) =>
        s.source_text.startsWith("For H2O, the flow mode")
      );
      expect(metaRef?.source_rich_text).toContain('ctype="x-xml-meta-ref"');

      // Cross-references: a <meta-ref> renders text generated from
      // another node, so that text stays OUT of the segment — it's translated
      // once at its defining <meta> node instead. Keeping it out is what lets
      // this source align with a TM that models the same construct as a
      // placeholder, turning fuzzy matches into exact ones.
      expect(metaRef?.source_text).not.toContain("Eco");
      expect(metaRef?.source_text).toContain("can be set to  or Boost");
      // Self-closing, not a pair.
      expect(metaRef?.source_rich_text).toMatch(/<x id="\d+" ctype="x-xml-meta-ref"/);
      // …while the defining node stays translatable.
      expect(texts).toContain("Eco");

      // Enclosing ST4 node title surfaces as segment context.
      const tableCell = segments.find(
        (s) => s.source_text === "Sample flow rate"
      );
      expect(tableCell?.context).toBe("Revised Information");

      // Keys are element paths, so a sentence-split paragraph is addressable.
      const secondSentence = segments.find(
        (s) => s.source_text === "Keep it for future reference."
      );
      expect(secondSentence?.segment_key).toMatch(/#2$/);

      // The cross-reference's attributes ride along on the chip so the editor
      // can show them on hover — a <meta-ref> is only identifiable by its
      // linkid, and the ctype can't carry that.
      expect(metaRef?.source_rich_text).toContain("original_markup=");
      expect(metaRef?.source_rich_text).toContain(
        "linkid=&quot;datanode_100000000004&quot;"
      );
      // Formatting tags with nothing to preserve stay lean.
      expect(sup?.source_rich_text).not.toContain("original_markup=");
    } finally {
      await job.cleanup();
    }
  });

  test("sidebar References section jumps to the defining segment", async ({
    page,
  }) => {
    // The {Eco} chip on the "flow mode" row renders the translation
    // of the metalist/meta node — a separate segment. The sidebar lists that
    // reference for the active segment; clicking the entry activates the
    // defining row. The chip itself is deliberately NOT clickable (a jump on
    // plain click teleported linguists who meant to activate the row).
    const job = await createIsolatedCATJob(page, {
      fileName: `st4-refjump-${Date.now()}.xml`,
      content: FIXTURE,
      contentType: "application/xml",
      targetLanguages: ["de"],
    });

    try {
      await job.navigateToEditor(/flow mode/);
      const activeBox = page.getByTestId("active-source-box");

      // Clicking the {Eco} chip activates its OWN row like any other click —
      // no teleport. (The chip renders the resolved term, hence the text.)
      await page.locator("span.xliff-tag", { hasText: "{Eco}" }).click();
      await expect(activeBox).toContainText("flow mode", { timeout: 5000 });

      // The sidebar lists the segment's cross-reference; clicking it jumps to
      // the defining row.
      const refsSection = page.getByTestId("references-section");
      await expect(refsSection).toBeVisible();
      await expect(refsSection).toContainText("{Eco}");
      await refsSection.getByText("{Eco}").click();
      await expect(activeBox).toHaveText("Eco", { timeout: 5000 });
      // A jump that activates the row but leaves it off-screen is half a jump —
      // the whole point is that the linguist SEES the defining segment. The
      // active-row expansion + scroll compensation can move the ground
      // under the smooth scrollToIndex, so pin actual viewport visibility.
      await expect(activeBox).toBeInViewport({ timeout: 5000 });
    } finally {
      await job.cleanup();
    }
  });

  test("exports translations back into the original document structure", async ({
    page,
  }) => {
    const job = await createIsolatedCATJob(page, {
      fileName: `st4-export-${Date.now()}.xml`,
      content: FIXTURE,
      contentType: "application/xml",
      targetLanguages: ["de"],
    });

    try {
      const headers = { Authorization: `Bearer ${job.token}` };

      const segments = await fetchSegments(page, job.fileId, job.token);
      const tagged = segments.find((s) =>
        s.source_text.startsWith("The F7m Flow Sensor")
      );
      expect(tagged, "fixture segment with a <sup> chip").toBeTruthy();
      expect(tagged!.source_rich_text).toBeTruthy();

      const trRes = await page.request.get(
        `/api/files/${job.fileId}/translations?lang=de`,
        { headers }
      );
      expect(
        trRes.ok(),
        `list translations: ${trRes.status()} ${await trRes.text()}`
      ).toBe(true);
      const translations: ApiTranslation[] = (await trRes.json()).data;
      const translation = translations.find(
        (t) => t.segment_id === tagged!.id
      );
      expect(translation, "translation row for the tagged segment").toBeTruthy();

      // Keep the chip, change the words around it — the tag has to come back
      // as a real <sup> element wrapping the same content.
      const richTarget = tagged!.source_rich_text!.replace("The F7", "Der F7");

      const saveRes = await page.request.put(
        `/api/files/${job.fileId}/translations/${translation!.id}`,
        {
          headers,
          data: {
            human_text: "Der F7m Flow Sensor complies with the standard.",
            human_rich_text: richTarget,
            editing_time_ms: 0,
          },
        }
      );
      expect(
        saveRes.ok(),
        `save translation: ${saveRes.status()} ${await saveRes.text()}`
      ).toBe(true);

      const exportRes = await page.request.get(
        `/api/files/${job.fileId}/export?lang=de`,
        { headers }
      );
      expect(exportRes.ok(), `export: ${exportRes.status()}`).toBe(true);
      const exported = await exportRes.text();

      // Translation landed, with the <sup> element restored around it.
      expect(exported).toContain("Der F7<sup>m</sup> Flow Sensor");

      // Everything the byte-splice is supposed to protect is still intact.
      expect(exported).toContain('<!DOCTYPE tms SYSTEM "Tms.dtd"[]>');
      expect(exported).toContain(
        'file-hash="E2ETESTFIXTUREHASH0123456789ABCD"'
      );
      expect(exported).toContain('xml:space="preserve"');
      expect(exported).toContain("<!--TMS Settings");
      // meta-ref cross-reference keeps its linkid.
      expect(exported).toContain('linkid="datanode_100000000004"');
      // Untranslated content passes through unchanged, entity and all.
      expect(exported).toContain("<p>&lt;5 seconds</p>");
    } finally {
      await job.cleanup();
    }
  });
});
