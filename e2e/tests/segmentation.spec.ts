import { test, expect, type Page } from "@playwright/test";
import {
  authHeaders, deleteDraftFromURL, deleteViaAPI, getProjectSettingsViaAPI, login, pickComboboxValue, pickProjectWorkflow,
  selectLanguage, selectTargetLanguages, sourceLanguageTrigger, standardWorkflowId, startFromScratch, submitProjectWizard, uploadFileViaAPI,
} from "./helpers";

/**
 * Segmentation profiles: the page, and the pick every project makes. The
 * profiles made here are Icelandic, which no built-in covers, so a project in
 * any other language never lists them.
 */
const LANG = "is";
const SAMPLE = "Fundur kl. Tvö byrjar. Svo.";

interface Profile { id: number; name: string; builtin_key?: string; suggested?: boolean }

/** Picks options of a multi-select language field; the list stays open between picks. */
async function pickLanguages(page: Page, testId: string, picks: [search: string, code: string][]) {
  const popover = page.locator('[data-slot="popover-content"]');
  await expect(popover).toHaveCount(0, { timeout: 3000 });
  await page.getByTestId(testId).click();
  await expect(popover).toHaveCount(1, { timeout: 3000 });
  for (const [search, code] of picks) {
    await popover.getByPlaceholder(/search languages/i).fill(search);
    const option = popover.getByTestId(`${testId}-option-${code}`);
    await option.click();
    await expect(option).toHaveAttribute("data-checked", "true");
  }
  await page.keyboard.press("Escape");
  await expect(popover).toHaveCount(0, { timeout: 3000 });
}

async function segmentsOf(page: Page, fileId: number): Promise<string[]> {
  const res = await page.request.get(`/api/files/${fileId}/segments`, { headers: await authHeaders(page) });
  expect(res.ok(), `list segments: ${res.status()}`).toBe(true);
  return ((await res.json()).data as { source_text: string }[]).map(s => s.source_text);
}

/** The English built-in. */
async function builtinEnglish(page: Page): Promise<Profile> {
  return (await profilesFor(page, "en")).find(p => p.builtin_key === "builtin:en@1")!;
}

/** A card of the profile page, by its title. */
function card(page: Page, name: string) {
  return page.locator("section").filter({ has: page.getByRole("heading", { name, exact: true }) });
}

async function profilesFor(page: Page, lang: string): Promise<Profile[]> {
  const res = await page.request.get(`/api/segmentation-profiles?source_language=${lang}`, { headers: await authHeaders(page) });
  expect(res.ok(), `list profiles: ${res.status()}`).toBe(true);
  return (await res.json()).data;
}

