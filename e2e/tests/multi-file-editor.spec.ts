import { test, expect, type Page } from "@playwright/test";
import { authHeaders, createIsolatedCATJob, login, seedDrafts, uploadFileViaAPI } from "./helpers";

// The multi-file editor: two files of one project and one target language
// open in one grid from the Tasks tab, a confirmed segment's internal match
// shows for a near-copy in the other file, a confirm propagates into the
// other file, and Complete step completes both files' steps.

const FILE_A = "Save\nDelete this item?";
const FILE_B = "Delete these items?\nSave";

interface Row { id: number; segment_id: number; active_text: string | null; status: string }

async function rows(page: Page, jobId: number): Promise<Row[]> {
  const res = await page.request.get(`/api/jobs/${jobId}/translations`, { headers: await authHeaders(page) });
  expect(res.ok()).toBe(true);
  return ((await res.json()).data as Row[]).sort((a, b) => a.segment_id - b.segment_id);
}

/** Assigns the job's step in progress to the signed-in user, so it shows on the Tasks page. */
async function assignCurrentStepToMe(page: Page, jobId: number): Promise<void> {
  const headers = await authHeaders(page);
  const me = await page.request.get(`/api/auth/me`, { headers });
  const userId: number = (await me.json()).data.id;
  const res = await page.request.get(`/api/jobs/${jobId}/workflow/steps`, { headers });
  const step = ((await res.json()).data as { id: number; status: string }[]).find(s => s.status === "in_progress")!;
  const assign = await page.request.post(`/api/jobs/${jobId}/workflow/steps/${step.id}/assign`, { headers, data: { user_id: userId } });
  expect(assign.ok(), await assign.text()).toBe(true);
}

async function activeStepName(page: Page, jobId: number): Promise<string | undefined> {
  const res = await page.request.get(`/api/jobs/${jobId}/workflow/steps`, { headers: await authHeaders(page) });
  expect(res.ok()).toBe(true);
  const steps: { step_name: string; status: string }[] = (await res.json()).data;
  return steps.find(s => s.status === "in_progress")?.step_name;
}

