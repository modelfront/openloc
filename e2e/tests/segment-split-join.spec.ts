import { test, expect, type Page } from "@playwright/test";
import { login, authHeaders, createIsolatedCATJob } from "./helpers";

// Split and join in the CAT editor: a split changes only the job it is made
// in, numbers the pieces 1 and 1.1, and joining them gives back the imported
// segment.

const LINES = ["Press the button and wait for the light", "Next one"];

interface Index {
  segment_ids: number[];
  positions: number[];
  sub_positions: number[];
}

async function index(page: Page, jobId: number): Promise<Index> {
  const res = await page.request.get(`/api/jobs/${jobId}/editor/index`);
  expect(res.ok()).toBe(true);
  return (await res.json()).data;
}

/** Puts the caret in the open row's source text, `offset` characters in, as
 *  a click there does. */
async function placeSourceCaret(page: Page, offset: number) {
  await page.getByRole("textbox", { name: "Source text" }).evaluate((field, at) => {
    (field as HTMLElement).focus();
    const walker = document.createTreeWalker(field, NodeFilter.SHOW_TEXT);
    const node = walker.nextNode();
    if (!node) throw new Error("no source text");
    const range = document.createRange();
    range.setStart(node, at);
    range.collapse(true);
    const sel = document.getSelection()!;
    sel.removeAllRanges();
    sel.addRange(range);
  }, offset);
}

const editResponse = (page: Page, action: "split" | "join") =>
  page.waitForResponse(r => r.url().endsWith(`/segments/${action}`) && r.request().method() === "POST");

test.describe("Split and join", () => {
  let projectIds: number[] = [];
  let headers: Record<string, string> = {};

  test.beforeEach(async ({ page }) => {
    test.slow();
    await login(page);
    headers = await authHeaders(page);
  });

  test.afterEach(async ({ request }) => {
    for (const id of projectIds) await request.delete(`/api/projects/${id}`, { headers }).catch(() => {});
    projectIds = [];
  });

  test("a split changes only its job, and joining the pieces gives the segment back", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `split-join-${Date.now()}.txt`,
      content: LINES.join("\n"),
      targetLanguages: ["de", "fr"],
    });
    const jobs = (await (await page.request.get(`/api/projects/${job.projectId}/jobs`, { headers })).json()).data as { id: number; target_language: string }[];
    const de = jobs.find(j => j.target_language === "de")!.id;
    const fr = jobs.find(j => j.target_language === "fr")!.id;
    const frBefore = await index(page, fr);
    const deBefore = await index(page, de);

    await page.goto(`/projects/${job.projectId}/jobs/${de}/edit`);
    await page.getByText(LINES[0]).click();
    // Split waits for a cursor in the source, and says so.
    const splitButton = page.getByTestId("split-segment-btn");
    await expect(splitButton).toHaveAttribute("aria-disabled", "true");
    await splitButton.hover();
    await expect(page.getByText("First click in the source text where you want to cut the segment.")).toBeVisible();
    await placeSourceCaret(page, "Press the button".length);
    await expect(splitButton).not.toHaveAttribute("aria-disabled");
    // The source takes a caret but no edits.
    await page.keyboard.press("Backspace");
    await page.keyboard.type("x");
    await expect(page.getByRole("textbox", { name: "Source text" })).toHaveText(LINES[0]);
    // The rows change in one step: none shows as loading on the way.
    await page.evaluate(() => {
      const w = window as unknown as { sawLoading: boolean };
      w.sawLoading = false;
      new MutationObserver(() => {
        if (document.querySelector('[data-testid="segment-row-loading"]')) w.sawLoading = true;
      }).observe(document.body, { subtree: true, childList: true });
    });
    const split = editResponse(page, "split");
    await page.getByTestId("split-segment-btn").click();
    expect((await split).status()).toBe(200);

    await expect(page.getByTestId("segment-select-1.1")).toBeVisible();
    expect(await page.evaluate(() => (window as unknown as { sawLoading: boolean }).sawLoading), "a row showed as loading during the split").toBe(false);
    await expect(page.getByText("Press the button", { exact: true })).toBeVisible();
    await expect(page.getByText("and wait for the light", { exact: true })).toBeVisible();
    const deSplit = await index(page, de);
    expect(deSplit.positions).toEqual([0, 0, 1]);
    expect(deSplit.sub_positions).toEqual([0, 1, 0]);
    expect(await index(page, fr)).toEqual(frBefore);

    // The split reopens the first piece, which joins with the next one.
    const join = editResponse(page, "join");
    await page.keyboard.press("Alt+Shift+KeyJ");
    expect((await join).status()).toBe(200);
    await expect(page.getByTestId("segment-select-1.1")).toHaveCount(0);
    await expect(page.getByText(LINES[0], { exact: true })).toBeVisible();
    expect((await index(page, de)).segment_ids).toEqual(deBefore.segment_ids);
  });

  test("joining two sentences of one line keeps the first number; the other language keeps them", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `join-sentences-${Date.now()}.txt`,
      content: "One one. Two two. Three three.",
      targetLanguages: ["de", "fr"],
    });
    const jobs = (await (await page.request.get(`/api/projects/${job.projectId}/jobs`, { headers })).json()).data as { id: number; target_language: string }[];
    const de = jobs.find(j => j.target_language === "de")!.id;
    const fr = jobs.find(j => j.target_language === "fr")!.id;
    const frBefore = await index(page, fr);
    expect((await index(page, de)).positions).toEqual([0, 1, 2]);

    await page.goto(`/projects/${job.projectId}/jobs/${de}/edit`);
    await page.getByText("One one.", { exact: true }).click();
    const join = editResponse(page, "join");
    await page.keyboard.press("Alt+Shift+KeyJ");
    expect((await join).status()).toBe(200);

    await expect(page.getByText("One one. Two two.", { exact: true })).toBeVisible();
    await expect(page.getByTestId("segment-select-2")).toHaveCount(0);
    await expect(page.getByTestId("segment-select-3")).toBeVisible();
    expect((await index(page, de)).positions).toEqual([0, 2]);
    expect(await index(page, fr)).toEqual(frBefore);
  });

  test("Join on the selection bar joins the selected sentences", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `join-selection-${Date.now()}.txt`,
      content: "One one. Two two. Three three.",
    });
    await page.goto(`/projects/${job.projectId}/jobs/${job.jobId}/edit`);
    await expect(page.getByText("One one.", { exact: true })).toBeVisible();

    await page.getByTestId("segment-select-1").click();
    await page.getByTestId("segment-select-2").click({ modifiers: ["Shift"] });
    await expect(page.getByTestId("segment-selected-count")).toHaveText("2 segments selected");
    const join = editResponse(page, "join");
    await page.getByTestId("segment-bulk-join").click();
    expect((await join).status()).toBe(200);

    await expect(page.getByText("One one. Two two.", { exact: true })).toBeVisible();
    await expect(page.getByTestId("segment-select-2")).toHaveCount(0);
    expect((await index(page, job.jobId)).positions).toEqual([0, 2]);
  });
});
