import { test, expect } from "@playwright/test";
import {
  createTMViaAPI,
  deleteDraftFromURL,
  deleteTMViaAPI,
  deleteViaAPI,
  expectNoOverflow,
  getSeedTemplateMTRouter,
  getSeedTMId,
  login,
  pickComboboxValue,
  selectLanguage,
  selectTargetLanguages,
  startFromTemplate,
  submitProjectWizard,
  UNBREAKABLE_NAME,
  systemLabelsViaAPI,
  addLabelValueViaAPI,
  projectLabelsViaAPI,
  pickComboboxOption,
} from "./helpers";

test.describe("Templates", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  // A template name is user text and can be one long unbreakable word. The row
  // has to absorb it rather than push the page sideways.
  test("a long template name stays inside its row", async ({ page }) => {
    const token = await page.evaluate(() => localStorage.getItem("token"));
    const headers = { Authorization: `Bearer ${token}` };
    const name = `${UNBREAKABLE_NAME}Tpl${Date.now()}`;
    let templateId: number | undefined;
    try {
      const res = await page.request.post(`/api/templates`, { headers, data: { name } });
      expect(res.status(), `create template: ${await res.text()}`).toBe(201);
      templateId = (await res.json()).data.id;

      await page.goto("/templates");
      await expectNoOverflow(page.getByTestId(`template-${templateId}`));
    } finally {
      if (templateId) await page.request.delete(`/api/templates/${templateId}`, { headers });
    }
  });

  test("template list shows Default Template", async ({ page }) => {
    await page.getByRole("link", { name: /templates/i }).click();
    await expect(page).toHaveURL(/\/templates/);
    await expect(
      page.getByRole("heading", { name: /templates/i })
    ).toBeVisible();
    await expect(page.getByText(/default template/i)).toBeVisible();
  });

  test("template detail shows TM and MT assignments", async ({ page }) => {
    await page.getByRole("link", { name: /templates/i }).click();
    await page.getByText(/default template/i).click();
    await expect(page).toHaveURL(/\/templates\/\d+/);

    await expect(page.getByRole('checkbox', { name: /machine translation.*start or upload/i })).toBeVisible();
    await expect(page.getByText(/fuzzy.*threshold/i)).toBeVisible();
    await expect(page.getByText(/translation memory/i).first()).toBeVisible();
    await expect(page.getByText(/machine translation/i).first()).toBeVisible();

    // Assert on the seeded resource NAMES, not a count of controls: the
    // seeded template assigns Default TM (one row per TM,
    // TMAssignmentSection) and the seeded MT router
    // (cmd/seed/main.go::createTemplateWithAssignments).
    await expect(
      page.locator('[data-testid^="tm-assignment-row-"]').filter({ hasText: /default tm/i }).first()
    ).toBeVisible({ timeout: 5000 });
    // MT is one router per template: the Machine
    // translation card shows the seeded router as the picked value.
    const seedRouter = await getSeedTemplateMTRouter(page);
    await expect(page.getByTestId("mt-section").getByTestId("mt-router-select")).toContainText(seedRouter.name);
  });

  test("template CRUD: create and delete", async ({ page }) => {
    await page.getByRole("link", { name: /templates/i }).click();

    const templateName = `E2E CRUD Template ${Date.now()}`;
    let templateId: number | undefined;
    try {
      await page.getByRole("button", { name: /new template/i }).click();
      const nameInput = page.getByPlaceholder(/template name/i);
      await expect(nameInput).toBeVisible();
      await nameInput.fill(templateName);
      const [createRes] = await Promise.all([
        page.waitForResponse(r => /\/api\/templates\/?$/.test(r.url()) && r.request().method() === "POST"),
        page.getByRole("button", { name: /^create$/i }).click(),
      ]);
      expect(createRes.status(), `create template: ${await createRes.text()}`).toBe(201);
      templateId = (await createRes.json()).data.id as number;

      // Create stays on the list and the new row appears in it.
      await expect(page).toHaveURL(/\/templates$/);
      const row = page.getByTestId(`template-${templateId}`);
      await expect(row).toContainText(templateName);

      await row.hover();
      await row.getByRole("button", { name: "Delete", exact: true }).click();
      await Promise.all([
        page.waitForResponse(r => r.url().endsWith(`/api/templates/${templateId}`) && r.request().method() === "DELETE"),
        page.getByRole("alertdialog").getByRole("button", { name: /delete/i }).click(),
      ]);
      await expect(row).toHaveCount(0);
    } finally {
      // 404 (already gone) when the UI delete above succeeded; removes the template when it did not.
      if (templateId) await deleteViaAPI(page, `/api/templates/${templateId}`, `template ${templateId}`);
    }
  });

  test("template customer + domain: edited on the template page, pre-filled on New project", async ({ page }) => {
    test.setTimeout(30_000);
    const stamp = Date.now();
    const templateName = `E2E Meta Template ${stamp}`;
    let templateId: number | undefined;
    let projectId: number | undefined;
    const token = await page.evaluate(() => localStorage.getItem("token"));
    const headers = { Authorization: `Bearer ${token}` };
    try {
      // Customer and Domain are built-in labels, so the template presets them
      // as label picks rather than columns of its own.
      const { customer: customerLabelId, domain: domainLabelId } = await systemLabelsViaAPI(page);
      const customerValueId = await addLabelValueViaAPI(page, customerLabelId, `E2E Meta Customer ${stamp}`);
      const domainValueId = await addLabelValueViaAPI(page, domainLabelId, "Patents");
      const createResp = await page.request.post("/api/templates", {
        headers, data: { name: templateName },
      });
      expect(createResp.ok()).toBe(true);
      templateId = (await createResp.json()).data.id;

      // Preset the template's labels, then check the page shows them.
      const setLabels = await page.request.put(`/api/templates/${templateId}/labels`, {
        headers,
        data: { labels: [
          { label_definition_id: customerLabelId, label_value_id: customerValueId },
          { label_definition_id: domainLabelId, label_value_id: domainValueId },
        ] },
      });
      expect(setLabels.ok()).toBe(true);

      await page.goto(`/templates/${templateId}`);
      const form = page.getByTestId("template-metadata-form");
      await expect(form).toContainText(`E2E Meta Customer ${stamp}`);
      await expect(form).toContainText("Patents");
      // A new template's project name starts as the template's name. Each
      // field saves on its own: text on blur, pickers on pick — one PATCH per change.
      const patch = () => page.waitForResponse(r => r.url().includes(`/api/templates/${templateId}`) && r.request().method() === "PATCH");
      const projectName = page.getByTestId("template-project-name-edit");
      await expect(projectName).toHaveValue(templateName);
      await projectName.fill(`E2E Meta Project ${stamp}`);
      let [resp] = await Promise.all([patch(), projectName.blur()]);
      expect((await resp.json()).data.project_name).toBe(`E2E Meta Project ${stamp}`);
      [resp] = await Promise.all([patch(), selectLanguage(page, form.locator('[data-field="source_language"]').getByTestId("lang"), "en")]);
      expect((await resp.json()).data.source_language).toBe("en");
      [resp] = await Promise.all([patch(), selectTargetLanguages(page, form.getByRole("button", { name: /add target languages/i }), ["fr"])]);
      const saved = (await resp.json()).data;
      expect(saved.target_languages).toEqual(["fr"]);
      expect(saved.project_name).toBe(`E2E Meta Project ${stamp}`);

      // New project from this template: project name and the label presets
      // arrive with it.
      await page.goto("/projects/new");
      await startFromTemplate(page, templateName);
      await expect(page.getByPlaceholder(/project name/i)).toHaveValue(`E2E Meta Project ${stamp}`);
      await expect(page.getByTestId(`project-label-value-${domainLabelId}`)).toContainText("Patents");
      await expect(page.getByTestId(`project-label-value-${customerLabelId}`)).toContainText(`E2E Meta Customer ${stamp}`);

      // Languages come from the template too, so the form is complete as-is.
      // Visible only: the combobox keeps a hidden measuring mirror of the chips.
      await expect(page.locator('[data-field="target_languages"]').getByTestId(/^lang-remove-/).locator('visible=true')).toHaveCount(1);
      const { response: publishResp, id } = await submitProjectWizard(page);
      expect(publishResp.ok()).toBe(true);
      const created = (await publishResp.json()).data;
      projectId = id;
      // The published row, not the draft the autosave made on the way — a
      // publish that failed would leave the project in 'draft'.
      expect(created.status).toBe("in_progress");
      expect(created.name).toBe(`E2E Meta Project ${stamp}`);
      expect(await projectLabelsViaAPI(page, projectId!)).toMatchObject({
        Domain: "Patents",
        Customer: `E2E Meta Customer ${stamp}`,
      });
      expect(created.source_language).toBe("en");
      expect(created.target_languages).toEqual(["fr"]);
    } finally {
      if (projectId) await page.request.delete(`/api/projects/${projectId}`, { headers });
      if (templateId) await page.request.delete(`/api/templates/${templateId}`, { headers });
    }
  });

  test("Save as template on the New project page saves the whole form", async ({ page }) => {
    test.setTimeout(30_000);
    const stamp = Date.now();
    const token = await page.evaluate(() => localStorage.getItem("token"));
    const headers = { Authorization: `Bearer ${token}` };
    let templateId: number | undefined;
    // The wizard autosaves a draft while the form is filled.
    let draftId: string | null = null;
    try {
      // Start from the seed template so the form carries assignments, then
      // change what the template must pick up.
      // The value has to exist before the form loads, or its picker will not
      // offer it — the labels are fetched once on mount.
      const { domain: domainLabel } = await systemLabelsViaAPI(page);
      // Stamped: label values are unique case-insensitively and are never
      // cleaned up, so a fixed "Legal" would resolve to whatever an earlier
      // spec created and assert on its spelling, not this test's.
      const domainValue = `Legal ${stamp}`;
      await addLabelValueViaAPI(page, domainLabel, domainValue);

      await page.goto("/projects/new");
      await startFromTemplate(page, "Default Template");
      // Applying a template writes its own name into the form, and that write
      // can land after the banner this helper waits on. Typing before it
      // arrives loses the name to the template's, so wait for the value first.
      const nameField = page.getByPlaceholder(/project name/i);
      await expect(nameField).toHaveValue("Default Template");
      await nameField.fill(`E2E Saved Form ${stamp}`);
      await pickComboboxOption(page, page.getByTestId(`project-label-value-${domainLabel}`), domainValue);
      const fuzzy = page.getByRole("spinbutton", { name: /fuzzy match threshold/i });
      await fuzzy.fill("62");
      await fuzzy.blur();

      await page.getByTestId("save-as-template").click();
      const dialog = page.getByTestId("save-as-template-dialog");
      await expect(dialog).toBeVisible();
      // The template name starts as the project name.
      await expect(page.getByTestId("save-as-template-name")).toHaveValue(`E2E Saved Form ${stamp}`);
      await page.getByTestId("save-as-template-name").fill(`E2E Saved Template ${stamp}`);
      const [resp] = await Promise.all([
        page.waitForResponse(r => r.url().endsWith("/api/templates") && r.request().method() === "POST"),
        page.getByTestId("save-as-template-confirm").click(),
      ]);
      expect(resp.ok()).toBe(true);
      const created = (await resp.json()).data;
      templateId = created.id;
      expect(created.name).toBe(`E2E Saved Template ${stamp}`);
      expect(created.project_name).toBe(`E2E Saved Form ${stamp}`);
      expect(created.source_language).toBe("en");
      expect(created.target_languages).toEqual(["es", "fr", "de"]);
      expect(created.fuzzy_threshold).toBeCloseTo(0.62, 5);
      await expect(dialog).toHaveCount(0);
      // The page stays and nothing is published: the form's own draft is still
      // a draft, and the form still holds what was typed.
      await expect(page).toHaveURL(/\/projects\/new\?draft=\d+$/);
      await expect(page.getByPlaceholder(/project name/i)).toHaveValue(`E2E Saved Form ${stamp}`);
      draftId = new URL(page.url()).searchParams.get("draft");
      expect(draftId, "the form was typed into, so the wizard has autosaved a draft").toBeTruthy();
      const proj = await page.request.get(`/api/projects/${draftId}`, { headers });
      expect((await proj.json()).data.status).toBe("draft");

      // The form's label picks travelled onto the template too — they are saved
      // against it after the create, not carried in its payload.
      const tmplLabels = await page.request.get(`/api/templates/${templateId}/labels`);
      expect(tmplLabels.ok()).toBe(true);
      const labelRows = (await tmplLabels.json()).data as { label_name: string; label_value: string }[];
      expect(Object.fromEntries(labelRows.map(r => [r.label_name, r.label_value]))).toMatchObject({ Domain: domainValue });

      // The assignments and the MT router travelled with the settings.
      const detail = await page.request.get(`/api/templates/${templateId}`, { headers });
      const body = (await detail.json()).data;
      expect(body.tm_assignments.length).toBeGreaterThanOrEqual(1);
      const seedRouter = await getSeedTemplateMTRouter(page);
      expect(body.template.mt_router_id).toBe(seedRouter.id);
      expect(body.template.mt_router_name).toBe(seedRouter.name);
    } finally {
      if (templateId) await page.request.delete(`/api/templates/${templateId}`, { headers });
      // Reads ?draft= off the live URL, so it cleans up however far the test got.
      await deleteDraftFromURL(page);
    }
  });

  test("the completion switch toggles and persists on a template", async ({ page }) => {
    const stamp = Date.now();
    const token = await page.evaluate(() => localStorage.getItem("token"));
    const headers = { Authorization: `Bearer ${token}` };
    let templateId: number | undefined;
    try {
      const createResp = await page.request.post("/api/templates", { headers, data: { name: `E2E AutoComplete ${stamp}` } });
      expect(createResp.ok()).toBe(true);
      templateId = (await createResp.json()).data.id;

      await page.goto(`/templates/${templateId}`);
      const box = page.getByTestId("settings-auto-complete");
      await expect(box).toBeChecked();
      const [patchResp] = await Promise.all([
        page.waitForResponse(r => r.url().includes(`/api/templates/${templateId}`) && r.request().method() === "PATCH"),
        box.click(),
      ]);
      expect(patchResp.ok()).toBe(true);
      expect((await patchResp.json()).data.auto_complete, "the PATCH must carry the new value").toBe(false);
      await expect(box).not.toBeChecked();

      // Persisted: a reload shows it off, and the API agrees.
      await page.reload();
      await expect(page.getByTestId("settings-auto-complete")).not.toBeChecked();
      const detail = await page.request.get(`/api/templates/${templateId}`, { headers });
      expect((await detail.json()).data.template.auto_complete).toBe(false);
    } finally {
      if (templateId) await page.request.delete(`/api/templates/${templateId}`, { headers });
    }
  });

  test.describe("Strings contain HTML", () => {
    let templateId: number | undefined;
    test.afterEach(async ({ page }) => {
      if (!templateId) return;
      const token = await page.evaluate(() => localStorage.getItem("token"));
      await page.request.delete(`/api/templates/${templateId}`, { headers: { Authorization: `Bearer ${token}` } });
      templateId = undefined;
    });

    test("the switch toggles and persists on a template", async ({ page }) => {
      const token = await page.evaluate(() => localStorage.getItem("token"));
      const headers = { Authorization: `Bearer ${token}` };
      const createResp = await page.request.post("/api/templates", { headers, data: { name: `E2E InlineHTML ${Date.now()}` } });
      expect(createResp.ok()).toBe(true);
      templateId = (await createResp.json()).data.id;

      await page.goto(`/templates/${templateId}`);
      const box = page.getByTestId("settings-inline-html");
      await expect(box).toBeChecked();
      const [patchResp] = await Promise.all([
        page.waitForResponse(r => r.url().includes(`/api/templates/${templateId}`) && r.request().method() === "PATCH"),
        box.click(),
      ]);
      expect(patchResp.ok()).toBe(true);
      expect((await patchResp.json()).data.inline_html, "the PATCH must carry the new value").toBe(false);

      await page.reload();
      await expect(page.getByTestId("settings-inline-html")).not.toBeChecked();
      const detail = await page.request.get(`/api/templates/${templateId}`, { headers });
      expect((await detail.json()).data.template.inline_html).toBe(false);
    });
  });

  test("the TM lookup order toggle saves and persists on a template", async ({ page }) => {
    const stamp = Date.now();
    const token = await page.evaluate(() => localStorage.getItem("token"));
    const headers = { Authorization: `Bearer ${token}` };
    let templateId: number | undefined;
    const tmIds: number[] = [];
    try {
      tmIds.push(await createTMViaAPI(page, `E2E template order TM A ${stamp}`));
      tmIds.push(await createTMViaAPI(page, `E2E template order TM B ${stamp}`));
      const createResp = await page.request.post("/api/templates", { headers, data: {
        name: `E2E LookupOrder ${stamp}`,
        tm_assignments: tmIds.map((tm_id, priority) => ({ tm_id, mode: "read_write", priority })),
      } });
      expect(createResp.ok()).toBe(true);
      templateId = (await createResp.json()).data.id;

      await page.goto(`/templates/${templateId}`);
      const orderSelect = page.getByTestId("tm-lookup-order-select");
      await expect(orderSelect).toContainText("By match percentage");
      const [patchResp] = await Promise.all([
        page.waitForResponse(r => r.url().includes(`/api/templates/${templateId}`) && r.request().method() === "PATCH"),
        pickComboboxValue(page, "tm-lookup-order-select", "tm_order"),
      ]);
      expect(patchResp.ok()).toBe(true);
      expect((await patchResp.json()).data.tm_lookup_order, "the PATCH must carry the new value").toBe("tm_order");
      await expect(orderSelect).toContainText("By TM order");

      await page.reload();
      await expect(page.getByTestId("tm-lookup-order-select")).toContainText("By TM order");
      const detail = await page.request.get(`/api/templates/${templateId}`, { headers });
      expect((await detail.json()).data.template.tm_lookup_order).toBe("tm_order");
    } finally {
      if (templateId) await page.request.delete(`/api/templates/${templateId}`, { headers });
      for (const tmId of tmIds) await deleteTMViaAPI(page, tmId);
    }
  });

  test("duplicate template deep-copies settings + assignments", async ({ page }) => {
    // Create source + assignments, duplicate, verify the copy, then delete both
    // — a dozen sequential round trips against one 10s budget. Under 8 workers
    // it ran out mid-`finally`, which fails the test AND leaks the two template
    // rows it was midway through deleting (an E2E test that creates side-effect
    // entities leaks rows across runs).
    test.setTimeout(30_000);

    // Self-contained: duplicating the shared "Default Template" would drop a
    // "Copy of Default Template" row that breaks parallel tests matching
    // /default template/i (strict-mode) and makes the row locator ambiguous
    // on reruns. Own source template + cleanup of both instead.
    const sourceName = `E2E DupTmpl ${Date.now()}`;
    let sourceId: number | undefined;
    let copyId: number | undefined;
    try {
      await page.getByRole("link", { name: /templates/i }).click();
      const token = await page.evaluate(() => localStorage.getItem("token"));
      const headers = { Authorization: `Bearer ${token}` };

      const createResp = await page.request.post("/api/templates", {
        headers, data: { name: sourceName, mt_on_upload: true, fuzzy_threshold: 0.8 },
      });
      expect(createResp.ok()).toBe(true);
      sourceId = (await createResp.json()).data.id;
      const tmId = await getSeedTMId(page);
      const tmResp = await page.request.put(`/api/templates/${sourceId}/tms`, {
        headers, data: { assignments: [{ tm_id: tmId, mode: "read_write", priority: 1 }] },
      });
      expect(tmResp.ok()).toBe(true);

      // Duplicate from the row action (reload so the API-created row shows).
      await page.reload();
      const row = page.locator('a[href^="/templates/"]').filter({ hasText: sourceName });
      await expect(row).toBeVisible();
      const [dupResp] = await Promise.all([
        page.waitForResponse(
          resp => resp.url().includes("/duplicate") && resp.request().method() === "POST",
          { timeout: 10000 },
        ),
        row.getByRole("button", { name: /duplicate/i }).click(),
      ]);
      expect(dupResp.ok()).toBe(true);
      copyId = (await dupResp.json()).data.id;

      // Navigates to the copy's detail page.
      await expect(page).toHaveURL(new RegExp(`/templates/${copyId}$`), { timeout: 5000 });
      await expect(page.getByText(`Copy of ${sourceName}`).first()).toBeVisible();

      // The copy carries the source's scalars + assignments (deep copy, not
      // an empty shell).
      await expect(
        page.getByTestId(`tm-assignment-row-${tmId}`)
      ).toBeVisible({ timeout: 5000 });
      const copyDetail = await page.request.get(`/api/templates/${copyId}`, { headers });
      const copyData = (await copyDetail.json()).data;
      expect(copyData.template.fuzzy_threshold).toBeCloseTo(0.8, 5);
      expect(copyData.template.mt_on_upload).toBe(true);
      expect(copyData.tm_assignments.length).toBe(1);
    } finally {
      const token = await page.evaluate(() => localStorage.getItem("token"));
      const headers = { Authorization: `Bearer ${token}` };
      if (copyId) await page.request.delete(`/api/templates/${copyId}`, { headers }).catch(() => {});
      if (sourceId) await page.request.delete(`/api/templates/${sourceId}`, { headers }).catch(() => {});
    }
  });
});
