import { test, expect } from "@playwright/test";
import { login, authHeaders, createIsolatedCATJob, seedDrafts } from "./helpers";

// A line break in a segment's source or translation shows as a line break in
// the grid, in a closed row as in the open one: innerText follows the CSS, so
// it reads what the user sees.

test.describe("Line breaks in the CAT editor grid", () => {
  let projectIds: number[] = [];

  test.beforeEach(async ({ page }) => {
    test.slow();
    await login(page);
  });

  test.afterEach(async ({ request, page }) => {
    const headers = await authHeaders(page).catch(() => ({}));
    for (const id of projectIds) await request.delete(`/api/projects/${id}`, { headers }).catch(() => {});
    projectIds = [];
  });

  test("source and translation keep their line breaks in closed and open rows", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      onCreated: id => projectIds.push(id),
      fileName: `line-breaks-${Date.now()}.json`,
      contentType: "application/json",
      content: JSON.stringify({ greeting: "Hello there\nGeneral" }),
    });
    await seedDrafts(page, job, ["Eins\n\nZwei"]);
    await job.navigateToEditor(/Hello there/);

    const targetCell = page.locator("[data-translation-cell]").first();
    const sourceCell = targetCell.locator("..").locator(":scope > div").nth(1);
    const shown = (cell: import("@playwright/test").Locator) => cell.evaluate(el => (el as HTMLElement).innerText.trim());

    expect(await shown(sourceCell), "closed row source").toBe("Hello there\nGeneral");
    expect(await shown(targetCell), "closed row translation").toBe("Eins\n\nZwei");

    await targetCell.click();
    await expect(page.getByTestId("active-source-text")).toBeVisible();
    expect(await shown(page.getByTestId("active-source-text")), "open row source").toBe("Hello there\nGeneral");
  });
});
