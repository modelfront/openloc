import { test, expect } from "@playwright/test";
import {
  login,
  segmentEditor,
  uploadFileViaAPI,
  createTMViaAPI,
  deleteTMViaAPI,
  createProjectViaAPI,
  deleteProjectViaAPI,
  setProjectTMsViaAPI,
} from "./helpers";

/**
 * J30 — TM auto-refresh on confirm
 *
 * When a CAT confirm
 * writes to one or more TMs, the backend response carries the list of
 * `updated_tm_ids`. The frontend uses those IDs to (1) invalidate the
 * matching TM browser caches and (2) show a throttled "TM updated"
 * toast. Without these, a translator's confirmed entry would only
 * appear in the TM browser after a manual page reload.
 *
 * There is no cross-tab sync for TM caches, so J30 covers a single tab.
 *
 * **What the test pins:**
 *
 * 1. Backend save writes to the configured read_write TM.
 * 2. Backend response includes a non-empty `updated_tm_ids` array
 *    (proven indirectly via the toast — `notifyTMSaved` early-returns
 *    when `tmIds.length === 0`, so a visible toast = non-empty IDs).
 * 3. FE shows the "TM updated" toast after confirm.
 * 4. TM browser route (`/memories/:tmId`) renders the new entry on next
 *    mount — proves the user's actual flow (confirm → switch tab →
 *    see entry) works end-to-end.
 *
 * Locale: da-dk — distinct from j1 (es-mx), j2 (fr-ca), j4 (pt-br),
 * j6 (zh-hans), j25 (cs-CZ), j29 (ja-jp). Mock supports `da` natively;
 * `da-dk` exercises the base-code fallback.
 *
 * Anti-tautology proof:
 *   Mutation:    handler.go::HandleUpdateHuman — at the JSON write
 *                site, force `updatedTMIDs = []int64{}` regardless of
 *                what the TM writer returned.
 *   Diagnostic:  Phase B's "TM updated" toast assertion fails, because
 *                `notifyTMSaved` early-returns on empty IDs. The TM
 *                still has the entry (the SaveToTM side-effect is
 *                unaffected) — pinning the difference between the
 *                backend-write contract (always works) and the
 *                response-shape contract (slice 6c addition).
 *   Proven:      2026-05-07.
 */

const API = "";

let tmId: number;
let projectId: number;
let fileId: number;
let jobId: number;
let translationId: number;

