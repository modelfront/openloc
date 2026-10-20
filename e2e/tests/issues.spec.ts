// Issues: a manager reports a bug with files from the account menu and sees
// the whole org's issues while a linguist sees only their own; the platform
// operator filters, triages, assigns, tags, writes an internal note and asks
// a question, and the answer puts the issue back in the operator's queue; the
// reporter finds it by searching, and confirms the fix once it is resolved.
// Last, the reporter deletes it; if an earlier test failed first, the
// clean-up deletes it as the operator, with the duplicate report.

import { test, expect, type Browser, type Locator, type Page } from "@playwright/test";
import { authHeaders, login, pageAs, pickComboboxValue } from "./helpers";
import { SEED_OPERATOR_EMAIL, SEED_OPERATOR_NAME, SEED_ORG_NAME, SEED_VENDOR_EMAIL } from "../seed-identity";

// A 1×1 PNG, enough for the browser to show and the server to accept.
const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==",
  "base64",
);

test.describe.configure({ mode: "serial" });

let issueId = 0;
// The report the duplicate test files as a copy of issueId.
let copyId = 0;
// Long enough to be cut off in the lists, so the hover tooltip has something to show.
const title = `Export does nothing when the document has tables, comments and tracked changes ${Date.now()}`;

const operatorPage = (browser: Browser) => pageAs(browser, SEED_OPERATOR_EMAIL);

/** The tests share one issue, filed by the first; each later one needs it. */
const requireIssue = () => expect(issueId, "the report test ran first").toBeGreaterThan(0);

/** The support team's next PATCH of the shared issue (a triage field saved). */
const patched = (ops: Page) =>
  ops.waitForResponse(r => r.url().endsWith(`/api/platform/issues/${issueId}`) && r.request().method() === "PATCH");

/** The severity chip sits at the right end of the title cell, however long the title. */
async function expectChipAtCellEnd(row: Locator) {
  const cell = await row.locator("td").first().boundingBox();
  const chip = await row.getByTestId("issue-severity").boundingBox();
  expect(cell, "title cell").not.toBeNull();
  expect(chip, "severity chip").not.toBeNull();
  expect(cell!.x + cell!.width - (chip!.x + chip!.width)).toBeLessThan(16);
}

test.afterAll(async ({ browser }) => {
  const left = [issueId, copyId].filter(Boolean);
  if (!left.length) return;
  const page = await operatorPage(browser);
  for (const id of left) {
    const res = await page.request.delete(`/api/platform/issues/${id}`, { headers: await authHeaders(page) });
    expect(res.ok(), `delete test issue ${id}: ${res.status()}`).toBe(true);
  }
  await page.context().close();
});

test("a manager reports a bug with files from the account menu", async ({ page }) => {
  await login(page);
  await page.getByRole("button", { name: /account menu/i }).click();
  await page.getByTestId("user-menu-report-issue").click();

  const dialog = page.getByTestId("report-issue-dialog");
  await expect(dialog.getByTestId("report-issue-context")).toContainText("/projects");
  await dialog.getByTestId("report-issue-severity-critical").click();
  await pickComboboxValue(page, "report-issue-area", "editor");
  await dialog.getByTestId("report-issue-title").fill(title);
  await dialog.getByTestId("report-issue-description").fill("I click Export and nothing downloads.");
  await dialog.getByTestId("report-issue-files-input").setInputFiles([
    { name: "screen.png", mimeType: "image/png", buffer: PNG },
    { name: "console.log", mimeType: "text/plain", buffer: Buffer.from("TypeError: export failed\n") },
  ]);
  await expect(dialog.getByTestId("report-issue-files-file")).toHaveCount(2);
  // The picked image goes into the text at the cursor; the log stays a file.
  await expect(dialog.getByTestId("report-issue-description")).toHaveValue(
    "I click Export and nothing downloads.\n![screen.png](upload:0)");

  const created = page.waitForResponse(r => r.url().endsWith("/api/issues") && r.request().method() === "POST");
  await dialog.getByTestId("report-issue-submit").click();
  const res = await created;
  expect(res.status()).toBe(201);
  const issue = (await res.json()).data;
  issueId = issue.id;
  expect(issue).toMatchObject({ kind: "bug", severity: "critical", area: "editor" });
  expect(issue.attachments.map((a: { filename: string }) => a.filename)).toEqual(["screen.png", "console.log"]);
  expect(issue.description).toBe(`I click Export and nothing downloads.\n![screen.png](attachment:${issue.attachments[0].id})`);

  await page.goto("/issues");
  const row = page.getByTestId(`issue-row-${issueId}`);
  await expect(row).toContainText(title);
  await expect(row.getByTestId("issue-severity")).toHaveText("Critical");
  await expect(row.getByTestId("issue-status")).toHaveText("Open");
  // A title shorter than the cell (the seeded demo bug) still has its chip at the end.
  await expectChipAtCellEnd(page.getByRole("row").filter({ hasText: "Exported DOCX loses the table borders" }));
});

