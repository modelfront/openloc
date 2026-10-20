import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test, expect, type Page } from "@playwright/test";
import { authHeaders, createProjectViaAPI, login } from "./helpers";

test.describe("Project files", () => {
  // Projects the running test created, deleted in afterEach with headers read
  // right after login, so a test that times out still cleans up.
  const projectIds: number[] = [];
  let headers: Record<string, string> = {};

  test.beforeEach(async ({ page }) => {
    await login(page);
    headers = await authHeaders(page);
  });

  test.afterEach(async ({ request }) => {
    for (const id of projectIds.splice(0)) await request.delete(`/api/projects/${id}`, { headers }).catch(() => {});
  });

  test("project file list shows upload button", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await page.getByRole("link", { name: /^files$/i }).click();
    await expect(
      page.getByRole("button", { name: /upload/i })
    ).toBeVisible();
  });

  // A workbook without language codes in row 1: the upload dialog itself asks
  // which columns of each sheet to translate (none: the whole sheet).
  // Translations go back into their own cells, so no target column is asked.
  test("an Excel file without language codes is uploaded with the columns chosen in the upload dialog", async ({ page }) => {
    const projectId = await createProjectViaAPI(page, `xlsx columns ${Date.now()}`, { target_languages: ["de", "fr"] });
    projectIds.push(projectId);
    await page.goto(`/projects/${projectId}/files`);
    await page.getByRole("link", { name: /^files$/i }).click();
    await page.locator('input[type="file"]').setInputFiles(catalog());

    // One dialog: the upload's, with the sheet collapsed on the suggestion.
    const dialog = page.locator('[data-slot="alert-dialog-content"]');
    const sheet = "upload-xlsx-catalog-columns.xlsx-sheet-Products";
    await expect(dialog.getByTestId(`${sheet}-toggle`)).toContainText("Column B");
    await expect(page.getByTestId("xlsx-layout-dialog")).toHaveCount(0);

    await dialog.getByTestId(`${sheet}-toggle`).click();
    await dialog.getByTestId(`${sheet}-columns`).click();
    await expect(page.getByTestId(`${sheet}-columns-option-A`)).toHaveText("A (ID)");
    await page.keyboard.press("Escape");
    await dialog.getByTestId(`${sheet}-select-all`).click();
    await expect(dialog.getByTestId(`${sheet}-toggle`)).toContainText("All columns");

    const uploaded = page.waitForResponse(r => r.url().endsWith(`/api/projects/${projectId}/files`) && r.request().method() === "POST");
    await dialog.getByTestId("upload-confirm").click();
    const res = await uploaded;
    expect(res.status()).toBe(201);
    const file = (await res.json()).data;
    // P-1, Blue chair, P-2, Red table: both columns, below the header row.
    expect(file.segment_count).toBe(4);
    expect(file.xlsx_layout.sheets).toEqual([{ name: "Products", header_row: true, columns: ["A", "B"] }]);
    await expect(page.getByTestId("xlsx-layout-dialog")).toHaveCount(0);
  });

  // The New project wizard has no upload dialog, so it asks in its own; the
  // file's row changes the choice while the project is a draft.
  test("a draft's Excel file switches between the whole sheet and a column", async ({ page }) => {
    const draftId = await createProjectViaAPI(page, `xlsx draft ${Date.now()}`, { status: "draft" });
    projectIds.push(draftId);
    await page.goto(`/projects/new?draft=${draftId}`);
    await page.locator('[data-testid="create-file-dropzone"] input[type="file"]').setInputFiles(catalog());

    const dialog = page.getByTestId("xlsx-layout-dialog");
    await dialog.getByTestId("xlsx-sheet-Products-toggle").click();
    // Remove B: no column left is the whole sheet.
    await dialog.getByRole("button", { name: "Remove B (English)" }).click();
    await expect(dialog.getByTestId("xlsx-sheet-Products-toggle")).toContainText("Whole sheet");
    const uploaded = page.waitForResponse(r => r.url().endsWith(`/api/projects/${draftId}/files`) && r.request().method() === "POST");
    await dialog.getByTestId("xlsx-layout-submit").click();
    const file = (await (await uploaded).json()).data;
    expect(file.segment_count).toBe(4);
    expect(file.xlsx_layout.sheets).toEqual([{ name: "Products", header_row: true }]);
    await expect(page.getByTestId(`create-file-${file.id}`)).toContainText("(4 segments)");

    await page.getByTestId(`create-file-xlsx-columns-${file.id}`).click();
    await expect(dialog.getByTestId("xlsx-sheet-Products-toggle")).toContainText("Whole sheet");
    await dialog.getByTestId("xlsx-sheet-Products-toggle").click();
    await chooseColumn(page, "xlsx-sheet-Products-columns", "B", "B (English)");
    const saved = page.waitForResponse(r => r.url().endsWith(`/files/${file.id}/xlsx-layout`) && r.request().method() === "PUT");
    await dialog.getByTestId("xlsx-layout-submit").click();
    expect((await saved).status()).toBe(200);
    await expect(dialog).toBeHidden();
    await expect(page.getByTestId(`create-file-${file.id}`)).toContainText("(2 segments)");
  });
});

/** Adds a column in a sheet's multi-select; the list stays open, so Escape closes it. */
async function chooseColumn(page: Page, triggerTestId: string, letter: string, label: string) {
  await page.getByTestId(triggerTestId).click();
  const option = page.getByTestId(`${triggerTestId}-option-${letter}`);
  await expect(option).toHaveText(label);
  await option.click();
  await page.keyboard.press("Escape");
  await expect(page.locator('[data-slot="popover-content"]')).toHaveCount(0);
}

function catalog() {
  return {
    name: "catalog-columns.xlsx",
    mimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    buffer: readFileSync(join(__dirname, "../fixtures/catalog-columns.xlsx")),
  };
}
