// The assistant reports a bug: it asks for a screenshot, which the user takes
// and sends; it asks the user to repeat the steps, which the browser records;
// then its report opens in the form, which sends the report with the
// screenshot, the action trail and the recording. The support team sees them
// on the issue.
//
// The chat stream is mocked at the network boundary (as in chat.spec.ts): the
// seed connects a fake model key. Everything else is real: the capture, the
// upload, the report form, the issue routes. A check the browser makes up is
// dropped; a real one is signed in the assistant's preview, which the Go
// integration tests drive (TestIntegrationChatIssueCheckTokenGoesToTheBrowserOnly).

import { test, expect, type Browser, type Locator, type Page } from "@playwright/test";
import { authHeaders, login, pageAs } from "./helpers";
import { SEED_OPERATOR_EMAIL } from "../seed-identity";

test.describe.configure({ mode: "serial" });
// Each test drives two sessions, renders the page to an image and uploads it.
test.beforeEach(() => test.setTimeout(30_000));

const created: number[] = [];

const sse = (...events: string[]) => [...events, 'event: done\ndata: {"conversation_id":7}', ""].join("\n\n");
const action = (payload: Record<string, unknown>) =>
  `event: client_action\ndata: ${JSON.stringify({ type: "client_action", ...payload })}`;
const text = (t: string) => `event: text\ndata: ${JSON.stringify({ type: "text", text: t })}`;

const operatorPage = (browser: Browser) => pageAs(browser, SEED_OPERATOR_EMAIL);

/** The report card the assistant's stream ends with. */
function reportCard(args: Record<string, unknown>, verification?: string) {
  const confirm = {
    tool: "openloc_report_issue", args, summary: `Send “${args.title}” to the support team.`,
    token: "1790000000.unused", expires_at: "2026-12-31T00:00:00Z", verification_token: verification,
  };
  return `event: confirm_required\ndata: ${JSON.stringify({ type: "confirm_required", tool: confirm.tool, confirm })}`;
}

/** The value of a text field in a multipart form body. */
const formField = (body: string, name: string) => body.match(new RegExp(`name="${name}"\r\n\r\n([^\r]*)`))?.[1];

/**
 * Opens the assistant's report in the form, runs `check` on the open form,
 * sends it, and returns the new issue as the reporter's org sees it, with the
 * form body the browser sent.
 */
async function sendFromForm(
  page: Page, check?: (dialog: Locator) => Promise<void>,
): Promise<{ issue: { id: number } & Record<string, unknown>; sent: string }> {
  await page.getByTestId("chat-confirm-review").click();
  const dialog = page.getByTestId("report-issue-dialog");
  await expect(dialog).toBeVisible();
  await check?.(dialog);
  const posted = page.waitForResponse(r => r.url().endsWith("/api/issues") && r.request().method() === "POST");
  await dialog.getByTestId("report-issue-submit").click();
  const res = await posted;
  expect(res.status()).toBe(201);
  const issue = (await res.json()).data;
  created.push(issue.id);
  return { issue, sent: res.request().postDataBuffer()?.toString("latin1") ?? "" };
}

test.afterAll(async ({ browser }) => {
  const ops = await operatorPage(browser);
  for (const id of created) {
    const res = await ops.request.delete(`/api/platform/issues/${id}`, { headers: await authHeaders(ops) });
    expect(res.ok(), `delete test issue ${id}: ${res.status()}`).toBe(true);
  }
  await ops.context().close();
});

/** A token the browser made up: the server keeps no check with it. */
const FORGED = "4102444800.bm90LWEtcmVhbC1zaWduYXR1cmU";

