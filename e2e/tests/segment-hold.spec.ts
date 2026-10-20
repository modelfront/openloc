import { test, expect, type Browser, type BrowserContext, type Page } from "@playwright/test";
import { authHeaders, createIsolatedCATJob, inviteMember, login, segmentEditor, sessionAs } from "./helpers";

// Segment holds with two people in one job: a segment one user has open is
// read-only for the other, with the holder's name, until the holder moves on;
// then it turns editable and shows the holder's saved text. The other user's
// editor learns of each change from the server's hold stream, not its poll.

const API = process.env.API_BASE ?? "";
// Far below the 30 s poll an editor keeps while its hold stream is live, so
// only the stream can meet it.
const LIVE_MS = 3_000;

const path = (url: string) => url.split("?")[0];
const isHoldPut = (url: string, method: string) => /\/api\/jobs\/\d+\/holds\/\d+$/.test(path(url)) && method === "PUT";
const isHoldDelete = (url: string, method: string) => /\/api\/jobs\/\d+\/holds\/\d+$/.test(path(url)) && method === "DELETE";
const isHoldStream = (url: string) => /\/api\/jobs\/\d+\/holds\/stream$/.test(path(url));

// What the running test created, deleted in afterEach with the owner's
// headers read right after login and the `request` fixture: the hook has its
// own time budget and reads nothing from the page. Best-effort, so a failed
// cleanup never changes a test's result.
let ownerHeaders: Record<string, string> = {};
let projectId: number | null = null;
let memberId: number | null = null;
let memberContext: BrowserContext | null = null;

test.afterEach(async ({ request }) => {
  await memberContext?.close().catch(() => {});
  memberContext = null;
  if (projectId) await request.delete(`${API}/api/projects/${projectId}`, { headers: ownerHeaders }).catch(() => {});
  if (memberId) await request.delete(`${API}/api/members/${memberId}`, { headers: ownerHeaders }).catch(() => {});
  projectId = null;
  memberId = null;
});

async function signInOwner(page: Page): Promise<string> {
  await login(page);
  ownerHeaders = await authHeaders(page);
  const me = await page.request.get(`${API}/api/auth/me`, { headers: ownerHeaders });
  expect(me.ok(), `/api/auth/me: ${me.status()}`).toBe(true);
  const name: string = (await me.json()).data.name;
  expect(name, "the seed owner has a name, so the hold shows it").toBeTruthy();
  return name;
}

// Opens the job's editor as `email` in a browser session of its own and
// returns once its hold stream is live: the editor marks it with
// data-hold-stream="live" when the stream says `connected`, and only then
// slows its hold poll to 30 s.
async function openEditorAs(browser: Browser, email: string, job: { projectId: number; jobId: number }): Promise<Page> {
  const session = await sessionAs(browser, email);
  memberContext = session.context;
  const page = session.page;
  const [stream] = await Promise.all([
    page.waitForResponse(r => isHoldStream(r.url()), { timeout: 10_000 }),
    page.goto(`/projects/${job.projectId}/jobs/${job.jobId}/edit`),
  ]);
  expect(stream.status(), "the hold stream is open").toBe(200);
  await expect(page.locator('[data-hold-stream="live"]'), "the hold stream said connected").toBeAttached({ timeout: 10_000 });
  return page;
}

