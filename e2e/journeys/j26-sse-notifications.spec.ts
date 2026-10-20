import { test, expect, type BrowserContext, type Page } from "@playwright/test";
import {
  inviteMember,
  login,
  sessionAs,
  uploadFileViaAPI,
  createProjectViaAPI,
  deleteProjectViaAPI,
  setProjectWorkflowViaAPI,
} from "./helpers";

/**
 * J26 — SSE notifications: cross-tab, cross-user delivery
 *
 * The notification broker + SSE handler fires notifications to
 * the recipient's bell without waiting for the 60s polling tick. The
 * contract:
 *
 *   When user A advances a workflow step assigned to user B, user
 *   B's bell badge increments within ~2 seconds — via SSE, not the
 *   polling fallback.
 *
 * **Why a second browser context:** the contract is inherently between
 * two users + two bells. Playwright's default `{ page }` fixture gives
 * one context with one auth state, so the member needs an isolated
 * `browser.newContext()` with its own JWT, cookies, and SSE channel.
 *
 * **Architecture note:** the server's broker sends every event through
 * Postgres NOTIFY, so it reaches the bell on whichever replica holds
 * user B's SSE connection; this journey runs one server, so the publish
 * and the stream share it. The cross-replica case is covered by
 * internal/notification's two-process integration tests.
 *
 * **Locale:** sv-se (Swedish, Sweden). Distinct from j1/j2/j4/j6/
 * j25/j29/j30/j27. Mock supports `sv` natively.
 *
 * Anti-tautology proof:
 *   Mutation:    `internal/notification/service.go::publishLive` —
 *                early-return before `broker.Publish`. The notification
 *                row is still created (Step 2 is unaffected). This proof
 *                targets the SSE-delivery contract specifically: row
 *                exists but the live channel is silent.
 *   Diagnostic:  Phase D's "badge visible within 5s" assertion fails.
 *                The badge would still eventually appear via the 60s
 *                polling fallback, but that's well past the test's
 *                5s budget — the mutation is detected in well under
 *                a minute.
 *   Proven:      2026-05-07.
 */

const API = "";

let ownerProjectId: number;
let ownerJobId: number;
let memberUserId: number;
let memberContext: BrowserContext | undefined;
let memberPage: Page | undefined;

const MEMBER_EMAIL = `j26-member-${Date.now()}@test.com`;
const MEMBER_PASSWORD = "J26TestPassword123!";

