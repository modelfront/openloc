import { readFile } from "node:fs/promises";
import { test, expect } from "@playwright/test";
import { login, getSeedProjectId, createIsolatedCATJob, withResizeStripsHidden } from "./helpers";

const API = "";

test.describe("Project Analyses", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("create a leverage snapshot, wait for it to compute, and read the band breakdown", async ({
    page,
  }) => {
    test.setTimeout(60_000);

    const projectId = await getSeedProjectId(page, "Demo Project");
    const token = await page.evaluate(() => localStorage.getItem("token"));

    await page.goto(`/projects/${projectId}/files`);
    await page.getByRole("link", { name: /analyses/i }).click();
    await expect(page.getByRole("heading", { name: /saved analyses/i })).toBeVisible();

    const createResp = page.waitForResponse(
      (r) =>
        new RegExp(`/api/projects/${projectId}/analyses$`).test(r.url()) &&
        r.request().method() === "POST",
    );
    await page.getByRole("button", { name: /new analysis/i }).click();
    await expect(page.getByRole("dialog")).toBeVisible();
    await page.getByRole("button", { name: /run analysis/i }).click();
    const created = await createResp;
    expect(created.ok(), "POST /analyses should succeed").toBe(true);
    const analysisId: number = (await created.json()).data.id;

    try {
      // A row shows completion only through data-status and by becoming
      // clickable (no status badge). Wait for data-status to flip to
      // completed, then open it.
      const row = page.locator(`[data-testid="analysis-row-${analysisId}"]`);
      await expect(
        row,
        "worker should finish and the row status should flip to completed",
      ).toHaveAttribute("data-status", "completed", { timeout: 45_000 });

      await row.click();

      await expect(
        page.getByRole("heading", { name: /→ (es|fr|de)/ }).first(),
        "a completed analysis must render a per-language breakdown (real computed rows, not an empty table)",
      ).toBeVisible({ timeout: 10_000 });
      await expect(page.getByText("100%").first()).toBeVisible();
      await expect(page.getByText("85–99%").first()).toBeVisible();

      // The opened details stay inside the list's frame, and the breakdown
      // fits its own box until the file column is widened.
      const detail = page.locator(`[data-testid="analysis-detail-${analysisId}"]`);
      const overflow = () => detail.evaluate(d => {
        const outer = d.closest("table")!.parentElement!;
        const inner = d.querySelector("table")!.parentElement!;
        return {
          outer: outer.scrollWidth > outer.clientWidth,
          inner: inner.scrollWidth > inner.clientWidth,
        };
      });
      expect(await overflow(), "nothing scrolls sideways by default").toEqual({ outer: false, inner: false });

      const fileHandle = detail.getByRole("separator", { name: "Resize file column" }).first();
      await fileHandle.focus();
      for (let i = 0; i < 25; i++) await fileHandle.press("ArrowRight");
      expect(await overflow(), "a widened file column scrolls the breakdown, not the list").toEqual({ outer: false, inner: true });
      await fileHandle.dblclick();

      const metric = detail.getByRole("radiogroup", { name: "Metric" });
      await metric.getByRole("radio", { name: "Segments" }).click();
      await expect(metric.getByRole("radio", { name: "Segments" })).toHaveAttribute(
        "aria-checked",
        "true",
      );
      await metric.getByRole("radio", { name: "Characters" }).click();
      await expect(metric.getByRole("radio", { name: "Characters" })).toHaveAttribute(
        "aria-checked",
        "true",
      );
    } finally {
      await page.request.delete(`${API}/api/analyses/${analysisId}`, {
        headers: { Authorization: `Bearer ${token}` },
      });
    }
  });

  // The default analysis name carries the file name, so one long unbreakable
  // name reaches the list's Name column and the breakdown's File column.
  test("an unbreakably long file name keeps the analyses list and its details in shape", async ({ page }) => {
    test.setTimeout(60_000);
    const longName = "quarterlyfinancialstatementsconsolidatedreportfinalversion".repeat(3) + ".txt";
    const { projectId, cleanup } = await createIsolatedCATJob(page, { fileName: longName, content: "Hello overflow test." });

    try {
      await page.goto(`/projects/${projectId}/analyses`);
      const created = page.waitForResponse(r => new RegExp(`/api/projects/${projectId}/analyses$`).test(r.url()) && r.request().method() === "POST");
      await page.getByRole("button", { name: /new analysis/i }).click();
      await page.getByRole("button", { name: /run analysis/i }).click();
      const analysisId: number = (await (await created).json()).data.id;

      const row = page.locator(`[data-testid="analysis-row-${analysisId}"]`);
      await expect(row).toHaveAttribute("data-status", "completed", { timeout: 45_000 });

      const list = page.locator('[data-guide="project_analyses.list"]');
      expect(await collectOverflows(list), "the closed list stays inside its box").toEqual([]);

      // The Jobs column keeps its place instead of being pushed by the name.
      const jobsHeader = (await list.getByRole("columnheader", { name: "Jobs" }).boundingBox())!;
      const jobsCell = (await row.locator("td").nth(1).boundingBox())!;
      expect(Math.abs(jobsCell.x - jobsHeader.x), "Jobs cell under its header").toBeLessThan(2);

      await row.click();
      await expect(page.locator(`[data-testid="analysis-detail-${analysisId}"]`).getByRole("table").first()).toBeVisible();
      expect(await collectOverflows(list), "the opened details stay inside the list's box").toEqual([]);
    } finally {
      await cleanup();
    }
  });

  // The dialog is a CSS grid. Grid items have min-width:auto, so an
  // unbreakable string whose min-content exceeds the dialog width (a long
  // file name with no spaces) inflates the implicit column track and every
  // child renders wider than the dialog box itself — content spills over the
  // backdrop. Only triggers when the name is long enough that its min-content
  // beats the dialog's max-width, which is why short names look fine.
  test("dialog content stays inside the dialog for an unbreakably long file name", async ({
    page,
  }) => {
    // Own project, not the shared seed one: this file never runs pretranslate,
    // and an incomplete job in Demo Project breaks the leverage-snapshot
    // test's "select all jobs" gate when the two run in parallel.
    const longName =
      "ksdhfkjshdfkjshdkjfhskdjfhksjdhfksjhdfkjshkfjdhskjdfhkjsdhfdemo".repeat(3) + ".txt";
    const { projectId, cleanup } = await createIsolatedCATJob(page, {
      fileName: longName,
      content: "Hello overflow test.",
    });

    try {
      await page.goto(`/projects/${projectId}/files`);
      await page.getByRole("link", { name: /analyses/i }).click();
      await page.getByRole("button", { name: /new analysis/i }).click();

      const dialog = page.getByRole("dialog");
      await expect(dialog).toBeVisible();
      // Don't measure until the job list actually shows the long-named job.
      await expect(dialog.getByText(longName.slice(0, 40)).first()).toBeVisible();

      expect(
        await collectOverflows(dialog),
        "no element inside the dialog may render wider than the dialog box",
      ).toEqual([]);

      // Same file name through the OTHER dialog family: the delete-file
      // ConfirmDialog interpolates the name into AlertDialogTitle, which has
      // no word-breaking of its own — the long name must wrap, not spill.
      await page.keyboard.press("Escape");
      await page.getByRole("link", { name: /^files$/i }).click();
      const fileRow = page.getByText(longName.slice(0, 40)).first();
      await fileRow.hover();
      await page.getByRole("button", { name: /delete file/i }).click();
      const confirm = page.getByRole("alertdialog");
      await expect(confirm).toBeVisible();
      expect(
        await collectOverflows(confirm),
        "no element inside the confirm dialog may render or scroll wider than its box",
      ).toEqual([]);
      await confirm.getByRole("button", { name: /cancel/i }).click();
    } finally {
      await cleanup();
    }
  });

  test("downloads a completed analysis as CSV and XLSX from its row", async ({ page }) => {
    test.setTimeout(60_000);
    const { projectId, cleanup } = await createIsolatedCATJob(page, { fileName: "export-check.txt", content: "Hello export test." });

    try {
      await page.goto(`/projects/${projectId}/analyses`);
      const created = page.waitForResponse(r => new RegExp(`/api/projects/${projectId}/analyses$`).test(r.url()) && r.request().method() === "POST");
      await page.getByRole("button", { name: /new analysis/i }).click();
      await page.getByRole("button", { name: /run analysis/i }).click();
      const analysisId: number = (await (await created).json()).data.id;

      const row = page.locator(`[data-testid="analysis-row-${analysisId}"]`);
      await expect(row).toHaveAttribute("data-status", "completed", { timeout: 45_000 });
      const detail = page.locator(`[data-testid="analysis-detail-${analysisId}"]`);

      await page.getByTestId(`analysis-export-${analysisId}`).click();
      const csvDownload = page.waitForEvent("download");
      await page.getByTestId(`analysis-export-${analysisId}-csv`).click();
      const csv = await csvDownload;
      expect(csv.suggestedFilename()).toMatch(/ \d{4}-\d{2}-\d{2}\.csv$/);
      const lines = (await readFile(await csv.path(), "utf8")).replace(/^\uFEFF/, "").trim().split(/\r?\n/);
      expect(lines[0]).toMatch(/^File,Target language,Repetitions segments,/);
      expect(lines.length, "one line per file and target language, no totals").toBe(2);
      expect(lines[1]).toContain("export-check");

      // Picking a format downloads; it does not open the row underneath.
      await expect(detail).toHaveCount(0);

      await page.getByTestId(`analysis-export-${analysisId}`).click();
      const xlsxDownload = page.waitForEvent("download");
      await page.getByTestId(`analysis-export-${analysisId}-xlsx`).click();
      const xlsx = await xlsxDownload;
      expect(xlsx.suggestedFilename()).toMatch(/\.xlsx$/);
      const bytes = await readFile(await xlsx.path());
      expect(bytes.subarray(0, 2).toString(), "an XLSX is a zip").toBe("PK");
      await expect(detail).toHaveCount(0);
    } finally {
      await cleanup();
    }
  });
});

// Width complaints inside a dialog come in two shapes: a box that outgrows the
// dialog (grid track blowout), and text ink that paints past its own box
// (missing word-breaking — box widths look fine, scrollWidth doesn't). Column
// resize strips stick out of their header cell by design and are left out.
function collectOverflows(dialog: import("@playwright/test").Locator) {
  return withResizeStripsHidden(dialog.page(), () => dialog.evaluate((dlg) => {
    const dialogWidth = dlg.getBoundingClientRect().width;
    return [...dlg.querySelectorAll("*")]
      .filter((el) => {
        const cls = `${el.className ?? ""}`;
        if (cls.includes("truncate") || cls.includes("sr-only")) return false; // clip by design
        return (
          el.getBoundingClientRect().width > dialogWidth + 1 ||
          el.scrollWidth > el.clientWidth + 1
        );
      })
      .map((el) =>
        `${el.tagName} box=${Math.round(el.getBoundingClientRect().width)} scroll=${el.scrollWidth} ${el.className}`.slice(0, 100),
      );
  }));
}
