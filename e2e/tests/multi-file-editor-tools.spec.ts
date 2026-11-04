import { test, expect, type Page } from "@playwright/test";
import { authHeaders, createIsolatedCATJob, login, seedDrafts, uploadFileViaAPI } from "./helpers";

// The single-file tools in the multi-file editor, with two files open: Find &
// replace, Run QA, Spell check, Split and Join, and track changes each reach
// the rows of both files, every request going to its row's own job.

const FILE_A = "Order 42 items\nSave the file";
const FILE_B = "Press the button and wait for the light\nSave the file now";

interface Row { id: number; segment_id: number; active_text: string | null }
interface Scope {
  projectId: number;
  jobA: number;
  jobB: number;
  fileB: number;
  rowsA: Row[];
  rowsB: Row[];
}

let headers: Record<string, string> = {};
let cleanup: (() => Promise<void>) | undefined;
let profileId: number | null = null;

async function rows(page: Page, jobId: number): Promise<Row[]> {
  const res = await page.request.get(`/api/jobs/${jobId}/translations`, { headers });
  expect(res.ok()).toBe(true);
  return ((await res.json()).data as Row[]).sort((a, b) => a.segment_id - b.segment_id);
}

/** A project with files A and B in German, drafts in both, open together. */
async function openScope(page: Page, draftsA: string[], draftsB: string[]): Promise<Scope> {
  const stamp = Date.now();
  const a = await createIsolatedCATJob(page, { fileName: `mft-a-${stamp}.txt`, content: FILE_A, targetLanguages: ["de"], qaProfile: null });
  cleanup = a.cleanup;
  await uploadFileViaAPI(page, a.projectId, `mft-b-${stamp}.txt`, FILE_B);
  const jobs = (await (await page.request.get(`/api/projects/${a.projectId}/jobs`, { headers })).json()).data as { id: number; file_id: number }[];
  const b = jobs.find(j => j.id !== a.jobId)!;
  const seed = async (job: { jobId: number; fileId: number }, texts: string[]) =>
    texts.length > 0 ? seedDrafts(page, job, texts, headers) : rows(page, job.jobId);
  const rowsA = await seed(a, draftsA);
  const rowsB = await seed({ jobId: b.id, fileId: b.file_id }, draftsB);
  return { projectId: a.projectId, jobA: a.jobId, jobB: b.id, fileB: b.file_id, rowsA, rowsB };
}

async function gotoScope(page: Page, s: Scope) {
  await page.goto(`/projects/${s.projectId}/edit?jobs=${s.jobA},${s.jobB}`);
  // The editor's code loads on first use, which a cold dev server compiles.
  await expect(page.getByTestId(`scope-file-header-${s.jobB}`)).toBeVisible({ timeout: 30_000 });
}

/** Puts the caret in the open row's source text, `offset` characters in. */
async function placeSourceCaret(page: Page, offset: number) {
  await page.getByRole("textbox", { name: "Source text" }).evaluate((field, at) => {
    (field as HTMLElement).focus();
    const node = document.createTreeWalker(field, NodeFilter.SHOW_TEXT).nextNode();
    if (!node) throw new Error("no source text");
    const range = document.createRange();
    range.setStart(node, at);
    range.collapse(true);
    const sel = document.getSelection()!;
    sel.removeAllRanges();
    sel.addRange(range);
  }, offset);
}

// Rows of the grid: A's two rows, then B's.
const rowCell = (page: Page, idx: number) => page.locator(`[data-segment-idx="${idx}"] [data-translation-cell]`);

