import { test, expect, type Page } from "@playwright/test";
import { login, authHeaders, createIsolatedCATJob, seedDrafts, segmentEditor } from "./helpers";

// The segment list is virtualized: every row is placed from its measured
// height, and the open row's action bar reaches above the row, under the
// sticky column header when the row sits right below it. Only a real browser
// lays out and measures, hence E2E.

const sticky = (page: Page) => page.getByTestId("segment-column-resize-0").locator("../..");

/** Each mounted row's top and bottom, in list order. */
const rowEdges = (page: Page) => page.evaluate(() =>
  Array.from(document.querySelectorAll<HTMLElement>("[data-index]"))
    .sort((a, b) => Number(a.dataset.index) - Number(b.dataset.index))
    .map(el => { const r = el.getBoundingClientRect(); return { top: r.top, bottom: r.bottom }; }));

/** The open row's action bar: its box, whether the page shows it at its centre
 *  (nothing on top), and where the box it sits on starts. */
const actionBar = (page: Page) => page.getByTestId("segment-editor-actions").evaluate(bar => {
  const r = bar.getBoundingClientRect();
  const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
  const frame = bar.closest(".segment-editor-frame")!.getBoundingClientRect();
  return { top: r.top, centre: r.top + r.height / 2, shown: !!hit && bar.contains(hit), frameTop: frame.top, frameBottom: frame.bottom };
});

test.describe("CAT editor row geometry", () => {
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

  // Rows are placed from their measured heights, one-line and multi-line,
  // open or closed: no gap between two rows and no overlap.
  test("rows sit flush, open or closed: each starts where the one above ends", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `row-flush-${Date.now()}.txt`,
      content: ["One", "Two", "Three", "Four", "Five"].join("\n"),
    });
    await seedDrafts(page, job, ["Uno", "Dos\nlínea dos", "Tres", "Cuatro", "Cinco"]);
    await job.navigateToEditor(/Three/);

    const gaps = async () => {
      const rows = await rowEdges(page);
      return rows.slice(1).map((r, i) => Math.round(Math.abs(r.top - rows[i].bottom)));
    };
    await expect.poll(gaps, "closed rows").toEqual([0, 0, 0, 0]);
    for (const idx of [2, 1]) {
      await page.locator(`[data-segment-idx="${idx}"] [data-translation-cell]`).click();
      await expect(segmentEditor(page).first()).toBeFocused();
      await expect.poll(gaps, `row ${idx} open`).toEqual([0, 0, 0, 0]);
    }
  });

  test("the open row's action bar is never under the sticky header", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `row-bar-${Date.now()}.txt`,
      content: Array.from({ length: 40 }, (_, i) => `Source line ${i + 1}`).join("\n"),
    });
    await seedDrafts(page, job, Array.from({ length: 40 }, (_, i) => `Línea ${i + 1}`));
    await job.navigateToEditor(/Source line 1\b/);
    const headerBottom = async () => (await sticky(page).boundingBox())!.y + (await sticky(page).boundingBox())!.height;

    // The first row: the bar hangs below its box.
    await page.locator('[data-segment-idx="0"] [data-translation-cell]').click();
    await expect(segmentEditor(page).first()).toBeFocused();
    let bar = await actionBar(page);
    expect(bar.shown, "first row: nothing covers the bar").toBe(true);
    expect(bar.top, "first row: the bar starts below the header").toBeGreaterThanOrEqual(await headerBottom() - 0.5);
    expect(Math.abs(bar.centre - bar.frameBottom), "first row: the bar sits on the box's bottom border").toBeLessThanOrEqual(1);

    // A row further down: the bar sits on its box's top border.
    await page.locator('[data-segment-idx="2"] [data-translation-cell]').click();
    await expect(segmentEditor(page).first()).toBeFocused();
    bar = await actionBar(page);
    expect(bar.shown, "row 3: nothing covers the bar").toBe(true);
    expect(Math.abs(bar.centre - bar.frameTop), "row 3: the bar sits on the box's top border").toBeLessThanOrEqual(1);

    // Scrolled so a row's top is just under the header, as after a scroll or
    // Shift+Tab: its bar would reach under the header, so it hangs below.
    const row = 20;
    await sticky(page).locator("..").evaluate(el => { el.scrollTop = 20 * 33; });
    await expect(page.locator(`[data-segment-idx="${row}"]`)).toBeVisible();
    await sticky(page).locator("..").evaluate((el, row) => {
      const header = el.querySelector(".sticky")!.getBoundingClientRect();
      const top = document.querySelector(`[data-segment-idx="${row}"]`)!.getBoundingClientRect().top;
      el.scrollTop += top - header.bottom - 2;
    }, row);
    await page.locator(`[data-segment-idx="${row}"] [data-translation-cell]`).click();
    await expect(segmentEditor(page).first()).toBeFocused();
    await expect.poll(async () => (await actionBar(page)).shown, "row under the header: nothing covers the bar").toBe(true);
    bar = await actionBar(page);
    expect(bar.top, "row under the header: the bar starts below the header").toBeGreaterThanOrEqual(await headerBottom() - 0.5);
  });
});
