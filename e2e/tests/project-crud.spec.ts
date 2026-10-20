import { test, expect } from "@playwright/test";
import { backgroundAndBehind, chooseTMFromScratch, deleteDraftFromURL, deleteProjectViaAPI, deleteRowViaHover, login, openTaskSteps, pickComboboxOption, startFromScratch, startFromTemplate, submitProjectWizard } from "./helpers";
import type { Page } from "@playwright/test";

// The target-language chips on the New project form (one remove control per
// chip). Visible only: the combobox keeps a hidden mirror of the chips to
// measure how many fit, which would double every count.
const targetChips = (page: Page) => page.locator('[data-field="target_languages"]').getByTestId(/^lang-remove-/).locator('visible=true');

test.describe("Project CRUD", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  // Typing into the wizard autosaves a real draft row.
  // These tests discard theirs in-test, but a failure between the autosave and
  // the discard would leave an "Untitled draft" behind for the rest of the run.
  test.afterEach(async ({ page }) => {
    await deleteDraftFromURL(page);
  });

  test("project list shows Demo Project with languages", async ({ page }) => {
    await expect(
      page.getByRole("heading", { name: /projects/i })
    ).toBeVisible();
    // Scope to the Demo Project card so the language assertion can't be
    // satisfied (or strict-mode-tripped) by orphan E2E projects from prior
    // runs. Demo Project shows "en → es, fr, de" (3 target langs); a stray
    // project with "en → es" only would have made `getByText(/en → es/)`
    // ambiguous under load.
    const demoCard = page.getByRole("link", { name: /demo project/i });
    await expect(demoCard).toBeVisible();
    await expect(demoCard).toContainText(/en → es/);
  });

  test("can create and delete a project", async ({ page }) => {
    test.setTimeout(20_000); // template loading + project creation under parallel load
    const projectName = `E2E Project ${Date.now()}`;

    await page.getByRole("link", { name: /new project/i }).click();
    await expect(page.getByRole("heading", { name: /new project/i })).toBeVisible();

    // Select a template to auto-fill languages and settings — the form only
    // renders after this choice.
    await startFromTemplate(page, "Default Template");

    const nameInput = page.getByPlaceholder(/project name/i);
    await expect(nameInput).toBeVisible();
    await nameInput.fill(projectName);

    // Wait for template settings to load AND target languages to populate
    await expect(page.getByText(/fuzzy match threshold/i)).toBeVisible({ timeout: 8000 });
    // Target language badges should appear (template has target languages)
    await expect(page.locator('[data-slot="combobox-value-chip"]').first()).toBeVisible({ timeout: 5000 });

    const { response: createResp, id: projectId } = await submitProjectWizard(page);
    try {
      expect(createResp.ok()).toBe(true);

      // Should navigate to project detail page
      await expect(page.getByRole("link", { name: /^files$/i })).toBeVisible({
        timeout: 10000,
      });

      // Go back to project list and verify it's there
      await page.goto("/");
      await expect(page.getByText(projectName)).toBeVisible({
        timeout: 5000,
      });

      await deleteRowViaHover(page, projectName);
    } finally {
      // The UI delete above is the test; this catches a failure before it.
      if (projectId) await deleteProjectViaAPI(page, projectId);
    }
  });

  test("new project page shows the settings only after a Start from choice", async ({ page }) => {
    await page.getByRole("link", { name: /new project/i }).click();
    await expect(page.getByRole("heading", { name: /new project/i })).toBeVisible();

    // Only the two start buttons at first — no form, no settings blocks.
    // Template leads: it is the primary action and renders first.
    const startButtons = page.getByRole("group", { name: "Start from" }).getByRole("button");
    await expect(startButtons).toHaveCount(2);
    await expect(startButtons.nth(0)).toHaveText(/From template/);
    await expect(startButtons.nth(1)).toHaveText(/From scratch/);
    await expect(page.getByTestId("project-create-form")).toHaveCount(0);
    await expect(page.getByPlaceholder(/project name/i)).toHaveCount(0);

    await startFromScratch(page);

    // Should see all settings sections. The wizard's first card carries the
    // project's own fields and no heading — the page title says what it is.
    await expect(page.getByPlaceholder(/project name/i)).toBeVisible();
    await expect(page.getByText("Project settings")).toHaveCount(0);
    await expect(page.getByRole("heading", { name: "Translation memory" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Machine translation" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Terminology" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Quality assurance" })).toBeVisible();
    // The seed has library workflows, so the wizard opens based on one.
    await expect(page.getByTestId("use-workflow-select")).toBeVisible();
  });

  test("the template picker searches on the server", async ({ page }) => {
    await page.goto("/projects/new");
    await page.getByRole("button", { name: "From template" }).click();
    const dialog = page.getByTestId("template-picker-dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("button", { name: "Default Template" })).toBeVisible();

    // A search that matches nothing says so; clearing it brings the list back.
    const search = dialog.getByTestId("template-picker-search");
    const [searchResp] = await Promise.all([
      page.waitForResponse(r => r.url().includes("/api/templates?") && r.url().includes("q=zzz")),
      search.fill("zzz-no-such-template"),
    ]);
    expect(searchResp.ok()).toBe(true);
    await expect(dialog.getByTestId("template-picker-empty")).toBeVisible();

    await search.fill("Default");
    await dialog.getByRole("button", { name: "Default Template" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByTestId("start-from-source")).toContainText("Using template Default Template");
  });

  test("the header stays in view and says when the draft is saved", async ({ page }) => {
    await page.goto("/projects/new");
    await startFromScratch(page);

    const nameInput = page.getByPlaceholder(/project name/i);
    await Promise.all([
      page.waitForResponse(r => r.url().endsWith("/api/projects") && r.request().method() === "POST"),
      (async () => { await nameInput.fill(`E2E sticky ${Date.now()}`); await nameInput.blur(); })(),
    ]);
    const saveState = page.getByTestId("draft-save-state");
    await expect(saveState).toHaveAttribute("data-state", "saved");
    await expect(saveState).toHaveText("Saved");

    // Scrolled to the bottom of a long form, the header is still there.
    await page.getByRole("heading", { name: "Quality assurance" }).scrollIntoViewIfNeeded();
    await expect(page.getByTestId("project-status-badge-draft")).toBeInViewport();
    await expect(page.getByTestId("discard")).toBeInViewport();

    // And it covers the top of the scroll area: a sticky child sticks BELOW its
    // container's top padding, which left a strip of form rows scrolling above
    // the bar.
    const headerBox = (await page.getByTestId("project-create-header").boundingBox())!;
    const mainBox = (await page.locator("main").boundingBox())!;
    expect(Math.abs(headerBox.y - mainBox.y)).toBeLessThanOrEqual(1);

    // It is opaque in the page's own colour, so form rows disappear under it
    // rather than showing through a lighter panel. Compared against the shell
    // that paints the page rather than a hard-coded value: a palette
    // repaints the whole gray scale, and the bar has to follow.
    const shades = await backgroundAndBehind(page.getByTestId("project-create-header"));
    expect(shades.self, "the sticky header must be painted in the page's own background").toBe(shades.behind);

    await page.getByTestId("discard").click();
    await page.getByRole("alertdialog").getByRole("button", { name: "Discard" }).click();
    await expect(page.getByTestId("start-from")).toBeVisible();
  });

  test("a new project asks where the TMs and MT come from before switching them on", async ({ page }) => {
    await page.goto("/projects/new");
    await startFromScratch(page);

    // Nothing chosen: no memory list, and both switches are on but locked.
    await expect(page.getByTestId("tm-assignments")).toHaveCount(0);
    const tmSwitch = page.getByRole("checkbox", { name: /translation memory.*start or upload/i });
    const mtSwitch = page.getByRole("checkbox", { name: /machine translation.*start or upload/i });
    await expect(tmSwitch).toBeChecked();
    await expect(tmSwitch).toBeDisabled();
    await expect(mtSwitch).toBeChecked();
    await expect(mtSwitch).toBeDisabled();

    // "From scratch" reveals the memory list and unlocks its switch.
    await chooseTMFromScratch(page);
    await expect(tmSwitch).toBeEnabled();
    await tmSwitch.uncheck();
    await expect(tmSwitch).not.toBeChecked();

    // An MT router does the same for machine translation.
    await pickComboboxOption(page, page.getByTestId("mt-router-select"), "Default MT router");
    await expect(mtSwitch).toBeEnabled();

    // Both edits above arm the autosave, so this test owns a draft. Wait for
    // the id to reach the URL before cleaning up: deleteDraftFromURL reads it
    // from there and returns silently if the save has not landed yet, which
    // would leave an untitled draft in the list for the rest of the run.
    await expect(page).toHaveURL(/\/projects\/new\?draft=\d+$/);
    await deleteDraftFromURL(page);
  });

  test("the start buttons give way to the source line; Discard brings them back", async ({ page }) => {
    // Two drafts, two discards and the template picker: ~4 s alone, past the
    // default 10 s under the full suite.
    test.setTimeout(30_000);
    await page.getByRole("link", { name: /new project/i }).click();
    await expect(page.getByRole("heading", { name: /new project/i })).toBeVisible();

    // Nothing chosen yet → dismiss the picker → still nothing chosen, form still hidden.
    await page.getByRole("button", { name: "From template" }).click();
    const dialog = page.getByTestId("template-picker-dialog");
    await expect(dialog).toBeVisible();
    await dialog.getByRole("button", { name: "Close" }).click();
    await expect(dialog).toHaveCount(0);
    // Both buttons are back; nothing was chosen.
    await expect(page.getByRole("button", { name: "From template" })).toBeVisible();
    await expect(page.getByRole("button", { name: "From scratch" })).toBeVisible();
    await expect(page.getByTestId("project-create-form")).toHaveCount(0);

    // From scratch: the buttons give way to the form. Discard appears in the
    // header only once a draft exists.
    await startFromScratch(page);
    await expect(page.getByTestId("start-from")).toHaveCount(0);
    // No draft yet → nothing to discard, so no Discard button.
    await expect(page.getByTestId("discard")).toHaveCount(0);
    // Blur forces the autosave, so a draft exists deterministically — without
    // it the next steps race the 800 ms debounce.
    const nameInput = page.getByPlaceholder(/project name/i);
    await Promise.all([
      page.waitForResponse(r => r.url().endsWith("/api/projects") && r.request().method() === "POST"),
      (async () => { await nameInput.fill("E2E start over"); await nameInput.blur(); })(),
    ]);
    // Now it lives in the sticky header, which also carries the Draft chip and
    // the save state beside it.
    const header = page.getByTestId("project-create-header");
    await expect(header.getByTestId("discard")).toBeVisible();
    await expect(header.getByTestId("project-status-badge-draft")).toBeVisible();
    await expect(header.getByTestId("draft-save-state")).toHaveText("Saved");

    // Discard (confirmed): buttons back, form gone; From scratch again shows an empty form.
    await page.getByTestId("discard").click();
    await page.getByRole("alertdialog").getByRole("button", { name: "Discard" }).click();
    await expect(page.getByTestId("start-from")).toBeVisible();
    await expect(page.getByTestId("project-create-form")).toHaveCount(0);
    await startFromScratch(page);
    await expect(page.getByPlaceholder(/project name/i)).toHaveValue("");

    // A template pick names the template on the line. (Getting back to the
    // start buttons needs a draft to discard, so type first.)
    await Promise.all([
      page.waitForResponse(r => r.url().endsWith("/api/projects") && r.request().method() === "POST"),
      (async () => { await nameInput.fill("E2E to template"); await nameInput.blur(); })(),
    ]);
    await page.getByTestId("discard").click();
    await page.getByRole("alertdialog").getByRole("button", { name: "Discard" }).click();
    await startFromTemplate(page, "Default Template");
    await expect(page.getByTestId("start-from-source")).toContainText("Using template Default Template");
    // Selected targets render as chips with a `lang-remove-<code>` control each.
    await expect(targetChips(page)).not.toHaveCount(0, { timeout: 8000 });

    // Discard drops the template: no chips, buttons back. A template prefill
    // saves nothing on its own, so Discard is not offered until an edit lands.
    await expect(page.getByTestId("discard")).toHaveCount(0);
    await Promise.all([
      page.waitForResponse(r => r.url().endsWith("/api/projects") && r.request().method() === "POST"),
      (async () => { await nameInput.fill("E2E from template"); await nameInput.blur(); })(),
    ]);
    await page.getByTestId("discard").click();
    await page.getByRole("alertdialog").getByRole("button", { name: "Discard" }).click();
    await expect(page.getByTestId("start-from")).toBeVisible();
    await expect(page.getByTestId("start-from-source")).toHaveCount(0);
    await startFromScratch(page);
    await expect(targetChips(page)).toHaveCount(0);
  });

  test("Use as template on a project opens a prefilled New project", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await expect(page.getByRole("link", { name: /^files$/i })).toBeVisible({ timeout: 5000 });

    await page.getByRole("button", { name: "Use as template" }).click();
    await expect(page).toHaveURL(/\/projects\/new\?fromProject=\d+$/);

    // The form opens straight away, the source line naming the project.
    await expect(page.getByTestId("start-from")).toHaveCount(0);
    await expect(page.getByTestId("start-from-source")).toContainText("Settings copied from project Demo Project");
    await expect(page.getByTestId("project-create-form")).toBeVisible();
    // Languages and the name come from the project.
    // Selected targets render as chips with a `lang-remove-<code>` control each.
    await expect(targetChips(page)).not.toHaveCount(0, { timeout: 8000 });
    await expect(page.getByPlaceholder(/project name/i)).toHaveValue("Demo Project");

    // The prefill saved nothing, so there is nothing to discard yet.
    await expect(page.getByTestId("discard")).toHaveCount(0);
    // One edit makes the draft; Discard then also clears the deep link, so a
    // reload does not re-apply it.
    const nameInput = page.getByPlaceholder(/project name/i);
    await Promise.all([
      page.waitForResponse(r => r.url().endsWith("/api/projects") && r.request().method() === "POST"),
      // Deliberately NOT a name containing "Demo Project": two tests below pick
      // the project list's first link matching that text, and this draft is a
      // link in the same list while it lives.
      (async () => { await nameInput.fill("E2E use-as-template draft"); await nameInput.blur(); })(),
    ]);
    await page.getByTestId("discard").click();
    await page.getByRole("alertdialog").getByRole("button", { name: "Discard" }).click();
    await expect(page).toHaveURL(/\/projects\/new$/);
    await expect(page.getByTestId("start-from")).toBeVisible();
  });

  test("project detail shows files tab and jobs", async ({ page }) => {
    await page.locator("a").filter({ hasText: "Demo Project" }).first().click();

    await expect(page.getByRole("link", { name: /^files$/i })).toBeVisible({ timeout: 5000 });
    await expect(page.getByRole("link", { name: /analyses/i })).toBeVisible();

    // Tasks: one row per job, by target language. The step sections start closed.
    await openTaskSteps(page);
    for (const lang of ["es", "fr", "de"]) {
      await expect(
        page.locator('[data-testid^="task-row-language-"]').filter({ hasText: new RegExp(`^${lang}$`, "i") }),
      ).not.toHaveCount(0);
    }

    // Files: the file.
    await page.getByRole("link", { name: /^files$/i }).click();
    await expect(page.getByText("demo.txt").first()).toBeVisible({ timeout: 5000 });
  });

  test("processing-time icon opens the timings panel", async ({ page }) => {
    // The trigger is a 28px icon next to the language pair, deliberately NOT a
    // button in the action row (read-only diagnostics shouldn't compete with
    // Upload).
    await page.locator("a").filter({ hasText: "Demo Project" }).first().click();
    await expect(page.getByRole("link", { name: /^files$/i })).toBeVisible({ timeout: 5000 });

    // Icon-only, so the accessible name is the only label a user or a screen
    // reader gets — assert on the role+name, not the testid.
    const trigger = page.getByRole("button", { name: "Processing time" });
    await expect(trigger).toBeVisible();

    // The panel fetches on open (the query is gated on `open`), so await the
    // round trip rather than racing it.
    const timings = page.waitForResponse(
      r => /\/api\/projects\/\d+\/timings/.test(r.url()) && r.status() === 200,
      { timeout: 30_000 },
    );
    await trigger.click();
    await timings;

    const panel = page.getByTestId("processing-time-dialog");
    await expect(panel).toBeVisible();
    // Seed writes timings for the demo jobs, so the populated view renders —
    // an average, never a sum, since jobs run concurrently.
    await expect(panel.getByText("Avg per job")).toBeVisible();
    await expect(panel.getByText("Where the time goes")).toBeVisible();
  });

  test("language combobox does not cause page scroll jump", async ({ page }) => {
    await page.getByRole("link", { name: /new project/i }).click();
    await expect(page.getByRole("heading", { name: /new project/i })).toBeVisible();
    await startFromScratch(page);

    // Scroll down to source language section so there's scroll position to lose
    const sourceLabel = page.getByText("Source language").first();
    await sourceLabel.scrollIntoViewIfNeeded();
    await page.waitForTimeout(200);

    const scrollBefore = await page.evaluate(() => window.scrollY);

    // Click the source language combobox
    const trigger = sourceLabel.locator('..').locator('[data-slot="combobox-trigger"]').first();
    await trigger.click();

    // Wait for popover to render
    await expect(page.getByPlaceholder(/search languages/i)).toBeVisible();
    await page.waitForTimeout(300);

    const scrollAfter = await page.evaluate(() => window.scrollY);

    // Scroll should not have jumped more than 10px
    expect(Math.abs(scrollAfter - scrollBefore)).toBeLessThan(10);
  });

  test("project detail settings tab has no template controls", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();

    // Settings tab should be visible (project as core entity)
    await expect(page.getByRole("link", { name: /settings/i })).toBeVisible();
    await page.getByRole("link", { name: /settings/i }).click();

    // Anchor on a block that IS there, so the negatives below are not
    // trivially green on a blank tab. Templates are create-time-only: no
    // post-create apply, and a template is made from the New project page,
    // not from a project.
    await expect(page.getByText("Project settings")).toBeVisible();
    await expect(page.getByText("Template actions")).toHaveCount(0);
    await expect(page.getByRole("button", { name: /save as template/i })).toHaveCount(0);
    await expect(page.getByText("Apply template:")).toHaveCount(0);
  });

});