test.describe("Multi-file editor tools", () => {
  test.beforeEach(async ({ page }) => {
    test.setTimeout(90_000);
    await login(page);
    headers = await authHeaders(page);
    await page.evaluate(() => localStorage.removeItem("cat-track-changes"));
  });
  test.afterEach(async ({ request }) => {
    await cleanup?.();
    cleanup = undefined;
    // After the project: a profile in use cannot be deleted.
    if (profileId) await request.delete(`/api/qa-profiles/${profileId}`, { headers }).catch(() => {});
    profileId = null;
  });

  test("Replace all changes both files", async ({ page }) => {
    const s = await openScope(page, ["42 Artikel bestellen", "Datei speichern"], ["Knopf drücken", "Datei jetzt speichern"]);
    await gotoScope(page, s);

    await page.getByRole("button", { name: "Find and replace" }).click();
    await page.getByPlaceholder("Find...").fill("Datei");
    await page.getByPlaceholder("Replace...").fill("Akte");
    const replaced = page.waitForResponse(r => r.url().endsWith(`/jobs/${s.jobB}/find-replace`) && r.request().method() === "POST");
    await page.getByRole("button", { name: "Replace all" }).click();
    expect((await replaced).ok()).toBe(true);
    await expect(page.getByText("Replaced 2 occurrences in 2 segments")).toBeVisible();

    expect((await rows(page, s.jobA))[1].active_text).toBe("Akte speichern");
    expect((await rows(page, s.jobB))[1].active_text).toBe("Akte jetzt speichern");
    await expect(rowCell(page, 3)).toContainText("Akte jetzt speichern");
  });

  test("Run QA shows findings in both files", async ({ page }) => {
    const s = await openScope(page, ["Artikel bestellen", "Datei speichern"], ["Knopf 3 drücken", "Datei jetzt speichern"]);
    // A profile with the number check only: A's first row drops the 42, B's
    // first row adds a 3.
    const created = await page.request.post(`/api/qa-profiles`, { headers, data: { name: `mft-qa-${Date.now()}` } });
    expect(created.ok()).toBe(true);
    profileId = (await created.json()).data.id;
    expect((await page.request.post(`/api/qa-profiles/${profileId}/rules`, {
      headers, data: { check_type: "number_consistency", enabled: true, severity: "error" },
    })).ok()).toBe(true);
    expect((await page.request.patch(`/api/projects/${s.projectId}/settings`, { headers, data: { qa_profile_id: profileId } })).ok()).toBe(true);
    await gotoScope(page, s);

    const run = page.waitForResponse(r => r.url().includes(`/api/projects/${s.projectId}/editor/run-qa`) && r.request().method() === "POST");
    await page.getByTestId("run-qa-btn").click();
    const outcomes = ((await (await run).json()).data as { job_id: number; outcome: string; issues: number }[]);
    expect(outcomes).toEqual([
      { job_id: s.jobA, outcome: "done", issues: 1 },
      { job_id: s.jobB, outcome: "done", issues: 1 },
    ]);
    await expect(page.getByTestId("run-qa-btn")).toHaveText("QA (2)");
    // A dot on the first row of each file.
    await expect(page.locator('[data-segment-idx="0"] [data-testid="qa-issue-badge"]')).toBeVisible();
    await expect(page.locator('[data-segment-idx="2"] [data-testid="qa-issue-badge"]')).toBeVisible();
    await expect(page.locator('[data-segment-idx="1"] [data-testid="qa-issue-badge"]')).toHaveCount(0);
  });

  test("spell check fixes a word in the second file", async ({ page }) => {
    const s = await openScope(page, ["42 Artikel bestellen", "Datei speichern"], ["Knopf drücken", "Datei jetz speichern"]);
    const typoRow = s.rowsB[1].id;
    // LanguageTool stands in: "jetz" is the only finding, in B's second row.
    const checked: { url: string; ids: number[] }[] = [];
    await page.route("**/api/jobs/*/spellcheck", async route => {
      const ids: number[] = route.request().postDataJSON()?.translation_ids ?? [];
      checked.push({ url: route.request().url(), ids });
      const results = ids.includes(typoRow) ? [{
        translation_id: typoRow, text: "Datei jetz speichern", source_text: "Save the file now",
        matches: [{
          message: "Possible spelling mistake found.", offset: 6, length: 4, replacements: [{ value: "jetzt" }],
          rule: { id: "GERMAN_SPELLER_RULE", description: "Typo", category: { id: "TYPOS", name: "Possible Typo" } },
        }],
      }] : [];
      await route.fulfill({ json: { data: { results, failed: 0 } } });
    });
    await gotoScope(page, s);

    await page.getByTestId("open-spellcheck").click();
    await page.getByTestId("spellcheck-start").click();
    await expect(page.getByTestId("spellfix-context")).toContainText("jetz");
    // Each file's rows went to its own job.
    expect(checked.find(c => c.url.includes(`/jobs/${s.jobA}/`))?.ids).toEqual(s.rowsA.map(r => r.id));
    expect(checked.find(c => c.url.includes(`/jobs/${s.jobB}/`))?.ids).toEqual(s.rowsB.map(r => r.id));

    const write = page.waitForResponse(r => r.url().includes(`/files/${s.fileB}/translations/batch-draft`) && r.request().method() === "PUT");
    await page.getByTestId("spellcheck-change").click();
    expect((await write).ok()).toBe(true);
    expect((await rows(page, s.jobB))[1].active_text).toBe("Datei jetzt speichern");
  });

  test("split then join a row in the second file", async ({ page }) => {
    const s = await openScope(page, [], []);
    const indexB = async () => (await (await page.request.get(`/api/jobs/${s.jobB}/editor/index`, { headers })).json()).data as { positions: number[]; sub_positions: number[] };
    await gotoScope(page, s);

    await page.getByText("Press the button and wait for the light", { exact: true }).click();
    await placeSourceCaret(page, "Press the button".length);
    const split = page.waitForResponse(r => r.url().endsWith(`/jobs/${s.jobB}/segments/split`) && r.request().method() === "POST");
    await page.getByTestId("split-segment-btn").click();
    expect((await split).status()).toBe(200);
    await expect(page.getByText("and wait for the light", { exact: true })).toBeVisible();
    expect((await indexB()).sub_positions).toEqual([0, 1, 0]);

    // The split reopens the first piece, which joins with the next one.
    const join = page.waitForResponse(r => r.url().endsWith(`/jobs/${s.jobB}/segments/join`) && r.request().method() === "POST");
    await page.keyboard.press("Alt+Shift+KeyJ");
    expect((await join).status()).toBe(200);
    await expect(page.getByText("Press the button and wait for the light", { exact: true })).toBeVisible();
    expect((await indexB()).sub_positions).toEqual([0, 0]);
  });

  test("track changes marks a row whose file has a previous step", async ({ page }) => {
    const s = await openScope(page, ["Artikel bestellen"], ["Knopf drücken"]);
    // B moves to its next step, where the reviewer changes one word; A stays
    // on its first step.
    const advance = await page.request.post(`/api/jobs/${s.jobB}/workflow/advance`, { headers });
    expect(advance.ok(), await advance.text()).toBe(true);
    const save = await page.request.put(`/api/files/${s.fileB}/translations/${s.rowsB[0].id}`, {
      headers, data: { human_text: "Knopf betätigen", editing_time_ms: 0 },
    });
    expect(save.ok(), await save.text()).toBe(true);
    await gotoScope(page, s);

    const toggle = page.getByRole("button", { name: "Track changes" });
    await expect(toggle).toBeEnabled();
    await toggle.click();
    const marked = page.getByTestId("track-changes");
    await expect(marked).toHaveCount(1);
    await expect(page.locator('[data-segment-idx="2"] [data-testid="track-changes"] span.line-through')).toHaveText("drücken");
    await expect(page.locator('[data-segment-idx="2"] [data-testid="track-changes"] span.bg-yellow-300')).toHaveText("betätigen");
  });
});
