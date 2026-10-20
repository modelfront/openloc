import { test, expect, type Page } from "@playwright/test";
import {
  authHeaders,
  createProjectViaAPI,
  deleteDraftFromURL,
  deleteProjectViaAPI,
  getProjectSettingsViaAPI,
  getSeedProjectId,
  getSeedTemplateMTRouter,
  login,
  pickComboboxValue,
  selectLanguage,
  selectTargetLanguages,
  sourceLanguageTrigger,
  startFromScratch,
  submitProjectWizard,
  uploadFileViaAPI,
} from "./helpers";

// The built-in Pre-translation workflow: a single automated step that
// pre-translates the file and completes itself. The New project form picks it
// through the Workflow card's Raw MT mode, which locks the MT switch; a
// manager's edit reopens the finished task through the regular flow. The dev
// login is an owner, i.e. a manager.

interface Step {
  id: number;
  step_name: string;
  step_type: string;
  status: string;
}

async function builtInWorkflowId(page: Page): Promise<number> {
  const res = await page.request.get("/api/workflows/pretranslate-only", { headers: await authHeaders(page) });
  expect(res.ok(), `get built-in workflow: ${res.status()}`).toBe(true);
  return (await res.json()).data.id;
}

async function listSteps(page: Page, jobId: number): Promise<Step[]> {
  const res = await page.request.get(`/api/jobs/${jobId}/workflow/steps`, { headers: await authHeaders(page) });
  expect(res.ok(), `list steps: ${res.status()}`).toBe(true);
  return (await res.json()).data;
}

async function jobStatus(page: Page, jobId: number): Promise<string> {
  const res = await page.request.get(`/api/jobs/${jobId}`, { headers: await authHeaders(page) });
  expect(res.ok(), `get job: ${res.status()}`).toBe(true);
  return (await res.json()).data.pipeline_status;
}

async function eventCount(page: Page, projectId: number, type: string): Promise<number> {
  const res = await page.request.get(`/api/projects/${projectId}/events?type=${type}`, { headers: await authHeaders(page) });
  expect(res.ok(), `list events: ${res.status()}`).toBe(true);
  return ((await res.json()).data as { event_type: string }[]).filter((e) => e.event_type === type).length;
}

