import { authHeaders, createIsolatedCATJob, expect, login, seedDrafts, segmentEditor, sessionAs, test } from "./helpers";

// The toolbar Clear (whole job) stays in place when nothing can be cleared:
// disabled, with the reason on hover. It turns live again as soon as a draft
// save gives a segment text, without reopening the file. The assigned linguist
// gets every editor Clear the server allows them.
test.describe("CAT editor toolbar Clear", () => {
  // Each test's project, deleted after it with the `request` fixture: the hook
  // has its own budget, so a test that ran out of time is still cleaned up.
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

  test("disabled with a reason while nothing can be cleared, live again after a draft save", async ({ page }) => {
    test.setTimeout(30_000);
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `job-clear-e2e-${Date.now()}.txt`,
      content: "Hello world\n\nSecond line\n",
      contentType: "text/plain",
    });
    await job.navigateToEditor(/Hello world/);
    const clearButton = page.getByTestId("clear-translations-btn");

    await expect(clearButton, "shown on a job with nothing to clear").toBeVisible();
    await expect(clearButton).toBeDisabled();
    await clearButton.hover();
    await expect(page.locator('[data-slot="tooltip-content"]')).toHaveText("Nothing to clear. Every segment is empty or locked.");

    // A draft save (moving to another segment, no confirm) gives segment 1 text.
    const cells = page.locator("[data-translation-cell]");
    await cells.first().click();
    await expect(segmentEditor(page).first()).toBeVisible({ timeout: 5000 });
    await page.keyboard.type("Hola");
    const drafted = page.waitForResponse(
      r => /\/translations\/\d+\/draft$/.test(r.url().split("?")[0]) && r.request().method() === "PUT" && r.ok(),
      { timeout: 15_000 },
    );
    await cells.nth(1).click();
    await drafted;
    await expect(clearButton, "live again without reopening the file").toBeEnabled();

    // Clearing it all leaves the button in place, disabled again.
    const cleared = page.waitForResponse(
      r => /\/jobs\/\d+\/clear-translations$/.test(r.url().split("?")[0]) && r.request().method() === "POST" && r.ok(),
      { timeout: 15_000 },
    );
    await clearButton.click();
    await page.getByRole("alertdialog").getByRole("button", { name: "Clear translations" }).click();
    await cleared;
    await expect(clearButton).toBeVisible();
    await expect(clearButton).toBeDisabled();
  });

  test("the assigned linguist can clear a segment, a selection and the job", async ({ page, browser }) => {
    test.setTimeout(60_000);
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `job-clear-linguist-e2e-${Date.now()}.txt`,
      content: "Hello world\n\nSecond line\n",
      contentType: "text/plain",
    });
    const linguist = await sessionAs(browser, "linguist@seed.example.com");
    const me = await (await linguist.page.request.get("/api/auth/me", { headers: await authHeaders(linguist.page) })).json();
    const assigned = await page.request.post(`/api/jobs/${job.jobId}/assign`, { headers, data: { user_id: me.data.id } });
    expect(assigned.ok(), `assign: ${assigned.status()} ${await assigned.text()}`).toBe(true);
    await seedDrafts(page, job, ["Hola mundo", "Segunda línea"], headers);

    const lp = linguist.page;
    await lp.goto(`/projects/${job.projectId}/jobs/${job.jobId}/edit`);
    await expect(lp.getByText("Hello world").first()).toBeVisible({ timeout: 10_000 });
    const cells = lp.locator("[data-translation-cell]");

    // Segment Clear on a saved row: asks, then clears on the server.
    await cells.first().click();
    await expect(segmentEditor(lp).first()).toHaveText("Hola mundo");
    const segmentCleared = lp.waitForResponse(
      r => /\/translations\/\d+\/clear$/.test(r.url().split("?")[0]) && r.request().method() === "POST",
      { timeout: 15_000 },
    );
    await lp.getByTestId("clear-translation-btn").click();
    await lp.getByRole("alertdialog").getByRole("button", { name: "Clear" }).click();
    expect((await segmentCleared).status()).toBe(200);
    await expect(segmentEditor(lp).first()).toHaveText("");

    // Selection bar Clear translation on the other row.
    await lp.getByText("Second line").click({ modifiers: ["ControlOrMeta"] });
    await expect(lp.getByTestId("segment-selected-count")).toHaveText("1 segment selected");
    await lp.getByTestId("segment-bulk-clear").click();
    const selectionCleared = lp.waitForResponse(
      r => r.url().split("?")[0].endsWith("/translations/clear") && r.request().method() === "POST",
      { timeout: 15_000 },
    );
    await lp.getByRole("alertdialog").getByRole("button", { name: "Clear", exact: true }).click();
    expect((await selectionCleared).status()).toBe(200);
    const after = (await (await page.request.get(`/api/jobs/${job.jobId}/translations`, { headers })).json()).data as { active_text: string | null }[];
    expect(after.map(r => r.active_text ?? null)).toEqual([null, null]);
    await lp.keyboard.press("Escape");

    // Toolbar Clear: live again once a draft gives the job text, then clears it.
    await cells.first().click();
    await segmentEditor(lp).first().click();
    await lp.keyboard.type("Hola");
    await expect(segmentEditor(lp).first()).toHaveText("Hola");
    const lingDraft = lp.waitForResponse(
      r => /\/translations\/\d+\/draft$/.test(r.url().split("?")[0]) && r.request().method() === "PUT" && r.ok(),
      { timeout: 15_000 },
    );
    await cells.nth(1).click();
    await lingDraft;
    const toolbarClear = lp.getByTestId("clear-translations-btn");
    await expect(toolbarClear).toBeEnabled();
    const jobCleared = lp.waitForResponse(
      r => /\/jobs\/\d+\/clear-translations$/.test(r.url().split("?")[0]) && r.request().method() === "POST",
      { timeout: 15_000 },
    );
    await toolbarClear.click();
    await lp.getByRole("alertdialog").getByRole("button", { name: "Clear translations" }).click();
    expect((await jobCleared).status()).toBe(200);
    await expect(toolbarClear).toBeDisabled();
  });
});
