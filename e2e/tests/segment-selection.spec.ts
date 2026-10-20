import { test, expect, type Page } from "@playwright/test";
import { login, authHeaders, createIsolatedCATJob, getSeedTemplateMTRouter } from "./helpers";

// Multi-segment selection in the CAT editor: the # checkboxes, Shift range,
// Cmd/Ctrl+click, select-all, and the bulk Lock / Unlock / Clear actions on
// the selection bar. Each test works on its own isolated job and checks the
// result through the API, not only the toast.

const LINES = ["Line one", "Line two", "Line three", "Line four", "Line five"];
// Words, not digits: the job's Standard QA profile flags a number that the
// source does not have.
const TARGETS = ["Zeile eins", "Zeile zwei", "Zeile drei", "Zeile vier", "Zeile fünf"];

interface Row {
  id: number;
  segment_id: number;
  active_text: string | null;
  locked: boolean;
  status: string;
}

async function rows(page: Page, jobId: number): Promise<Row[]> {
  const res = await page.request.get(`/api/jobs/${jobId}/translations`);
  expect(res.ok()).toBe(true);
  const data: Row[] = (await res.json()).data;
  return data.sort((a, b) => a.segment_id - b.segment_id);
}

/** Gives every row a draft translation, so Clear has something to clear. */
async function translateAll(page: Page, fileId: number, jobId: number) {
  const fixes = (await rows(page, jobId)).map((r, i) => ({ translation_id: r.id, human_text: TARGETS[i] }));
  const res = await page.request.put(`/api/files/${fileId}/translations/batch-draft`, { data: { fixes } });
  expect(res.ok(), await res.text()).toBe(true);
}

const bulkResponse = (page: Page, action: string) =>
  page.waitForResponse(r => r.url().endsWith(`/translations/${action}`) && r.request().method() === "POST");

