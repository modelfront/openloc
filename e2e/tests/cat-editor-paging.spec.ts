import { test, expect, type Page } from "@playwright/test";
import { authHeaders, createIsolatedCATJob, fillEditorExactly, login, segmentEditor, waitForSegmentGridToSettle } from "./helpers";

/**
 * The CAT editor loads a small index of every segment, then the text of the
 * rows on screen in pages (GET /api/jobs/:id/editor/rows). Search runs on the
 * server, and the open segment is kept in the URL (?seg=<segment id>).
 */

const LINES = 300;
const lineText = (n: number) => `Paging line ${n} of the long file.`;
const longFile = () => Array.from({ length: LINES }, (_, i) => lineText(i + 1)).join("\n") + "\n";

const ROWS = /\/api\/jobs\/\d+\/editor\/rows(\?|$)/;

/** The segment ids the editor asked for, one array per rows request. */
function recordRowRequests(page: Page): number[][] {
  const requests: number[][] = [];
  page.on("request", req => {
    if (req.method() !== "GET" || !ROWS.test(req.url())) return;
    const ids = new URL(req.url()).searchParams.get("segment_ids") ?? "";
    requests.push(ids.split(",").filter(Boolean).map(Number));
  });
  return requests;
}

async function segmentIds(page: Page, jobId: number, token: string | null): Promise<number[]> {
  const res = await page.request.get(`/api/jobs/${jobId}/editor/index`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  expect(res.ok(), `editor index: ${res.status()}`).toBe(true);
  return (await res.json()).data.segment_ids;
}

const row = (page: Page, idx: number) => page.locator(`[data-segment-idx="${idx}"]`);

test.describe("CAT editor paging, search and deep links", () => {
  let projectIds: number[] = [];
  let headers: Record<string, string> = {};

  test.beforeEach(async ({ page }) => {
    await login(page);
    headers = await authHeaders(page);
  });

  test.afterEach(async ({ request }) => {
    for (const id of projectIds) await request.delete(`/api/projects/${id}`, { headers }).catch(() => {});
    projectIds = [];
  });

  const createLongJob = (page: Page) => createIsolatedCATJob(page, {
    fileName: `paging-${Date.now()}-${Math.random().toString(36).slice(2, 8)}.txt`,
    content: longFile(),
    // No QA profile: the confirm in the paging test must not be blocked by QA
    // checks on its made-up target text.
    qaProfile: null,
    onCreated: id => projectIds.push(id),
  });

  test("Ctrl/Cmd+F focuses the segment search", async ({ page }) => {
    test.setTimeout(30_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `find-shortcut-${Date.now()}.txt`,
      content: "find shortcut first line\nfind shortcut second line\n",
      onCreated: id => projectIds.push(id),
    });
    await job.navigateToEditor(/find shortcut first line/);

    const search = page.getByTestId("segment-search");
    await expect(search).not.toBeFocused();
    await page.keyboard.press("ControlOrMeta+f");
    await expect(search).toBeFocused();
  });

  test("the search finds a segment whose text has not loaded yet", async ({ page }) => {
    test.setTimeout(30_000);
    const job = await createLongJob(page);
    const requested = recordRowRequests(page);
    await job.navigateToEditor(new RegExp(lineText(1)));
    const ids = await segmentIds(page, job.jobId, job.token);
    expect(ids).toHaveLength(LINES);
    const target = 271;
    // The match is far below the first pages, so only the server can find it.
    expect(requested.flat()).not.toContain(ids[target - 1]);

    const search = page.getByTestId("segment-search");
    await search.fill(`line ${target} of`);
    await expect(search).toHaveValue(`line ${target} of`);
    const answered = page.waitForResponse(r => r.url().includes(`/api/jobs/${job.jobId}/editor/search?`) && r.ok());
    await search.press("Enter");
    const body = await (await answered).json();
    expect(body.data.segment_ids).toEqual([ids[target - 1]]);

    await expect(page.locator("[data-segment-idx]")).toHaveCount(1);
    await expect(row(page, target - 1)).toContainText(lineText(target));
  });

  test("the open segment is kept in the URL and reopens on reload", async ({ page }) => {
    test.setTimeout(30_000);
    const job = await createLongJob(page);
    await job.navigateToEditor(new RegExp(lineText(1)));
    const ids = await segmentIds(page, job.jobId, job.token);

    await row(page, 4).locator("[data-translation-cell]").click();
    await expect(row(page, 4).getByTestId("segment-editor")).toBeVisible();
    await expect(page).toHaveURL(new RegExp(`[?&]seg=${ids[4]}(&|$)`));

    await page.reload();
    await expect(row(page, 4).getByTestId("segment-editor")).toBeVisible({ timeout: 10_000 });
    await expect(row(page, 4)).toContainText(lineText(5));

    // A link to a segment far down the file opens it, with its text loaded.
    const far = 250;
    await page.goto(`/projects/${job.projectId}/jobs/${job.jobId}/edit?seg=${ids[far]}`);
    await expect(row(page, far).getByTestId("segment-editor")).toBeVisible({ timeout: 10_000 });
    await expect(row(page, far)).toContainText(lineText(far + 1));
  });

  test("scrolling far loads the rows in pages, and a late row can be edited and confirmed", async ({ page }) => {
    test.setTimeout(60_000);
    const job = await createLongJob(page);
    const requested = recordRowRequests(page);
    await job.navigateToEditor(new RegExp(lineText(1)));
    const ids = await segmentIds(page, job.jobId, job.token);
    const late = LINES - 10;
    expect(requested.flat(), "the late row is not part of the first load").not.toContain(ids[late]);

    // Scroll the grid to the bottom.
    await row(page, 0).hover();
    await page.mouse.wheel(0, 1_000_000);
    await expect(row(page, late)).toContainText(lineText(late + 1), { timeout: 10_000 });
    await expect(page.getByTestId("segment-row-loading")).toHaveCount(0);
    expect(requested.flat()).toContain(ids[late]);
    for (const batch of requested) expect(batch.length, "one page of rows per request").toBeLessThanOrEqual(50);
    // The rows that just loaded are shorter than their placeholders, so the
    // grid is still shrinking under the pointer; a click now can miss the row.
    await waitForSegmentGridToSettle(page);

    // Edit and confirm the late row.
    await row(page, late).locator("[data-translation-cell]").click();
    const editor = segmentEditor(page).first();
    await expect(row(page, late).getByTestId("segment-editor")).toBeVisible();
    await expect(editor).toBeFocused();
    await fillEditorExactly(editor, "late row confirmed");
    const [saved] = await Promise.all([
      page.waitForResponse(
        r => /\/api\/files\/\d+\/translations\/\d+$/.test(r.url().split("?")[0]) && r.request().method() === "PUT",
        { timeout: 10_000 },
      ),
      page.keyboard.press("ControlOrMeta+Enter"),
    ]);
    expect(saved.ok(), `confirm save: ${saved.status()}`).toBe(true);
    const translationId = Number(/\/translations\/(\d+)$/.exec(saved.url().split("?")[0])?.[1]);

    const stored = await page.request.get(`/api/files/${job.fileId}/translations/${translationId}`, {
      headers: { Authorization: `Bearer ${job.token}` },
    });
    expect(stored.ok()).toBe(true);
    const data = (await stored.json()).data;
    expect(data.segment_id).toBe(ids[late]);
    expect(data.active_text).toBe("late row confirmed");
    expect(data.status).toBe("approved");

    // The grid shows the confirmed text in that row.
    await expect(row(page, late)).toContainText("late row confirmed");
  });

  test("Jump to segment goes to segment N of the file, and says when the filter hides it", async ({ page }) => {
    test.setTimeout(30_000);
    const job = await createLongJob(page);
    await job.navigateToEditor(new RegExp(lineText(1)));

    const jump = async (n: number) => {
      await page.keyboard.press("ControlOrMeta+g");
      await expect(page.getByText("Jump to segment")).toBeVisible();
      await page.getByRole("dialog", { name: "Jump to segment" }).getByRole("textbox").fill(String(n));
      await page.getByRole("button", { name: "Go" }).click();
    };

    // Far below the first pages: the jump loads that page and opens the row.
    await jump(280);
    await expect(row(page, 279).getByTestId("segment-editor")).toBeVisible({ timeout: 10_000 });
    await expect(row(page, 279)).toContainText(lineText(280));

    // With a search that keeps only segment 5, segment 280 is hidden.
    await page.keyboard.press("Escape");
    const search = page.getByTestId("segment-search");
    await search.fill("line 5 of");
    const answered = page.waitForResponse(r => r.url().includes(`/api/jobs/${job.jobId}/editor/search?`) && r.ok());
    await search.press("Enter");
    await answered;
    await expect(page.locator("[data-segment-idx]")).toHaveCount(1);
    await search.blur();
    await jump(280);
    await expect(page.getByText("Segment 280 is hidden by the filter")).toBeVisible();
  });

  test("a failed segment index says so and loads on Retry", async ({ page }) => {
    test.setTimeout(30_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `index-retry-${Date.now()}.txt`,
      content: "index retry first line\nindex retry second line\n",
      onCreated: id => projectIds.push(id),
    });
    const INDEX = /\/api\/jobs\/\d+\/editor\/index(\?|$)/;
    await page.route(INDEX, route => route.fulfill({ status: 500, contentType: "application/json", body: '{"error":"failed to list editor index"}' }));
    await page.goto(`/projects/${job.projectId}/jobs/${job.jobId}/edit`);
    await expect(page.getByText(/Couldn't load the segments/)).toBeVisible({ timeout: 15_000 });
    await expect(page.locator("[data-segment-idx]")).toHaveCount(0);

    await page.unroute(INDEX);
    await page.getByRole("button", { name: "Retry" }).click();
    await expect(row(page, 0)).toContainText("index retry first line");
  });
});
