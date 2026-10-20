import { test, expect } from "@playwright/test";
import {
  login,
  uploadFileViaAPI,
  createProjectViaAPI,
  deleteProjectViaAPI,
  setProjectWorkflowViaAPI,
} from "./helpers";

/**
 * J27 — Deadline notification cron
 *
 * An hourly asynq scheduler runs `RunDeadlineCheck`, which scans for
 * jobs whose active step-assignment deadline falls in the next 24 hours, dedups against any deadline notification fired in
 * the past 20 hours, and publishes one `EventDeadline` per
 * (assigned-to user, job) pair.
 *
 * The notification surfaces in the bell as an unread row. Translators
 * see the badge increment; clicking the bell shows the deadline subject
 * + project/file/lang context.
 *
 * **Why not test the actual cron firing:** the asynq scheduler runs
 * `@hourly`, so a real-time test would either wait up to 60 minutes
 * or mock time. `POST /api/admin/notifications/deadline-check`
 * (admin-only) is the synchronous trigger — same code path as the cron
 * (`RunDeadlineCheck`), without the 1-hour scheduling delay.
 *
 * **Locale:** nb-NO (Norwegian Bokmål) — distinct from j1 (es-mx),
 * j2 (fr-ca), j4 (pt-br), j6 (zh-hans), j25 (cs-CZ), j29 (ja-jp),
 * j30 (da-dk).
 *
 * Anti-tautology proof:
 *   Mutation:    `internal/notification/scheduled.go::RunDeadlineCheck`
 *                — short-circuit at the top with `return 0, nil` so
 *                no notification is ever created.
 *   Diagnostic:  Phase B's "deadline notification exists for jobId"
 *                assertion fails — the notifications list never
 *                contains a deadline event for our job.
 *   Proven:      2026-05-07.
 */

const API = "";

let projectId: number;
let fileId: number;
let jobId: number;
let userId: number;