test.describe("Segmentation profiles", () => {
  const cleanup: string[] = [];

  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test.afterEach(async ({ page }) => {
    await deleteDraftFromURL(page).catch(() => {});
    for (const path of cleanup.splice(0)) {
      await deleteViaAPI(page, path, path).catch(() => {});
    }
  });

  test("a copied profile with a new no-break rule splits the uploads of a project that names it", async ({ page }) => {
    test.setTimeout(45_000);
    const name = `E2E Icelandic ${Date.now()}`;

    // File configurations in the left menu opens its Segmentation profiles tab.
    await page.goto("/projects");
    await page.getByTestId("nav-rail").getByRole("link", { name: "File configurations" }).click();
    await expect(page).toHaveURL(/\/file-configurations\/segmentation-profiles$/);
    await expect(page.getByRole("heading", { name: "File configurations" })).toBeVisible();
    await expect(page.getByRole("navigation", { name: "Section" }).getByRole("link", { name: "Segmentation profiles" })).toHaveAttribute("aria-current", "page");
    const anyLanguage = page.getByRole("row").filter({ hasText: "Default (built-in)" });
    await anyLanguage.getByRole("button", { name: "Copy" }).click();
    await expect(page).toHaveURL(/\/file-configurations\/segmentation-profiles\/new\?from=\d+/);

    await page.getByTestId("segmentation-name").fill(name);
    await pickLanguages(page, "segmentation-languages", [["Icelandic", LANG]]);
    await page.getByTestId("segmentation-preview-text").fill(SAMPLE);
    const segments = page.getByTestId("segmentation-preview-segments").locator("li");
    await expect(segments).toHaveCount(3);

    // A no-break rule for "kl." added at the end, then dragged to the top:
    // below the break rule it would never be reached.
    const rules = page.getByTestId("segmentation-rules");
    const last = await rules.locator('[data-testid^="segmentation-rule-before-"]').count();
    await page.getByTestId("segmentation-rule-add").click();
    await page.getByTestId(`segmentation-rule-before-${last}`).fill("kl\\.");
    await page.getByTestId(`segmentation-rule-after-${last}`).fill("\\s");
    await page.getByTestId(`segmentation-rule-action-${last}-no-break`).click();
    await expect(segments).toHaveCount(3);
    // dnd-kit's pointer sensor needs real pointer travel: cross its 4px
    // activation distance, wait for the row to be dragging, then land its
    // centre on the first row's centre (closestCenter collision).
    const moved = page.getByTestId(`segmentation-rule-row-${last}`);
    const first = page.getByTestId("segmentation-rule-row-0");
    await moved.scrollIntoViewIfNeeded();
    const hb = (await page.getByTestId(`segmentation-rule-drag-${last}`).boundingBox())!;
    const target = (await first.boundingBox())!;
    const x = hb.x + hb.width / 2;
    const startY = hb.y + hb.height / 2;
    await page.mouse.move(x, startY);
    await page.mouse.down();
    await page.mouse.move(x, startY - 6);
    await expect(moved).toHaveAttribute("data-dragging", "true");
    const targetY = target.y + target.height / 2;
    for (let i = 1; i <= 8; i++) await page.mouse.move(x, startY - 6 + ((targetY - startY + 6) * i) / 8);
    await page.mouse.up();
    await expect(page.getByTestId("segmentation-rule-before-0")).toHaveValue("kl\\.");
    await expect(segments).toHaveCount(2);
    await expect(segments.first()).toContainText("Fundur kl. Tvö byrjar.");

    const created = page.waitForResponse(r => r.request().method() === "POST" && r.url().endsWith("/api/segmentation-profiles"));
    await page.getByTestId("segmentation-save").click();
    const profile = (await (await created).json()).data as Profile;
    cleanup.push(`/api/segmentation-profiles/${profile.id}`);
    // Create goes back to the list, with the new profile in it.
    await expect(page).toHaveURL(/\/file-configurations\/segmentation-profiles$/);
    await expect(page.getByTestId(`segmentation-row-${profile.id}`)).toBeVisible();

    const projectRes = await page.request.post("/api/projects", {
      headers: await authHeaders(page),
      data: {
        name: `E2E segmentation ${Date.now()}`, source_language: LANG, target_languages: ["en"],
        workflow_id: await standardWorkflowId(page), settings: { segmentation_profile_id: profile.id },
      },
    });
    expect(projectRes.ok(), `create project: ${projectRes.status()}`).toBe(true);
    const projectId = (await projectRes.json()).data.id as number;
    cleanup.unshift(`/api/projects/${projectId}`);
    const file = await uploadFileViaAPI(page, projectId, "sample.txt", `${SAMPLE}\n`);
    expect(await segmentsOf(page, file.data.id)).toEqual(["Fundur kl. Tvö byrjar.", "Svo."]);
  });

  test("an SRX import lists what it left out and saves the drafts", async ({ page }) => {
    await page.goto("/file-configurations/segmentation-profiles");
    const parsed = page.waitForResponse(r => r.url().endsWith("/api/segmentation-profiles/srx/parse") && r.ok());
    await page.getByTestId("srx-file-input").setInputFiles("fixtures/segmentation-cascade.srx");
    await parsed;

    const dialog = page.getByTestId("srx-import-dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText('segmentsubflows="no" is ignored.');
    await expect(dialog.getByTestId("srx-draft-warnings-0")).toContainText("look-behind is not supported");

    // Keep the English draft only, under a unique name.
    const stamp = Date.now();
    await dialog.getByTestId("srx-draft-name-0").fill(`E2E SRX English ${stamp}`);
    await dialog.getByTestId("srx-draft-keep-1").uncheck();
    await dialog.getByTestId("srx-draft-keep-2").uncheck();
    const saved = page.waitForResponse(r => r.request().method() === "POST" && r.url().endsWith("/api/segmentation-profiles"));
    await dialog.getByTestId("srx-import-save").click();
    const profile = (await (await saved).json()).data as Profile & { source_languages: string[] };
    cleanup.push(`/api/segmentation-profiles/${profile.id}`);
    expect(profile.source_languages).toEqual(["en"]);
    await expect(dialog).toBeHidden();
    await expect(page.getByTestId(`segmentation-row-${profile.id}`)).toContainText(`E2E SRX English ${stamp}`);
  });

  test("the New project form offers the profiles that suit the source language and saves the pick", async ({ page }) => {
    test.setTimeout(40_000);
    const stamp = Date.now();
    const builtins = await profilesFor(page, LANG);
    const anyLanguage = builtins.find(p => p.builtin_key?.startsWith("builtin:default@"))!;
    const conservative = builtins.find(p => p.builtin_key?.startsWith("builtin:conservative@"))!;
    expect(anyLanguage.suggested).toBe(true);

    const profileRes = await page.request.post("/api/segmentation-profiles", {
      headers: await authHeaders(page),
      data: { name: `E2E Icelandic pick ${stamp}`, source_languages: [LANG], from: anyLanguage.id },
    });
    expect(profileRes.ok(), `create profile: ${profileRes.status()}`).toBe(true);
    const profile = (await profileRes.json()).data as Profile;
    cleanup.push(`/api/segmentation-profiles/${profile.id}`);

    await page.goto("/projects/new");
    await startFromScratch(page);
    await page.getByPlaceholder(/project name/i).fill(`E2E segmentation pick ${stamp}`);
    await selectLanguage(page, sourceLanguageTrigger(page), LANG);
    await selectTargetLanguages(page, page.getByRole("button", { name: /add target languages/i }), ["en"]);

    // The suggested profile is preselected for the new language.
    const picker = page.getByTestId("segmentation-profile-select");
    await expect(picker).toHaveText(`${anyLanguage.name} (built-in)`);

    const popover = page.locator('[data-slot="popover-content"]');
    await picker.click();
    await expect(popover).toHaveCount(1, { timeout: 3000 });
    for (const p of [anyLanguage, conservative, profile]) {
      await expect(popover.getByTestId(`segmentation-profile-select-option-${p.id}`)).toBeVisible();
    }
    // A built-in for another language is not offered.
    await expect(popover.getByRole("option", { name: "German (built-in)" })).toHaveCount(0);
    await page.keyboard.press("Escape");
    await expect(popover).toHaveCount(0, { timeout: 3000 });

    await pickComboboxValue(page, "segmentation-profile-select", String(profile.id));
    await expect(picker).toHaveText(profile.name);
    await pickProjectWorkflow(page);

    const { response, id } = await submitProjectWizard(page);
    expect(response.ok(), `publish: ${response.status()}`).toBe(true);
    cleanup.unshift(`/api/projects/${id}`);
    const settings = await getProjectSettingsViaAPI(page, id);
    expect(settings.data.project.segmentation_profile_id).toBe(profile.id);
  });
  test("a template without a language that keeps Default gives a German project the German built-in", async ({ page }) => {
    test.setTimeout(40_000);
    const stamp = Date.now();
    const german = (await profilesFor(page, "de")).find(p => p.suggested)!;
    const templateRes = await page.request.post("/api/templates", {
      headers: await authHeaders(page),
      data: { name: `E2E no-language template ${stamp}` },
    });
    expect(templateRes.ok(), `create template: ${templateRes.status()}`).toBe(true);
    const templateId = (await templateRes.json()).data.id as number;
    cleanup.push(`/api/templates/${templateId}`);

    await page.goto(`/projects/new?fromTemplate=${templateId}`);
    await expect(page.getByTestId("project-create-form")).toBeVisible();
    await page.getByPlaceholder(/project name/i).fill(`E2E template no language ${stamp}`);
    await selectLanguage(page, sourceLanguageTrigger(page), "de");
    await selectTargetLanguages(page, page.getByRole("button", { name: /add target languages/i }), ["en"]);
    await expect(page.getByTestId("segmentation-profile-select")).toHaveText(`${german.name} (built-in)`);
    // The template has no workflow, so the project needs one picked.
    await pickProjectWorkflow(page);

    const { response, id } = await submitProjectWizard(page);
    expect(response.ok(), `publish: ${response.status()}`).toBe(true);
    cleanup.unshift(`/api/projects/${id}`);
    const settings = await getProjectSettingsViaAPI(page, id);
    expect(settings.data.project.segmentation_profile_id).toBe(german.id);
  });
  test("the profile editor keeps room for abbreviations and patterns on a laptop-size window", async ({ page }) => {
    const english = await builtinEnglish(page);
    for (const width of [1024, 1280, 1440]) {
      await page.setViewportSize({ width, height: 800 });
      await page.goto(`/file-configurations/segmentation-profiles/${english.id}`);
      const abbreviations = await page.getByTestId("segmentation-abbreviations").boundingBox();
      const pattern = await page.getByTestId("segmentation-rule-before-0").boundingBox();
      expect(abbreviations!.width, `abbreviation box at ${width}px`).toBeGreaterThanOrEqual(480);
      expect(pattern!.width, `rule pattern field at ${width}px`).toBeGreaterThanOrEqual(160);
      // One column: the Preview sits between the Rules and the Abbreviations.
      const rules = await page.getByTestId("segmentation-rules").boundingBox();
      const preview = await page.getByRole("heading", { name: "Preview" }).boundingBox();
      expect(preview!.y, `Preview below the Rules at ${width}px`).toBeGreaterThan(rules!.y + rules!.height);
      expect(preview!.y, `Preview above the Abbreviations at ${width}px`).toBeLessThan(abbreviations!.y);
    }
    // A wide screen puts the Preview beside the editor.
    await page.setViewportSize({ width: 1600, height: 900 });
    await page.goto(`/file-configurations/segmentation-profiles/${english.id}`);
    const rules = await page.getByTestId("segmentation-rules").boundingBox();
    const preview = await page.getByRole("heading", { name: "Preview" }).boundingBox();
    expect(preview!.x).toBeGreaterThan(rules!.x + rules!.width);
    expect((await page.getByTestId("segmentation-rule-before-0").boundingBox())!.width).toBeGreaterThanOrEqual(160);
  });
  test("a long preview on a wide screen leaves the editor cards packed", async ({ page }) => {
    const english = await builtinEnglish(page);
    await page.setViewportSize({ width: 1600, height: 900 });
    await page.goto(`/file-configurations/segmentation-profiles/${english.id}`);
    const sample = Array.from({ length: 80 }, (_, i) => `Sentence number ${i + 1} is here.`).join(" ");
    await page.getByTestId("segmentation-preview-text").fill(sample);
    await expect(page.getByText("80 segments")).toBeVisible();
    const preview = (await card(page, "Preview").boundingBox())!;
    const settings = (await card(page, "Settings").boundingBox())!;
    const rules = (await card(page, "Rules").boundingBox())!;
    const abbreviations = (await card(page, "Abbreviations").boundingBox())!;
    expect(preview.height, "the sample makes the Preview the taller column").toBeGreaterThan(abbreviations.y + abbreviations.height - settings.y);
    expect(rules.y - (settings.y + settings.height)).toBeLessThanOrEqual(25);
    expect(abbreviations.y - (rules.y + rules.height)).toBeLessThanOrEqual(25);
  });
  test("the New profile page shows its errors only when Create is clicked", async ({ page }) => {
    let posted = 0;
    page.on("request", r => {
      if (r.method() === "POST" && r.url().endsWith("/api/segmentation-profiles")) posted++;
    });
    await page.goto("/file-configurations/segmentation-profiles/new");
    const checked = page.waitForResponse(r => r.url().endsWith("/api/segmentation-profiles/preview") && r.status() === 400);
    await page.getByTestId("segmentation-rule-add").click();
    await checked;

    // Typing is not saving: an empty name and an empty rule show nothing yet.
    await expect(page.getByText("Enter a name.")).toHaveCount(0);
    await expect(page.getByTestId("segmentation-rule-error-0")).toHaveCount(0);
    await expect(page.getByTestId("segmentation-preview-error")).toHaveCount(0);

    await page.getByTestId("segmentation-save").click();
    await expect(page.getByText("Enter a name.")).toBeVisible();
    await expect(page.getByTestId("segmentation-rule-error-0")).toHaveText("Rule needs a before or an after pattern.");
    expect(posted).toBe(0);

    // Once shown, an error goes away as soon as it is fixed.
    await page.getByTestId("segmentation-name").fill(`E2E errors ${Date.now()}`);
    await expect(page.getByText("Enter a name.")).toHaveCount(0);
  });
  test("a rule's Break / No break toggle has no empty room after its last option", async ({ page }) => {
    const english = await builtinEnglish(page);
    await page.goto(`/file-configurations/segmentation-profiles/${english.id}`);
    const toggle = (await page.getByTestId("segmentation-rule-action-0").boundingBox())!;
    const noBreak = (await page.getByTestId("segmentation-rule-action-0-no-break").boundingBox())!;
    expect(toggle.x + toggle.width - (noBreak.x + noBreak.width)).toBeLessThanOrEqual(4);
  });
  test("a profile for several languages is offered to a project in any of them; Any language stands alone", async ({ page }) => {
    test.setTimeout(40_000);
    const stamp = Date.now();
    await page.goto("/file-configurations/segmentation-profiles/new");
    await page.getByTestId("segmentation-name").fill(`E2E Icelandic and Irish ${stamp}`);
    const picker = page.getByTestId("segmentation-languages");
    await expect(picker).toContainText("Any language");

    await pickLanguages(page, "segmentation-languages", [["Icelandic", "is"], ["Irish", "ga"]]);
    await expect(picker).toContainText("IS");
    await expect(picker).toContainText("GA");
    await expect(picker).not.toContainText("Any language");
    // Any language clears the languages; a language then clears Any language.
    await pickLanguages(page, "segmentation-languages", [["Any", "any"]]);
    await expect(picker).toContainText("Any language");
    await expect(picker).not.toContainText("IS");
    await pickLanguages(page, "segmentation-languages", [["Icelandic", "is"], ["Irish", "ga"]]);
    await expect(picker).not.toContainText("Any language");

    const created = page.waitForResponse(r => r.request().method() === "POST" && r.url().endsWith("/api/segmentation-profiles"));
    await page.getByTestId("segmentation-save").click();
    const profile = (await (await created).json()).data as Profile & { source_languages: string[] };
    cleanup.push(`/api/segmentation-profiles/${profile.id}`);
    expect(profile.source_languages).toEqual(["is", "ga"]);

    // Save goes back to the list too.
    await page.goto(`/file-configurations/segmentation-profiles/${profile.id}`);
    await page.getByTestId("segmentation-mode-paragraph").click();
    const saved = page.waitForResponse(r => r.request().method() === "PUT" && r.url().endsWith(`/api/segmentation-profiles/${profile.id}`));
    await page.getByTestId("segmentation-save").click();
    expect((await saved).ok()).toBe(true);
    await expect(page).toHaveURL(/\/file-configurations\/segmentation-profiles$/);
    await expect(page.getByTestId(`segmentation-row-${profile.id}`)).toContainText("Paragraph");

    await page.goto("/projects/new");
    await startFromScratch(page);
    await selectLanguage(page, sourceLanguageTrigger(page), "ga");
    const popover = page.locator('[data-slot="popover-content"]');
    await page.getByTestId("segmentation-profile-select").click();
    await expect(popover.getByTestId(`segmentation-profile-select-option-${profile.id}`)).toBeVisible();
    await page.keyboard.press("Escape");
  });
  test("Paragraph mode hides the rules and word lists, and Sentence mode brings them back unchanged", async ({ page }) => {
    const thai = (await profilesFor(page, "th")).find(p => p.builtin_key === "builtin:th@1")!;
    await page.goto(`/file-configurations/segmentation-profiles/${thai.id}`);
    // The preview has rendered before its parts are checked.
    await expect(page.getByTestId("segmentation-preview-segments")).toBeVisible();
    for (const name of ["Rules", "Abbreviations", "Abbreviations before a number"]) await expect(card(page, name)).toHaveCount(0);
    await expect(page.getByText("Breaks and exceptions")).toHaveCount(0);

    const english = await builtinEnglish(page);
    await page.goto(`/file-configurations/segmentation-profiles/new?from=${english.id}`);
    const firstRule = page.getByTestId("segmentation-rule-before-0");
    const before = await firstRule.inputValue();
    await expect(page.getByText("Breaks and exceptions")).toBeVisible();
    const paragraphPreview = page.waitForResponse(r => r.url().endsWith("/api/segmentation-profiles/preview") &&
      r.request().postDataJSON()?.rule_set?.mode === "paragraph");
    await page.getByTestId("segmentation-mode-paragraph").click();
    await paragraphPreview;
    await expect(page.getByTestId("segmentation-preview-segments")).toBeVisible();
    for (const name of ["Rules", "Abbreviations", "Abbreviations before a number"]) await expect(card(page, name)).toHaveCount(0);
    await expect(page.getByText("Breaks and exceptions")).toHaveCount(0);
    await page.getByTestId("segmentation-mode-sentence").click();
    for (const name of ["Rules", "Abbreviations", "Abbreviations before a number"]) await expect(card(page, name)).toBeVisible();
    await expect(firstRule).toHaveValue(before);

    // A rule that is not valid still stops the save, so Create shows it.
    let posted = 0;
    page.on("request", r => {
      if (r.method() === "POST" && r.url().endsWith("/api/segmentation-profiles")) posted++;
    });
    const last = await page.locator('[data-testid^="segmentation-rule-before-"]').count();
    const checked = page.waitForResponse(r => r.url().endsWith("/api/segmentation-profiles/preview") && r.status() === 400);
    await page.getByTestId("segmentation-rule-add").click();
    await checked;
    await page.getByTestId("segmentation-mode-paragraph").click();
    await expect(card(page, "Rules")).toHaveCount(0);
    await page.getByTestId("segmentation-save").click();
    await expect(card(page, "Rules")).toBeVisible();
    await expect(page.getByTestId(`segmentation-rule-error-${last}`)).toHaveText("Rule needs a before or an after pattern.");
    expect(posted).toBe(0);
  });
  test("deleting a profile takes it off its projects: an upload says why it is refused, and Settings asks for another", async ({ page }) => {
    test.setTimeout(45_000);
    const stamp = Date.now();
    const english = await builtinEnglish(page);
    const profileRes = await page.request.post("/api/segmentation-profiles", {
      headers: await authHeaders(page),
      data: { name: `E2E deleted ${stamp}`, source_languages: [LANG], from: english.id },
    });
    expect(profileRes.ok(), `create profile: ${profileRes.status()}`).toBe(true);
    const profile = (await profileRes.json()).data as Profile;
    cleanup.push(`/api/segmentation-profiles/${profile.id}`);
    const projectRes = await page.request.post("/api/projects", {
      headers: await authHeaders(page),
      data: { name: `E2E deleted profile ${stamp}`, source_language: LANG, target_languages: ["en"], workflow_id: await standardWorkflowId(page), settings: { segmentation_profile_id: profile.id } },
    });
    expect(projectRes.ok(), `create project: ${projectRes.status()}`).toBe(true);
    const projectId = (await projectRes.json()).data.id as number;
    cleanup.unshift(`/api/projects/${projectId}`);
    const deleted = await page.request.delete(`/api/segmentation-profiles/${profile.id}`, { headers: await authHeaders(page) });
    expect(deleted.ok(), `delete profile: ${deleted.status()}`).toBe(true);

    await page.goto(`/projects/${projectId}/files`);
    await page.locator('input[type="file"]').setInputFiles({ name: "refused.txt", mimeType: "text/plain", buffer: Buffer.from(SAMPLE) });
    const refused = page.waitForResponse(r => r.url().endsWith(`/api/projects/${projectId}/files`) && r.request().method() === "POST");
    await page.locator('[data-slot="alert-dialog-content"]').getByTestId("upload-confirm").click();
    expect((await refused).status()).toBe(400);
    await expect(page.getByText("This project has no segmentation profile. Pick one in the project's settings first.").first()).toBeVisible();

    await page.goto(`/projects/${projectId}/settings`);
    await expect(page.getByTestId("segmentation-profile-missing")).toHaveText("Pick a segmentation profile.");
  });
});