test.describe.serial("J26 — SSE notifications cross-user", () => {

  // ════════════════════════════════════════════════════════════════════════
  // Phase A: Setup — invite member, build 2-step workflow with member as
  //          step 2's default assignee, upload file
  // ════════════════════════════════════════════════════════════════════════

  test("A1: owner invites member, captures memberUserId", async ({ page }) => {
    test.setTimeout(20_000);
    await login(page);

    // The member's id wires them as a workflow step's default_assignee, and
    // the afterAll removes them by it.
    ({ userId: memberUserId } = await inviteMember(page, MEMBER_EMAIL));
    expect(memberUserId, "must capture memberUserId").toBeGreaterThan(0);
  });

  test("A2: owner creates project en→sv-se with 2-step workflow (step 2 → member)", async ({ page }) => {
    test.setTimeout(15_000);
    await login(page);

    ownerProjectId = await createProjectViaAPI(page, "J26 SSE Notifications", "en", ["sv-se"]);

    // Set workflow with default_assignee on step 2 = the member.
    // When owner advances step 1, the backend's
    // `workflow.HandleAdvance` reads next.DefaultAssignee and fires
    // an EventAssignment notification for that user.
    //
    await setProjectWorkflowViaAPI(page, ownerProjectId, [
      { name: "translate", role_required: "linguist" },
      { name: "review", role_required: "linguist", default_assignee: memberUserId },
    ]);
  });

  test("A3: owner uploads file → job + step assignments materialised", async ({ page }) => {
    test.setTimeout(15_000);
    await login(page);

    const result = await uploadFileViaAPI(
      page,
      ownerProjectId,
      "j26-sse.txt",
      "Hello world.\n",
      "text/plain",
    );

    const ownerHeaders = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const jobsResp = await page.request.get(
      `${API}/api/projects/${ownerProjectId}/jobs`,
      { headers: ownerHeaders },
    );
    const jobs = (await jobsResp.json()).data;
    const job = jobs.find((j: { target_language: string }) => j.target_language === "sv-se");
    expect(job, "sv-se job must exist").toBeDefined();
    ownerJobId = job.id;
    void result; // file id not used past this point
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B: Member opens bell in SECOND browser context, SSE channel
  //          establishes, baseline confirms NO unread badge
  // ════════════════════════════════════════════════════════════════════════

  test("B: member logs in in a fresh browser context, bell mounts with 0 unread", async ({ browser }) => {
    test.setTimeout(15_000);

    // A browser session of the member's own: their token for REST calls and
    // their auth cookie for the SSE channel (EventSource only does cookie
    // auth). Sharing the owner's context would clobber the owner's session.
    ({ page: memberPage, context: memberContext } = await sessionAs(browser, MEMBER_EMAIL));

    // Register the SSE response listener BEFORE the navigation. The
    // EventSource opens during the page mount; if we awaited
    // waitForResponse AFTER goto, the headers would already be
    // received and the listener would miss the event. The pattern
    // mirrors Playwright's recommendation for any race-prone request.
    const ssePromise = memberPage.waitForResponse(
      (resp) => resp.url().includes("/api/notifications/stream") && resp.status() === 200,
      { timeout: 10000 },
    );

    await memberPage.goto("/");

    // Wait for the bell to render. This also means the page's
    // useNotificationStream hook has mounted and the EventSource is
    // connecting / connected.
    const bellButton = memberPage.getByTitle("Notifications");
    await expect(bellButton, "member's bell must mount").toBeVisible({ timeout: 10000 });

    // Sanity: NO unread badge yet. The badge `<span>` is conditionally
    // rendered only when `count > 0`. Member is fresh — invite
    // accepted, no notifications fired yet.
    const badge = bellButton.locator("span");
    await expect(badge, "bell starts with 0 unread (badge absent)").toHaveCount(0);

    // Confirm the SSE channel established. Without this, a race exists:
    // owner's advance in Phase C could fire BEFORE the member's
    // EventSource has subscribed, and the event would be dropped
    // (the broker doesn't queue for not-yet-connected subscribers).
    await ssePromise;
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C: Owner advances step 1 → step 2 starts → EventAssignment for
  //          member published to the broker → SSE fans out
  // ════════════════════════════════════════════════════════════════════════

  test("C: owner advances workflow step", async ({ page }) => {
    test.setTimeout(15_000);
    await login(page);

    const ownerHeaders = {
      Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}`,
    };
    const advanceResp = await page.request.post(
      `${API}/api/jobs/${ownerJobId}/workflow/advance`,
      { headers: ownerHeaders },
    );
    expect(advanceResp.ok(), `advance step: ${advanceResp.status()}`).toBe(true);

    // Sanity: the next-step transition reported success.
    const advanceBody = await advanceResp.json();
    expect(advanceBody.data?.status ?? advanceBody.status).toBe("advanced");
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D: Member's bell increments via SSE within 5s
  // ════════════════════════════════════════════════════════════════════════

  test("D: member's bell badge appears within 5s of owner's advance", async () => {
    test.setTimeout(15_000);
    if (!memberPage) throw new Error("memberPage not initialised by Phase B");

    const bellButton = memberPage.getByTitle("Notifications");
    const badge = bellButton.locator("span");

    // OBSERVABLE OUTCOME — the unread badge `<span>` becomes visible.
    // 5s budget: the contract is "within ~2s"; we double the
    // budget for CI flake margin while staying well under the 60s
    // polling fallback. If SSE is broken, polling would eventually
    // catch up — but not within 5s, so this assertion will fail.
    await expect(badge, "member's bell badge should appear within 5s via SSE").toBeVisible({ timeout: 5000 });
    // Badge text shows the count — should be 1 since this is the only
    // notification fired for this fresh member.
    await expect(badge, "badge should read '1'").toHaveText("1", { timeout: 1000 });
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase E: Cleanup
  // ════════════════════════════════════════════════════════════════════════

  test("E: cleanup", async ({ page }) => {
    await login(page);
    if (ownerProjectId) await deleteProjectViaAPI(page, ownerProjectId);
    if (memberPage) await memberPage.close().catch(() => {});
    if (memberContext) await memberContext.close().catch(() => {});
    // Remove the invited member, or every run leaves one more member in the org.
    const removeRes = await page.request.delete(`${API}/api/members/${memberUserId}`, {
      headers: { Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}` },
    });
    expect(removeRes.ok(), `E: removing the invited member failed: ${removeRes.status()}`).toBe(true);
  });

  test.afterAll(async ({ browser }) => {
    if (memberPage) await memberPage.close().catch(() => {});
    if (memberContext) await memberContext.close().catch(() => {});
    const page = await browser.newPage();
    try {
      await login(page);
      if (ownerProjectId) await deleteProjectViaAPI(page, ownerProjectId).catch(() => {});
      if (memberUserId) {
        await page.request.delete(`${API}/api/members/${memberUserId}`, {
          headers: { Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}` },
        }).catch(() => {});
      }
    } catch {} finally {
      await page.close();
    }
  });
});
