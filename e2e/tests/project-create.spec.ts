import { test, expect, type Page } from "@playwright/test";
import {
  login,
  startFromScratch,
  startFromTemplate,
  selectLanguage,
  sourceLanguageTrigger,
  selectTargetLanguages,
  pickComboboxOption,
  addAssignment,
  assertProjectAssignments,
  deleteProjectViaAPI,
  deleteDraftFromURL,
  getProjectSettingsViaAPI,
  getSeedTemplateMTRouter,
  getSeedTMId,
  uploadFileViaAPI,
  pretranslateFileViaAPI,
  segmentEditor,
  ensureStepDefinitionViaAPI,
  deleteStepDefinitionViaAPI,
  createWorkflowViaAPI,
  deleteWorkflowViaAPI,
  submitProjectWizard,
  systemLabelsViaAPI,
  addLabelValueViaAPI,
  projectLabelsViaAPI,
  setDeadline,
} from "./helpers";

// Seed-data shape that A1/A2 depend on. Default Template pre-fills these
// for en→es/fr/de + 2 workflow steps. If the seed evolves, update here.
// See cmd/seed/main.go::createTemplateWithAssignments.
// TM assignments are per-TM: one TM covers all its targets → 1 row.
// MT is one router per profile; a project made from the
// template follows the template's router (getSeedTemplateMTRouter).
const DEFAULT_TEMPLATE_TM_COUNT = 1;
const DEFAULT_TEMPLATE_WORKFLOW_STEPS = ["Translation", "Review"];

/**
 * Fills the project name + source/target languages on the create form.
 *
 * Uses the popover-scoped helpers in `helpers.ts` that explicitly wait for
 * the source popover to fully unmount before opening the target popover.
 * Without that discipline, target language selection races against the
 * still-closing source popover and intermittently picks the wrong option
 * (or no option at all).
 */
async function fillBasicInfo(page: Page, name: string, source: string, targets: string[]) {
  await page.getByPlaceholder(/project name/i).fill(name);

  // Source language.
  await selectLanguage(page, sourceLanguageTrigger(page), source);

  // Target languages — the MultiCombobox. Its trigger has an
  // accessible name ("Add target languages...") so we grab it by role.
  const targetTrigger = page.getByRole('button', { name: /add target languages/i });
  await selectTargetLanguages(page, targetTrigger, targets);
}

/**
 * Submits the form and returns the created project ID via API response.
 */
async function submitAndGetId(page: Page): Promise<number> {
  const { response, id } = await submitProjectWizard(page);
  expect(response.ok()).toBe(true);
  return id;
}

