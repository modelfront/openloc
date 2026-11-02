import { test, expect } from "@playwright/test";
import { createIsolatedCATJob, login } from "./helpers";

/**
 * Placeables: the source marks numbers, addresses, variables and codes. F8
 * inserts the next missing tag or placeable in source order, Alt+F8 the rest,
 * and a click on a placeable in the source inserts it. Only missing tags get
 * the bar above the target.
 */
test.describe("Placeables in the CAT editor", () => {
  let job: Awaited<ReturnType<typeof createIsolatedCATJob>> | undefined;

  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test.afterEach(async () => {
    await job?.cleanup().catch(() => {});
    job = undefined;
  });

  test("F8 walks tags and placeables in source order; a click inserts one", async ({ page }) => {
    test.setTimeout(30_000);
    job = await createIsolatedCATJob(page, {
      fileName: `placeables-${Date.now()}.txt`,
      content: "Order <b>XJ-500B</b> for {name} at https://example.com/help?a=1&b=2.\n",
      inlineHTML: true,
      qaProfile: null,
    });
    await job.navigateToEditor(/Order/);

    const marked = page.locator("[data-placeable]");
    await expect(marked).toHaveCount(3);
    await expect(marked.nth(0)).toHaveAttribute("data-placeable", "XJ-500B");

    await page.locator("[data-translation-cell]").first().click();
    const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
    await editor.click();
    await page.keyboard.type("Bestellen ");

    // The bar lists the missing tags only.
    await expect(page.getByTestId("missing-tag-chip")).toHaveCount(2);
    await expect(page.getByText(/missing placeable/i)).toHaveCount(0);

    const chips = editor.locator(".xliff-tag");
    await page.keyboard.press("F8");
    await expect(chips).toHaveCount(1);
    await page.keyboard.press("F8");
    await expect(editor).toContainText("XJ-500B");
    await page.keyboard.press("F8");
    await expect(chips).toHaveCount(2);

    // A click on the variable in the source inserts it at the cursor.
    await page.getByTestId("active-source-text").locator('[data-placeable="{name}"]').click();
    await expect(editor).toContainText("{name}");

    // Alt+F8 inserts what is left: the web address.
    await page.keyboard.press("Alt+F8");
    await expect(editor).toContainText("https://example.com/help?a=1&b=2");
    await expect(page.getByTestId("missing-tags-bar")).toHaveCount(0);

    // A double-click (selecting text to copy) inserts the placeable once.
    const before = (await editor.textContent()) ?? "";
    await page.getByTestId("active-source-text").locator('[data-placeable="{name}"]').dblclick();
    await expect(editor).toHaveText(before + "{name}");
  });
});

test.describe("Term targets on F8", () => {
  let job: Awaited<ReturnType<typeof createIsolatedCATJob>> | undefined;
  let termBaseId: number | undefined;

  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test.afterEach(async ({ page }) => {
    if (termBaseId !== undefined && job) {
      await page.request.delete(`/api/terminology/${termBaseId}`, { headers: { Authorization: `Bearer ${job.token}` } }).catch(() => {});
    }
    await job?.cleanup().catch(() => {});
    job = undefined;
    termBaseId = undefined;
  });

  test("F8 inserts a missing term target among the tags and placeables", async ({ page }) => {
    test.setTimeout(30_000);
    job = await createIsolatedCATJob(page, {
      fileName: `term-f8-${Date.now()}.txt`,
      content: "Reset your <b>password</b> for {name}.\n",
      targetLanguages: ["de"],
      inlineHTML: true,
      qaProfile: null,
    });
    // Own term base, so the test does not lean on the seeded one.
    const headers = { Authorization: `Bearer ${job.token}` };
    const base = await page.request.post("/api/terminology", {
      headers,
      data: { name: `term-f8 ${Date.now()}`, description: "", source_language: "en" },
    });
    expect(base.ok(), `create term base: ${base.status()}`).toBe(true);
    termBaseId = (await base.json()).data.id;
    const concept = await page.request.post(`/api/terminology/${termBaseId}/concepts`, {
      headers,
      data: { terms: [
        { language: "en", text: "password", status: "approved" },
        { language: "de", text: "Passwort", status: "approved" },
      ] },
    });
    expect(concept.ok(), `create concept: ${concept.status()}`).toBe(true);
    const assign = await page.request.put(`/api/projects/${job.projectId}/terminology`, {
      headers,
      data: { assignments: [{ term_base_id: termBaseId, source_language: "en", target_language: "de", priority: 0 }] },
    });
    expect(assign.ok(), `assign term base: ${assign.status()}`).toBe(true);

    await job.navigateToEditor(/Reset your/);
    const lookup = page.waitForResponse(r => r.url().includes("/api/terminology/lookup") && r.status() === 200);
    await page.locator("[data-translation-cell]").first().click();
    await lookup;
    await expect(page.getByTestId("terminology-section")).toContainText("Passwort");

    const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
    await editor.click();
    const chips = editor.locator(".xliff-tag");
    await page.keyboard.press("F8");
    await expect(chips).toHaveCount(1);
    await page.keyboard.press("F8");
    await expect(editor).toContainText("Passwort");
    await expect(page.getByTestId("terminology-section").getByTitle("Already applied")).toBeVisible();

    // Alt+F8 inserts the rest: the closing tag and the variable.
    await page.keyboard.press("Alt+F8");
    await expect(chips).toHaveCount(2);
    await expect(editor).toContainText("{name}");
    const order = await editor.evaluate(el =>
      [...(el.querySelector("p")?.childNodes ?? [])].map(n =>
        n instanceof HTMLElement && n.classList.contains("xliff-tag") ? "TAG" : (n.textContent ?? "").trim(),
      ).filter(Boolean),
    );
    expect(order).toEqual(["TAG", "Passwort", "TAG", "{name}"]);

    // Nothing is left, so F8 adds nothing more.
    const before = (await editor.textContent()) ?? "";
    await page.keyboard.press("F8");
    await expect(editor).toHaveText(before);
  });
});
