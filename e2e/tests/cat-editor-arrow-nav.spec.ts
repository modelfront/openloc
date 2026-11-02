import { test, expect, type Page } from "@playwright/test";
import { login, authHeaders, createIsolatedCATJob, seedDrafts, lockTranslationViaAPI, segmentEditor } from "./helpers";

// Up and Down in the translation move between lines of a segment, and from
// its first or last line into the segment above or below, with the cursor at
// the same horizontal place on the line it enters.

const LINES = ["First source line", "Second source line", "Third source line"];
// Long enough to wrap in the translation column; words, not digits (the job's
// QA profile flags a number the source does not have).
const LONG_A = "Erste lange Übersetzung, die über mehrere Zeilen läuft, damit die Pfeiltasten zuerst innerhalb des Segments wandern und erst danach weiter";
const LONG_B = "Zweite lange Übersetzung, die ebenfalls über mehrere Zeilen läuft und den Cursor an derselben Stelle aufnehmen soll wie das Segment davor";
const TARGETS = [LONG_A, LONG_B, "Dritte Zeile"];

interface Caret { x: number; top: number; bottom: number }

/** The open editor's caret, and its first and last line boxes. */
async function caret(page: Page): Promise<{ caret: Caret; firstLineBottom: number; lastLineTop: number; text: string }> {
  return segmentEditor(page).first().evaluate(el => {
    const sel = document.getSelection()!;
    const r = sel.getRangeAt(0).getClientRects()[0] ?? sel.getRangeAt(0).getBoundingClientRect();
    const all = document.createRange();
    all.selectNodeContents(el);
    const lines = [...all.getClientRects()].filter(l => l.width > 0);
    return {
      caret: { x: r.left, top: r.top, bottom: r.bottom },
      firstLineBottom: Math.min(...lines.map(l => l.bottom)),
      lastLineTop: Math.max(...lines.map(l => l.top)),
      text: el.textContent ?? "",
    };
  });
}

/** Clicks the open editor `dx` pixels in, on its first or last line. */
async function clickLine(page: Page, line: "first" | "last", dx: number) {
  const box = (await segmentEditor(page).first().boundingBox())!;
  await page.mouse.click(box.x + dx, line === "first" ? box.y + 12 : box.y + box.height - 12);
}

/** Opens the first row with a click at the start of its closed translation,
 *  which puts the caret there. A second click right after opening can be
 *  undone by ProseMirror's focus write-back (docs/internal/testing.md, "A text
 *  selection made right after activating a segment is gone by the next step"). */
async function openAtLineStart(page: Page) {
  const box = (await page.getByTestId("segment-target-box").first().boundingBox())!;
  await page.mouse.click(box.x + 2, box.y + 12);
}

/** How many characters of the open editor's text sit before the caret. */
const caretOffset = (page: Page) => segmentEditor(page).first().evaluate(el => {
  const sel = document.getSelection()!;
  const r = document.createRange();
  r.selectNodeContents(el);
  r.setEnd(sel.focusNode!, sel.focusOffset);
  return r.toString().length;
});