test("a manager sees every member's issues; a linguist only their own", async ({ page, browser }) => {
  requireIssue();
  const linguistIssue = "QA flags a number that is the same in both languages"; // seeded for the linguist

  await login(page);
  await page.getByRole("button", { name: /account menu/i }).click();
  await page.getByTestId("user-menu-reported-issues").click();
  await expect(page).toHaveURL(/\/issues$/);
  await expect(page.getByTestId(`issue-row-${issueId}`)).toBeVisible();
  const colleague = page.getByRole("row").filter({ hasText: linguistIssue });
  await expect(colleague.getByTestId("issue-reporter")).toContainText(SEED_VENDOR_EMAIL);

  // Most recently updated first; the Updated heading flips it. The order is
  // checked against the older seeded report, since other specs may file newer
  // reports in the same org meanwhile.
  const rows = page.locator('tbody tr[data-testid^="issue-row-"]');
  const ours = `issue-row-${issueId}`;
  const seeded = await colleague.getAttribute("data-testid");
  const oursFirst = async () => {
    const order = await rows.evaluateAll(els => els.map(e => e.getAttribute("data-testid")));
    return order.includes(ours) && order.includes(seeded) ? order.indexOf(ours) < order.indexOf(seeded) : undefined;
  };
  await expect.poll(oursFirst).toBe(true);
  const listed = page.waitForResponse(r => r.url().includes("/api/issues?") && r.url().includes("sort=updated"));
  await page.getByTestId("sort-updated").click();
  await listed;
  await expect(page).toHaveURL(/sort=updated/);
  await expect(page.getByRole("columnheader", { name: /Updated/ })).toHaveAttribute("aria-sort", "ascending");
  await expect.poll(oursFirst).toBe(false);

  const linguist = await pageAs(browser, SEED_VENDOR_EMAIL);
  await linguist.goto("/issues?status=");
  await expect(linguist.getByRole("row").filter({ hasText: linguistIssue })).toBeVisible();
  await expect(linguist.getByTestId(`issue-row-${issueId}`)).toHaveCount(0);
  await expect(linguist.getByTestId("issue-reporter")).toHaveCount(0);
  const res = await linguist.request.get(`/api/issues/${issueId}`, { headers: await authHeaders(linguist) });
  expect(res.status(), "a linguist opening a colleague's issue").toBe(404);
  await linguist.context().close();
});

test("the operator finds it under Reported issues and reads the files", async ({ browser }) => {
  requireIssue();
  const ops = await operatorPage(browser);
  await ops.goto("/projects");
  await ops.getByRole("button", { name: /account menu/i }).click();
  await ops.getByTestId("user-menu-reported-issues").click();
  const row = ops.getByTestId(`issue-row-${issueId}`);
  await expect(row.getByText(title)).toBeVisible();
  await expect(row.getByLabel("Bug")).toBeVisible();
  await expect(row.getByTestId("issue-severity")).toHaveText("Critical");
  await expectChipAtCellEnd(row);
  await expect(row.locator("td").last()).toBeInViewport();
  await row.getByText(title).hover();
  await expect(ops.locator('[data-slot="tooltip-content"]')).toHaveText(title);
  await row.click();

  await expect(ops.getByTestId("issue-description")).toHaveText("I click Export and nothing downloads.");
  // The image shows inside the description, where the reporter placed it.
  await expect(ops.getByTestId("issue-description").getByTestId("issue-image")).toBeVisible();
  await expect(ops.getByTestId("issue-image")).toHaveCount(1);
  const download = ops.waitForEvent("download");
  await ops.getByTestId("issue-file").click();
  expect((await download).suggestedFilename()).toBe("console.log");
  await ops.context().close();
});