test("the assistant asks for a screenshot, and its report reaches the support team with it", async ({ page, browser }) => {
  await login(page);
  const title = `Grid stays empty ${Date.now()}`;
  const args = {
    kind: "bug", severity: "major", area: "editor", title, description: "The grid stays empty after the upload.",
    verification: "verified", verification_note: "The screenshot shows the empty grid.",
  };

  const sends: Record<string, unknown>[] = [];
  await page.route("**/api/chat", async route => {
    sends.push(route.request().postDataJSON());
    const body = sends.length === 1
      ? sse(text("Oh no! Could you add a screenshot?"), action({ action: "ask_screenshot", reason: "So the team sees the empty grid." }))
      : sse(text("Got it. Have a look in the form."),
        reportCard({ ...args, attachment_upload_ids: [sends[1].screenshot_upload_id] }, FORGED));
    await route.fulfill({ status: 200, contentType: "text/event-stream", body });
  });
  await page.getByTestId("chat-launcher").click();
  await page.getByTestId("chat-input").fill("the grid is empty");
  await page.getByTestId("chat-send").click();

  await expect(page.getByTestId("chat-ask-screenshot")).toContainText("So the team sees the empty grid.");
  await page.getByTestId("chat-screenshot-take").click();
  await expect(page.getByTestId("chat-screenshot-preview")).toBeVisible();
  expect(sends).toHaveLength(1);
  const uploaded = page.waitForResponse(r => r.url().endsWith("/api/chat/uploads") && r.request().method() === "POST");
  await page.getByTestId("chat-screenshot-send").click();
  expect((await uploaded).status()).toBe(201);
  await expect.poll(() => sends.length).toBe(2);
  expect(sends[1]).toMatchObject({ message: "Here is the screenshot.", screenshot_upload_id: expect.any(String) });

  // The form attaches the screenshot and shows the recent actions; the reporter sees no check.
  const { issue: { id }, sent } = await sendFromForm(page, async dialog => {
    await expect(dialog.getByTestId("report-issue-files-file")).toHaveCount(1);
    await expect(dialog.getByTestId("report-issue-trail")).toBeVisible();
    await expect(dialog).not.toContainText("Verified");
  });
  expect(formField(sent, "verification")).toBe("verified");
  expect(formField(sent, "verification_token")).toBe(FORGED);

  const ops = await operatorPage(browser);
  await ops.goto(`/platform/issues/${id}`);
  await expect(ops.getByTestId("issue-attachments").getByTestId("issue-image")).toHaveCount(1);
  await ops.getByTestId("issue-trail-toggle").click();
  await expect(ops.getByTestId("issue-trail-events")).toContainText("Opened /projects");
  // The form sent the forged token, and the server dropped it: no check is stored.
  await expect(ops.getByTestId("issue-check")).toHaveCount(0);
  await ops.context().close();
});

test("the user repeats the steps; the assistant hears only that a recording is attached", async ({ page, browser }) => {
  await login(page);
  const title = `Search breaks ${Date.now()}`;
  const typed = `typed while recording ${Date.now()}`;
  const args = {
    kind: "bug", severity: "minor", area: "other", title, description: "Searching breaks the list.",
  };

  const sends: Record<string, unknown>[] = [];
  await page.route("**/api/chat", async route => {
    sends.push(route.request().postDataJSON());
    const body = sends.length === 1
      ? sse(text("Could you do it once more while I record?"), action({ action: "ask_repeat", reason: "So the team sees each step." }))
      : sse(text("Thanks! Have a look in the form."), reportCard(args));
    await route.fulfill({ status: 200, contentType: "text/event-stream", body });
  });
  await page.getByTestId("chat-launcher").click();
  await page.getByTestId("chat-input").fill("search breaks sometimes");
  await page.getByTestId("chat-send").click();

  // Nothing of the recording goes to the assistant: no upload, no screenshot on the next message.
  const uploads: string[] = [];
  page.on("request", r => { if (r.method() === "POST" && r.url().endsWith("/api/chat/uploads")) uploads.push(r.url()); });
  await page.getByTestId("chat-repeat-start").click();
  await expect(page.getByTestId("chat-recording-banner")).toContainText("Do it again");
  await expect(page.getByTestId("chat-panel")).toBeHidden();
  const search = page.getByTestId("project-search");
  await search.fill(typed);
  await page.evaluate(() => fetch("/api/no-such-route-for-the-recording"));
  await page.getByTestId("chat-recording-done").click();

  await expect.poll(() => sends.length).toBe(2);
  expect(sends[1].message).toBe("I repeated the steps. The recording is attached to the report for the support team.");
  expect(sends[1].screenshot_upload_id).toBeUndefined();
  expect(uploads).toEqual([]);
  expect(JSON.stringify(sends[1])).not.toContain(typed);

  // The recording and the screenshot taken at its first error go with the report.
  const { issue: { id } } = await sendFromForm(page, async dialog => {
    await dialog.getByTestId("report-issue-recording-toggle").click();
    await expect(dialog.getByTestId("report-issue-recording-events")).toContainText(typed);
    await expect(dialog.getByTestId("report-issue-files-file")).toHaveText(/recording-first-error\.jpg/);
  });
  const ops = await operatorPage(browser);
  await ops.goto(`/platform/issues/${id}`);
  await expect(ops.getByTestId("issue-attachments").getByTestId("issue-image")).toHaveCount(1);
  await ops.getByTestId("issue-recording-toggle").click();
  const steps = ops.getByTestId("issue-recording-events");
  await expect(steps).toContainText(typed);
  await expect(steps).toContainText("GET /api/no-such-route-for-the-recording failed (404)");
  await ops.context().close();
});