test.describe("Arrow keys between segments", () => {
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

  test("Down on the last line opens the next segment at the same place; Up on the first line goes back", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `arrow-nav-${Date.now()}.txt`,
      content: LINES.join("\n"),
    });
    await seedDrafts(page, job, TARGETS);
    await job.navigateToEditor(/First source line/);

    await page.locator("[data-translation-cell]").first().click();
    await expect(segmentEditor(page).first()).toHaveText(LONG_A);

    // Down on the first line stays in the segment.
    await clickLine(page, "first", 60);
    await page.keyboard.press("ArrowDown");
    let at = await caret(page);
    expect(at.text).toBe(LONG_A);
    expect(at.caret.top, "the caret leaves the first line").toBeGreaterThanOrEqual(at.firstLineBottom - 1);
    expect(at.caret.bottom, "the caret is not on the last line").toBeLessThanOrEqual(at.lastLineTop + 1);

    // Down on the last line opens the next segment, caret on its first line,
    // below where it was.
    await clickLine(page, "last", 60);
    const before = (await caret(page)).caret;
    await page.keyboard.press("ArrowDown");
    await expect(segmentEditor(page).first()).toHaveText(LONG_B);
    await expect.poll(async () => (await caret(page)).text).toBe(LONG_B);
    at = await caret(page);
    expect(at.caret.bottom, "the caret is on the first line").toBeLessThanOrEqual(at.firstLineBottom + 1);
    expect(Math.abs(at.caret.x - before.x), "the caret keeps its horizontal place").toBeLessThanOrEqual(8);

    // Up on the first line goes back, caret on the previous segment's last line.
    await page.keyboard.press("ArrowUp");
    await expect(segmentEditor(page).first()).toHaveText(LONG_A);
    await expect.poll(async () => (await caret(page)).text).toBe(LONG_A);
    at = await caret(page);
    expect(at.caret.top, "the caret is on the last line").toBeGreaterThanOrEqual(at.lastLineTop - 1);
    expect(Math.abs(at.caret.x - before.x), "the caret keeps its horizontal place").toBeLessThanOrEqual(8);

    // Shift+Down on the last line selects; it does not leave the segment.
    await clickLine(page, "last", 60);
    await page.keyboard.press("Shift+ArrowDown");
    await expect(segmentEditor(page).first()).toHaveText(LONG_A);
  });

  // Like an IDE: a run of Up/Down keeps the column it started in, sitting at
  // the end of a shorter line on the way.
  test("Up and Down keep their column through shorter and empty segments", async ({ page }) => {
    const sources = ["Line one", "Line two", "Line three", "Line four"];
    const targets = ["Abcde", "Ab", "", "Abcdefghij"];
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `arrow-nav-column-${Date.now()}.txt`,
      content: sources.join("\n"),
    });
    await seedDrafts(page, job, targets);
    await job.navigateToEditor(/Line one/);

    const openSource = page.getByTestId("active-source-text");
    await openAtLineStart(page);
    await expect(segmentEditor(page).first()).toHaveText("Abcde");
    await expect(segmentEditor(page).first()).toBeFocused();
    for (let i = 0; i < 3; i++) await page.keyboard.press("ArrowRight");
    expect(await caretOffset(page)).toBe(3);

    const expectAt = async (source: string, offset: number) => {
      await expect(openSource).toHaveText(source);
      await expect.poll(() => caretOffset(page), `caret in "${source}"`).toBe(offset);
    };
    await page.keyboard.press("ArrowDown");
    await expectAt("Line two", 2);
    await page.keyboard.press("ArrowDown");
    await expectAt("Line three", 0);
    await page.keyboard.press("ArrowDown");
    await expectAt("Line four", 3);
    await page.keyboard.press("ArrowUp");
    await expectAt("Line three", 0);
    await page.keyboard.press("ArrowUp");
    await expectAt("Line two", 2);
    await page.keyboard.press("ArrowUp");
    await expectAt("Line one", 3);
  });

  test("Up and Down keep their column through the lines of one segment", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `arrow-nav-lines-${Date.now()}.txt`,
      content: "Only line\n",
    });
    await seedDrafts(page, job, ["Abcde\nAb\n\nAbcdefghij"]);
    await job.navigateToEditor(/Only line/);

    await openAtLineStart(page);
    await expect(segmentEditor(page).first()).toContainText("Abcdefghij");
    await expect(segmentEditor(page).first()).toBeFocused();
    for (let i = 0; i < 3; i++) await page.keyboard.press("ArrowRight");
    expect(await caretOffset(page)).toBe(3);

    // Offsets count characters only: line breaks add none.
    for (const [key, offset] of [["ArrowDown", 7], ["ArrowDown", 7], ["ArrowDown", 10], ["ArrowUp", 7], ["ArrowUp", 7], ["ArrowUp", 3]] as const) {
      await page.keyboard.press(key);
      expect(await caretOffset(page), `after ${key}`).toBe(offset);
    }
  });

  test("Down, Up, Tab and Shift+Tab move through a locked segment", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `arrow-nav-locked-${Date.now()}.txt`,
      content: LINES.join("\n"),
    });
    const trs = await seedDrafts(page, job, ["Erste Zeile", "Zweite Zeile", "Dritte Zeile"]);
    await lockTranslationViaAPI(page, job, trs[1].id);
    await job.navigateToEditor(/First source line/);

    const openSource = page.getByTestId("active-source-text");
    await page.locator("[data-translation-cell]").first().click();
    await expect(openSource).toHaveText(LINES[0]);
    await expect(segmentEditor(page).first()).toBeFocused();

    await page.keyboard.press("ArrowDown");
    await expect(openSource).toHaveText(LINES[1]);
    await page.keyboard.press("ArrowDown");
    await expect(openSource).toHaveText(LINES[2]);
    await expect(segmentEditor(page).first()).toBeFocused();

    await page.keyboard.press("ArrowUp");
    await expect(openSource).toHaveText(LINES[1]);
    await page.keyboard.press("ArrowUp");
    await expect(openSource).toHaveText(LINES[0]);
    await expect(segmentEditor(page).first()).toBeFocused();

    // Tab and Shift+Tab move on from a locked segment too.
    await page.keyboard.press("Tab");
    await expect(openSource).toHaveText(LINES[1]);
    await page.keyboard.press("Tab");
    await expect(openSource).toHaveText(LINES[2]);
    await page.keyboard.press("Shift+Tab");
    await expect(openSource).toHaveText(LINES[1]);
    await page.keyboard.press("Shift+Tab");
    await expect(openSource).toHaveText(LINES[0]);
  });
});
