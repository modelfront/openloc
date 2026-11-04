import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test, expect, type Page } from "@playwright/test";
import { authHeaders, createProjectViaAPI, login, openFilterProfiles, pickComboboxValue, signInWithToken, startFromScratch } from "./helpers";

/**
 * Filter profiles: an Excel profile made on its page reads a workbook's
 * column B under a header row, so an upload that picks it asks nothing.
 */
const catalog = () => readFileSync(join(__dirname, "..", "fixtures", "catalog-columns.xlsx"));

/** The Filter profiles list, every format open (each starts closed). */
async function openFilterProfilesList(page: Page) {
  await page.goto("/file-configurations/filter-profiles");
  await page.getByTestId("filter-profiles-expand-all").click();
  await expect(page.getByTestId("filter-profiles-expand-all")).toHaveText("Collapse all");
}

test.describe("Filter profiles", () => {
  const projectIds: number[] = [];
  const profileIds: number[] = [];
  let headers: Record<string, string> = {};

  test.beforeEach(async ({ page }) => {
    await login(page);
    headers = await authHeaders(page);
  });

  test.afterEach(async ({ request }) => {
    for (const id of projectIds.splice(0)) await request.delete(`/api/projects/${id}`, { headers }).catch(() => {});
    for (const id of profileIds.splice(0)) await request.delete(`/api/filter-profiles/${id}`, { headers }).catch(() => {});
  });

  test("an Excel profile created on its page lays out an upload that picks it", async ({ page }) => {
    // UI create, list, project create and an upload: more than the default 10s on a shared stack.
    test.setTimeout(30_000);
    const name = `E2E column B ${Date.now()}`;
    await page.goto("/file-configurations/filter-profiles");
    await expect(page.getByRole("navigation", { name: "Section" }).getByRole("link", { name: "Filter profiles" })).toHaveAttribute("aria-current", "page");
    await page.getByTestId("filter-profile-new").click();
    await expect(page).toHaveURL(/\/file-configurations\/filter-profiles\/new$/);

    await pickComboboxValue(page, "filter-profile-format", "xlsx");
    await page.getByTestId("filter-profile-name").fill(name);
    await page.getByTestId("filter-xlsx-header-row").click();
    const popover = page.locator('[data-slot="popover-content"]');
    await page.getByTestId("filter-xlsx-columns").click();
    await popover.getByTestId("filter-xlsx-columns-option-B").click();
    await page.keyboard.press("Escape");
    await expect(popover).toHaveCount(0);

    const [created] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith("/api/filter-profiles") && r.request().method() === "POST"),
      page.getByTestId("filter-profile-save").click(),
    ]);
    expect(created.status()).toBe(201);
    const profile = (await created.json()).data;
    profileIds.push(profile.id);
    expect(profile).toMatchObject({ name, format: "xlsx", builtin: false });
    expect(profile.options.xlsx).toEqual({ sheets: [], header_row: true, columns: ["B"], target_columns: [] });

    // The list shows it with a summary of its rules.
    await openFilterProfilesList(page);
    await expect(page.getByRole("row").filter({ hasText: name })).toContainText("Column B · header row");

    // An upload that picks the profile is laid out by its rules.
    const projectId = await createProjectViaAPI(page, `filter profile ${Date.now()}`, { target_languages: ["de"] });
    projectIds.push(projectId);
    const res = await page.request.post(`/api/projects/${projectId}/files`, {
      headers,
      multipart: {
        file: { name: "catalog.xlsx", mimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", buffer: catalog() },
        filter_profile_id: String(profile.id),
      },
    });
    expect(res.status(), await res.text()).toBe(201);
    const file = (await res.json()).data;
    expect(file.xlsx_layout.sheets).toEqual([{ name: "Products", header_row: true, columns: ["B"] }]);
    expect(file.segment_count).toBe(2);
  });

  // A multilingual workbook: the source in B and an empty column per language.
  // Each language's export fills its own column and keeps the source.
  test("an Excel profile with target columns exports each language into its column", async ({ page }) => {
    // UI create, an upload, three saves and six exports and uploads: more than the default 10s.
    test.setTimeout(60_000);
    const name = `E2E multilingual ${Date.now()}`;
    await page.goto("/file-configurations/filter-profiles/new");
    await pickComboboxValue(page, "filter-profile-format", "xlsx");
    await page.getByTestId("filter-profile-name").fill(name);
    await page.getByTestId("filter-xlsx-header-row").click();
    const popover = page.locator('[data-slot="popover-content"]');
    await page.getByTestId("filter-xlsx-columns").click();
    await popover.getByTestId("filter-xlsx-columns-option-B").click();
    await page.keyboard.press("Escape");
    await expect(popover).toHaveCount(0);
    for (const [i, [lang, column]] of [["fr", "C"], ["de", "D"], ["es", "E"]].entries()) {
      await page.getByTestId("filter-xlsx-target-add").click();
      await pickComboboxValue(page, `filter-xlsx-target-language-${i}`, lang);
      await page.getByTestId(`filter-xlsx-target-column-${i}`).fill(column.toLowerCase());
    }
    // A column that overlaps the source is refused before the save.
    await page.getByTestId("filter-xlsx-target-column-2").fill("b");
    await page.getByTestId("filter-profile-save").click();
    await expect(page.getByText("Column B holds the source text, so es cannot be written into it.")).toBeVisible();
    await page.getByTestId("filter-xlsx-target-column-2").fill("e");

    const [created] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith("/api/filter-profiles") && r.request().method() === "POST"),
      page.getByTestId("filter-profile-save").click(),
    ]);
    expect(created.status()).toBe(201);
    const profile = (await created.json()).data;
    profileIds.push(profile.id);
    expect(profile.options.xlsx).toEqual({
      sheets: [], header_row: true, columns: ["B"],
      target_columns: [{ language: "fr", column: "C" }, { language: "de", column: "D" }, { language: "es", column: "E" }],
    });
    await openFilterProfilesList(page);
    await expect(page.getByRole("row").filter({ hasText: name })).toContainText("Column B · Translations in FR C, DE D, ES E · header row");

    const projectId = await createProjectViaAPI(page, `multilingual ${Date.now()}`, { target_languages: ["fr", "de", "es"] });
    projectIds.push(projectId);
    const upload = async (fileName: string, buffer: Buffer, fields: Record<string, string>) => {
      const res = await page.request.post(`/api/projects/${projectId}/files`, {
        headers,
        multipart: { file: { name: fileName, mimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", buffer }, ...fields },
      });
      expect(res.status(), await res.text()).toBe(201);
      return (await res.json()).data as { id: number; segment_count: number; xlsx_layout: unknown };
    };
    const sources = async (fileId: number) => {
      const res = await page.request.get(`/api/files/${fileId}/segments`, { headers });
      expect(res.ok(), `list segments: ${res.status()}`).toBe(true);
      return ((await res.json()).data as { id: number; source_text: string }[]);
    };

    const workbook = readFileSync(join(__dirname, "..", "fixtures", "catalog-multilingual.xlsx"));
    const file = await upload("catalog-multilingual.xlsx", workbook, { filter_profile_id: String(profile.id) });
    expect(file.segment_count).toBe(2);
    expect(file.xlsx_layout).toEqual({ sheets: [{ name: "Products", header_row: true, columns: ["B"], target_columns: profile.options.xlsx.target_columns }] });
    const segments = await sources(file.id);
    expect(segments.map(s => s.source_text)).toEqual(["Blue chair", "Red table"]);

    // Every cell of B to E, read back by uploading the export with a layout
    // of those columns: row by row, left to right, empty cells left out.
    const readBack = { sheets: [{ name: "Products", header_row: true, columns: ["B", "C", "D", "E"] }] };
    for (const lang of ["fr", "de", "es"]) {
      const list = await page.request.get(`/api/files/${file.id}/translations?lang=${lang}`, { headers });
      expect(list.ok(), `list translations: ${list.status()}`).toBe(true);
      for (const t of (await list.json()).data as { id: number; segment_id: number }[]) {
        const source = segments.find(s => s.id === t.segment_id)!.source_text;
        const saved = await page.request.put(`/api/files/${file.id}/translations/${t.id}`, {
          headers, data: { human_text: `${lang.toUpperCase()}:${source}`, editing_time_ms: 0 },
        });
        expect(saved.ok(), `save ${lang}: ${saved.status()} ${await saved.text()}`).toBe(true);
      }
      const exported = await page.request.get(`/api/files/${file.id}/export?lang=${lang}`, { headers });
      expect(exported.ok(), `export ${lang}: ${exported.status()}`).toBe(true);
      const back = await upload(`export-${lang}.xlsx`, await exported.body(), { xlsx_layout: JSON.stringify(readBack) });
      const L = lang.toUpperCase();
      expect((await sources(back.id)).map(s => s.source_text), `${lang} export`)
        .toEqual(["Blue chair", `${L}:Blue chair`, "Red table", `${L}:Red table`]);
    }

    // The Files tab downloads the file's three jobs together: one workbook,
    // every language in its own column, read row by row from B to E.
    const jobsRes = await page.request.get(`/api/projects/${projectId}/jobs`, { headers });
    expect(jobsRes.ok(), `list jobs: ${jobsRes.status()}`).toBe(true);
    const jobs = ((await jobsRes.json()).data as { id: number; file_id: number; file_multilingual: boolean }[])
      .filter(j => j.file_id === file.id);
    expect(jobs.map(j => j.file_multilingual)).toEqual([true, true, true]);
    const combined = await page.request.post(`/api/projects/${projectId}/download`, {
      headers, data: { format: "source", job_ids: jobs.map(j => j.id) },
    });
    expect(combined.ok(), `combined download: ${combined.status()}`).toBe(true);
    expect(combined.headers()["content-type"]).not.toBe("application/zip");
    const all = await upload("export-all.xlsx", await combined.body(), { xlsx_layout: JSON.stringify(readBack) });
    expect((await sources(all.id)).map(s => s.source_text)).toEqual([
      "Blue chair", "FR:Blue chair", "DE:Blue chair", "ES:Blue chair",
      "Red table", "FR:Red table", "DE:Red table", "ES:Red table",
    ]);
  });

  // The org default changes what every new project of the org starts with, so
  // this test runs in an organization of its own: specs running beside it in
  // the seed org never see it. A fresh org has no "Standard" workflow, so its
  // project is a draft.
  test("an org default set on the list starts new projects without a template", async ({ page }) => {
    test.setTimeout(30_000);
    const runId = Date.now();
    const provisioned = await page.request.post("/api/auth/dev-login", {
      data: { email: `filter-default-${runId}@example.com`, tenant_name: `Filter default ${runId}` },
    });
    expect(provisioned.ok(), `provision an org: ${provisioned.status()}`).toBe(true);
    const token = (await provisioned.json()).token as string;
    await signInWithToken(page, token);
    const own = { Authorization: `Bearer ${token}` };

    const name = `Plain JSON ${runId}`;
    const created = await page.request.post("/api/filter-profiles", {
      headers: own, data: { name, format: "json_i18n", options: { inline_html: false } },
    });
    expect(created.status()).toBe(201);
    const profile = (await created.json()).data;

    // Every format starts closed; the heading names nothing until a default is set.
    await page.goto("/file-configurations/filter-profiles");
    const jsonSection = page.getByTestId("filter-profile-section-json_i18n");
    await expect(jsonSection.locator("[aria-expanded]")).toHaveAttribute("aria-expanded", "false");
    await expect(jsonSection).not.toContainText("Org default");
    // A click opens that format alone.
    await jsonSection.locator("[aria-expanded]").click();
    await expect(jsonSection.locator("[aria-expanded]")).toHaveAttribute("aria-expanded", "true");
    await expect(jsonSection.getByTestId(`filter-profile-row-${profile.id}`)).toBeVisible();
    await expect(page.getByTestId("filter-profiles-expand-all")).toHaveText("Expand all");
    await openFilterProfilesList(page);
    const [set] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith("/api/filter-profiles/defaults") && r.request().method() === "PUT"),
      page.getByTestId(`filter-profile-set-default-${profile.id}`).click(),
    ]);
    expect(set.status()).toBe(200);
    await expect(page.getByTestId(`filter-profile-org-default-${profile.id}`)).toHaveText("Org default");
    // The list is grouped by format: the row sits in the JSON section, whose
    // heading now names the default.
    await expect(jsonSection.getByTestId(`filter-profile-row-${profile.id}`)).toBeVisible();
    await expect(jsonSection).toContainText(`Org default: ${name}`);
    await page.getByTestId("filter-profiles-expand-all").click();
    await expect(jsonSection.locator("[aria-expanded]")).toHaveAttribute("aria-expanded", "false");

    // The New project form starts with it…
    await page.goto("/projects/new");
    await startFromScratch(page);
    await openFilterProfiles(page);
    await expect(page.getByTestId("filter-profile-select-json_i18n")).toContainText(name);

    // …and a project created without a template names it.
    const projectRes = await page.request.post("/api/projects", {
      headers: own, data: { name: `Org default ${runId}`, source_language: "en", target_languages: ["de"], status: "draft" },
    });
    expect(projectRes.status()).toBe(201);
    const projectId = (await projectRes.json()).data.id;
    const settings = await page.request.get(`/api/projects/${projectId}/settings`, { headers: own });
    expect(settings.ok()).toBe(true);
    expect((await settings.json()).data.project.filter_profiles).toContainEqual({ format: "json_i18n", id: profile.id, name });

    // Deleting the default is refused until it is removed.
    const refused = await page.request.delete(`/api/filter-profiles/${profile.id}`, { headers: own });
    expect(refused.status()).toBe(409);
    await openFilterProfilesList(page);
    const [unset] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith("/api/filter-profiles/defaults") && r.request().method() === "PUT"),
      page.getByTestId(`filter-profile-unset-default-${profile.id}`).click(),
    ]);
    expect(unset.status()).toBe(200);
    await expect(page.getByTestId(`filter-profile-org-default-${profile.id}`)).toHaveCount(0);

    // Once it is not the default it can go; a draft project is deleted with it.
    expect((await page.request.delete(`/api/projects/${projectId}`, { headers: own })).status()).toBe(204);
    expect((await page.request.delete(`/api/filter-profiles/${profile.id}`, { headers: own })).status()).toBe(204);
  });
});
