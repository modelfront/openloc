// Step + workflow libraries: /workflows (Workflows |
// Steps), a project's Workflow tab (pick a library workflow; its steps are a
// read-only copy), and the template twin. Every test cleans up what it makes.
import { test, expect, type Page } from "@playwright/test";
import {
  login,
  startFromScratch,
  startFromTemplate,
  pickComboboxOption,
  selectLanguage,
  sourceLanguageTrigger,
  selectTargetLanguages,
  createWorkflowViaAPI,
  clickComboboxAction,
  expectNoOverflow,
  UNBREAKABLE_NAME,
  deleteWorkflowViaAPI,
  deleteStepDefinitionViaAPI,
  ensureStepDefinitionViaAPI,
  deleteProjectViaAPI,
  createProjectViaAPI,
  getProjectSettingsViaAPI,
  submitProjectWizard,
} from "./helpers";


/**
 * Detach and Save-as are pinned rows inside the workflow picker's dropdown, so
 * asserting on one means opening the dropdown and closing it again.
 */
async function expectWorkflowAction(page: Page, key: string, count: number) {
  const popover = page.locator('[data-slot="popover-content"]');
  await expect(popover).toHaveCount(0, { timeout: 3000 });
  await page.getByTestId("use-workflow-select").click();
  await expect(popover).toHaveCount(1, { timeout: 3000 });
  await expect(popover.getByTestId(`use-workflow-select-action-${key}`)).toHaveCount(count);
  await page.keyboard.press("Escape");
  await expect(popover).toHaveCount(0, { timeout: 3000 });
}