test("the operator filters by organization, kind, severity and area", async ({ browser }) => {
  // Six full page loads (sign-in and five filtered lists), about 3s each on a
  // CI node with two workers: the default budget ends at the last click.
  test.slow();
  requireIssue();
  const ops = await operatorPage(browser);
  const orgs = await ops.request.get("/api/platform/issues/orgs", { headers: await authHeaders(ops) });
  const list: { id: number; name: string }[] = (await orgs.json()).data;
  const ours = list.find(o => o.name === SEED_ORG_NAME);
  expect(ours, `${SEED_ORG_NAME} has issues`).toBeTruthy();
  const row = ops.getByTestId(`issue-row-${issueId}`);
  // The list has rendered: a row, or the empty message.
  const rendered = ops.getByText("No issues match these filters.").or(ops.locator('tbody tr[data-testid^="issue-row-"]').first());
  for (const [query, shown] of [
    [`org=${ours!.id}&kind=bug&severity=critical&area=editor`, true],
    [`org=${ours!.id + 100000}`, false],
    ["kind=feature_request", false],
    ["severity=minor,major", false],
    ["area=tm", false],
  ] as const) {
    // The list request carries every filter of the page's address.
    const wanted = [...new URLSearchParams(query)];
    const listed = ops.waitForResponse(r => {
      const url = new URL(r.url());
      return url.pathname === "/api/platform/issues" && r.request().method() === "GET" &&
        wanted.every(([k, v]) => url.searchParams.get(k) === v);
    });
    await ops.goto(`/platform/issues?${query}`);
    await listed;
    await expect(rendered).toBeVisible();
    if (shown) await expect(row).toBeVisible();
    else await expect(row).toHaveCount(0);
  }

  // Clear filters goes back to the default list, which holds the open report.
  await ops.getByTestId("issues-clear-filters").click();
  await expect(ops).toHaveURL(/\/platform\/issues$/);
  await expect(row).toBeVisible();
  await ops.context().close();
});

test("the operator corrects the severity and the area", async ({ browser }) => {
  requireIssue();
  const ops = await operatorPage(browser);
  await ops.goto(`/platform/issues/${issueId}`);
  const triaged = patched(ops);
  await pickComboboxValue(ops, "issue-severity-select", "blocker");
  expect((await triaged).status()).toBe(200);
  const summary = ops.getByTestId("issue-summary");
  await expect(summary.getByTestId("issue-severity")).toHaveText("Blocker");
  const moved = patched(ops);
  await pickComboboxValue(ops, "issue-area-select", "tm");
  expect((await moved).status()).toBe(200);
  await expect(summary.getByTestId("issue-area")).toHaveText("Translation memory");
  await expect(summary.getByTestId("issue-severity")).toHaveText("Blocker");
  await ops.context().close();
});

