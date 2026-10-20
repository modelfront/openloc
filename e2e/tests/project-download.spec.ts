import { test, expect } from "@playwright/test";
import { login, createIsolatedCATJob } from "./helpers";

/**
 * Download from the project Files tab.
 *
 * The unit + Go tests cover which endpoint gets called and what the ZIP
 * contains. What only a browser can prove is that the affordance is actually
 * reachable and that a real download event fires with the right filename. So these tests
 * click the menu the way a user does and assert on Playwright's `download`
 * event, not on network calls.
 */

test.describe("project file download", () => {
  test("downloads the source file from the file-group header, without selecting anything", async ({
    page,
  }) => {
    await login(page);
    const job = await createIsolatedCATJob(page, {
      fileName: `dl-source-${Date.now()}.txt`,
      content: "Hello world\nSecond line",
    });

    try {
      await page.goto(`/projects/${job.projectId}/files`);

      // No checkbox selection — the point of the per-file menu is that it's
      // there before you've selected anything.
      await page.getByTestId(`file-download-${job.fileId}-btn`).click();

      const [download] = await Promise.all([
        page.waitForEvent("download"),
        page.getByTestId(`file-download-${job.fileId}-original`).click(),
      ]);

      expect(download.suggestedFilename()).toMatch(/^dl-source-\d+\.txt$/);
      // The bytes must be the upload, not a reconstruction.
      const stream = await download.createReadStream();
      const chunks: Buffer[] = [];
      for await (const chunk of stream) chunks.push(Buffer.from(chunk));
      expect(Buffer.concat(chunks).toString()).toContain("Hello world");
    } finally {
      await job.cleanup();
    }
  });

  test("the file row's actions stay inside their column, clear of the Status text", async ({ page }) => {
    // As an admin, so the row also carries Delete: the widest set of actions.
    await login(page);
    const job = await createIsolatedCATJob(page, {
      fileName: `dl-fit-${Date.now()}.txt`,
      content: "Hello world\nSecond line",
    });

    try {
      await page.goto(`/projects/${job.projectId}/files`);
      const download = page.getByTestId(`file-download-${job.fileId}-btn`);
      const remove = page.getByTestId(`delete-file-${job.fileId}`);
      await expect(download).toBeVisible();
      await expect(remove).toBeVisible();

      const cell = (await download.locator("xpath=ancestor::td[1]").boundingBox())!;
      const status = (await page.getByTestId(`file-readiness-${job.fileId}`).boundingBox())!;
      for (const [name, btn] of [["Download", download], ["Delete", remove]] as const) {
        const box = (await btn.boundingBox())!;
        expect(box.x, `${name} starts left of its column`).toBeGreaterThanOrEqual(cell.x - 0.5);
        expect(box.x + box.width, `${name} ends right of its column`).toBeLessThanOrEqual(cell.x + cell.width + 0.5);
        expect(box.x, `${name} covers the Status text`).toBeGreaterThanOrEqual(status.x + status.width);
      }
    } finally {
      await job.cleanup();
    }
  });

  // An XLIFF file, so the row carries Re-import beside Download.
  const HOVER_XLIFF = `<?xml version="1.0" encoding="utf-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
  <file source-language="en" target-language="es" datatype="plaintext" original="hover.xliff">
    <body><trans-unit id="1"><source>Hello</source></trans-unit></body>
  </file>
</xliff>`;

  for (const theme of ["light", "dark"] as const) {
    test(`the file row's Download button changes colour on hover (${theme})`, async ({ page }) => {
      // A ghost button's hover background matches the hovered row's, so the
      // text colour is the only hover feedback it has.
      await page.addInitScript(t => localStorage.setItem("openloc-theme", t), theme);
      await login(page);
      const job = await createIsolatedCATJob(page, {
        fileName: `dl-hover-${Date.now()}.xliff`,
        content: HOVER_XLIFF,
        contentType: "application/xliff+xml",
      });
      try {
        await page.goto(`/projects/${job.projectId}/files`);
        await expect(page.locator("html")).toHaveClass(theme === "dark" ? /\bdark\b/ : /^(?!.*\bdark\b)/);
        for (const testId of [`file-download-${job.fileId}-btn`]) {
          const btn = page.getByTestId(testId);
          const color = () => btn.evaluate(el => getComputedStyle(el).color);

          await btn.locator("xpath=ancestor::tr").getByText(/^dl-hover-/).hover();
          const rest = await color();
          await btn.hover();
          await expect.poll(color, { message: `${testId} hover colour` }).not.toBe(rest);
        }
      } finally {
        await job.cleanup();
      }
    });
  }

  test("a multi-job selection downloads as one zip from the bulk bar", async ({ page }) => {
    await login(page);
    // Two target languages → two jobs on one file, the shape a PM actually
    // selects when grabbing everything for a delivery.
    const job = await createIsolatedCATJob(page, {
      fileName: `dl-zip-${Date.now()}.txt`,
      content: "Hello world\nSecond line",
      targetLanguages: ["es", "fr"],
    });

    try {
      await page.goto(`/projects/${job.projectId}/files`);

      // One file, two languages: selecting the file selects both jobs.
      await page.getByTestId("select-all-files").check();
      await expect(page.getByTestId("file-selected-count")).toHaveText("1 file selected");

      await page.getByTestId("file-bulk-download-btn").click();
      // Nothing is approved yet, so the deliverable is disabled by design and
      // the bilingual hand-off is the live option — which is also the one an
      // external-translator hand-off actually uses.
      await expect(page.getByTestId("file-bulk-download-source")).toHaveAttribute(
        "aria-disabled",
        "true",
      );

      const [download] = await Promise.all([
        page.waitForEvent("download"),
        page.getByTestId("file-bulk-download-xliff").click(),
      ]);
      expect(download.suggestedFilename()).toMatch(/\.zip$/);
    } finally {
      await job.cleanup();
    }
  });

  test("the CAT editor keeps a download menu", async ({ page }) => {
    await login(page);
    const job = await createIsolatedCATJob(page, {
      fileName: `dl-editor-${Date.now()}.txt`,
      content: "Hello world\nSecond line",
    });

    try {
      await job.navigateToEditor();

      await page.getByTestId("editor-download-btn").click();
      const [download] = await Promise.all([
        page.waitForEvent("download"),
        page.getByTestId("editor-download-xliff").click(),
      ]);
      // One job in scope → the single-item route, so a bare .xlf, not a zip.
      expect(download.suggestedFilename()).toMatch(/\.xlf$/);
    } finally {
      await job.cleanup();
    }
  });
});