test.describe("Workflow libraries", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("Workflows nav → Steps subnav lists the seeded library with types", async ({ page }) => {
    await page.getByRole("link", { name: /^workflows$/i }).click();
    await expect(page).toHaveURL(/\/workflows$/);
    await expect(page.getByRole("heading", { name: /workflows/i })).toBeVisible();
    // Seeded workflows carry their step chips.
    await expect(page.getByTestId(/^workflow-\d+$/).filter({ hasText: "Standard" })).toContainText("Translation");
    await page.getByRole("link", { name: /^steps$/i }).click();
    await expect(page).toHaveURL(/\/workflows\/steps$/);
    const translation = page.getByTestId(/^step-definition-\d+$/).filter({ hasText: /^Translation/ }).first();
    await expect(translation).toBeVisible();
    await expect(translation.getByTestId("step-type-human")).toBeVisible();
    // The seeded ai step shows its type + engine.
    const autoAPE = page.getByTestId(/^step-definition-\d+$/).filter({ hasText: "Auto APE" }).first();
    await expect(autoAPE.getByTestId("step-type-ai")).toBeVisible();
  });

  test("create, edit and delete a step definition; in-use delete is refused", async ({ page }) => {
    test.setTimeout(20_000);
    const name = `E2E step ${Date.now()}`;
    let workflowId: number | undefined;
    let definitionId: number | undefined;
    try {
      await page.goto("/workflows/steps");
      await page.getByTestId("new-step-definition").click();
      const dialog = page.getByTestId("step-definition-dialog");
      await dialog.getByTestId("step-definition-name").fill(name);
      // Type: hybrid without a router is refused inline; back to human submits.
      await dialog.getByTestId("step-type-option-hybrid").click();
      await dialog.getByTestId("step-definition-submit").click();
      await expect(dialog.getByText("AI router is required")).toBeVisible();
      // The router picker lists the seeded router by name.
      await dialog.getByTestId("step-definition-router").click();
      const popover = page.locator('[data-slot="popover-content"]');
      await expect(popover.getByRole("option", { name: /Default QE\+APE router/ })).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(popover).toHaveCount(0);
      await dialog.getByTestId("step-type-option-human").click();
      const [res] = await Promise.all([
        page.waitForResponse((r) => r.url().includes("/api/step-definitions") && r.request().method() === "POST"),
        dialog.getByTestId("step-definition-submit").click(),
      ]);
      expect(res.status()).toBe(201);
      definitionId = (await res.json()).data.id;
      const row = page.getByTestId(`step-definition-${definitionId}`);
      await expect(row).toContainText(name);
      await expect(row).toContainText(/not in any workflow/i);

      // Rename through the dialog. The dialog seeds its fields from `initial`
      // in an effect, a tick after it opens, so wait for the seeded value
      // before typing over it — a fill that lands first gets reset away.
      await row.getByTestId(`edit-step-definition-${definitionId}`).click();
      const nameInput = dialog.getByTestId("step-definition-name");
      await expect(nameInput).toHaveValue(name);
      await nameInput.fill(`${name} v2`);
      const [upd] = await Promise.all([
        page.waitForResponse((r) => r.url().includes(`/api/step-definitions/${definitionId}`) && r.request().method() === "PUT"),
        dialog.getByTestId("step-definition-submit").click(),
      ]);
      expect(upd.status()).toBe(200);
      await expect(row).toContainText(`${name} v2`);

      // Compose it into a workflow → delete is refused and names the workflow.
      workflowId = await createWorkflowViaAPI(page, `E2E wf ${Date.now()}`, [{ name: `${name} v2` }]);
      await page.reload();
      await expect(row).toContainText(/in 1 workflow/i);
      await row.getByTestId(`delete-step-definition-${definitionId}`).click();
      const [del] = await Promise.all([
        page.waitForResponse((r) => r.url().includes(`/api/step-definitions/${definitionId}`) && r.request().method() === "DELETE"),
        page.getByRole("button", { name: /^delete$/i }).click(),
      ]);
      expect(del.status()).toBe(409);
      await expect(row).toBeVisible();

      // Free it, then the delete goes through.
      await deleteWorkflowViaAPI(page, workflowId);
      workflowId = undefined;
      await page.reload();
      await row.getByTestId(`delete-step-definition-${definitionId}`).click();
      const [del2] = await Promise.all([
        page.waitForResponse((r) => r.url().includes(`/api/step-definitions/${definitionId}`) && r.request().method() === "DELETE"),
        page.getByRole("button", { name: /^delete$/i }).click(),
      ]);
      expect(del2.status()).toBe(204);
      await expect(row).toHaveCount(0);
      definitionId = undefined;
    } finally {
      if (workflowId) await deleteWorkflowViaAPI(page, workflowId);
      if (definitionId) await deleteStepDefinitionViaAPI(page, definitionId);
    }
  });

  test("create a workflow from the library page, then reshape it in the edit dialog", async ({ page }) => {
    test.setTimeout(20_000);
    const name = `E2E workflow ${Date.now()}`;
    let workflowId: number | undefined;
    try {
      await page.goto("/workflows");
      // "New workflow" opens the same name + steps dialog the New Project
      // wizard uses, seeded with the default pair and
      // and with no due-date fields (a library workflow carries no dates).
      await page.getByTestId("new-workflow").click();
      const create = page.getByTestId("workflow-create-dialog");
      await expect(create.getByTestId("workflow-step-picker-0")).toContainText("Translation");
      await expect(create.getByTestId("workflow-step-picker-1")).toContainText("Review");
      await expect(create.getByTestId("workflow-deadline-input-0")).toHaveCount(0);
      await create.getByTestId("workflow-add-step").click();
      await pickComboboxOption(page, create.getByTestId("workflow-step-picker-2"), /^Auto APE\b/);
      await expect(create.getByTestId("workflow-step-automated-2")).toBeVisible();
      await create.getByTestId("workflow-create-name").fill(name);
      const [res] = await Promise.all([
        page.waitForResponse((r) => r.url().endsWith("/api/workflows") && r.request().method() === "POST"),
        create.getByTestId("workflow-create-save").click(),
      ]);
      expect(res.status()).toBe(201);
      workflowId = (await res.json()).data.id;
      const created = (await res.json()).data.steps as { name: string; step_type: string }[];
      expect(created.map((s) => `${s.name}:${s.step_type}`)).toEqual(["Translation:human", "Review:human", "Auto APE:ai"]);
      await expect(create).toHaveCount(0);

      // The row carries the steps; its pencil opens the edit dialog, which
      // drops one and saves.
      const row = page.getByTestId(`workflow-${workflowId}`);
      await expect(row).toContainText("Auto APE");
      await row.getByTestId(`edit-workflow-${workflowId}`).click();
      const edit = page.getByTestId("workflow-edit-dialog");
      await expect(edit.getByTestId("workflow-edit-name")).toHaveValue(name);
      await edit.getByTestId("workflow-step-remove-2").click();
      const [upd] = await Promise.all([
        page.waitForResponse((r) => r.url().includes(`/api/workflows/${workflowId}`) && r.request().method() === "PUT"),
        edit.getByTestId("workflow-edit-save").click(),
      ]);
      expect(upd.status()).toBe(200);
      const steps = (await upd.json()).data.steps as { name: string }[];
      expect(steps.map((s) => s.name)).toEqual(["Translation", "Review"]);
      await expect(edit).toHaveCount(0);
      await expect(row).not.toContainText("Auto APE");
    } finally {
      if (workflowId) await deleteWorkflowViaAPI(page, workflowId);
    }
  });

  test("project Workflow tab: use a library workflow", async ({ page }) => {
    test.setTimeout(30_000);
    const stamp = Date.now();
    let projectId: number | undefined;
    let libraryId: number | undefined;
    let proofreadId: number | undefined;
    const proofread = `Proofread ${stamp}`;
    try {
      proofreadId = await ensureStepDefinitionViaAPI(page, { name: proofread });
      libraryId = await createWorkflowViaAPI(page, `E2E lib ${stamp}`, [{ name: "Translation" }, { name: proofread }]);
      projectId = await createProjectViaAPI(page, `E2E wf project ${stamp}`);

      await page.goto(`/projects/${projectId}/workflow`);
      // Steps with no library workflow behind them: the field is empty and the
      // steps still render under it.
      await expect(page.getByTestId("use-workflow-select")).toContainText("Choose a workflow");
      await expect(page.getByTestId("workflow-step-badge-1")).toContainText("Review");

      // The picker offers library workflows and nothing else — the tab has the
      // wizard's shape, so there is no Custom entry, no detach, no save-as and
      // no step editor.
      await page.getByTestId("use-workflow-select").click();
      const wfPopover = page.locator('[data-slot="popover-content"]');
      await expect(wfPopover.getByRole("option", { name: /^Custom$/ })).toHaveCount(0);
      await expect(wfPopover.getByTestId("use-workflow-select-action-detach")).toHaveCount(0);
      await expect(wfPopover.getByTestId("use-workflow-select-action-save-as")).toHaveCount(0);
      await page.keyboard.press("Escape");
      await expect(page.getByRole("button", { name: /^edit$/i })).toHaveCount(0);
      await expect(page.getByTestId("workflow-add-step")).toHaveCount(0);

      // Use the library workflow: confirmed first, because a live project's
      // steps may already be in flight.
      await pickComboboxOption(page, page.getByTestId("use-workflow-select"), `E2E lib ${stamp}`);
      const [applied] = await Promise.all([
        page.waitForResponse((r) => r.url().includes(`/projects/${projectId}/workflow/apply`) && r.request().method() === "POST"),
        page.getByRole("button", { name: /^use workflow$/i }).click(),
      ]);
      expect(applied.status()).toBe(200);
      await expect(page.getByTestId("use-workflow-select")).toContainText(`E2E lib ${stamp}`);
      await expect(page.getByTestId("workflow-step-badge-1")).toContainText(proofread);

      // The copy is the project's own: the library workflow is untouched.
      const settings = await getProjectSettingsViaAPI(page, projectId);
      expect((settings.data.workflow_steps as { name: string }[]).map((s) => s.name)).toEqual(["Translation", proofread]);
      const libToken = await page.evaluate(() => localStorage.getItem("token"));
      const lib = await page.request.get(`/api/workflows/${libraryId}`, { headers: { Authorization: `Bearer ${libToken}` } });
      expect(((await lib.json()).data.steps as { name: string }[]).map((s) => s.name)).toEqual(["Translation", proofread]);
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
      if (libraryId) await deleteWorkflowViaAPI(page, libraryId);
      if (proofreadId) await deleteStepDefinitionViaAPI(page, proofreadId);
    }
  });

  test("new project: workflow picker pre-fills the steps and stamps provenance", async ({ page }) => {
    test.setTimeout(30_000);
    const stamp = Date.now();
    let projectId: number | undefined;
    let libraryId: number | undefined;
    let proofreadId: number | undefined;
    const proofread = `Proofread ${stamp}`;
    try {
      proofreadId = await ensureStepDefinitionViaAPI(page, { name: proofread });
      libraryId = await createWorkflowViaAPI(page, `E2E create ${stamp}`, [{ name: proofread }, { name: "Review" }]);
      await page.goto("/projects/new");
      await startFromScratch(page);
      await page.getByPlaceholder(/project name/i).fill(`E2E create wf ${stamp}`);
      await selectLanguage(page, sourceLanguageTrigger(page), "en");
      await selectTargetLanguages(page, page.getByRole("button", { name: /add target languages/i }), ["fr"]);
      // The wizard's Workflow card is the project tab's picker: choosing a
      // workflow pre-fills the steps, shown read-only.
      await pickComboboxOption(page, page.getByTestId("use-workflow-select"), `E2E create ${stamp}`);
      await expect(page.getByTestId("use-workflow-select")).toContainText(`E2E create ${stamp}`);
      await expect(page.getByTestId("workflow-step-badge-0")).toContainText(proofread);

      // The wizard offers library workflows only: no
      // "Custom" entry to fall back to and no step editor on the page.
      await expect(page.getByTestId("workflow-add-step")).toHaveCount(0);
      await expectWorkflowAction(page, "detach", 0);
      await expectWorkflowAction(page, "save-as", 0);
      await page.getByTestId("use-workflow-select").click();
      const wfPopover = page.locator('[data-slot="popover-content"]');
      await expect(wfPopover.getByRole("option", { name: /^Custom$/ })).toHaveCount(0);
      await page.keyboard.press("Escape");
      await expect(wfPopover).toHaveCount(0);

      const { response: res, id: createdId } = await submitProjectWizard(page);
      expect(res.status()).toBe(200);
      projectId = createdId;
      const settings = await getProjectSettingsViaAPI(page, projectId);
      expect((settings.data.workflow_steps as { name: string }[]).map((s) => s.name)).toEqual([proofread, "Review"]);
      expect(settings.data.project.workflow_id).toBe(libraryId);
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
      if (libraryId) await deleteWorkflowViaAPI(page, libraryId);
      if (proofreadId) await deleteStepDefinitionViaAPI(page, proofreadId);
    }
  });

  test("new project: New workflow creates one in the library and uses it right away", async ({ page }) => {
    test.setTimeout(30_000);
    const stamp = Date.now();
    const workflowName = `E2E wizard wf ${stamp}`;
    let projectId: number | undefined;
    let workflowId: number | undefined;
    let proofreadId: number | undefined;
    const proofread = `Proofread ${stamp}`;
    try {
      proofreadId = await ensureStepDefinitionViaAPI(page, { name: proofread });
      await page.goto("/projects/new");
      await startFromScratch(page);
      await page.getByPlaceholder(/project name/i).fill(`E2E wizard wf project ${stamp}`);
      await selectLanguage(page, sourceLanguageTrigger(page), "en");
      await selectTargetLanguages(page, page.getByRole("button", { name: /add target languages/i }), ["fr"]);

      // The dialog opens seeded with the steps on screen; one more goes on top.
      await clickComboboxAction(page, "use-workflow-select", "create");
      const dialog = page.getByTestId("workflow-create-dialog");
      await expect(dialog).toBeVisible();
      await dialog.getByTestId("workflow-add-step").click();
      const newRow = await dialog.getByTestId(/^workflow-step-picker-\d+$/).count();
      await pickComboboxOption(page, dialog.getByTestId(`workflow-step-picker-${newRow - 1}`), proofread);
      await dialog.getByTestId("workflow-create-name").fill(workflowName);
      const [created] = await Promise.all([
        page.waitForResponse((r) => r.url().includes("/api/workflows") && r.request().method() === "POST"),
        dialog.getByTestId("workflow-create-save").click(),
      ]);
      expect(created.status()).toBe(201);
      workflowId = (await created.json()).data.id;
      await expect(dialog).toHaveCount(0);

      // It is the project's workflow straight away, steps and all.
      await expect(page.getByTestId("use-workflow-select")).toContainText(workflowName);
      await expect(page.getByTestId(`workflow-step-badge-${newRow - 1}`)).toContainText(proofread);

      const { response: res, id: createdId } = await submitProjectWizard(page);
      expect(res.status()).toBe(200);
      projectId = createdId;
      const settings = await getProjectSettingsViaAPI(page, projectId);
      expect(settings.data.project.workflow_id).toBe(workflowId);
      const names = (settings.data.workflow_steps as { name: string }[]).map((s) => s.name);
      expect(names[names.length - 1]).toBe(proofread);

      // And it is a library workflow like any other.
      await page.goto("/workflows");
      await expect(page.getByTestId(`workflow-${workflowId}`)).toContainText(workflowName);
    } finally {
      if (projectId) await deleteProjectViaAPI(page, projectId);
      if (workflowId) await deleteWorkflowViaAPI(page, workflowId);
      if (proofreadId) await deleteStepDefinitionViaAPI(page, proofreadId);
    }
  });

  test("template Workflow tab uses a library workflow", async ({ page }) => {
    test.setTimeout(20_000);
    const stamp = Date.now();
    const token = await page.evaluate(() => localStorage.getItem("token"));
    const headers = { Authorization: `Bearer ${token}` };
    let templateId: number | undefined;
    let libraryId: number | undefined;
    let proofreadId: number | undefined;
    const proofread = `Proofread ${stamp}`;
    try {
      proofreadId = await ensureStepDefinitionViaAPI(page, { name: proofread });
      libraryId = await createWorkflowViaAPI(page, `E2E tpl wf ${stamp}`, [{ name: proofread }]);
      const tpl = await page.request.post(`/api/templates`, { headers, data: { name: `E2E tpl ${stamp}` } });
      expect(tpl.status()).toBe(201);
      templateId = (await tpl.json()).data.id;

      await page.goto(`/templates/${templateId}/workflow`);
      await expect(page.getByTestId("workflow-step-badge-0")).toContainText("Translation");
      await pickComboboxOption(page, page.getByTestId("use-workflow-select"), `E2E tpl wf ${stamp}`);
      const [applied] = await Promise.all([
        page.waitForResponse((r) => r.url().includes(`/templates/${templateId}/workflow/apply`) && r.request().method() === "POST"),
        page.getByRole("button", { name: /^use workflow$/i }).click(),
      ]);
      expect(applied.status()).toBe(200);
      await expect(page.getByTestId("use-workflow-select")).toContainText(`E2E tpl wf ${stamp}`);
      await expect(page.getByTestId("workflow-step-badge-0")).toContainText(proofread);
    } finally {
      if (templateId) await page.request.delete(`/api/templates/${templateId}`, { headers });
      if (libraryId) await deleteWorkflowViaAPI(page, libraryId);
      if (proofreadId) await deleteStepDefinitionViaAPI(page, proofreadId);
    }
  });

  // A name is user text: it can be one long unbreakable word. It must stay
  // inside its row instead of pushing the page sideways.
  test("a very long name stays inside its row on both library pages", async ({ page }) => {
    const long = `${UNBREAKABLE_NAME}Workflow${Date.now()}`;
    let workflowId: number | undefined;
    let definitionId: number | undefined;
    try {
      definitionId = await ensureStepDefinitionViaAPI(page, { name: long });
      workflowId = await createWorkflowViaAPI(page, long, [{ name: long }]);

      await page.goto("/workflows");
      await expectNoOverflow(page.getByTestId(`workflow-${workflowId}`));

      await page.goto("/workflows/steps");
      await expectNoOverflow(page.getByTestId(`step-definition-${definitionId}`));
    } finally {
      if (workflowId) await deleteWorkflowViaAPI(page, workflowId);
      if (definitionId) await deleteStepDefinitionViaAPI(page, definitionId);
    }
  });
});