test.describe("Multi-file editor", () => {
  let cleanup: (() => Promise<void>) | undefined;
  test.beforeEach(async ({ page }) => {
    await login(page);
  });
  test.afterEach(async () => {
    await cleanup?.();
    cleanup = undefined;
  });

  test("opens two files together; internal match, propagation across files and Complete step for both", async ({ page }) => {
    test.setTimeout(90_000);
    const stamp = Date.now();
    const a = await createIsolatedCATJob(page, { fileName: `mf-a-${stamp}.txt`, content: FILE_A, targetLanguages: ["de"] });
    cleanup = a.cleanup;
    const headers = await authHeaders(page);
    await uploadFileViaAPI(page, a.projectId, `mf-b-${stamp}.txt`, FILE_B);
    const jobsRes = await page.request.get(`/api/projects/${a.projectId}/jobs`, { headers });
    expect(jobsRes.ok()).toBe(true);
    const jobB: number = ((await jobsRes.json()).data as { id: number }[]).find(j => j.id !== a.jobId)!.id;

    // A's "Delete this item?" is confirmed; A's "Save" is a draft.
    const [saveA, deleteA] = await seedDrafts(page, a, ["Speichern", "Dieses Element löschen?"], headers);
    const confirm = await page.request.post(`/api/jobs/${a.jobId}/translations/confirm`, { headers, data: { translation_ids: [deleteA.id] } });
    expect(confirm.ok(), await confirm.text()).toBe(true);
    const firstStep = await activeStepName(page, a.jobId);

    // Open both from the Tasks tab, A first.
    await page.goto(`/projects/${a.projectId}`);
    await page.getByRole("button", { name: /Show 2 tasks/ }).click();
    await page.getByRole("checkbox", { name: new RegExp(`Select mf-a-${stamp}\\.txt`) }).check();
    await page.getByRole("checkbox", { name: new RegExp(`Select mf-b-${stamp}\\.txt`) }).check();
    await page.evaluate(() => { window.open = () => null; });
    await page.getByTestId("bulk-open-editor-btn").click();
    await expect(page).toHaveURL(new RegExp(`/projects/${a.projectId}/edit\\?jobs=${a.jobId},${jobB}`));
    await expect(page.getByTestId(`scope-file-header-${a.jobId}`)).toBeVisible({ timeout: 30_000 });
    await expect(page.getByTestId(`scope-file-header-${jobB}`)).toBeVisible();

    // B's "Delete these items?" (row 2 of the grid) finds A's confirmed near-copy.
    await page.locator('[data-segment-idx="2"] [data-translation-cell]').click();
    const card = page.getByTestId("internal-match-card");
    await expect(card).toContainText("Dieses Element löschen?");
    await expect(page.getByTestId("internal-match-percent")).toHaveText("84%");
    await expect(page.locator('[data-segment-idx="2"] [data-testid="active-source-box"]')).toBeVisible();

    // Confirming A's "Save" (row 0) fills B's "Save" too.
    await page.locator('[data-segment-idx="0"] [data-translation-cell]').click();
    const propagated = page.waitForResponse(r => r.url().includes(`/api/projects/${a.projectId}/editor/propagate`) && r.request().method() === "POST");
    await page.keyboard.press("ControlOrMeta+Enter");
    const res = await propagated;
    expect(res.ok()).toBe(true);
    expect((await res.json()).data.propagated).toBe(1);
    const bRows = await rows(page, jobB);
    expect(bRows[1]).toMatchObject({ active_text: "Speichern", status: "approved" });
    expect((await rows(page, a.jobId))[0]).toMatchObject({ id: saveA.id, active_text: "Speichern", status: "approved" });

    // Complete step for both files.
    await page.getByTestId("scope-complete-open").click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByTestId("scope-complete-files")).toContainText(`mf-a-${stamp}.txt`);
    await expect(dialog.getByTestId("scope-complete-files")).toContainText(`mf-b-${stamp}.txt`);
    // Each file goes with the step it is on, so a resent request never
    // completes the next step too.
    const indexRes = await page.request.get(`/api/projects/${a.projectId}/editor/index?jobs=${a.jobId},${jobB}`, { headers });
    const stepIds = Object.fromEntries(((await indexRes.json()).data.files as { job_id: number; step_id: number }[])
      .map(f => [String(f.job_id), f.step_id]));
    const completed = page.waitForResponse(r => r.url().includes(`/api/projects/${a.projectId}/editor/complete`) && r.request().method() === "POST");
    await dialog.getByTestId("complete-confirm").click();
    const done = await completed;
    expect(done.ok()).toBe(true);
    expect(done.request().postDataJSON().steps).toEqual(stepIds);
    const outcomes = ((await done.json()).data as { job_id: number; outcome: string }[]).map(r => r.outcome);
    expect(outcomes).toEqual(["advanced", "advanced"]);
    expect(await activeStepName(page, a.jobId)).not.toBe(firstStep);
    expect(await activeStepName(page, jobB)).not.toBe(firstStep);
  });

  test("the Tasks page opens ticked files together", async ({ page }) => {
    test.setTimeout(60_000);
    const stamp = Date.now();
    const a = await createIsolatedCATJob(page, { fileName: `mf-tasks-a-${stamp}.txt`, content: FILE_A, targetLanguages: ["de"] });
    cleanup = a.cleanup;
    await uploadFileViaAPI(page, a.projectId, `mf-tasks-b-${stamp}.txt`, FILE_B);
    const jobsRes = await page.request.get(`/api/projects/${a.projectId}/jobs`, { headers: await authHeaders(page) });
    const jobB: number = ((await jobsRes.json()).data as { id: number }[]).find(j => j.id !== a.jobId)!.id;

    await assignCurrentStepToMe(page, a.jobId);
    await assignCurrentStepToMe(page, jobB);
    await page.goto(`/tasks?q=${stamp}`);
    await page.getByTestId(`task-select-${a.jobId}`).check();
    await page.getByTestId(`task-select-${jobB}`).check();
    await page.evaluate(() => { window.open = () => null; });
    await page.getByTestId("tasks-open-editor-btn").click();
    await expect(page).toHaveURL(new RegExp(`/projects/${a.projectId}/edit\\?jobs=`));
    // The editor's code loads on first use, which a cold dev server compiles.
    await expect(page.getByTestId("scope-file-count")).toHaveText("2 files", { timeout: 30_000 });
  });
});
