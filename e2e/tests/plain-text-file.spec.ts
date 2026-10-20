import { test, expect } from "@playwright/test";
import { createIsolatedCATJob, login } from "./helpers";

/**
 * A text file uploaded with "Strings contain HTML" off is plain text in the
 * CAT editor: markup in a saved translation loads back as characters, not as
 * formatting, and is saved again as typed.
 */
test.describe("Plain-text file in the CAT editor", () => {
  let job: Awaited<ReturnType<typeof createIsolatedCATJob>> | undefined;

  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test.afterEach(async () => {
    await job?.cleanup().catch(() => {});
    job = undefined;
  });

  test("markup in a saved translation reloads as text", async ({ page }) => {
    test.setTimeout(30_000);
    const text = "Use <strong>bold</strong> text";
    job = await createIsolatedCATJob(page, {
      fileName: `plain-text-${Date.now()}.txt`,
      content: `${text}\n`,
      inlineHTML: false,
    });
    await job.navigateToEditor(/Use/);
    await expect(page.getByText(text).first()).toBeVisible();

    const cell = page.locator("[data-translation-cell]").first();
    const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
    await cell.click();
    await editor.click();
    await page.keyboard.press("ControlOrMeta+Shift+s");
    await expect(editor).toHaveText(text);

    const saved = page.waitForResponse(
      r => r.request().method() === "PUT" && /\/api\/files\/\d+\/translations\/\d+$/.test(r.url()) && r.ok(),
    );
    await page.keyboard.press("ControlOrMeta+Enter");
    const body = (await saved).request().postDataJSON() as { human_text: string; human_rich_text?: string | null };
    expect(body.human_text).toBe(text);
    expect(body.human_rich_text ?? null).toBeNull();

    // Back on the confirmed segment: the markup is characters, not bold.
    await cell.click();
    await expect(editor).toHaveText(text);
    await expect(editor.locator("strong")).toHaveCount(0);
  });
});
