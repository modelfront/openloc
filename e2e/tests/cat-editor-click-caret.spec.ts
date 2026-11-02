import { test, expect, type Locator, type Page } from "@playwright/test";
import { login, authHeaders, createIsolatedCATJob, segmentEditor } from "./helpers";

// A closed row's translation is drawn as the editor that replaces it, so one
// click opens the row AND puts the caret where the user clicked: in the
// middle of a word, on a second line, on either side of a tag chip. Only a
// real browser lays out text and maps a click to a caret, hence E2E.

const long = "A clause that keeps going across the column, ".repeat(5).trim();
const xliff = `<?xml version="1.0" encoding="UTF-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
<file source-language="en" target-language="es" datatype="plaintext" original="caret"><body>
<trans-unit id="1"><source>Short one</source><target>Corto uno</target></trans-unit>
<trans-unit id="2"><source>${long}</source><target>${long} END</target></trans-unit>
<trans-unit id="3"><source>First line
Second line</source><target>Primera línea
Segunda línea</target></trans-unit>
<trans-unit id="4"><source>Click <g id="1">here</g> now</source><target>Haga clic <g id="1">aquí</g> ahora</target></trans-unit>
</body></file></xliff>`;

/** Viewport point of the `k`th character of `needle` in row `idx`'s closed
 *  translation: its left edge, so the caret goes before it. */
function charPoint(page: Page, idx: number, needle: string, k: number) {
  return textPoint(page.locator(`[data-segment-idx="${idx}"]`).getByTestId("segment-target-box"), needle, k);
}

/** Viewport point of the `k`th character of `needle` in `box`: its left edge. */
async function textPoint(box: Locator, needle: string, k: number) {
  await expect(box).toContainText(needle);
  return box.evaluate((el, [needle, k]) => {
    const w = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    for (let n = w.nextNode(); n; n = w.nextNode()) {
      const i = n.textContent!.indexOf(needle as string);
      if (i < 0) continue;
      const r = document.createRange();
      r.setStart(n, i + (k as number));
      r.setEnd(n, i + (k as number) + 1);
      const b = r.getBoundingClientRect();
      return { x: b.left + 1, y: (b.top + b.bottom) / 2 };
    }
    throw new Error(`no ${needle}`);
  }, [needle, k] as const);
}

/** Viewport point just inside the left or right edge of row `idx`'s first chip. */
async function chipPoint(page: Page, idx: number, side: "left" | "right") {
  const chip = page.locator(`[data-segment-idx="${idx}"]`).getByTestId("segment-target-box").locator(".xliff-tag").first();
  const b = (await chip.boundingBox())!;
  return { x: side === "left" ? b.x + 2 : b.x + b.width - 2, y: b.y + b.height / 2 };
}

/** The open editor's caret: the text before it (chips left out) and how many
 *  chips sit before it. */
async function caret(page: Page) {
  const editor = segmentEditor(page);
  await expect(editor).toBeFocused();
  return editor.evaluate(el => {
    const sel = getSelection()!;
    const r = document.createRange();
    r.selectNodeContents(el);
    r.setEnd(sel.focusNode!, sel.focusOffset);
    const frag = r.cloneContents();
    const chips = frag.querySelectorAll(".xliff-tag");
    chips.forEach(c => c.remove());
    return { collapsed: sel.isCollapsed, before: frag.textContent, chips: chips.length };
  });
}

test.describe("Click to caret in the CAT editor", () => {
  let projectIds: number[] = [];

  test.beforeEach(async ({ page }) => {
    test.slow();
    await login(page);
  });

  test.afterEach(async ({ request, page }) => {
    const headers = await authHeaders(page).catch(() => ({}));
    for (const id of projectIds) await request.delete(`/api/projects/${id}`, { headers }).catch(() => {});
    projectIds = [];
  });

  test("one click on a closed translation opens it with the caret where the user clicked", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `click-caret-${Date.now()}.xlf`,
      contentType: "application/xliff+xml",
      content: xliff,
    });
    await job.navigateToEditor(/Short one/);

    // Each case clicks a closed row: consecutive cases use different rows.
    const cases: { name: string; point: () => Promise<{ x: number; y: number }>; want: { before: string; chips: number } }[] = [
      { name: "short", point: () => charPoint(page, 0, "Corto", 3), want: { before: "Cor", chips: 0 } },
      { name: "before a chip", point: () => chipPoint(page, 3, "left"), want: { before: "Haga clic ", chips: 0 } },
      { name: "long, last line", point: () => charPoint(page, 1, "END", 1), want: { before: `${long} E`, chips: 0 } },
      { name: "after a chip", point: () => chipPoint(page, 3, "right"), want: { before: "Haga clic ", chips: 1 } },
      { name: "second line", point: () => charPoint(page, 2, "Segunda", 3), want: { before: "Primera líneaSeg", chips: 0 } },
    ];
    for (const c of cases) {
      const { x, y } = await c.point();
      await page.mouse.click(x, y);
      expect(await caret(page), c.name).toEqual({ collapsed: true, ...c.want });
    }
  });

  // A selection dragged in a closed row: in the source it stays, with the row
  // closed, to be copied; in the translation the row opens with it selected.
  test("a selection dragged in a closed row stays in the source and opens the translation with it", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `drag-caret-${Date.now()}.xlf`,
      contentType: "application/xliff+xml",
      content: xliff,
    });
    await job.navigateToEditor(/Short one/);
    const drag = async (box: Locator, needle: string, from: number, to: number) => {
      const a = await textPoint(box, needle, from);
      const b = await textPoint(box, needle, to);
      await page.mouse.move(a.x, a.y);
      await page.mouse.down();
      await page.mouse.move(b.x, b.y, { steps: 5 });
      await page.mouse.up();
    };

    await drag(page.locator('[data-segment-idx="0"] .segment-source-box'), "Short one", 0, 5);
    await expect(segmentEditor(page), "the row stays closed").toHaveCount(0);
    expect(await page.evaluate(() => getSelection()!.toString())).toBe("Short");

    await drag(page.locator('[data-segment-idx="0"]').getByTestId("segment-target-box"), "Corto uno", 0, 5);
    await expect(segmentEditor(page)).toBeFocused();
    expect(await segmentEditor(page).evaluate(() => {
      const sel = getSelection()!;
      return { collapsed: sel.isCollapsed, text: sel.toString() };
    })).toEqual({ collapsed: false, text: "Corto" });
  });
});