test.describe("Pre-translation workflow", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  // The form autosaves a draft on the first blur; a test that fails before
  // publishing still leaves it behind.
  test.afterEach(async ({ page }) => {
    await deleteDraftFromURL(page);
  });

  test("New project: Raw MT picks the built-in workflow, locks MT, and the project is created with it", async ({ page }) => {
    await page.goto("/projects/new");
    await startFromScratch(page);
    await page.getByPlaceholder(/project name/i).fill(`Pre-translate only ${Date.now()}`);
    await selectLanguage(page, sourceLanguageTrigger(page), "en");
    await selectTargetLanguages(page, page.getByRole("button", { name: /add target languages/i }), ["de"]);

    const rawMT = page.getByTestId("workflow-mode-raw_mt");
    await expect(rawMT).toBeEnabled();
    await expect(rawMT).toHaveAttribute("aria-checked", "false");
    await rawMT.click();

    await expect(rawMT).toHaveAttribute("aria-checked", "true");
    await expect(page.getByTestId("raw-mt-pretranslation")).toBeVisible();
    await expect(page.getByTestId("use-workflow-select")).toHaveCount(0);
    const mt = page.getByTestId("settings-mt-on-upload");
    await expect(mt).toBeChecked();
    await expect(mt).toBeDisabled();

    // No MT router yet: the project cannot start, and the form says so.
    await expect(page.getByTestId("settings-mt-router-required")).toBeVisible();
    await page.getByRole("button", { name: /^create project$/i }).click();
    await expect(
      page.locator("[data-sonner-toast]").filter({
        hasText: "Choose an MT router: the Pre-translation workflow fills files in with machine translation.",
      }),
    ).toBeVisible();
    await expect(page).toHaveURL(/\/projects\/new/);

    const router = await getSeedTemplateMTRouter(page);
    await pickComboboxValue(page, "mt-router-select", String(router.id));
    await expect(page.getByTestId("settings-mt-router-required")).toBeHidden();
    // The Raw MT row follows the picker without a save.
    await expect(page.getByTestId("raw-mt-router-link")).toHaveText(router.name);

    const { response, id } = await submitProjectWizard(page);
    try {
      expect(response.ok(), `publish: ${response.status()}`).toBe(true);
      const settings = (await getProjectSettingsViaAPI(page, id)).data;
      expect(settings.workflow_steps.map((s: { step_type: string }) => s.step_type)).toEqual(["pretranslate"]);
      expect(settings.project.mt_on_upload, "Pre-translation fills in MT on upload").toBe(true);
      expect(settings.project.mt_router_id).toBe(router.id);
    } finally {
      await deleteProjectViaAPI(page, id);
    }
  });

  test("New project, Workflow mode: the Pre-translation row appears with a router and takes you to it", async ({ page }) => {
    await page.goto("/projects/new");
    await startFromScratch(page);
    await expect(page.getByTestId("workflow-mode-workflow")).toHaveAttribute("aria-checked", "true");
    // No router yet: nothing would run on upload, so there is no row.
    await expect(page.getByTestId("workflow-pretranslation")).toHaveCount(0);

    const router = await getSeedTemplateMTRouter(page);
    await pickComboboxValue(page, "mt-router-select", String(router.id));
    const row = page.getByTestId("workflow-pretranslation");
    await expect(row).toContainText(router.name);

    await row.getByTestId("raw-mt-router-link").click();
    await expect(page.getByTestId("mt-router-select")).toBeFocused();
    await expect(page.getByTestId("mt-section")).toHaveAttribute("data-attention", "");
  });

  test("project Workflow tab: the Pre-translation row's router opens Settings on the MT router", async ({ page }) => {
    const projectId = await getSeedProjectId(page);
    const settings = (await getProjectSettingsViaAPI(page, projectId)).data;
    expect(settings.project.mt_on_upload, "seed Demo Project fills in MT on upload").toBe(true);
    expect(settings.project.mt_router_id, "seed Demo Project has an MT router").toEqual(expect.any(Number));

    await page.goto(`/projects/${projectId}/workflow`);
    const row = page.getByTestId("workflow-pretranslation");
    await expect(row).toBeVisible();
    await row.getByTestId("raw-mt-router-link").click();
    await expect(page).toHaveURL(new RegExp(`/projects/${projectId}/settings$`));
    await expect(page.getByTestId("mt-router-select")).toBeFocused();
  });

  test("an upload pre-translates and completes itself; a manager's edit reopens it", async ({ page }) => {
    test.setTimeout(60_000);
    const headers = await authHeaders(page);
    const router = await getSeedTemplateMTRouter(page);
    const projectId = await createProjectViaAPI(page, `Pre-translate only upload ${Date.now()}`, {
      workflow_id: await builtInWorkflowId(page),
      settings: { mt_router_id: router.id },
    });
    try {
      await uploadFileViaAPI(page, projectId, `pre-${Date.now()}.txt`, "Hello world\nSecond line\n");
      const jobsRes = await page.request.get(`/api/projects/${projectId}/jobs`, { headers });
      expect(jobsRes.ok(), `list jobs: ${jobsRes.status()}`).toBe(true);
      const jobs = (await jobsRes.json()).data as { id: number }[];
      expect(jobs).toHaveLength(1);
      const jobId = jobs[0].id;

      // The upload queued pre-translation; when it finishes the task and the
      // job complete themselves.
      await expect
        .poll(async () => (await listSteps(page, jobId)).map((s) => `${s.step_type}:${s.status}`), { timeout: 30_000 })
        .toEqual(["pretranslate:completed"]);
      expect(await jobStatus(page, jobId)).toBe("completed");
      expect(await eventCount(page, projectId, "workflow.step_auto_completed")).toBe(1);

      await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
      const banner = page.getByTestId("task-actions");
      await expect(banner).toHaveAttribute("data-mode", "reopenable");
      await expect(page.getByTestId("cat-current-step")).toHaveText("Pre-translation");

      // Leaving the edited segment flushes the draft, and that save reopens
      // the task.
      await page.locator("[data-translation-cell]").first().click();
      await expect(page.locator('[data-testid="segment-editor"] .ProseMirror').first()).toBeVisible();
      const saved = page.waitForResponse(
        (r) => /\/translations\/\d+\/draft$/.test(r.url()) && r.request().method() === "PUT",
      );
      await page.keyboard.type(" geprüft");
      await page.locator("[data-translation-cell]").nth(1).click();
      const res = await saved;
      expect(res.ok(), `draft: ${res.status()}`).toBe(true);
      expect((await res.json()).task_reopened, "the first edit reports the reopen").toBe(true);

      await expect(page.locator("[data-sonner-toast]").filter({ hasText: "Task reopened" })).toBeVisible();
      await expect(banner).toHaveAttribute("data-mode", "editable");
      expect((await listSteps(page, jobId))[0].status).toBe("in_progress");
      expect(await jobStatus(page, jobId)).not.toBe("completed");
      expect(await eventCount(page, projectId, "workflow.step_reopened"), "history records the reopen once").toBe(1);
    } finally {
      await deleteProjectViaAPI(page, projectId);
    }
  });
});
