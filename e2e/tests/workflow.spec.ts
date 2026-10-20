import { test, expect, type Page } from "@playwright/test";
import { deleteProjectViaAPI, login, openJob, setProjectWorkflowStepsViaAPI, uploadFileViaAPI } from "./helpers";

const API = "";

async function authHeaders(page: Page): Promise<Record<string, string>> {
  const token = await page.evaluate(() => localStorage.getItem("token"));
  return token ? { Authorization: `Bearer ${token}` } : {};
}

/**
 * Create an isolated project + file + job for workflow tests. The project has
 * the standard 2-step workflow (translate → review). Returns { projectId, fileId,
 * jobId } so the test can assert on a known state without sharing Demo Project
 * with other tests.
 */
async function createIsolatedWorkflowJob(page: Page, suffix: string): Promise<{
  projectId: number;
  fileId: number;
  jobId: number;
}> {
  const headers = await authHeaders(page);

  const projRes = await page.request.post(`${API}/api/projects`, {
    headers,
    data: { name: `Workflow E2E ${suffix} ${Date.now()}`, source_language: "en", target_languages: ["es"] },
  });
  expect(projRes.ok(), `create project: ${projRes.status()}`).toBe(true);
  const projectId: number = (await projRes.json()).data.id;

  // Set up workflow: Translation → Review — the tenant's default library
  // steps, so the spec leaves no definitions behind.
  await setProjectWorkflowStepsViaAPI(page, projectId, [
    { name: "Translation", role_required: "linguist" },
    { name: "Review", role_required: "linguist" },
  ]);

  // Disable pre-translate-on-upload. The template default is ON, which means
  // uploading triggers MT pipeline → segments transition to `pre_translated`
  // → `hasUntranslated` flips false in CATEditor → the toolbar's
  // "Pre-translate" button (gated on `hasUntranslated && isFirstStep`) is
  // hidden. The "shows Pre-translate on translate step" test needs an
  // untranslated segment to keep the button visible. Same gotcha
  // createIsolatedCATJob solves for the CAT lifecycle tests.
  const settingsRes = await page.request.patch(`${API}/api/projects/${projectId}/settings`, {
    headers,
    data: { mt_on_upload: false },
  });
  expect(settingsRes.ok(), `disable pre-translate: ${settingsRes.status()}`).toBe(true);

  const upload = await uploadFileViaAPI(
    page,
    projectId,
    `wf-${suffix}.txt`,
    "Hello world\nGoodbye world\n",
    "text/plain",
  );
  const fileId: number = upload.data?.id ?? upload.id;

  const jobsRes = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
  const jobs = (await jobsRes.json()).data;
  expect(jobs.length, "expected at least one job for the uploaded file").toBeGreaterThan(0);
  const jobId: number = jobs[0].id;

  return { projectId, fileId, jobId };
}

/** Advance a job's workflow step via API. */
async function advanceJobStep(page: Page, jobId: number, comment = ""): Promise<void> {
  const headers = await authHeaders(page);
  const res = await page.request.post(`${API}/api/jobs/${jobId}/workflow/advance`, {
    headers,
    data: { comment },
  });
  expect(res.ok(), `advance job ${jobId}: ${res.status()}`).toBe(true);
}

