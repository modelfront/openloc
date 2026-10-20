import { test, expect } from "@playwright/test";
import {
  selectFirstTask,
  login,
  uploadFileViaAPI,
  createProjectViaAPI,
  deleteProjectViaAPI,
  openBulkPretranslate,
} from "./helpers";

/**
 * J32 — Apply TM / MT on a project with no assignment shows a useful toast
 *
 * "No assignment" flows through a typed PhaseSkipReason in the pipeline
 * outcome; the handler maps it to 400 with a help message
 * ("No MT engine configured for en → fr — add one in project settings"),
 * and the FE toast shows the backend message verbatim (no prefix that
 * would duplicate the noun).
 *
 * What this journey pins:
 *   - Apply MT on a project with no MT assignment surfaces a toast that
 *     mentions "MT" + "settings" so the user knows what to do.
 *   - Apply TM on a project with no TM assignment surfaces a similar toast.
 *   - The toast is NEVER the green success toast ("MT applied" /
 *     "TM lookup complete").
 *
 * Locale: en→fr. Locale isn't load-bearing (this is about the
 * config check, not language resolution); en→fr is the universal
 * vanilla pair already used by J31.
 *
 * Surface: actions moved into the bulk-action bar; the bulk hook surfaces
 * the shared backend reason verbatim, so the "name the capability + point
 * at settings" contract holds here too.
 *
 * Anti-tautology proof:
 *   - handler_pipeline.go: short-circuit phaseUnactionableMessage ⇒ handler
 *     returns 200 + green success toast; the "no MT engine" assertion fails.
 *   - useBulkJobActions.ts: drop the single-reason surfacing ⇒ toast no longer
 *     names MT/settings; the `/No MT engine configured.*settings/i` fails.
 */

let projectId: number;

test.describe.serial("J32 — Apply TM/MT with no assignment shows help toast", () => {
  test.afterAll(async ({ browser }) => {
    if (!projectId) return;
    const page = await browser.newPage();
    await login(page);
    await deleteProjectViaAPI(page, projectId);
    await page.close();
  });

  test("A: create bare project en→fr (no template applied → no TM/MT assignments)", async ({ page }) => {
    await login(page);
    projectId = await createProjectViaAPI(page, "J32 No-Config Toasts", "en", ["fr"]);
    expect(projectId).toBeGreaterThan(0);

    const uploaded = await uploadFileViaAPI(
      page,
      projectId,
      "j32.txt",
      "Hello, world.",
      "text/plain",
    );
    expect(uploaded?.data?.id, "uploaded file id").toBeGreaterThan(0);
  });

  async function selectTheJob(page: import("@playwright/test").Page) {
    await selectFirstTask(page);
    await expect(page.getByText("j32.txt")).toBeVisible({ timeout: 10000 });
  }

  test("B: Apply MT shows a toast that names MT + points the user at settings", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}`);
    await selectTheJob(page);

    await (await openBulkPretranslate(page, "mt")).click();

    // Contract: name the capability + point at where to fix it (exact wording not load-bearing).
    await expect(page.getByText(/No MT engine configured.*settings/i)).toBeVisible();
    // The green success toast MUST NOT appear.
    await expect(page.getByText(/Apply MT: \d+ job\(s\) done/)).toHaveCount(0);
    await expect(page).toHaveURL(new RegExp(`/projects/${projectId}$`));
  });

  test("C: Apply TM shows a toast that names TM + points the user at settings", async ({ page }) => {
    await login(page);
    await page.goto(`/projects/${projectId}`);
    await selectTheJob(page);

    await (await openBulkPretranslate(page, "tm")).click();

    await expect(page.getByText(/No TM configured.*settings/i)).toBeVisible();
    await expect(page.getByText(/Apply TM: \d+ job\(s\) done/)).toHaveCount(0);
    await expect(page).toHaveURL(new RegExp(`/projects/${projectId}$`));
  });
});
