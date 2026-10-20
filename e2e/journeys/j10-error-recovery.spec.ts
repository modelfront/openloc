/**
 * J10 — Error Recovery (Adversarial)
 *
 * Core purpose: errors surface clearly, user can recover without refresh.
 * Exercises QA findings #24 (rapid confirm crash) and #28 (file upload failure).
 *
 * NOT NEGOTIABLE: "no crash" is NOT enough. Each error must produce a
 * meaningful message (not blank 500 or generic "Upload failed"). Each
 * recovery step must verify the system returned to a working state.
 */
import { test, expect } from "@playwright/test";
import {
  login,
  uploadFileViaAPI,
  pretranslateFileViaAPI,
  createProjectViaAPI,
  setProjectMTViaAPI,
  createMTEngineViaAPI,
  deleteProjectViaAPI,
  deleteMTEngineViaAPI,
} from "./helpers";

const API = "";

const VALID_SOURCE = [
  "Reset your password.",
  "Enter your email address.",
  "Your password has been updated.",
  "Click the forgot password link.",
  "Confirm your identity.",
].join("\n");

let projectId: number;
let mtEngineId: number;
let fileId: number;
let jobId: number;

test.describe.serial("J10 — Error Recovery", () => {
  test("A1: create project en→es-mx (no MT assigned)", async ({ page }) => {
    await login(page);
    projectId = await createProjectViaAPI(page, "Error Recovery Journey", "en", ["es-mx"]);
    expect(projectId).toBeGreaterThan(0);
  });

  test("A2: upload corrupt file (random bytes as .xliff) — error must be meaningful", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Create corrupt data — random bytes, not valid XML
    const corruptData = Buffer.from("THIS IS NOT VALID XLIFF CONTENT \x00\xFF\xFE garbage");

    const res = await page.request.post(`${API}/api/projects/${projectId}/files`, {
      headers,
      multipart: {
        file: {
          name: "corrupt.xliff",
          mimeType: "application/xliff+xml",
          buffer: corruptData,
        },
      },
    });

    // OBSERVABLE OUTCOME: server must reject with 400 and a meaningful error.
    // NOT 200 (silent accept), NOT 500 (crash).
    expect(res.status(), "corrupt upload should be 400 Bad Request").toBe(400);

    const body = await res.json();
    expect(body.error, "error response should have error object").toBeTruthy();
    expect(body.error.message, "error message should explain WHY it failed").toBeTruthy();
    // The actual message is "failed to parse file: no translatable segments found"
    // We check for keywords rather than exact string (message wording may evolve)
    expect(
      body.error.message.toLowerCase(),
      `error should mention parsing/segments, got: "${body.error.message}"`,
    ).toMatch(/parse|segment|invalid|format/);
  });

  test("A3: upload valid .txt immediately after — upload state not broken", async ({ page }) => {
    await login(page);

    // OBSERVABLE OUTCOME: system must recover from previous error.
    // A valid upload MUST succeed. If it fails, the corrupt upload
    // left the system in a bad state — that's a bug.
    const result = await uploadFileViaAPI(page, projectId, "recovery-test.txt", VALID_SOURCE, "text/plain");
    fileId = result.data?.id ?? result.id;
    expect(fileId, "valid upload must succeed after corrupt upload").toBeGreaterThan(0);

    // Get the job
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const jobsRes = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    const jobs = (await jobsRes.json()).data;
    expect(jobs.length, "should have 1 job after valid upload").toBe(1);
    jobId = jobs[0].id;
  });

  test("A5: assign mock MT + upload second file — proves MT assignment works", async ({ page }) => {
    await login(page);
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Assign the mock engine, then upload a SECOND file so its segments are
    // translated by that engine. The first file stays untouched for A6.
    mtEngineId = await createMTEngineViaAPI(page, "Error Recovery MT", "mock", "");
    await setProjectMTViaAPI(page, projectId, [
      { mt_engine_id: mtEngineId, source_language: "en", target_language: "es-mx" },
    ]);

    const secondSource = "New segment for mock MT.\nAnother fresh segment.\n";
    const result2 = await uploadFileViaAPI(page, projectId, "second-file.txt", secondSource, "text/plain");
    const file2Id = result2.data?.id ?? result2.id;

    // Pre-translate the second file via per-job phase endpoints
    await pretranslateFileViaAPI(page, projectId, file2Id);

    // Get the second file's job
    const jobsRes = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    const jobs = (await jobsRes.json()).data;
    const file2Job = jobs.find((j: any) => j.file_id === file2Id);
    expect(file2Job, "second file should have a job").toBeTruthy();

    // OBSERVABLE OUTCOME: second file's segments should have mock MT text [ES-MX]
    const transRes = await page.request.get(`${API}/api/jobs/${file2Job.id}/translations`, { headers });
    const translations = (await transRes.json()).data;
    const withMock = translations.filter((t: any) => t.active_text?.includes("[ES-MX]"));
    expect(
      withMock.length,
      `second file should have mock MT [ES-MX] text, got ${withMock.length}/${translations.length}`,
    ).toBeGreaterThanOrEqual(1);
  });

  test("A6: rapid UI confirm via Ctrl+Enter WITH throttled network (QA finding #24)", async ({ page }) => {
    // *** QA #24: "can't access property id, s is undefined" ***
    // The bug: rapid Ctrl+Enter fires confirmAndAdvance() before the next
    // segment's translation data loads → `translation!.id` is undefined.
    //
    // To actually trigger the race, we THROTTLE the translations API so the
    // React Query refetch after invalidation takes >500ms. Without throttling,
    // local dev responds in <50ms and the race never manifests.
    await login(page);

    // Throttle translations API responses by 800ms to force the race window
    await page.route("**/api/jobs/*/translations**", async (route) => {
      await new Promise((r) => setTimeout(r, 800));
      await route.continue();
    });

    await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
    await expect(page.getByText(/Reset your password/i).first()).toBeVisible({ timeout: 15000 });

    // Click the first segment to activate it
    await page.getByText(/Reset your password/i).first().click();
    await page.waitForTimeout(500);

    const isMac = process.platform === "darwin";
    const confirmKey = isMac ? "ControlOrMeta+Enter" : "Control+Enter";

    // Collect JS errors — the specific crash is "cannot read properties of undefined"
    const jsErrors: string[] = [];
    page.on("pageerror", (err) => jsErrors.push(err.message));
    page.on("console", (msg) => {
      if (msg.type() === "error") jsErrors.push(msg.text());
    });

    // Rapid-fire Ctrl+Enter 5 times with ZERO delay — only a zero delay opens
    // the race window against the 800ms API throttle.
    for (let i = 0; i < 5; i++) {
      await page.keyboard.press(confirmKey);
      // No wait — fire as fast as Playwright can
    }

    // Wait for throttled responses + any errors to surface
    await page.waitForTimeout(5000);

    // Remove throttle for cleanup
    await page.unroute("**/api/jobs/*/translations**");

    // OBSERVABLE OUTCOME: no JS errors
    const qa24Errors = jsErrors.filter(
      (e) => e.includes("cannot read") || e.includes("undefined") || e.includes("null") || e.includes("is not"),
    );
    expect(
      qa24Errors,
      `QA #24: rapid confirm + throttled network should not crash, got: ${JSON.stringify(qa24Errors)}`,
    ).toHaveLength(0);

    // Verify at least some confirms went through despite the race
    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const verifyRes = await page.request.get(`${API}/api/jobs/${jobId}/translations`, { headers });
    const verified = (await verifyRes.json()).data;
    const approved = verified.filter((t: any) => t.status === "approved");
    expect(approved.length, "at least 1 segment should be approved").toBeGreaterThanOrEqual(1);
  });

  test("D1: cleanup all resources", async ({ page }) => {
    await login(page);
    await deleteProjectViaAPI(page, projectId);
    if (mtEngineId) await deleteMTEngineViaAPI(page, mtEngineId);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const res = await page.request.get(`${API}/api/projects/${projectId}`, { headers });
    expect(res.status(), "project should be deleted").toBe(404);
  });

  test.afterAll(async ({ browser }) => {
    // Belt-and-suspenders cleanup if D1 was skipped due to a
    // mid-suite failure. The throttling tests (A6) have a higher chance of
    // mid-suite flakes, making the safety net especially valuable here.
    const page = await browser.newPage();
    try {
      await login(page);
      if (projectId) await deleteProjectViaAPI(page, projectId).catch(() => {});
      if (mtEngineId) await deleteMTEngineViaAPI(page, mtEngineId).catch(() => {});
    } catch {
      // best-effort
    } finally {
      await page.close();
    }
  });
});