test("a segment someone has open is read-only for a second user, named, until they move on", async ({ page, browser }) => {
  test.setTimeout(90_000);
  const ownerName = await signInOwner(page);
  const memberEmail = `hold-${Date.now()}@test.com`;
  memberId = (await inviteMember(page, memberEmail)).userId;
  const job = await createIsolatedCATJob(page, {
    fileName: `hold-${Date.now()}.txt`,
    content: "first line to hold\nsecond line\nthird line\n",
    onCreated: (id) => (projectId = id),
  });

  // The owner opens segment 1.
  await job.navigateToEditor(/first line to hold/);
  const ownerCells = page.locator("[data-translation-cell]");
  const ownerEditor = segmentEditor(page).first();
  const [ownerHold] = await Promise.all([
    page.waitForResponse(r => isHoldPut(r.url(), r.request().method())),
    ownerCells.nth(0).click(),
  ]);
  expect(ownerHold.status(), "the owner takes the free segment").toBe(200);
  await expect(ownerEditor).toBeFocused();

  // The second user opens the same job: segment 1 shows who has it, and
  // opening it gives a read-only editor with the holder's name.
  const other = await openEditorAs(browser, memberEmail, job);
  await expect(other.getByText(/first line to hold/).first()).toBeVisible({ timeout: 10_000 });
  await expect(other.getByTestId("segment-held-icon")).toBeVisible({ timeout: 10_000 });
  await expect(other.getByLabel(`${ownerName} is editing this segment`)).toBeVisible();

  const otherCells = other.locator("[data-translation-cell]");
  const otherEditor = segmentEditor(other).first();
  const [refused] = await Promise.all([
    other.waitForResponse(r => isHoldPut(r.url(), r.request().method())),
    otherCells.nth(0).click(),
  ]);
  expect(refused.status(), "the second user is refused").toBe(409);
  await expect(other.getByTestId("segment-held-notice")).toHaveText(`${ownerName} is editing this segment`);
  await expect(otherEditor).toHaveAttribute("contenteditable", "false");

  // The owner types and moves on: the move flushes the draft (typed just now,
  // so the 3 s autosave has not taken it) and the hold moves to segment 2.
  await ownerEditor.fill("owner's text");
  await Promise.all([
    page.waitForResponse(r => /\/translations\/\d+\/draft$/.test(r.url().split("?")[0]) && r.request().method() === "PUT"),
    page.waitForResponse(r => isHoldPut(r.url(), r.request().method())),
    ownerCells.nth(1).click(),
  ]);

  // The stream tells the second user the owner moved on: they take segment 1
  // at once, see the owner's text, and can edit.
  await expect(other.getByTestId("segment-held-notice")).toBeHidden({ timeout: LIVE_MS });
  await expect(otherEditor).toHaveAttribute("contenteditable", "true");
  await expect(otherEditor).toHaveText("owner's text");

  // And the owner now sees the second user on segment 1.
  await expect(page.getByTestId("segment-held-icon")).toBeVisible({ timeout: 10_000 });
});

test("a segment someone opens shows as held for the others at once, and free once they close it", async ({ page, browser }) => {
  test.setTimeout(90_000);
  await signInOwner(page);
  const memberEmail = `hold-live-${Date.now()}@test.com`;
  memberId = (await inviteMember(page, memberEmail)).userId;
  const job = await createIsolatedCATJob(page, {
    fileName: `hold-live-${Date.now()}.txt`,
    content: "first line to hold\nsecond line\n",
    onCreated: (id) => (projectId = id),
  });

  // The member has the job open first.
  const other = await openEditorAs(browser, memberEmail, job);
  await expect(other.getByText(/first line to hold/).first()).toBeVisible({ timeout: 10_000 });
  const heldIcons = other.getByTestId("segment-held-icon");
  await expect(heldIcons).toHaveCount(0);

  // The owner opens segment 1: the member sees it held at once.
  await job.navigateToEditor(/first line to hold/);
  const [ownerHold] = await Promise.all([
    page.waitForResponse(r => isHoldPut(r.url(), r.request().method())),
    page.locator("[data-translation-cell]").nth(0).click(),
  ]);
  expect(ownerHold.status(), "the owner takes the free segment").toBe(200);
  await expect(heldIcons).toHaveCount(1, { timeout: LIVE_MS });

  // The owner closes it: free for the member at once.
  const [release] = await Promise.all([
    page.waitForResponse(r => isHoldDelete(r.url(), r.request().method())),
    page.keyboard.press("Escape"),
  ]);
  expect(release.status(), "closing the segment releases it").toBe(204);
  await expect(heldIcons).toHaveCount(0, { timeout: LIVE_MS });
});