// The Workflow picker (New project page and the project's Workflow tab)
// renders the step list as the option's right-hand "chip". Long step names
// must not overflow the picker row or trigger, and the workflow NAME keeps
// its width.
//
// Asserted by measurement, not by class name: jsdom has no layout engine, so
// this has to live in Playwright. `scrollWidth > clientWidth` is the overflow
// itself, and a label wider than 0 is the name still being readable.
test.describe("Workflow picker with long step names", () => {
  const LONG_STEPS = [
    { name: "Detailed linguistic review by the senior in-country reviewer" },
    { name: "Final client-facing terminology and brand consistency sign-off" },
  ];
  let workflowId: number | undefined;
  const stepIds: number[] = [];
  const wfName = `E2E long steps ${Date.now()}`;

  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    await login(page);
    if (workflowId) await deleteWorkflowViaAPI(page, workflowId);
    for (const id of stepIds) await deleteStepDefinitionViaAPI(page, id);
    await page.close();
  });

  test("neither the option row nor the trigger overflows, and the workflow name stays readable", async ({ page }) => {
    for (const s of LONG_STEPS) stepIds.push(await ensureStepDefinitionViaAPI(page, s));
    workflowId = await createWorkflowViaAPI(page, wfName, LONG_STEPS);

    await page.getByRole("link", { name: /new project/i }).click();
    await startFromScratch(page);

    const trigger = page.getByTestId("use-workflow-select");
    await expect(trigger).toBeVisible();
    await trigger.click();

    // ── The option row ──
    const row = page.locator("[cmdk-item]").filter({ hasText: wfName });
    await expect(row).toHaveCount(1);

    const rowBox = await row.evaluate((el) => ({
      clientWidth: el.clientWidth,
      scrollWidth: el.scrollWidth,
      labelWidth: el.querySelector("span.flex-1")?.getBoundingClientRect().width ?? -1,
    }));
    expect(rowBox.scrollWidth, "option row must not overflow its panel").toBeLessThanOrEqual(rowBox.clientWidth);
    expect(rowBox.labelWidth, "the workflow name must still have width to render").toBeGreaterThan(0);

    // ── The trigger, once the workflow is selected ──
    await row.click();
    await expect(trigger).toContainText(wfName.slice(0, 12));

    const trigBox = await trigger.evaluate((el) => ({
      clientWidth: el.clientWidth,
      scrollWidth: el.scrollWidth,
    }));
    expect(trigBox.scrollWidth, "trigger must not overflow its field").toBeLessThanOrEqual(trigBox.clientWidth);
  });
});