test.describe("Segment selection", () => {
  // Each test's project, deleted after it with the `request` fixture: the hook
  // has its own budget, so a test that ran out of time is still cleaned up.
  let projectIds: number[] = [];
  let headers: Record<string, string> = {};

  test.beforeEach(async ({ page }) => {
    // Each test builds its own CAT job and opens the editor: about 2 s on a
    // quiet stack, 7–9 s under full-suite load, near the default budget.
    test.slow();
    await login(page);
    headers = await authHeaders(page);
  });

  test.afterEach(async ({ request }) => {
    for (const id of projectIds) await request.delete(`/api/projects/${id}`, { headers }).catch(() => {});
    projectIds = [];
  });

  test("Shift-range lock approves the selected rows; unlock keeps them approved", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `seg-select-lock-${Date.now()}.txt`,
      content: LINES.join("\n"),
    });
    // Locking approves a segment, so it needs a translation.
    await translateAll(page, job.fileId, job.jobId);
    await job.navigateToEditor(/Line one/);

    await page.getByTestId("segment-select-1").click();
    await page.getByTestId("segment-select-3").click({ modifiers: ["Shift"] });
    await expect(page.getByTestId("segment-selected-count")).toHaveText("3 segments selected");

    const locked = bulkResponse(page, "lock");
    await page.getByTestId("segment-bulk-lock").click();
    expect((await locked).status()).toBe(200);
    await expect(page.getByText("Locked 3 segments")).toBeVisible();
    const afterLock = await rows(page, job.jobId);
    expect(afterLock.map(r => r.locked)).toEqual([true, true, true, false, false]);
    expect(afterLock.map(r => r.status)).toEqual(["approved", "approved", "approved", "needs_review", "needs_review"]);

    // Selection stays after an action, so the next one runs on the same rows.
    const unlocked = bulkResponse(page, "unlock");
    await page.getByTestId("segment-bulk-unlock").click();
    expect((await unlocked).status()).toBe(200);
    await expect(page.getByText("Unlocked 3 segments")).toBeVisible();
    const afterUnlock = await rows(page, job.jobId);
    expect(afterUnlock.every(r => !r.locked)).toBe(true);
    expect(afterUnlock.map(r => r.status)).toEqual(["approved", "approved", "approved", "needs_review", "needs_review"]);
  });

  test("Cmd/Ctrl+click selects without opening the row; Clear empties only the selection", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `seg-select-clear-${Date.now()}.txt`,
      content: LINES.join("\n"),
    });
    await translateAll(page, job.fileId, job.jobId);
    await job.navigateToEditor(/Line one/);

    await page.getByText("Line two").click({ modifiers: ["ControlOrMeta"] });
    await page.getByText("Line four").click({ modifiers: ["ControlOrMeta"] });
    await expect(page.getByTestId("segment-selected-count")).toHaveText("2 segments selected");
    // A modifier click selects; it never opens the editor.
    await expect(page.getByTestId("active-source-box")).toHaveCount(0);

    await page.getByTestId("segment-bulk-clear").click();
    await expect(page.getByRole("alertdialog")).toContainText("Clear 2 translations?");
    const cleared = bulkResponse(page, "clear");
    await page.getByRole("button", { name: "Clear", exact: true }).click();
    expect((await cleared).status()).toBe(200);
    await expect(page.getByText("Cleared 2 segments")).toBeVisible();

    expect((await rows(page, job.jobId)).map(r => r.active_text))
      .toEqual([TARGETS[0], null, TARGETS[2], null, TARGETS[4]]);
  });

  test("Confirm approves the selected drafts and skips a locked row", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `seg-select-confirm-${Date.now()}.txt`,
      content: LINES.join("\n"),
    });
    await translateAll(page, job.fileId, job.jobId);
    const before = await rows(page, job.jobId);
    const lock = await page.request.post(`/api/jobs/${job.jobId}/translations/lock`, {
      data: { translation_ids: [before[2].id] },
    });
    expect(lock.ok()).toBe(true);
    await job.navigateToEditor(/Line one/);

    await page.getByTestId("segment-select-1").click();
    await page.getByTestId("segment-select-4").click({ modifiers: ["Shift"] });
    const confirmed = bulkResponse(page, "confirm");
    await page.getByTestId("segment-bulk-confirm").click();
    expect((await confirmed).status()).toBe(200);
    await expect(page.getByText("Confirmed 3 segments · 1 locked")).toBeVisible();

    // The locked row was approved by its lock, not by this confirm.
    expect((await rows(page, job.jobId)).map(r => r.status))
      .toEqual(["approved", "approved", "approved", "approved", "needs_review"]);
  });

  test("Run QA checks only the selected rows", async ({ page }) => {
    // Untranslated rows: Standard QA's empty-target check fires on each one
    // that is checked.
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `seg-select-qa-${Date.now()}.txt`,
      content: LINES.join("\n"),
    });
    await job.navigateToEditor(/Line one/);
    await page.getByTestId("segment-select-1").click();
    await page.getByTestId("segment-select-2").click({ modifiers: ["Shift"] });

    const ran = page.waitForResponse(r => r.url().endsWith("/run-qa") && r.request().method() === "POST");
    await page.getByTestId("segment-bulk-qa").click();
    const res = await ran;
    expect(res.status()).toBe(200);
    const ids = (await rows(page, job.jobId)).map(r => r.id);
    expect(res.request().postDataJSON()).toEqual({ translation_ids: ids.slice(0, 2) });
    await expect(page.getByText(/QA on 2 segments: [1-9]\d* issue\(s\) found/)).toBeVisible();

    const report = await (await page.request.get(`/api/jobs/${job.jobId}/qa-report`)).json();
    const flagged = new Set<number>(report.data.results.map((r: { translation_id: number }) => r.translation_id));
    expect([...flagged].sort((a, b) => a - b)).toEqual(ids.slice(0, 2));
  });

  test("Spell check opens on the selection and skips a locked row", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `seg-select-spell-${Date.now()}.txt`,
      content: LINES.join("\n"),
    });
    await translateAll(page, job.fileId, job.jobId);
    const ids = (await rows(page, job.jobId)).map(r => r.id);
    const lock = await page.request.post(`/api/jobs/${job.jobId}/translations/lock`, { data: { translation_ids: [ids[1]] } });
    expect(lock.ok()).toBe(true);
    // LanguageTool's answer is not under test: report no findings, keep the request.
    const requested: number[][] = [];
    await page.route("**/api/jobs/*/spellcheck", async route => {
      requested.push(route.request().postDataJSON()?.translation_ids ?? []);
      await route.fulfill({ json: { data: { results: [], failed: 0 } } });
    });
    await job.navigateToEditor(/Line one/);

    await page.getByTestId("segment-select-1").click();
    await page.getByTestId("segment-select-3").click({ modifiers: ["Shift"] });
    await page.getByTestId("segment-bulk-spellcheck").click();
    await expect(page.getByRole("radio", { name: /Selected segments/ })).toBeChecked();
    await page.getByTestId("spellcheck-start").click();

    await expect.poll(() => requested.flat()).toEqual([ids[0], ids[2]]);
  });

  test("Find & replace in the selection writes drafts on the selected rows only", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `seg-select-fr-${Date.now()}.txt`,
      content: LINES.join("\n"),
    });
    await translateAll(page, job.fileId, job.jobId);
    const ids = (await rows(page, job.jobId)).map(r => r.id);
    // Row 1 confirmed: a replace is an edit, so it goes back to draft.
    const confirm = await page.request.post(`/api/jobs/${job.jobId}/translations/confirm`, { data: { translation_ids: [ids[0]] } });
    expect(confirm.ok()).toBe(true);
    await job.navigateToEditor(/Line one/);

    await page.getByTestId("segment-select-1").click();
    await page.getByTestId("segment-select-2").click({ modifiers: ["Shift"] });
    await page.getByTestId("segment-bulk-find-replace").click();
    await expect(page.getByTestId("find-in-selection")).toBeChecked();
    await page.getByPlaceholder("Find...").fill("Zeile");
    await page.getByPlaceholder("Replace...").fill("Linie");
    const replaced = page.waitForResponse(r => r.url().endsWith("/find-replace") && r.request().method() === "POST");
    await page.getByRole("button", { name: "Replace all" }).click();
    expect((await replaced).request().postDataJSON().translation_ids).toEqual(ids.slice(0, 2));
    await expect(page.getByText("Replaced 2 occurrences in 2 segments")).toBeVisible();

    const after = await rows(page, job.jobId);
    expect(after.map(r => r.active_text)).toEqual(["Linie eins", "Linie zwei", ...TARGETS.slice(2)]);
    expect(after.map(r => r.status).slice(0, 2)).toEqual(["needs_review", "needs_review"]);
  });

  test("Find & replace leaves locked rows out unless Include locked is ticked; an owner's replace unlocks them", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `seg-select-fr-locked-${Date.now()}.txt`,
      content: LINES.join("\n"),
    });
    await translateAll(page, job.fileId, job.jobId);
    const ids = (await rows(page, job.jobId)).map(r => r.id);
    const lock = await page.request.post(`/api/jobs/${job.jobId}/translations/lock`, { data: { translation_ids: [ids[1]] } });
    expect(lock.ok()).toBe(true);
    await job.navigateToEditor(/Line one/);

    await page.keyboard.press("ControlOrMeta+h");
    const includeLocked = page.getByTestId("find-include-locked");
    await expect(includeLocked).not.toBeChecked();
    await page.getByPlaceholder("Find...").fill("Zeile");
    await page.getByRole("button", { name: "Find All" }).click();
    await expect(page.getByTestId("match-count")).toHaveText("4 matches");

    await includeLocked.check();
    await page.getByRole("button", { name: "Find All" }).click();
    await expect(page.getByTestId("match-count")).toHaveText("5 matches");

    // The test user is an owner, so the locked row is replaced too. The
    // replace saves a draft, and a draft is never locked: the row unlocks.
    await page.getByPlaceholder("Replace...").fill("Linie");
    const replaced = page.waitForResponse(r => r.url().endsWith("/find-replace") && r.request().method() === "POST");
    await page.getByRole("button", { name: "Replace all" }).click();
    expect((await replaced).request().postDataJSON().include_locked).toBe(true);
    await expect(page.getByText("Replaced 5 occurrences in 5 segments")).toBeVisible();

    const after = await rows(page, job.jobId);
    expect(after.map(r => r.active_text)).toEqual(TARGETS.map(t => t.replace("Zeile", "Linie")));
    expect(after[1].locked).toBe(false);
    expect(after[1].status).toBe("needs_review");
  });

  test("Pre-translate with MT fills the selected empty rows and leaves a locked one alone", async ({ page }) => {
    // The seed router has an engine for German.
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `seg-select-pt-${Date.now()}.txt`,
      content: LINES.join("\n"),
      targetLanguages: ["de"],
    });
    const router = await getSeedTemplateMTRouter(page);
    const settings = await page.request.patch(`/api/projects/${job.projectId}/settings`, { data: { mt_router_id: router.id } });
    expect(settings.ok(), await settings.text()).toBe(true);
    const ids = (await rows(page, job.jobId)).map(r => r.id);
    // A locked segment always has a translation: give row 2 one, then lock it.
    const draft = await page.request.put(`/api/files/${job.fileId}/translations/batch-draft`, {
      data: { fixes: [{ translation_id: ids[1], human_text: TARGETS[1] }] },
    });
    expect(draft.ok(), await draft.text()).toBe(true);
    const lock = await page.request.post(`/api/jobs/${job.jobId}/translations/lock`, { data: { translation_ids: [ids[1]] } });
    expect(lock.ok()).toBe(true);
    await job.navigateToEditor(/Line one/);

    await page.getByTestId("segment-select-1").click();
    await page.getByTestId("segment-select-3").click({ modifiers: ["Shift"] });
    await page.getByTestId("segment-bulk-pretranslate").click();
    const applied = page.waitForResponse(r => r.url().endsWith("/mt-apply") && r.request().method() === "POST");
    await page.getByTestId("segment-bulk-apply-mt").click();
    const res = await applied;
    expect(res.status(), await res.text()).toBe(200);
    expect(res.request().postDataJSON()).toEqual({ translation_ids: ids.slice(0, 3) });
    await expect(page.getByText("MT applied to the selection")).toBeVisible();

    const after = await rows(page, job.jobId);
    expect(after.map(r => r.active_text !== null)).toEqual([true, true, true, false, false]);
    expect(after[1].active_text, "MT must not overwrite the locked row").toBe(TARGETS[1]);
  });

  test("actions run only on the selected rows the filter shows", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `seg-select-filter-${Date.now()}.txt`,
      content: LINES.join("\n"),
    });
    await translateAll(page, job.fileId, job.jobId);
    const ids = (await rows(page, job.jobId)).map(r => r.id);
    await job.navigateToEditor(/Line one/);

    await page.getByTestId("segment-select-all").check();
    await expect(page.getByTestId("segment-selected-count")).toHaveText("5 segments selected");
    // "Line t" keeps two and three.
    await page.getByPlaceholder("Search segments...").fill("Line t");
    await page.getByPlaceholder("Search segments...").press("Enter");
    await expect(page.getByTestId("segment-selected-count")).toHaveText("2 segments selected · 3 hidden by filter");

    const locked = bulkResponse(page, "lock");
    await page.getByTestId("segment-bulk-lock").click();
    const res = await locked;
    expect(res.request().postDataJSON()).toEqual({ translation_ids: [ids[1], ids[2]] });
    expect((await rows(page, job.jobId)).map(r => r.locked)).toEqual([false, true, true, false, false]);
  });

  test("header selects every row shown; Escape clears the selection", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `seg-select-all-${Date.now()}.txt`,
      content: LINES.join("\n"),
    });
    await job.navigateToEditor(/Line one/);

    await page.getByTestId("segment-select-all").check();
    await expect(page.getByTestId("segment-selected-count")).toHaveText("5 segments selected");

    await page.keyboard.press("Escape");
    await expect(page.getByTestId("segment-actions-bar")).toHaveCount(0);
    await expect(page.getByTestId("segment-select-all")).not.toBeChecked();
  });
});
