import { test, expect } from "@playwright/test";
import { deleteProjectViaAPI, login, uploadFileViaAPI } from "./helpers";
import { SEED_OWNER_NAME } from "../seed-identity";

// Project history tab: the audit log renders as a table
// (Type | Action | Changes | User | Date) with type/user filters.
test.describe("Project history", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("history table shows creation, rename diff, upload — and filters narrow it", async ({ page }) => {
    test.setTimeout(30_000);
    const token = await page.evaluate(() => localStorage.getItem("token"));
    const projName = `history-e2e ${Date.now()}`;
    // Settings travel in the create POST (atomic create). Pre-translate is
    // disabled so the upload below doesn't enqueue an async pipeline — its
    // job.pipeline_completed event would land at asynq's pace and make the
    // row count racy (covered instead by worker events_integration_test.go).
    const projRes = await page.request.post(`/api/projects`, {
      headers: { Authorization: `Bearer ${token}` },
      data: {
        name: projName,
        source_language: "en",
        target_languages: ["de"],
        settings: { tm_on_upload: false, mt_on_upload: false },
      },
    });
    expect(projRes.ok(), `create project: ${projRes.status()}`).toBe(true);
    const projectId: number = (await projRes.json()).data.id;

    try {
      // Rename via API (PATCH metadata) → history must show the old→new diff.
      const renamed = `${projName} renamed`;
      const patchRes = await page.request.patch(`/api/projects/${projectId}`, {
        headers: { Authorization: `Bearer ${token}` },
        data: { name: renamed },
      });
      expect(patchRes.ok(), `rename: ${patchRes.status()}`).toBe(true);

      // Upload a file → file.uploaded event.
      await uploadFileViaAPI(page, projectId, `history-${Date.now()}.txt`, "Hello history");

      // Open the History tab and verify the table.
      await page.goto(`/projects/${projectId}/history`);
      const list = page.getByTestId("project-history-list");
      await expect(list).toBeVisible({ timeout: 10_000 });

      const rows = list.locator("tbody tr");
      // Newest first: upload, rename, creation — 3 rows total.
      await expect(rows).toHaveCount(3);
      await expect(rows.filter({ hasText: "uploaded" }).first()).toBeVisible();
      await expect(rows.filter({ hasText: "created the project" }).first()).toBeVisible();

      // The rename row carries the old → new diff in the Changes column and a
      // "Settings" category badge in the Type column.
      const renameRow = rows.filter({ hasText: "updated project details" }).first();
      await expect(renameRow).toBeVisible();
      await expect(renameRow.getByTestId("event-type-badge")).toHaveText("Settings");
      await expect(renameRow.getByTestId("event-changes")).toContainText(projName);
      await expect(renameRow.getByTestId("event-changes")).toContainText(renamed);
      // The actor lands in the User column.
      await expect(renameRow).toContainText(SEED_OWNER_NAME);

      // Changes-focused layout: the toggle in the Changes cell widens that
      // column (480px) and shrinks the rest; a second click restores it.
      const changesHeader = list.locator("th", { hasText: "Changes" });
      await expect(changesHeader).toHaveCSS("width", "200px");
      const toggle = renameRow.getByTestId("history-changes-toggle");
      await toggle.click();
      await expect(toggle).toHaveAttribute("aria-pressed", "true");
      await expect(changesHeader).toHaveCSS("width", "480px");
      await toggle.click();
      await expect(toggle).toHaveAttribute("aria-pressed", "false");
      await expect(changesHeader).toHaveCSS("width", "200px");

      // Filter by type category: Files → only the upload row remains.
      await page.getByTestId("history-filter-type").click();
      await page.getByRole("button", { name: "Files", exact: true }).click();
      await expect(rows).toHaveCount(1);
      await expect(rows.first()).toContainText("uploaded");
      // Filter state is in the URL (shareable / reload-safe).
      expect(page.url()).toContain("evtype=file.");

      // Clear → all 3 rows come back.
      await page.getByTestId("history-filter-clear").click();
      await expect(rows).toHaveCount(3);
    } finally {
      await deleteProjectViaAPI(page, projectId);
    }
  });
});
