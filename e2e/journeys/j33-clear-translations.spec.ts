import { test, expect, type Locator, type Page } from "@playwright/test";
import {
  selectFirstTask,
  login,
  uploadFileViaAPI,
  createProjectViaAPI,
  deleteProjectViaAPI,
  createMTEngineViaAPI,
  deleteMTEngineViaAPI,
  setProjectMTViaAPI,
  openBulkPretranslate,
} from "./helpers";

/**
 * J33 — Clear translations → re-apply MT loop, via the bulk-action bar.
 *
 * Select the job → Apply MT → Clear (shared ClearTranslationsDialog) →
 * Apply MT again.
 *
 * Anti-tautology: short-circuit ClearTranslationsByJob SQL ⇒ segments stay
 * MT-filled, so the job stays clearable and the Clear button never disables
 * after the clear toast.
 */

const API = "";
void API; // imported for parity with other journeys; unused here

let projectId: number;
let mtEngineId: number;

/**
 * Apply MT runs TWICE in this journey and both runs raise a byte-identical
 * toast. Held in one constant so the "wait for the stale one to clear" step
 * can't drift out of sync with the assertion it protects.
 */
const APPLY_MT_TOAST = /Apply MT: 1 job\(s\) done/;

/**
 * Clicks a bulk action and waits for its POST, since toasts auto-dismiss after
 * ~4s and the response is the event actually under test. Takes the menu item
 * Locator: Apply TM/MT live inside the bulk bar's Pre-translate menu,
 * so the caller opens the menu first.
 */
async function runBulkAction(page: Page, target: Locator, urlFragment: string) {
  const [resp] = await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(urlFragment) && r.request().method() === "POST",
      { timeout: 30_000 },
    ),
    target.click(),
  ]);
  expect(resp.ok(), `${urlFragment} should succeed`).toBe(true);
}

test.describe.serial("J33 — Clear translations → re-apply MT loop", () => {
  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    await login(page);
    if (projectId) await deleteProjectViaAPI(page, projectId);
    if (mtEngineId) await deleteMTEngineViaAPI(page, mtEngineId);
    await page.close();
  });

  test("A: setup project en→fr with mock MT and a two-segment file", async ({ page }) => {
    await login(page);
    projectId = await createProjectViaAPI(page, "J33 Clear", "en", ["fr"]);
    expect(projectId).toBeGreaterThan(0);

    mtEngineId = await createMTEngineViaAPI(page, "J33 Mock", "mock");
    await setProjectMTViaAPI(page, projectId, [
      { mt_engine_id: mtEngineId, source_language: "en", target_language: "fr" },
    ]);

    const uploaded = await uploadFileViaAPI(
      page,
      projectId,
      "j33.txt",
      "Hello.\nWorld.",
      "text/plain",
    );
    expect(uploaded?.data?.id).toBeGreaterThan(0);
  });

  test("B-C-D: Apply MT → Clear (all) → Apply MT again — same page, loop closes", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}`);
    await selectFirstTask(page);
    await expect(page.getByText("j33.txt")).toBeVisible({ timeout: 10000 });

    // Apply MT → Clear becomes actionable.
    await runBulkAction(page, await openBulkPretranslate(page, "mt"), "/mt-apply");
    await expect(page.getByText(APPLY_MT_TOAST)).toBeVisible({ timeout: 30_000 });
    await expect(page.getByTestId("bulk-clear-btn")).toBeEnabled();

    // Clear (scope=all) via the shared ClearTranslationsDialog. The estimate
    // reflecting 2 MT-filled segments is the observable that MT actually filled
    // the job.
    await page.getByTestId("bulk-clear-btn").click();
    await expect(page.getByTestId("clear-count-estimate")).toContainText(/2 segments will be cleared/);
    await page.getByTestId("clear-scope-all").click();
    await expect(page.getByTestId("clear-count-estimate")).toContainText(/2 segments will be cleared/);
    await runBulkAction(page, page.getByTestId("clear-submit"), "/clear-translations");
    await expect(page.getByText(/Clear: 1 job\(s\) done/)).toBeVisible({ timeout: 30_000 });

    // The first Apply-MT toast is byte-identical to the one asserted below and
    // may still show (Sonner keeps it ~4s). Wait for it to clear so strict mode
    // matches only the new one.
    await expect(page.getByText(APPLY_MT_TOAST)).toHaveCount(0, { timeout: 15_000 });

    // A job with no translated segment is not clearable, so the Clear button
    // disabling proves the clear emptied the job and the job counts refetched.
    await expect(page.getByTestId("bulk-clear-btn")).toBeDisabled({ timeout: 15_000 });

    // Re-apply MT — loop closes.
    await expect(page.getByTestId("bulk-pretranslate-btn")).toBeEnabled({ timeout: 30_000 });
    await runBulkAction(page, await openBulkPretranslate(page, "mt"), "/mt-apply");
    await expect(page.getByText(APPLY_MT_TOAST)).toBeVisible({ timeout: 30_000 });
  });
});