test.describe.serial("J30 — TM auto-refresh on confirm", () => {

  // ════════════════════════════════════════════════════════════════════════
  // Phase A: Setup — write-mode TM + project + single-segment file
  // ════════════════════════════════════════════════════════════════════════

  test("A1: create TM + project en→da-dk + assign TM in read_write mode", async ({ page }) => {
    await login(page);

    tmId = await createTMViaAPI(page, "J30 TM");
    expect(tmId, "TM must be created").toBeGreaterThan(0);

    projectId = await createProjectViaAPI(page, "J30 TM Auto-Refresh", "en", ["da-dk"]);

    // CRITICAL: read_write mode — without it the TM writer skips the
    // entry and `updated_tm_ids` comes back empty, which would silently
    // make this whole test pass for the wrong reason. Pin the mode
    // explicitly here; if a future config drift makes "read_write"
    // mean something else, this fails fast at setup.
    await setProjectTMsViaAPI(page, projectId, [
      { tm_id: tmId, source_language: "en", target_language: "da-dk", mode: "read_write", priority: 1 },
    ]);
  });

  test("A2: upload file + capture jobId/translationId", async ({ page }) => {
    test.setTimeout(20_000);
    await login(page);

    const result = await uploadFileViaAPI(
      page,
      projectId,
      "j30-tm-refresh.txt",
      "Hello world.\n",
      "text/plain",
    );
    fileId = result.data?.id ?? result.id;
    expect(fileId, "file must upload").toBeGreaterThan(0);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    const jobsResp = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    const jobs = (await jobsResp.json()).data;
    const job = jobs.find((j: { target_language: string }) => j.target_language === "da-dk");
    expect(job, "da-dk job must exist").toBeDefined();
    jobId = job.id;

    const translationsResp = await page.request.get(
      `${API}/api/jobs/${jobId}/translations`,
      { headers },
    );
    const translations = (await translationsResp.json()).data;
    expect(translations.length, "exactly 1 translation").toBe(1);
    translationId = translations[0].id;

    // Verify TM is empty before we test write-back. If a previous test
    // run left an entry behind, the "entry appears in TM" assertion
    // would pass for the wrong reason.
    const tmEntriesResp = await page.request.get(`${API}/api/tms/${tmId}/entries?limit=10`, { headers });
    const tmEntries = (await tmEntriesResp.json()).data;
    expect(tmEntries.length, "TM must start empty").toBe(0);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B: Cmd+Enter triggers TM write-back + toast
  // ════════════════════════════════════════════════════════════════════════

  test("B: Cmd+Enter in editor → 'TM updated' toast + entry in TM via API", async ({ page }) => {
    test.setTimeout(20_000);
    await login(page);
    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);

    await expect(page.getByText(/Hello world/i).first()).toBeVisible({ timeout: 10000 });

    const translationCells = page.locator("[data-translation-cell]");
    await translationCells.first().click();
    await expect(page.locator("[data-active-row]")).toBeVisible({ timeout: 3000 });

    const editor = segmentEditor(page).first();
    await expect(editor).toBeVisible({ timeout: 5000 });

    // Type the Danish translation. The editor starts empty (no MT
    // pre-translate run), so just clicking + typing fills it.
    await editor.click();
    const isMac = process.platform === "darwin";
    await page.keyboard.press(isMac ? "ControlOrMeta+a" : "Control+a");
    await page.keyboard.type("Hej verden.");

    // Confirm. Backend's HandleUpdateHuman:
    //   1. Persists the human translation.
    //   2. Calls tmWriter.SaveToTM → writes to read_write TM(s).
    //   3. Returns `updated_tm_ids: [tmId]`.
    // Frontend's applyTMUpdatesAfterSave then:
    //   1. Invalidates queryKeys.tm.allEntries(tmId) etc.
    //   2. Calls notifyTMSaved → toast "TM updated".
    await page.keyboard.press("ControlOrMeta+Enter");

    // OBSERVABLE OUTCOME 1 — sonner success toast with literal "TM updated".
    // notifyTMSaved early-returns on empty `tmIds`, so this assertion
    // implicitly pins that `updated_tm_ids` came back non-empty.
    const tmToast = page.locator("[data-sonner-toast][data-type='success']").filter({ hasText: /TM updated/i });
    await expect(tmToast, "'TM updated' toast must appear").toBeVisible({ timeout: 5000 });

    // OBSERVABLE OUTCOME 2 — TM has the new entry. Proves the backend
    // write happened (SaveToTM ran, wasn't skipped by config drift).
    // No `waitForTimeout` needed: the toast only appears AFTER the
    // mutation's success handler runs, which happens AFTER the HTTP
    // response is fully read. The TM upsert is part of that same
    // request, so by the time the toast is visible, the entry is
    // committed.
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const entriesResp = await page.request.get(`${API}/api/tms/${tmId}/entries?limit=10`, { headers });
    const entries = (await entriesResp.json()).data;
    expect(entries.length, "TM should have exactly 1 entry after confirm").toBe(1);
    expect(entries[0].source_text, "source must be the seg's source text").toBe("Hello world.");
    expect(entries[0].target_text, "target must be what the user typed").toBe("Hej verden.");
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C: TM browser navigation shows the new entry
  // ════════════════════════════════════════════════════════════════════════
  //
  // Exercises the user's actual flow: confirm in editor → switch to
  // TM browser tab → entry visible. The refresh path is fresh
  // navigation; this phase pins
  // that the route renders correctly with the just-created data.

  test("C: navigate to TM browser → new entry visible in the page", async ({ page }) => {
    test.setTimeout(15_000);
    await login(page);
    await page.goto(`/memories/${tmId}`);

    // The TM browser fetches its own data on mount. Asserting the
    // source text appears proves both:
    //   (a) the route renders without a fetch error, AND
    //   (b) the API returns the entry created in Phase B.
    await expect(page.getByText("Hello world.").first(), "source text must render in TM browser").toBeVisible({ timeout: 5000 });
    await expect(page.getByText("Hej verden.").first(), "target text must render in TM browser").toBeVisible({ timeout: 5000 });
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D: Cleanup
  // ════════════════════════════════════════════════════════════════════════

  test("D: cleanup", async ({ page }) => {
    await login(page);
    if (projectId) await deleteProjectViaAPI(page, projectId);
    if (tmId) await deleteTMViaAPI(page, tmId);
  });

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    try {
      await login(page);
      if (projectId) await deleteProjectViaAPI(page, projectId).catch(() => {});
      if (tmId) await deleteTMViaAPI(page, tmId).catch(() => {});
    } catch {} finally {
      await page.close();
    }
  });
});