test("the operator assigns, prioritizes, tags and writes a note; the reporter sees only the assignee", async ({ page, browser }) => {
  requireIssue();
  const ops = await operatorPage(browser);
  const team = await ops.request.get("/api/platform/issues/assignees", { headers: await authHeaders(ops) });
  const me = ((await team.json()).data as { id: number; email: string }[]).find(p => p.email === SEED_OPERATOR_EMAIL);
  expect(me, "the seed operator is on the support team").toBeTruthy();
  await ops.goto(`/platform/issues/${issueId}`);

  let saved = patched(ops);
  await pickComboboxValue(ops, "issue-assignee-select", String(me!.id));
  expect((await saved).status()).toBe(200);
  await expect(ops.getByTestId("issue-assignee")).toHaveText(`Assigned to ${SEED_OPERATOR_NAME}`);
  saved = patched(ops);
  await pickComboboxValue(ops, "issue-priority-select", "high");
  const prioritized = await saved;
  expect(prioritized.status()).toBe(200);
  expect(prioritized.request().postDataJSON()).toEqual({ priority: "high" });
  await expect(ops.getByTestId("issue-priority-select")).toContainText("High");
  await ops.getByTestId("issue-tags-select").click();
  await ops.getByPlaceholder("Search or add a tag").fill("e2e-secret-tag");
  saved = patched(ops);
  await ops.getByTestId("issue-tags-select-action-create-option").click();
  expect((await saved).status()).toBe(200);
  await ops.keyboard.press("Escape");
  await expect(ops.getByTestId("issue-tags-select")).toContainText("e2e-secret-tag");

  await ops.getByTestId("issue-composer-note").click();
  await ops.getByTestId("issue-comment-input").fill("Internal: the exporter drops tables.");
  const noted = ops.waitForResponse(r => r.url().endsWith(`/api/platform/issues/${issueId}/notes`));
  await ops.getByTestId("issue-comment-send").click();
  expect((await noted).status()).toBe(201);
  await expect(ops.locator('[data-testid="issue-comment"][data-internal="true"]').last()).toContainText("Internal: the exporter drops tables.");
  await ops.context().close();

  await login(page);
  const loaded = page.waitForResponse(r => r.url().endsWith(`/api/issues/${issueId}`) && r.request().method() === "GET");
  await page.goto(`/issues/${issueId}`);
  const body = await (await loaded).text();
  for (const secret of ["e2e-secret-tag", "the exporter drops tables", '"priority"', '"tags"', '"internal"']) {
    expect(body, `the org's API carries ${secret}`).not.toContain(secret);
  }
  await expect(page.getByTestId("issue-assignee")).toHaveText(`Assigned to ${SEED_OPERATOR_NAME}`);
  await expect(page.getByTestId("issue-conversation")).toContainText(`assigned it to ${SEED_OPERATOR_NAME}`);
  await expect(page.getByTestId("issue-comment-internal")).toHaveCount(0);
  await expect(page.getByText("the exporter drops tables")).toHaveCount(0);
});

test("the operator asks a question and the reporter sees it", async ({ page, browser }) => {
  requireIssue();
  const ops = await operatorPage(browser);
  await ops.goto(`/platform/issues/${issueId}`);
  await pickComboboxValue(ops, "issue-status-select", "needs_info");
  await ops.getByTestId("issue-status-note").fill("Which file format do you export?");
  const updated = ops.waitForResponse(r => r.url().endsWith(`/api/platform/issues/${issueId}/status`));
  await ops.getByTestId("issue-status-save").click();
  expect((await updated).status()).toBe(200);
  await ops.context().close();

  await login(page);
  await page.goto(`/issues/${issueId}`);
  await expect(page.getByTestId("issue-status").first()).toHaveText("Needs info");
  await expect(page.getByTestId("issue-conversation")).toContainText("Which file format do you export?");
  await page.getByTitle("Notifications").click();
  await expect(page.getByText(`Update on your issue: ${title}`)).toBeVisible();
});

test("the reporter's answer puts the issue back in the operator's queue as Open", async ({ page, browser }) => {
  requireIssue();
  await login(page);
  await page.goto(`/issues/${issueId}`);
  await page.getByTestId("issue-comment-input").fill("A DOCX file.");
  await page.getByTestId("issue-comment-files-input").setInputFiles({ name: "docx.png", mimeType: "image/png", buffer: PNG });
  const replied = page.waitForResponse(r => r.url().endsWith(`/api/issues/${issueId}/comments`));
  await page.getByTestId("issue-comment-send").click();
  expect((await replied).status()).toBe(201);
  await expect(page.getByTestId("issue-status").first()).toHaveText("Open");
  await expect(page.getByTestId("issue-comment").last().getByTestId("issue-comment-body").getByTestId("issue-image")).toBeVisible();

  const ops = await operatorPage(browser);
  await ops.goto("/platform/issues");
  const row = ops.getByTestId(`issue-row-${issueId}`);
  await expect(row.getByTestId("issue-status")).toHaveText("Open");
  await expect(row.getByTestId("issue-waiting-on")).toHaveText("Support team");
  await ops.context().close();
});