test.describe.serial("J27 — Deadline notification cron", () => {

  // ════════════════════════════════════════════════════════════════════════
  // Phase A: Setup — project with deadline 12h out, job assigned to user
  // ════════════════════════════════════════════════════════════════════════

  test("A1: capture user id + create project en→nb-NO + workflow", async ({ page }) => {
    test.setTimeout(20_000);
    await login(page);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // The cron query joins `job_step_assignments.assigned_to`. We need
    // OUR user id so we can later assert "the deadline notification
    // landed in OUR bell." `/api/auth/me` returns `id` for the JWT subject.
    const meResp = await page.request.get(`${API}/api/auth/me`, { headers });
    expect(meResp.ok(), `auth/me: ${meResp.status()}`).toBe(true);
    userId = (await meResp.json()).id ?? (await meResp.json()).user?.id;
    if (!userId) {
      // Some builds wrap me in `{ data }`. Fetch fresh to be safe.
      const body = await (await page.request.get(`${API}/api/auth/me`, { headers })).json();
      userId = body.id ?? body.user?.id ?? body.data?.id;
    }
    expect(userId, "must capture userId").toBeGreaterThan(0);

    projectId = await createProjectViaAPI(page, "J27 Deadline Cron", "en", ["nb-no"]);

    // Single linguist workflow step. Assignment
    // happens explicitly in A3 via `assignJobStep`, not via default_assignee.
    await setProjectWorkflowViaAPI(page, projectId, [
      { name: "translate", role_required: "linguist" },
    ]);
  });

  test("A2: upload file → capture jobId", async ({ page }) => {
    test.setTimeout(15_000);
    await login(page);

    const result = await uploadFileViaAPI(
      page,
      projectId,
      "j27-deadline.txt",
      "Hello world.\n",
      "text/plain",
    );
    fileId = result.data?.id ?? result.id;

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const jobsResp = await page.request.get(`${API}/api/projects/${projectId}/jobs`, { headers });
    const jobs = (await jobsResp.json()).data;
    const job = jobs.find((j: { target_language: string }) => j.target_language === "nb-no");
    expect(job, "nb-NO job must exist").toBeDefined();
    jobId = job.id;
  });

  test("A3: assign job's translate step to our user + PATCH step deadline = now+12h", async ({ page }) => {
    test.setTimeout(15_000);
    await login(page);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Get the job's current step (created by the workflow setup +
    // file upload). Need its `id` — that's what `assignJobStep` keys on.
    const stepsResp = await page.request.get(`${API}/api/jobs/${jobId}/workflow/steps`, { headers });
    expect(stepsResp.ok(), `list job steps: ${stepsResp.status()}`).toBe(true);
    const steps = (await stepsResp.json()).data;
    expect(steps.length, "job must have at least 1 workflow step").toBeGreaterThan(0);
    const stepId = steps[0].id;

    // Assign the step to our user. Without this, the cron's
    // `WHERE assigned_to IS NOT NULL` filter would drop our job and
    // we'd be testing a no-op.
    const assignResp = await page.request.post(
      `${API}/api/jobs/${jobId}/workflow/steps/${stepId}/assign`,
      {
        headers,
        data: { user_id: userId },
      },
    );
    expect(assignResp.ok(), `assign step: ${assignResp.status()}`).toBe(true);

    // Set the ACTIVE STEP's deadline to 12 hours from now. The
    // deadline lives on the step assignment (jobs.current_step_id →
    // job_step_assignments.deadline) — there is NO project fallback, so the
    // cron's GetJobsApproachingDeadline only sees step deadlines.
    // GetJobsApproachingDeadline filters `BETWEEN now() AND now() + 24h`; 12h
    // fits squarely inside the window, far enough from the boundary that
    // client/server clock skew can't accidentally exclude us.
    const deadlineISO = new Date(Date.now() + 12 * 60 * 60 * 1000).toISOString();
    const patchResp = await page.request.patch(
      `${API}/api/jobs/${jobId}/workflow/steps/${stepId}/deadline`,
      {
        headers,
        data: { deadline: deadlineISO },
      },
    );
    expect(patchResp.ok(), `patch step deadline: ${patchResp.status()}`).toBe(true);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B: Trigger cron → notification created for our user/job
  // ════════════════════════════════════════════════════════════════════════

  test("B: POST /admin/.../deadline-check → notification exists for our jobId", async ({ page }) => {
    test.setTimeout(15_000);
    await login(page);

    const headers = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };

    // Trigger the synchronous deadline check — same `RunDeadlineCheck`
    // function the asynq cron calls.
    const triggerResp = await page.request.post(
      `${API}/api/admin/notifications/deadline-check`,
      { headers },
    );
    expect(triggerResp.ok(), `trigger deadline check: ${triggerResp.status()}`).toBe(true);

    // OBSERVABLE OUTCOME 1 — `dispatched: 1+`. The handler returns the
    // count of notifications it published. Our job is the one we just
    // wired; other tenants/jobs in the dev DB may also approach
    // deadlines, so `>= 1` is the safe lower bound.
    const triggerBody = (await triggerResp.json()).data;
    expect(triggerBody.dispatched, "at least our 1 deadline notification dispatched").toBeGreaterThanOrEqual(1);

    // OBSERVABLE OUTCOME 2 — the notifications list contains a deadline
    // event for our specific jobId. This is the load-bearing assertion:
    // a regression that fired a count-but-no-row would slip past
    // outcome 1 alone. Filter strictly by reference_id to avoid
    // matching unrelated deadline notifications from other tests.
    //
    // `?limit=50` is safe given two compounding facts:
    //   1. ListNotifications sorts `id DESC` (newest first), so our
    //      fresh notification is at the top of the response.
    //   2. Migration 031 added BEFORE DELETE triggers that wipe
    //      notifications when their referenced job/project is
    //      deleted, so per-test cleanup stops
    //      accumulation and 50 is plenty of headroom even for
    //      shared dev environments.
    const notifsResp = await page.request.get(
      `${API}/api/notifications?limit=50`,
      { headers },
    );
    expect(notifsResp.ok(), `list notifications: ${notifsResp.status()}`).toBe(true);
    const notifs = (await notifsResp.json()).data;
    const deadlineNotif = notifs.find(
      (n: { event_type: string; reference_type: string; reference_id: number }) =>
        n.event_type === "deadline" &&
        n.reference_type === "job" &&
        n.reference_id === jobId,
    );
    expect(deadlineNotif, "deadline notification must exist for our jobId").toBeDefined();
    expect(deadlineNotif.user_id, "notification must be addressed to our user").toBe(userId);
    expect(deadlineNotif.read, "fresh notification must be unread").toBe(false);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C: Bell UI shows the notification
  // ════════════════════════════════════════════════════════════════════════

  test("C: bell badge increments + dropdown shows deadline notification", async ({ page }) => {
    test.setTimeout(15_000);
    await login(page);
    await page.goto("/");

    // Wait for the bell to render. The bell mount fetches its count
    // via `/api/notifications/count`; the count >= 1 means our
    // Phase B notification is included. The bell button has no text
    // content (SVG only) — matched by its `title="Notifications"`
    // attribute which is what tooltips render.
    const bellButton = page.getByTitle("Notifications");
    await expect(bellButton, "bell button must render").toBeVisible({ timeout: 5000 });

    // OBSERVABLE OUTCOME 1 — the unread badge is visible (count >= 1).
    // The badge is a sibling `<span>` inside the bell button; it only
    // mounts when count > 0. So asserting on its visibility = asserting
    // on count > 0 without coupling to the exact rendered number.
    const badge = bellButton.locator("span");
    await expect(badge, "bell badge must show (count > 0)").toBeVisible({ timeout: 5000 });

    // OBSERVABLE OUTCOME 2 — clicking opens the dropdown with our
    // deadline notification. The subject line carries project name +
    // file name + lang context per the notification template.
    //
    // SCOPING NOTE: this page is `/` (ProjectList), which renders
    // "J27 Deadline Cron" in the project list as well. A page-wide
    // `getByText` would match the project list entry and pass for
    // the wrong reason. Scope to the bell's menu so the assertion can
    // only match dropdown content.
    await bellButton.click();
    const bellContainer = page.getByTestId("notification-menu");
    // Dropdown header inside the menu — proves it actually opened.
    await expect(bellContainer.getByText("Notifications", { exact: true })).toBeVisible({ timeout: 3000 });
    // Project name in the deadline notification, scoped to the dropdown.
    // `.first()` because a run whose cleanup was skipped can leave an
    // older "J27 Deadline Cron" row behind. The contract pin is "at least
    // one matching notification is visible in the bell".
    await expect(bellContainer.getByText(/J27 Deadline Cron/i).first(), "our deadline notification must show in the bell dropdown").toBeVisible({ timeout: 5000 });
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D: Cleanup
  // ════════════════════════════════════════════════════════════════════════

  test("D: cleanup", async ({ page }) => {
    await login(page);
    if (projectId) await deleteProjectViaAPI(page, projectId);
  });

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    try {
      await login(page);
      if (projectId) await deleteProjectViaAPI(page, projectId).catch(() => {});
    } catch {} finally {
      await page.close();
    }
  });
});
