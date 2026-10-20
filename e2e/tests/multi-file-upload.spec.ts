import { test, expect } from "@playwright/test";
import { deleteProjectViaAPI, login, uploadFileViaAPI } from "./helpers";

const API = "";

test.describe("Multi-file Upload", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("upload button says 'Upload files' and input accepts multiple", async ({
    page,
  }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await page.getByRole("link", { name: /^files$/i }).click();

    const uploadBtn = page.getByRole("button", { name: /upload files/i });
    await expect(uploadBtn).toBeVisible();

    // Verify the hidden file input has the `multiple` attribute
    const fileInput = page.locator('input[type="file"]');
    await expect(fileInput).toHaveAttribute("multiple", "");
  });

  test("multi-file upload via API creates separate files", async ({ page }) => {
    // Own project: other specs open Demo Project's first job link, so extra
    // files there would change which job they land on.
    const token = await page.evaluate(() => localStorage.getItem("token"));
    const projRes = await page.request.post(`${API}/api/projects`, {
      headers: { Authorization: `Bearer ${token}` },
      data: { name: `multi-upload ${Date.now()}`, source_language: "en", target_languages: ["de"] },
    });
    expect(projRes.ok(), `create project: ${projRes.status()}`).toBe(true);
    const projectId: number = (await projRes.json()).data.id;

    const files = [
      { name: `multi-test-a-${Date.now()}.txt`, content: "File A content" },
      { name: `multi-test-b-${Date.now()}.txt`, content: "File B content" },
    ];
    const fileIds: number[] = [];

    try {
      for (const f of files) {
        const result = await uploadFileViaAPI(
          page,
          projectId,
          f.name,
          f.content
        );
        const id = result.data?.id ?? result.id;
        fileIds.push(id);
      }

      expect(fileIds).toHaveLength(2);
      expect(fileIds[0]).not.toBe(fileIds[1]);

      await page.goto(`/projects/${projectId}/files`);
      await page.getByRole("link", { name: /^files$/i }).click();
      for (const f of files) {
        await expect(page.getByText(f.name)).toBeVisible({ timeout: 5000 });
      }
    } finally {
      // Cascade-delete the whole project (files + jobs) so nothing leaks.
      await deleteProjectViaAPI(page, projectId);
    }
  });

  // The upload dialog confirms the files and the analysis, nothing else.
  // Deadlines come from the project's workflow, resolved server-side per the
  // job's target language, so the upload cannot disagree with the project.
  test("the upload dialog asks nothing about deadlines", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await page.getByRole("link", { name: /^files$/i }).click();

    // Queue a file — this only opens the dialog; nothing uploads until Upload
    // is clicked, and we cancel instead.
    await page.locator('input[type="file"]').setInputFiles({
      name: "upload-layout.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("layout check"),
    });

    const dialog = page.locator('[data-slot="alert-dialog-content"]');
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText(/upload 1 file/i);
    await expect(dialog.getByTestId("upload-analyze-checkbox")).toBeVisible();

    await expect(page.locator('[data-testid^="workflow-deadline-"]')).toHaveCount(0);
    await expect(dialog).not.toContainText(/deadline/i);

    await page.getByRole("button", { name: /^cancel$/i }).click();
  });

  // The popup stays mounted through its close animation, so every title it
  // renders on the way out is on screen for a frame. Record them all until it
  // unmounts: anything other than the title it opened with is a visible flick.
  test("cancelling the upload dialog keeps its content until it is gone", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await page.getByRole("link", { name: /^files$/i }).click();

    await page.locator('input[type="file"]').setInputFiles({
      name: "upload-cancel.txt",
      mimeType: "text/plain",
      buffer: Buffer.from("cancel check"),
    });
    const dialog = page.locator('[data-slot="alert-dialog-content"]');
    await expect(dialog).toContainText(/upload 1 file/i);

    const titles = page.evaluate(
      () =>
        new Promise<string[]>(resolve => {
          const seen = new Set<string>();
          const record = () => {
            const title = document.querySelector('[data-slot="alert-dialog-title"]');
            if (!title) {
              observer.disconnect();
              resolve([...seen]);
              return;
            }
            seen.add(title.textContent ?? "");
          };
          const observer = new MutationObserver(record);
          observer.observe(document.body, { subtree: true, childList: true, characterData: true });
          record();
        }),
    );
    await page.getByRole("button", { name: /^cancel$/i }).click();

    expect(await titles).toEqual(["Upload 1 file"]);
    await expect(dialog).toHaveCount(0);
  });

  test("drag overlay text says 'Drop files to upload'", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await page.getByRole("link", { name: /^files$/i }).click();

    // Simulate dragenter via evaluate to trigger React's synthetic event. The
    // drop zone guards on `dataTransfer.types.includes('Files')` (isFileDrag)
    // so non-file drags don't flash the overlay — add a File so `types` includes
    // 'Files' and the overlay shows.
    const dropZone = page.locator('[data-slot="page-container"].relative');
    await dropZone.evaluate((el) => {
      const dt = new DataTransfer();
      dt.items.add(new File(["x"], "x.txt", { type: "text/plain" }));
      const event = new DragEvent("dragenter", { bubbles: true, dataTransfer: dt });
      el.dispatchEvent(event);
    });

    await expect(page.getByText("Drop files to upload")).toBeVisible();
  });
});