// One scroll area: the form scrolls between the pinned header and footer, so
// Send report is on screen without scrolling, and the description box grows
// with its text instead of scrolling on its own.
test("a long description keeps Send report on screen", async ({ browser }) => {
  const page = await pageAs(browser, SEED_VENDOR_EMAIL);
  await page.goto("/issues");
  await page.getByTestId("issues-report").click();
  const description = page.getByTestId("report-issue-description");
  await description.fill(Array(60).fill("A line of the description.").join("\n\n"));
  const send = page.getByTestId("report-issue-submit");
  const box = await send.boundingBox();
  expect(box, "Send report rendered").not.toBeNull();
  expect(box!.y + box!.height, "Send report inside the window, no scrolling").toBeLessThanOrEqual(page.viewportSize()!.height);
  const scrolls = (el: Element) => el.scrollHeight > el.clientHeight;
  expect(await description.evaluate(scrolls), "description box has no scrollbar of its own").toBe(false);
  expect(await page.locator("#report-issue-form").evaluate(scrolls), "the form scrolls instead").toBe(true);
  await page.context().close();
});

test("the operator marks another report as a duplicate, and its reporter follows the link", async ({ page, browser }) => {
  requireIssue();
  await login(page);
  const made = await page.request.post("/api/issues", {
    headers: await authHeaders(page),
    multipart: { kind: "bug", severity: "minor", area: "editor", title: `Same export problem ${Date.now()}`, description: "Found it again." },
  });
  expect(made.status()).toBe(201);
  copyId = (await made.json()).data.id;

  const ops = await operatorPage(browser);
  await ops.goto(`/platform/issues/${copyId}`);
  await pickComboboxValue(ops, "issue-status-select", "duplicate");
  await expect(ops.getByTestId("issue-status-save")).toHaveAttribute("aria-disabled", "true");
  // The original is found by words from its title, never by typing its number.
  await ops.getByTestId("issue-duplicate-of").click();
  const searched = ops.waitForResponse(r => r.url().includes("/api/platform/issues?") && r.url().includes("tracked+changes"));
  await ops.getByPlaceholder("Search by title or number").fill(title.split(" ").slice(-3).join(" "));
  await searched;
  const original = ops.getByTestId(`issue-duplicate-of-option-${issueId}`);
  await expect(original).toContainText(`#${issueId} Export does nothing`);
  await original.click();
  await expect(ops.getByTestId("issue-duplicate-of")).toContainText(`#${issueId}`);
  const marked = ops.waitForResponse(r => r.url().endsWith(`/api/platform/issues/${copyId}/status`));
  await ops.getByTestId("issue-status-save").click();
  const res = await marked;
  expect(res.status()).toBe(200);
  expect(res.request().postDataJSON()).toMatchObject({ status: "duplicate", duplicate_of: issueId });
  await ops.context().close();

  await page.goto(`/issues/${copyId}`);
  await expect(page.getByTestId("issue-summary").getByTestId("issue-status")).toHaveText("Duplicate");
  await expect(page.getByTestId("issue-duplicate-link")).toHaveAttribute("href", `/issues/${issueId}`);

  // The support team navigates both ways: the original lists its duplicate.
  const ops2 = await operatorPage(browser);
  await ops2.goto(`/platform/issues/${issueId}`);
  await ops2.getByTestId("issue-duplicates").getByRole("link", { name: new RegExp(`#${copyId} Same export problem`) }).click();
  await expect(ops2).toHaveURL(new RegExp(`/platform/issues/${copyId}$`));
  await expect(ops2.getByTestId("issue-duplicate-link")).toHaveAttribute("href", `/platform/issues/${issueId}`);
  await expect(ops2.getByTestId("issue-duplicate-link")).toContainText(title.slice(0, 30));
  await ops2.context().close();
});