test.describe("Project Create Flows", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  // Typing into the wizard autosaves a draft project, so the tests that end on
  // a validation error rather than a publish still leave a row behind.
  // Covers every test here; the ones that publish delete
  // their project themselves and this then finds no ?draft= to act on.
  test.afterEach(async ({ page }) => {
    await deleteDraftFromURL(page);
  });

  // ============================================================
  // A1: CRITICAL — catches the onChange clobbering bug in create mode.
  //     Avoids the cmdk language picker entirely — uses template pre-fill to
  //     populate TM/MT/workflow, then triggers TWO UI-level onChange callbacks:
  //       1. Workflow pick (handleWorkflowChange)
  //       2. Scalar toggle (handleScalarChange)
  //     Verifies ALL template data + both new changes survive submit.
  //
  //     Bug pattern: each onChange spreads stale `values` prop, clobbering
  //     data from other sections. If handleScalarChange runs AFTER the
  //     workflow pick, it clobbers the workflow. If the workflow pick runs
  //     AFTER the scalar change, it clobbers the scalar.
  //
  //     This test uses ONLY plain HTML inputs (checkbox, text input, select)
  //     and the template picker dialog — no cmdk. Fully reliable.
  // ============================================================
  test("A1: template + workflow pick + scalar all survive clobbering", async ({ page }) => {
    test.setTimeout(30_000);
    const stamp = Date.now();
    const projectName = `E2E Clobber ${stamp}`;
    const customStepName = "E2E custom step";
    const workflowName = `E2E clobber wf ${stamp}`;
    let projectId: number | undefined;

    let customStepId: number | undefined;
    let workflowId: number | undefined;
    try {
      customStepId = await ensureStepDefinitionViaAPI(page, { name: customStepName });
      workflowId = await createWorkflowViaAPI(page, workflowName, [
        { name: "Translation" },
        { name: "Review" },
        { name: customStepName },
      ]);
      await page.getByRole("link", { name: /new project/i }).click();
      await expect(page.getByRole("heading", { name: /new project/i })).toBeVisible();

      // Apply Default Template — populates TM/MT/workflow via ref writes.
      await startFromTemplate(page, "Default Template");
      await page.getByPlaceholder(/project name/i).fill(projectName);
      await expect(page.getByText(/fuzzy match threshold/i)).toBeVisible({ timeout: 8000 });
      // Let all template-loading effects settle
      await page.waitForTimeout(500);

      // The wizard's steps always come from a library workflow:
      // switching to the one made above fires the card's
      // pick handler, which rewrites the step draft.
      await pickComboboxOption(page, page.getByTestId("use-workflow-select"), workflowName);
      await expect(page.getByTestId("use-workflow-select")).toContainText(workflowName);

      // Toggle pre-translate → fires handleScalarChange.
      // This is the OTHER onChange — if BUGGY, either this or the workflow
      // step gets clobbered depending on callback ordering.
      // Target the MT-on-upload checkbox by its label — NOT getByRole('checkbox').first(),
      // which is fragile because the create form has other checkboxes.
      const preTranslateCheckbox = page.getByRole('checkbox', { name: /machine translation.*start or upload/i });
      const wasChecked = await preTranslateCheckbox.isChecked();
      if (wasChecked) {
        await preTranslateCheckbox.uncheck();
      } else {
        await preTranslateCheckbox.check();
      }
      const expectedPreTranslate = !wasChecked;

      projectId = await submitAndGetId(page);
      await expect(page.getByRole("link", { name: /^files$/i })).toBeVisible({ timeout: 10000 });

      // CRITICAL: verify NOTHING was clobbered. Exact counts + IDs catch
      // duplicate-clobber regressions that `toBeGreaterThan(0)` would miss.
      const tmId = await getSeedTMId(page);
      const seedRouter = await getSeedTemplateMTRouter(page);
      await assertProjectAssignments(page, projectId, {
        tmCount: DEFAULT_TEMPLATE_TM_COUNT,
        mtRouterId: seedRouter.id,
        mtRouterName: seedRouter.name,
        // The picked workflow replaced the template's steps with its own.
        workflowStepCount: DEFAULT_TEMPLATE_WORKFLOW_STEPS.length + 1,
        workflowStepNames: [...DEFAULT_TEMPLATE_WORKFLOW_STEPS, customStepName],
        tmIds: [tmId],
        mtOnUpload: expectedPreTranslate,
      });
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
      if (workflowId) await deleteWorkflowViaAPI(page, workflowId);
      if (customStepId) await deleteStepDefinitionViaAPI(page, customStepId);
    }
  });

  // ============================================================
  // A2: Template pre-fill creates all template data
  // ============================================================
  test("A2: template pre-fill saves all template assignments", async ({ page }) => {
    test.setTimeout(30_000);
    const projectName = `E2E Template ${Date.now()}`;
    let projectId: number | undefined;

    try {
      await page.getByRole("link", { name: /new project/i }).click();
      await expect(page.getByRole("heading", { name: /new project/i })).toBeVisible();

      // Select Default Template (populates TM/MT/workflow at once)
      await startFromTemplate(page, "Default Template");

      await page.getByPlaceholder(/project name/i).fill(projectName);

      // `fuzzy match threshold` is a static label that renders the moment
      // SettingsEditor mounts — waiting on it does NOT tell us the template
      // pre-fill has finished. The pre-fill is an async `getTemplate()` call
      // that then calls `setValue('target_languages', ...)`. If the test
      // clicks Create before that settles, the form is submitted with empty
      // targets, the client-side zod validator blocks the POST, and
      // `waitForResponse` hangs for 10 s and then fails.
      //
      // The real "template applied" signal is: the target-languages chips
      // area now contains the template's target codes. Default Template in
      // the seed data is en → es/fr/de, so wait for at least one chip to
      // appear inside the target_languages field. Chips render as
      // `<span>{code}<button>×</button></span>`, so the span's text content
      // is `"es×"` / `"fr×"` / `"de×"` — match any of them.
      await expect(
        page.locator('[data-field="target_languages"] span').filter({ hasText: /^(ES|FR|DE)×?$/ }).first(),
      ).toBeVisible({ timeout: 8000 });

      projectId = await submitAndGetId(page);
      await expect(page.getByRole("link", { name: /^files$/i })).toBeVisible({ timeout: 10000 });

      // Template pre-fill must reproduce Default Template exactly.
      const tmId = await getSeedTMId(page);
      const seedRouter = await getSeedTemplateMTRouter(page);
      await assertProjectAssignments(page, projectId, {
        tmCount: DEFAULT_TEMPLATE_TM_COUNT,
        mtRouterId: seedRouter.id,
        mtRouterName: seedRouter.name,
        workflowStepCount: DEFAULT_TEMPLATE_WORKFLOW_STEPS.length,
        workflowStepNames: DEFAULT_TEMPLATE_WORKFLOW_STEPS,
        tmIds: [tmId],
      });
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
    }
  });

  // ============================================================
  // A4: Create from an existing project — languages + settings prefill
  //     exactly, the copy persists, and the source stays untouched.
  // ============================================================
  test("A4: create-from-project copies settings, source untouched", async ({ page }) => {
    test.setTimeout(30_000);
    const sourceName = `E2E DupSource ${Date.now()}`;
    let sourceId: number | undefined;
    const { customer: customerLabelId, domain: domainLabelId } = await systemLabelsViaAPI(page);
    let copyId: number | undefined;

    try {
      const headers = { Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem('token'))}` };

      // Source project with distinctive settings (fuzzy 0.65 + one TM) and a
      // classification that must carry into the copy: its Customer and Domain
      // labels.
      const createResp = await page.request.post('/api/projects', {
        headers,
        data: { name: sourceName, source_language: 'en', target_languages: ['fr', 'de'] },
      });
      expect(createResp.ok()).toBe(true);
      sourceId = (await createResp.json()).data.id;
      const domainValueId = await addLabelValueViaAPI(page, domainLabelId, 'legal');
      const customerValueId = await addLabelValueViaAPI(page, customerLabelId, `E2E DupCustomer ${Date.now()}`);
      const setLabels = await page.request.put(`/api/projects/${sourceId}/labels`, {
        headers,
        data: { labels: [
          { label_definition_id: domainLabelId, label_value_id: domainValueId },
          { label_definition_id: customerLabelId, label_value_id: customerValueId },
        ] },
      });
      expect(setLabels.ok()).toBe(true);
      const tmId = await getSeedTMId(page);
      const patchResp = await page.request.patch(`/api/projects/${sourceId}/settings`, {
        headers, data: { fuzzy_threshold: 0.65 },
      });
      expect(patchResp.ok()).toBe(true);
      const tmResp = await page.request.put(`/api/projects/${sourceId}/tms`, {
        headers, data: { assignments: [{ tm_id: tmId, mode: 'read_write', priority: 1 }] },
      });
      expect(tmResp.ok()).toBe(true);

      // Deep-link the way the list's Duplicate action does.
      await page.goto(`/projects/new?fromProject=${sourceId}`);
      await expect(page.getByRole("heading", { name: /new project/i })).toBeVisible();

      // Prefill: exact languages + settings + name / domain / customer. The
      // user usually renames, so the copy gets its own name here.
      await expect(page.getByTestId(`tm-assignment-row-${tmId}`)).toBeVisible({ timeout: 8000 });
      await expect(page.getByPlaceholder(/project name/i)).toHaveValue(sourceName);
      // Duplicating carries the source's classification: its label picks come
      // across, not just its settings.
      await expect(page.getByTestId(`project-label-value-${domainLabelId}`)).toContainText('legal');

      await page.getByPlaceholder(/project name/i).fill(`E2E DupCopy ${Date.now()}`);
      copyId = await submitAndGetId(page);
      await expect(page.getByRole("link", { name: /^files$/i })).toBeVisible({ timeout: 10000 });

      const copy = await getProjectSettingsViaAPI(page, copyId);
      expect(copy.data.project.source_language).toBe('en');
      expect(copy.data.project.target_languages).toEqual(['fr', 'de']);
      expect(copy.data.project.fuzzy_threshold).toBeCloseTo(0.65, 5);
      expect(copy.data.tm_assignments.length).toBe(1);
      expect(copy.data.tm_assignments[0].tm_id).toBe(tmId);
      // Ephemeral by design: "start from project" records no provenance.
      expect(copy.data.project.template_id).toBeNull();
      // Name and classification travel with the copy; status/deadline/files do not.
      expect(await projectLabelsViaAPI(page, copyId!)).toMatchObject({ Domain: 'legal' });

      // Source untouched.
      const src = await getProjectSettingsViaAPI(page, sourceId);
      expect(src.data.project.fuzzy_threshold).toBeCloseTo(0.65, 5);
      expect(src.data.tm_assignments.length).toBe(1);
    } finally {
      if (copyId) await deleteProjectViaAPI(page, copyId);
      if (sourceId) await deleteProjectViaAPI(page, sourceId);
    }
  });

  // ============================================================
  // A5: Template selected, then the user CLEARS a prefilled list —
  //     the cleared list must win over the server-applied template
  //     (guards the unconditional-PUT logic in ProjectCreate.onSubmit).
  //     Also checks the provenance display on the Settings tab.
  // ============================================================
  test("A5: clearing template assignments before submit wins over server apply", async ({ page }) => {
    test.setTimeout(30_000);
    const projectName = `E2E ClearTmpl ${Date.now()}`;
    let projectId: number | undefined;

    try {
      await page.getByRole("link", { name: /new project/i }).click();
      await expect(page.getByRole("heading", { name: /new project/i })).toBeVisible();

      await startFromTemplate(page, "Default Template");
      await page.getByPlaceholder(/project name/i).fill(projectName);
      const tmId = await getSeedTMId(page);
      await expect(page.getByTestId(`tm-assignment-row-${tmId}`)).toBeVisible({ timeout: 8000 });

      // Clear the prefilled TM list.
      await page.getByTestId(`tm-assignment-remove-${tmId}`).click();
      await expect(page.getByTestId(`tm-assignment-row-${tmId}`)).not.toBeVisible();

      projectId = await submitAndGetId(page);
      await expect(page.getByRole("link", { name: /^files$/i })).toBeVisible({ timeout: 10000 });

      const settings = await getProjectSettingsViaAPI(page, projectId);
      expect(settings.data.tm_assignments.length,
        "cleared TM list must not resurrect from the server-side template apply",
      ).toBe(0);
      expect(settings.data.project.mt_router_id,
        "untouched MT router still comes from the template",
      ).toBe((await getSeedTemplateMTRouter(page)).id);

      // Provenance: template_id stamped + name shown on the Settings tab.
      expect(settings.data.project.template_id).not.toBeNull();
      await page.getByRole("link", { name: /settings/i }).click();
      await expect(page.getByTestId("based-on-template")).toContainText("Default Template");
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
    }
  });

  // Domain is a built-in label; the picked value persists through create.
  test("domain optional input persists through create", async ({ page }) => {
    test.setTimeout(20_000);
    const projectName = `E2E Domain ${Date.now()}`;
    let projectId: number | undefined;
    const { domain: domainLabelId } = await systemLabelsViaAPI(page);
    await addLabelValueViaAPI(page, domainLabelId, 'Patents');

    try {
      await page.getByRole("link", { name: /new project/i }).click();
      await expect(page.getByRole("heading", { name: /new project/i })).toBeVisible();

      await startFromScratch(page);
      await fillBasicInfo(page, projectName, 'en', ['fr']);

      // The form offers the label's values rather than free text, so the value
      // has to exist before it can be picked.
      await pickComboboxOption(page, page.getByTestId(`project-label-value-${domainLabelId}`), 'Patents');

      projectId = await submitAndGetId(page);

      // Verify via API to assert on the persisted DB shape. Domain lives in
      // project_labels, not on the project row.
      expect(await projectLabelsViaAPI(page, projectId!)).toMatchObject({ Domain: 'Patents' });
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
    }
  });

  // The wizard is where a project gets scheduled. A step's
  // own deadline covers every target language; one language can be pulled earlier.
  // Both must survive the publish and reach the jobs of a file uploaded after.
  test("step deadlines set in the wizard reach each language's job", async ({ page }) => {
    test.setTimeout(40_000);
    const projectName = `E2E Due dates ${Date.now()}`;
    let projectId: number | undefined;

    try {
      await page.getByRole("link", { name: /new project/i }).click();
      await startFromScratch(page);
      await fillBasicInfo(page, projectName, 'en', ['fr', 'de']);

      // The first step a human works — the seeded default workflow starts with
      // an AI step, which carries no deadline and is not listed. Index by what
      // is rendered, not by position in the workflow.
      const stepField = page.locator('[data-testid^="workflow-deadline-input-"]').first();
      await expect(stepField).toBeVisible();
      const idx = (await stepField.getAttribute('data-testid'))!.replace('workflow-deadline-input-', '');

      // The step's own date, then an earlier one for de only. The override is
      // a row: which languages it covers, and the date they share.
      await setDeadline(page, stepField, '2027-09-03T17:00');
      await page.getByTestId(`workflow-deadline-langs-toggle-${idx}`).click();
      await selectTargetLanguages(
        page,
        page.getByTestId(`workflow-deadline-override-${idx}-0`).getByTestId('lang'),
        ['de'],
      );
      await setDeadline(page, page.getByTestId(`workflow-deadline-override-input-${idx}-0`), '2027-09-01T09:00');

      projectId = await submitAndGetId(page);

      // Upload a file: its jobs are one per target language, and each takes
      // the date that resolves for its own language.
      // The project's panels are routes, not tabs.
      await page.goto(`/projects/${projectId}`);
      await page.locator('input[type="file"]').setInputFiles({
        name: "due-dates.txt",
        mimeType: "text/plain",
        buffer: Buffer.from("Hello world."),
      });
      await expect(page.getByTestId('upload-confirm')).toBeVisible();
      await Promise.all([
        page.waitForResponse(
          r => r.url().includes(`/projects/${projectId}/files`) && r.request().method() === 'POST',
          { timeout: 20_000 },
        ),
        page.getByTestId('upload-confirm').click(),
      ]);

      // Assert on the step ASSIGNMENT, not the job's effective deadline: the
      // seeded workflow's first step is an AI one, which carries no date, so
      // the job's active-step deadline is legitimately empty here.
      const token = await page.evaluate(() => localStorage.getItem('token'));
      const auth = { headers: { Authorization: `Bearer ${token}` } };
      const jobs = await page.request.get(`/api/projects/${projectId}/jobs`, auth);
      expect(jobs.ok()).toBe(true);

      const dated: Record<string, string | null> = {};
      for (const job of (await jobs.json()).data) {
        const steps = await page.request.get(`/api/jobs/${job.id}/workflow/steps`, auth);
        expect(steps.ok()).toBe(true);
        const human = (await steps.json()).data.find((s: any) => s.step_type !== 'ai');
        dated[job.target_language] = human?.deadline ?? null;
      }
      expect(new Date(dated.de!).toISOString()).toBe(new Date('2027-09-01T09:00').toISOString());
      expect(new Date(dated.fr!).toISOString()).toBe(new Date('2027-09-03T17:00').toISOString());
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
    }
  });

  // The project's own deadline goes through the draft autosave: it must come
  // back when the draft is reopened, and land on the published project.
  test("the project deadline survives a draft reload and the publish", async ({ page }) => {
    test.setTimeout(30_000);
    const projectName = `E2E Project deadline ${Date.now()}`;
    const picked = '2027-09-10T15:30';
    const expected = new Date(picked).toISOString();
    let projectId: number | undefined;

    try {
      await page.getByRole("link", { name: /new project/i }).click();
      await startFromScratch(page);
      await fillBasicInfo(page, projectName, 'en', ['fr']);

      // Match the save that carries the deadline: the name and languages save
      // first, and their save must not satisfy this wait.
      const field = page.getByTestId('project-deadline-input');
      const [saved] = await Promise.all([
        page.waitForResponse(r =>
          /\/api\/projects(\/\d+\/draft)?$/.test(r.url()) &&
          ['POST', 'PUT'].includes(r.request().method()) &&
          Boolean(r.request().postDataJSON()?.deadline)),
        setDeadline(page, field, picked),
      ]);
      expect(saved.ok()).toBe(true);
      expect(saved.request().postDataJSON().deadline).toBe(expected);

      await expect(page).toHaveURL(/[?&]draft=\d+/);
      await page.reload();
      await expect(page.getByTestId('project-deadline-input')).toHaveAttribute('data-value', expected);

      projectId = await submitAndGetId(page);
      const token = await page.evaluate(() => localStorage.getItem('token'));
      const res = await page.request.get(`/api/projects/${projectId}`, { headers: { Authorization: `Bearer ${token}` } });
      expect(res.ok()).toBe(true);
      expect(new Date((await res.json()).data.deadline).toISOString()).toBe(expected);
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
    }
  });

  // Labels are optional: a project created without touching them has no picks
  // at all. The legacy client/domain columns are never written, so asserting
  // they are null would pass whatever the form did.
  test("a project created without touching the labels has none", async ({ page }) => {
    test.setTimeout(20_000);
    const projectName = `E2E NoLabels ${Date.now()}`;
    let projectId: number | undefined;

    try {
      await page.getByRole("link", { name: /new project/i }).click();
      await startFromScratch(page);
      await fillBasicInfo(page, projectName, 'en', ['fr']);
      // Deliberately leave every label unanswered.

      projectId = await submitAndGetId(page);
      expect(await projectLabelsViaAPI(page, projectId!)).toEqual({});
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
    }
  });

  test("B1: rejects source equals target", async ({ page }) => {
    const projectName = `E2E Invalid1 ${Date.now()}`;
    await page.getByRole("link", { name: /new project/i }).click();
    await expect(page.getByRole("heading", { name: /new project/i })).toBeVisible();

    await startFromScratch(page);
    await fillBasicInfo(page, projectName, 'en', ['en']);

    // The autosave has already been refused for the same reason; the click
    // replays the form (PUT /draft when a draft exists, else POST) and gets
    // the same 400 — the server, not the form, owns this rule.
    const [createResp] = await Promise.all([
      page.waitForResponse(
        resp => /\/api\/projects(\/\d+\/draft)?$/.test(resp.url()) && ['POST', 'PUT'].includes(resp.request().method()),
        { timeout: 10000 }
      ),
      page.getByRole("button", { name: /create project/i }).click(),
    ]);
    expect(createResp.status()).toBe(400);
    await expect(page.getByText(/create failed/i)).toBeVisible();
  });

  // ============================================================
  // C1: Edit existing project settings — change persists
  // ============================================================
  test("C1: edit project settings — fuzzy threshold and pre-translate persist", async ({ page }) => {
    test.setTimeout(30_000);
    const projectName = `E2E Edit ${Date.now()}`;
    let projectId: number | undefined;

    try {
      const createResp = await page.request.post('/api/projects', {
        headers: {
          Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem('token'))}`,
        },
        data: { name: projectName, source_language: 'en', target_languages: ['fr'] },
      });
      expect(createResp.ok()).toBe(true);
      projectId = (await createResp.json()).data.id;

      await page.goto(`/projects/${projectId}/files`);
      await page.getByRole('link', { name: /settings/i }).click();
      await expect(page.getByText(/fuzzy match threshold/i)).toBeVisible({ timeout: 5000 });

      // Find the MT-on-upload checkbox via its label.
      const preTranslateCheckbox = page.getByRole('checkbox', { name: /machine translation.*start or upload/i });
      const wasChecked = await preTranslateCheckbox.isChecked();

      const [updateResp] = await Promise.all([
        page.waitForResponse(
          resp => resp.url().includes('/settings') && resp.request().method() === 'PATCH',
          { timeout: 5000 }
        ),
        preTranslateCheckbox.click(),
      ]);
      expect(updateResp.ok()).toBe(true);

      await page.reload();
      await page.getByRole('link', { name: /settings/i }).click();
      await expect(page.getByText(/fuzzy match threshold/i)).toBeVisible({ timeout: 5000 });

      const preTranslateAfter = page.getByRole('checkbox', { name: /machine translation.*start or upload/i });
      const isCheckedAfter = await preTranslateAfter.isChecked();
      expect(isCheckedAfter).toBe(!wasChecked);
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
    }
  });

  // ============================================================
  // D1: Full pipeline — create project + upload via API, verify
  //     pipeline runs and produces translations.
  // ============================================================
  test("D1: full pipeline — fresh project, upload, pre-translate", async ({ page }) => {
    test.setTimeout(60_000);
    const projectName = `E2E Pipeline ${Date.now()}`;
    let projectId: number | undefined;

    try {
      // Create via API with template (gets the TM assignment + MT router)
      const tmplResp = await page.request.get('/api/templates', {
        headers: { Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem('token'))}` },
      });
      const templates = (await tmplResp.json()).data;
      const defaultTemplate = templates.find((t: any) => t.name === 'Default Template');
      expect(defaultTemplate, 'Default Template should exist').toBeDefined();

      // template_id copies the TM assignments + MT router at creation (create-time-only)
      const createResp = await page.request.post('/api/projects', {
        headers: { Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem('token'))}` },
        data: {
          name: projectName,
          source_language: 'en',
          target_languages: ['es'],
          template_id: defaultTemplate.id,
        },
      });
      expect(createResp.ok()).toBe(true);
      projectId = (await createResp.json()).data.id;

      // Verify settings have TM + the template's MT router
      const settings = await getProjectSettingsViaAPI(page, projectId);
      expect(settings.data.tm_assignments?.length ?? 0).toBeGreaterThan(0);
      expect(settings.data.project.mt_router_id).toBe((await getSeedTemplateMTRouter(page)).id);

      const uploadResp = await uploadFileViaAPI(
        page,
        projectId,
        'pipeline-test.json',
        '{"greeting":"Hello world","farewell":"Goodbye"}',
        'application/json'
      );
      const fileData = uploadResp.data;
      expect(fileData.id).toBeDefined();

      // Trigger TM lookup + MT translation via production phase-split endpoints
      // (no QE+APE — that runs as a workflow step; tested separately).
      await pretranslateFileViaAPI(page, projectId, fileData.id);

      // Poll job status. pipeline_status='completed' is reserved for
      // /complete; pre-translate leaves 'in_progress' with a current_step ending
      // in "complete". Mirrors the FE's isPipelineRunning helper: done means a
      // terminal status or a current_step ending in "complete".
      const isPipelineDone = (j: any) =>
        j.pipeline_status === 'completed' ||
        j.pipeline_status === 'failed' ||
        (j.pipeline_status === 'in_progress' && (j.current_step ?? '').toLowerCase().endsWith('complete'));
      let completed = false;
      for (let i = 0; i < 30; i++) {
        const jobsResp = await page.request.get(
          `/api/projects/${projectId}/jobs`,
          {
            headers: { Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem('token'))}` },
          }
        );
        const jobs = (await jobsResp.json()).data;
        if (jobs.length > 0 && jobs.every(isPipelineDone)) {
          completed = true;
          break;
        }
        await page.waitForTimeout(500);
      }
      expect(completed, 'pipeline did not complete in 15s').toBe(true);

      // Verify translations have content (at least some are pre_translated)
      const jobsResp = await page.request.get(
        `/api/projects/${projectId}/jobs`,
        {
          headers: { Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem('token'))}` },
        }
      );
      const jobs = (await jobsResp.json()).data;
      expect(jobs.length).toBeGreaterThan(0);

      // At least one job should have translations beyond untranslated
      const job = jobs[0];
      const totalTranslated = (job.counts?.pre_translated ?? 0) + (job.counts?.approved ?? 0);
      expect(totalTranslated, 'pipeline produced no translations').toBeGreaterThan(0);
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
    }
  });

  // ============================================================
  // D2: Edit translation in CAT editor and confirm — verify saved
  // ============================================================
  test("D2: edit translation in CAT editor — confirm and persist", async ({ page }) => {
    test.setTimeout(45_000);
    const projectName = `E2E Edit Trans ${Date.now()}`;
    let projectId: number | undefined;

    try {
      const createResp = await page.request.post('/api/projects', {
        headers: { Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem('token'))}` },
        data: { name: projectName, source_language: 'en', target_languages: ['es'] },
      });
      expect(createResp.ok()).toBe(true);
      projectId = (await createResp.json()).data.id;

      const uploadResp = await uploadFileViaAPI(
        page,
        projectId,
        'edit-test.json',
        '{"key1":"Hello world"}',
        'application/json'
      );
      const fileData = uploadResp.data;

      const jobsResp = await page.request.get(
        `/api/projects/${projectId}/jobs`,
        {
          headers: { Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem('token'))}` },
        }
      );
      const jobs = (await jobsResp.json()).data;
      expect(jobs.length).toBeGreaterThan(0);
      const jobId = jobs[0].id;

      await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);

      // Wait for segments to load (the source text should be visible)
      await expect(page.getByText('Hello world').first()).toBeVisible({ timeout: 10000 });

      // Click on the first segment row to activate it
      const firstRow = page.locator('[class*="grid"]').filter({ hasText: 'Hello world' }).first();
      await firstRow.click();

      const editor = segmentEditor(page).first();
      await expect(editor).toBeVisible({ timeout: 3000 });

      await editor.click();
      await page.keyboard.type('Hola mundo');

      const [updateResp] = await Promise.all([
        page.waitForResponse(
          resp => resp.url().includes('/translations/') && resp.request().method() === 'PUT',
          { timeout: 5000 }
        ),
        page.keyboard.press('ControlOrMeta+Enter'),
      ]);
      expect(updateResp.ok()).toBe(true);

      await page.reload();
      await expect(page.getByText('Hola mundo').first()).toBeVisible({ timeout: 5000 });
      void fileData;
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
    }
  });

  // ============================================================
  // E1: Upload files during project creation.
  //     A file picked in the wizard goes straight into the autosaved draft
  //     (stored + segmented, no jobs); Create publishes the draft, which
  //     creates the jobs and — auto-analyze on, no pretranslate — exactly one
  //     coalesced auto analysis for the project.
  // ============================================================
  test("E1: create with a file uploads it + produces one auto analysis", async ({ page }) => {
    test.setTimeout(45_000);
    const projectName = `E2E CreateUpload ${Date.now()}`;
    let projectId: number | undefined;

    try {
      await page.getByRole("link", { name: /new project/i }).click();
      await expect(page.getByRole("heading", { name: /new project/i })).toBeVisible();

      await startFromScratch(page);
      await fillBasicInfo(page, projectName, "en", ["es"]);

      // Attach a small file to the create-form dropzone (hidden input). The
      // upload lands on the draft right away.
      const [uploadResp] = await Promise.all([
        page.waitForResponse(r => /\/api\/projects\/\d+\/files$/.test(r.url()) && r.request().method() === "POST", { timeout: 10000 }),
        page
          .locator('[data-testid="create-file-dropzone"] input[type="file"]')
          .setInputFiles({
            name: "create-upload.json",
            mimeType: "application/json",
            buffer: Buffer.from('{"greeting":"Hello world"}'),
          }),
      ]);
      expect(uploadResp.status()).toBe(201);
      // The uploaded file lists, and auto-analyze defaults on.
      await expect(page.getByTestId("create-file-list")).toContainText("create-upload.json");
      await expect(page.getByTestId("create-analyze-checkbox")).toBeChecked();

      // A draft has files but no jobs until it is published.
      const token = await page.evaluate(() => localStorage.getItem("token"));
      const auth = { Authorization: `Bearer ${token}` };
      const draftId = Number(new URL(page.url()).searchParams.get("draft"));
      expect(draftId).toBeGreaterThan(0);
      const draftJobs = await page.request.get(`/api/projects/${draftId}/jobs`, { headers: auth });
      expect(((await draftJobs.json()).data ?? []).length, "a draft must not have jobs").toBe(0);

      projectId = await submitAndGetId(page);
      expect(projectId).toBe(draftId);
      await expect(page.getByRole("link", { name: /^files$/i })).toBeVisible({ timeout: 10000 });

      // Publish created the job(s) for the draft's file.
      let jobsReady = false;
      for (let i = 0; i < 40; i++) {
        const jr = await page.request.get(`/api/projects/${projectId}/jobs`, { headers: auth });
        if (((await jr.json()).data ?? []).length > 0) { jobsReady = true; break; }
        await page.waitForTimeout(500);
      }
      expect(jobsReady, "uploaded file produced no jobs").toBe(true);

      // Exactly one auto analysis should be created + reach a terminal state.
      let analyses: any[] = [];
      for (let i = 0; i < 40; i++) {
        const ar = await page.request.get(`/api/projects/${projectId}/analyses`, { headers: auth });
        analyses = (await ar.json()).data ?? [];
        if (analyses.length >= 1 && analyses.every(a => a.status === "completed" || a.status === "failed")) break;
        await page.waitForTimeout(500);
      }
      expect(analyses.length, "expected exactly one coalesced auto analysis").toBe(1);
      expect(analyses[0].is_auto).toBe(true);
      expect(analyses[0].status).toBe("completed");
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
    }
  });

  // ============================================================
  // B3: A plain create (no template) gets the default Translation → Review
  //     workflow, linked to the tenant's library definitions. The create form
  //     pre-fills the steps from the library, so this checks the UI path.
  // ============================================================
  test("B3: an untouched create is based on the first library workflow; the API default is Translation → Review", async ({ page }) => {
    let projectId: number | undefined;
    let apiProjectId: number | undefined;
    const token = await page.evaluate(() => localStorage.getItem("token"));
    const headers = { Authorization: `Bearer ${token}` };
    try {
      // The seed has library workflows, so the wizard pre-selects the first
      // (alphabetical) one and the project is created based on it.
      const wfRes = await page.request.get(`/api/workflows`, { headers });
      const first = ((await wfRes.json()).data as { id: number; name: string; steps: { name: string }[] }[])[0];
      expect(first, "seed must provide at least one library workflow").toBeTruthy();

      await page.getByRole("link", { name: /new project/i }).click();
      await startFromScratch(page);
      await expect(page.getByTestId("use-workflow-select")).toContainText(first.name);
      await fillBasicInfo(page, `e2e-default-step-${Date.now()}`, "en", ["fr"]);
      projectId = await submitAndGetId(page);

      const settings = await getProjectSettingsViaAPI(page, projectId);
      expect(settings.data.project.workflow_id).toBe(first.id);
      expect((settings.data.workflow_steps as { name: string }[]).map((s) => s.name)).toEqual(first.steps.map((s) => s.name));

      // Without a UI in the loop (API / MCP create sending nothing), the
      // server's own default is Translation → Review, linked to the library.
      const apiRes = await page.request.post(`/api/projects`, {
        headers, data: { name: `e2e-api-default-${Date.now()}`, source_language: "en", target_languages: ["fr"] },
      });
      expect(apiRes.status()).toBe(201);
      apiProjectId = (await apiRes.json()).data.id as number;
      const apiSettings = await getProjectSettingsViaAPI(page, apiProjectId);
      const steps = apiSettings.data.workflow_steps as { name: string; step_type: string; step_definition_id: number | null }[];
      expect(steps.map((s) => s.name)).toEqual(["Translation", "Review"]);
      for (const s of steps) {
        expect(s.step_type, `${s.name} type`).toBe("human");
        expect(s.step_definition_id, `${s.name} must link to a library definition`).not.toBeNull();
      }
      expect(apiSettings.data.project.workflow_id).toBeNull();
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
      if (apiProjectId) await deleteProjectViaAPI(page, apiProjectId);
    }
  });

  // ============================================================
  // B2: Empty name is rejected (frontend validation)
  // ============================================================
  test("B2: rejects empty name", async ({ page }) => {
    await page.getByRole("link", { name: /new project/i }).click();
    await expect(page.getByRole("heading", { name: /new project/i })).toBeVisible();
    await startFromScratch(page);

    // Fill source + target but leave name empty
    await selectLanguage(page, sourceLanguageTrigger(page), 'en');
    const targetTrigger = page.getByRole('button', { name: /add target languages/i });
    await targetTrigger.click();
    await page.getByPlaceholder(/search languages/i).fill('fr');
    await page.getByRole('option').filter({ has: page.locator('text=/^fr$/i') }).first().click();
    await page.keyboard.press('Escape');

    await page.getByRole("button", { name: /create project/i }).click();

    // Form validation error should appear (zod schema: name required)
    await expect(page.getByText(/name is required/i)).toBeVisible({ timeout: 3000 });
  });
});