test.describe("Workflow", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("template detail shows workflow step editor on its Workflow tab", async ({ page }) => {
    await page.getByRole("link", { name: /templates/i }).click();
    await page.getByText(/default template/i).click();
    await page.getByRole("navigation", { name: "Section" }).getByRole("link", { name: "Workflow" }).click();
    // The seeded template is based on the "Standard" library workflow, so the
    // picker shows it and the copied steps are shown read-only.
    await expect(page.getByTestId("workflow-picker")).toBeVisible();
    await expect(page.getByTestId("use-workflow-select")).toContainText(/Standard/);
    await expect(page.getByTestId("workflow-step-badge-0")).toContainText("Translation");
  });

  test("template detail shows QA profile selector", async ({ page }) => {
    await page.getByRole("link", { name: /templates/i }).click();
    await page.getByText(/default template/i).click();
    // The selector itself, not any text matching /qa profile/i — the sidebar's
    // "QA profiles" nav link matches that too, so the loose locator was a
    // strict-mode violation waiting for the right interleaving.
    await expect(page.getByTestId("qa-profile-select")).toBeVisible();
  });

  test("CAT editor names the target language, the current step and its deadline after the breadcrumb, in one row", async ({ page }) => {
    // Read-only check on Demo Project — safe to share.
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt", lang: "de" });
    await expect(page.getByText(/source/i).first()).toBeVisible({ timeout: 10000 });
    const crumbs = page.getByRole("navigation", { name: "Breadcrumb" });
    const chips = ["cat-target-language", "cat-current-step", "cat-step-deadline"].map(id => crumbs.getByTestId(id));
    await expect(chips[0]).toHaveText("DE");
    await expect(chips[1]).toHaveText(/translation/i);
    await expect(chips[2]).toBeVisible();
    await expect(crumbs).toHaveText(/DE\s*Translation\s*(Due|No due date|Overdue)/i);
    // The task's controls sit at the right end of the line, after the nav.
    await expect(page.getByTestId("task-actions")).toHaveAttribute("data-mode", "editable");
    await expect(crumbs.getByTestId("task-actions")).toHaveCount(0);
    // A wrapper around one chip (e.g. a tooltip trigger button) drops it onto
    // the text baseline, a fraction of a pixel below its neighbours.
    const boxes = await Promise.all(chips.map(async c => (await c.boundingBox())!));
    for (const b of boxes.slice(1)) {
      expect(b.y).toBe(boxes[0].y);
      expect(b.height).toBe(boxes[0].height);
    }
  });

  test("CAT editor step and deadline chips both open the app tooltip on hover", async ({ page }) => {
    // Read-only check on Demo Project — safe to share.
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt", lang: "de" });
    const crumbs = page.getByRole("navigation", { name: "Breadcrumb" });
    // [data-open]: the previous tooltip lingers in the DOM while it fades out.
    const tooltip = page.locator('[data-slot="tooltip-content"][data-open]');

    await crumbs.getByTestId("cat-current-step").hover();
    await expect(tooltip).toContainText(/in progress/i);
    // The deadline chip beside it owns the due date.
    await expect(tooltip).not.toContainText(/due/i);

    const deadline = crumbs.getByTestId("cat-step-deadline");
    // A native title would show a second, browser-styled tooltip on top.
    await expect(deadline).not.toHaveAttribute("title");
    await deadline.hover();
    await expect(tooltip).toHaveText(/^(Deadline .+|No due date)$/);
  });

  test("toolbar shows Pre-translate on translate step, hides on review step", async ({ page }) => {
    // Isolated project, so the step state is known and both states are assertable.
    test.setTimeout(30_000);
    let projectId: number | undefined;
    try {
      const setup = await createIsolatedWorkflowJob(page, "pretranslate");
      projectId = setup.projectId;

      // Selected by testid: the always-mounted Pre-translated filter pill
      // shares the name (gotchas.md "Pre-translate button vs Pre-translated
      // filter pill"). The "Pre-translate" menu trigger
      // carries the show/hide gate; whether its items are *enabled* is decided
      // on the menu items, and this test only asserts visibility.
      const preTranslateBtn = page.getByTestId("pretranslate-btn");

      // (1) Translate step + untranslated segments → button MUST be visible.
      await page.goto(`/projects/${setup.projectId}/jobs/${setup.jobId}/edit`);
      await expect(page.getByText(/Hello world/i).first()).toBeVisible({ timeout: 10000 });
      await expect(preTranslateBtn).toBeVisible({ timeout: 5000 });

      // (2) Advance to review step → button MUST disappear.
      await advanceJobStep(page, setup.jobId);
      await page.goto(`/projects/${setup.projectId}/jobs/${setup.jobId}/edit`);
      await expect(page.getByText(/Hello world/i).first()).toBeVisible({ timeout: 10000 });
      await expect(preTranslateBtn).not.toBeAttached({ timeout: 3000 });
    } finally {
      if (projectId !== undefined) await deleteProjectViaAPI(page, projectId);
    }
  });

  test("can advance workflow step via dialog", async ({ page }) => {
    // Isolated project: no shared state with other tests.
    test.setTimeout(30_000);
    let projectId: number | undefined;
    try {
      const setup = await createIsolatedWorkflowJob(page, "advance");
      projectId = setup.projectId;

      await page.goto(`/projects/${setup.projectId}/jobs/${setup.jobId}/edit`);
      await expect(page.getByText(/Hello world/i).first()).toBeVisible({ timeout: 10000 });

      // Wait until jobSteps holds translate→review: while it holds only
      // [translate], the button reads "Complete final step ✓" and the dialog
      // says "mark as completed" instead of "advance".
      await expect(page.getByRole("button", { name: /^complete step →$/i })).toBeVisible({ timeout: 5000 });

      // Click "Complete step →" — opens confirmation dialog.
      await page.getByRole("button", { name: /^complete step/i }).click();
      await expect(page.getByRole("dialog").getByTestId("complete-unapproved")).toBeVisible({ timeout: 3000 });

      // Comment field MUST be present and editable.
      const commentField = page.getByPlaceholder(/pay attention/i);
      await expect(commentField).toBeVisible({ timeout: 3000 });
      await commentField.fill("Please check terminology");

      // A server refusal keeps the dialog open with the typed comment, so the
      // user fixes the comment instead of retyping it. Routed once; the real
      // submit below reaches the server.
      const dialog = page.locator('[role="dialog"]');
      await page.route(
        (url) => url.pathname.endsWith("/workflow/advance"),
        (route) =>
          route.fulfill({
            status: 400,
            contentType: "application/json",
            body: JSON.stringify({
              error: { code: "Bad Request", message: "The comment is too long: at most 4000 characters." },
            }),
          }),
        { times: 1 },
      );
      const [refusal] = await Promise.all([
        page.waitForResponse(
          (r) => r.url().includes("/workflow/advance") && r.request().method() === "POST",
          { timeout: 10000 },
        ),
        dialog.getByRole("button", { name: /^complete step/i }).click(),
      ]);
      expect(refusal.status()).toBe(400);
      await expect(page.getByText(/at most 4000 characters/i)).toBeVisible({ timeout: 3000 });
      // `data-open`, not visibility: a closing popup stays visible through
      // its exit animation.
      await expect(dialog).toHaveAttribute("data-open", "");
      await expect(commentField).toHaveValue("Please check terminology");

      // Confirm via dialog button. Capture API response so we know advance succeeded.
      const [advanceResp] = await Promise.all([
        page.waitForResponse(
          (r) => r.url().includes("/workflow/advance") && r.request().method() === "POST",
          { timeout: 10000 },
        ),
        dialog.getByRole("button", { name: /^complete step/i }).click(),
      ]);
      expect(advanceResp.ok(), `advance API failed: ${advanceResp.status()}`).toBe(true);
      expect(advanceResp.request().postDataJSON()).toEqual({ comment: "Please check terminology" });

      // Leaves the editor for the project page. The pattern is anchored: the
      // editor URL (/projects/N/jobs/M/edit) also starts with /projects/N.
      await expect(page).toHaveURL(/\/projects\/\d+$/, { timeout: 5000 });

      // The review step's editor shows the comment as a banner. The banner
      // shows to anyone who opens the job, so the same user reopening it
      // stands in for the reviewer.
      await page.goto(`/projects/${setup.projectId}/jobs/${setup.jobId}/edit`);
      const note = page.getByTestId("task-handoff-note");
      await expect(note).toContainText("Please check terminology", { timeout: 10000 });
      await note.getByRole("button", { name: /comment from the previous step/i }).click();
      await expect(note.getByText("Please check terminology")).toHaveCount(0);
      // Hiding it is not remembered: reopening the editor shows it again.
      await page.reload();
      await expect(page.getByTestId("task-handoff-note")).toContainText("Please check terminology", { timeout: 10000 });
    } finally {
      if (projectId !== undefined) await deleteProjectViaAPI(page, projectId);
    }
  });

  test("reject opens dialog with reason field and rejects step", async ({ page }) => {
    // Isolated project, pre-advanced to the review step so reject is
    // available. The reject button is hard-asserted, never guarded by
    // `if (rejectBtn.isVisible())`.
    test.setTimeout(30_000);
    let projectId: number | undefined;
    try {
      const setup = await createIsolatedWorkflowJob(page, "reject");
      projectId = setup.projectId;

      // Pre-advance to review step so the reject button is visible.
      await advanceJobStep(page, setup.jobId);

      await page.goto(`/projects/${setup.projectId}/jobs/${setup.jobId}/edit`);
      await expect(page.getByText(/Hello world/i).first()).toBeVisible({ timeout: 10000 });

      const rejectBtn = page.getByRole("button", { name: /reject/i });
      await expect(rejectBtn).toBeVisible({ timeout: 5000 });
      await rejectBtn.click();

      await expect(page.getByText(/send the job back/i)).toBeVisible({ timeout: 3000 });

      const reasonField = page.getByPlaceholder(/terminology/i);
      await expect(reasonField).toBeVisible({ timeout: 3000 });
      await reasonField.fill("Translations need more context");

      // A server refusal keeps the dialog open with the typed reason. Routed
      // once; the real submit below reaches the server.
      const dialog = page.locator('[role="dialog"]');
      await page.route(
        (url) => url.pathname.endsWith("/workflow/reject"),
        (route) =>
          route.fulfill({
            status: 400,
            contentType: "application/json",
            body: JSON.stringify({
              error: { code: "Bad Request", message: "The reason is too long: at most 4000 characters." },
            }),
          }),
        { times: 1 },
      );
      const [refusal] = await Promise.all([
        page.waitForResponse(
          (r) => r.url().includes("/workflow/reject") && r.request().method() === "POST",
          { timeout: 10000 },
        ),
        dialog.getByTestId("reject-confirm").click(),
      ]);
      expect(refusal.status()).toBe(400);
      await expect(page.getByText(/at most 4000 characters/i)).toBeVisible({ timeout: 3000 });
      await expect(dialog).toHaveAttribute("data-open", "");
      await expect(reasonField).toHaveValue("Translations need more context");

      const [rejectResp] = await Promise.all([
        page.waitForResponse(
          (r) => r.url().includes("/workflow/reject") && r.request().method() === "POST",
          { timeout: 10000 },
        ),
        dialog.getByTestId("reject-confirm").click(),
      ]);
      expect(rejectResp.ok(), `reject API failed: ${rejectResp.status()}`).toBe(true);
    } finally {
      if (projectId !== undefined) await deleteProjectViaAPI(page, projectId);
    }
  });
});