test("the reporter changes the kind of their report, and the change shows in the conversation", async ({ page, browser }) => {
  expect(copyId, "the duplicate test ran first").toBeGreaterThan(0);
  await login(page);
  await page.goto(`/issues/${copyId}`);
  const changed = page.waitForResponse(r => r.url().endsWith(`/api/issues/${copyId}/kind`));
  await pickComboboxValue(page, "issue-kind-select", "enhancement");
  const res = await changed;
  expect(res.status()).toBe(200);
  expect((await res.json()).data.issue).toMatchObject({ kind: "enhancement", severity: null });
  // The reporter sees the kind change, and never the severity entry it adds.
  const mine = page.getByTestId("issue-comment");
  await expect(mine.filter({ hasText: "changed the kind to Enhancement" })).toHaveCount(1);
  await expect(mine.filter({ hasText: "cleared the severity" })).toHaveCount(0);
  await expect(page.getByTestId("issue-severity")).toHaveCount(0);

  const ops = await operatorPage(browser);
  await ops.goto(`/platform/issues/${copyId}`);
  await expect(ops.getByTestId("issue-kind")).toHaveText("Enhancement");
  // The support team sees the kind change as the org does, and the severity it
  // cleared as an internal entry.
  const entries = ops.getByTestId("issue-comment");
  const kindEntry = entries.filter({ hasText: "changed the kind to Enhancement" });
  await expect(kindEntry).toHaveCount(1);
  await expect(kindEntry.getByTestId("issue-comment-internal")).toHaveCount(0);
  const severityEntry = entries.filter({ hasText: "cleared the severity" });
  await expect(severityEntry).toHaveCount(1);
  await expect(severityEntry.getByTestId("issue-comment-internal")).toBeVisible();
  await ops.context().close();
});

test("the reporter finds the issue by searching its description", async ({ page }) => {
  requireIssue();
  await login(page);
  await page.goto("/issues");
  const row = page.getByTestId(`issue-row-${issueId}`);
  await expect(row).toBeVisible();
  const searched = (q: string) => page.waitForResponse(r => {
    const url = new URL(r.url());
    return url.pathname === "/api/issues" && url.searchParams.get("q") === q;
  });
  let listed = searched("nothing downloads");
  await page.getByTestId("issues-search").fill("nothing downloads");
  await listed;
  await expect(row).toBeVisible();
  listed = searched("no issue says this");
  await page.getByTestId("issues-search").fill("no issue says this");
  await listed;
  await expect(page.getByText("No issues match this search.")).toBeVisible();
  await expect(row).toHaveCount(0);
});

test("the reporter confirms the fix of the resolved issue", async ({ page, browser }) => {
  requireIssue();
  const ops = await operatorPage(browser);
  const resolved = await ops.request.post(`/api/platform/issues/${issueId}/status`, {
    headers: await authHeaders(ops), data: { status: "resolved", note: "Fixed in the exporter." },
  });
  expect(resolved.status()).toBe(200);
  await ops.context().close();

  await login(page);
  await page.goto(`/issues/${issueId}`);
  const confirmed = page.waitForResponse(r => r.url().endsWith(`/api/issues/${issueId}/confirm`));
  await page.getByTestId("issue-confirm-fixed").click();
  expect((await confirmed).status()).toBe(200);
  await expect(page.getByTestId("issue-confirmed")).toHaveText("Fix confirmed");
  await expect(page.getByTestId("issue-confirm-fix")).toHaveCount(0);
  await page.reload();
  await expect(page.getByTestId("issue-confirmed")).toHaveText("Fix confirmed");
});

test("the reporter deletes the issue from the list, and the operator no longer sees it", async ({ page, browser }) => {
  requireIssue();
  await login(page);
  // The issue is resolved by now, so the list shows every status.
  await page.goto("/issues?status=");
  const row = page.getByTestId(`issue-row-${issueId}`);
  await row.hover();
  await row.getByTestId(`issue-delete-${issueId}`).click();
  const deleted = page.waitForResponse(r => r.url().endsWith(`/api/issues/${issueId}`) && r.request().method() === "DELETE");
  await page.getByRole("button", { name: "Delete", exact: true }).click();
  expect((await deleted).status()).toBe(204);
  await expect(row).toHaveCount(0);
  await expect(page).toHaveURL(/\/issues\?status=$/);

  const ops = await operatorPage(browser);
  const res = await ops.request.get(`/api/platform/issues/${issueId}`, { headers: await authHeaders(ops) });
  expect(res.status()).toBe(404);
  await ops.context().close();
  issueId = 0;
});
