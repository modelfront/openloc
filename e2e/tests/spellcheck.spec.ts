import { test, expect, type Page } from "@playwright/test";
import { login, openJob } from "./helpers";

/**
 * Spell check review dialog.
 *
 * LanguageTool is stubbed at the HTTP boundary rather than exercised for real:
 * its findings depend on the deployed rule set and dictionary version, so
 * asserting against the live service would make these tests fail for reasons
 * unrelated to our code. Everything downstream of the stub — scope selection,
 * navigation, the batch-draft write, the status downgrade — is real.
 *
 * Content comes from the dedicated "Spellcheck QA" project (`make seed-spellcheck`,
 * run by scripts/test-all.sh before each Playwright suite) — NOT from demo.txt,
 * whose targets are mostly `[DE] <english>` placeholders shared with every other
 * spec. The fixture deliberately includes `adresse` in five segments, tagged
 * segments, one locked row and two approved rows.
 */

const TYPO = "adresse";
const FIX = "Adresse";

/** Open the Spellcheck QA project's German job in the CAT editor. */
async function openGermanJob(page: Page) {
  await page.getByRole("link", { name: /spellcheck qa/i }).click();
  await openJob(page, { lang: "de" });
  // A fixture source string, so we know the grid has rendered.
  await expect(page.getByText(/Please confirm the delivery address/i).first())
    .toBeVisible({ timeout: 15000 });
}

interface FixtureRow {
  id: number;
  active_text: string | null;
  active_rich_text?: string | null;
  locked?: boolean;
}

/**
 * Put the fixture back the way it was.
 *
 * These specs mutate shared seed data, and the DB is reset once per suite — not
 * per test. Without this, the first full run passes and every subsequent run (or
 * any `-g` single-test run) fails with "No spelling issues found", because the
 * typos have already been corrected. Restores through batch-draft with
 * `include_locked`, so a locked fixture row corrected by the All scope can be
 * reverted too.
 */
async function restoreFixture(page: Page, before: FixtureRow[], appliedIds: number[]) {
  if (appliedIds.length === 0) return;
  const jobId = new URL(page.url()).pathname.split("/")[4];
  const job = await (await page.request.get(`/api/jobs/${jobId}`)).json();
  const fixes = appliedIds
    .map(id => before.find(r => r.id === id))
    .filter((r): r is FixtureRow => Boolean(r))
    .map(r => ({
      translation_id: r.id,
      human_text: r.active_text ?? "",
      ...(r.active_rich_text ? { human_rich_text: r.active_rich_text } : {}),
    }));
  const reverted = await page.request.put(`/api/files/${job.data.file_id}/translations/batch-draft`, {
    data: { fixes, include_locked: true },
  });
  expect(reverted.ok(), `revert spellcheck fixture: ${reverted.status()}`).toBe(true);
}

/**
 * Remove `TYPO` from the project dictionary. The dictionary is project-scoped,
 * so a word left behind would suppress the finding for every later run,
 * including manual QA.
 */
async function removeTypoFromDictionary(page: Page, projectId: string) {
  const listed = await page.request.get(`/api/projects/${projectId}/spellcheck-dictionary?language=de`);
  expect(listed.ok(), `list spellcheck dictionary: ${listed.status()}`).toBe(true);
  const entry = (await listed.json()).data.find((e: { word: string }) => e.word === TYPO);
  if (entry) {
    const removed = await page.request.delete(`/api/projects/${projectId}/spellcheck-dictionary/${entry.id}`);
    expect(removed.ok(), `delete spellcheck dictionary word: ${removed.status()}`).toBe(true);
  }
}

/**
 * Stub POST /api/jobs/:id/spellcheck. Flags `TYPO` in every requested
 * translation whose text contains it, computing offsets the way LanguageTool
 * would (UTF-16 code units, which JS string indices already are).
 */
async function stubLanguageTool(
  page: Page,
  opts: { word?: string; fix?: string; failed?: number } = {},
) {
  const word = opts.word ?? TYPO;
  const fix = opts.fix ?? FIX;
  // The stub needs each translation's text to compute an offset, so it reads the
  // translation list the page already fetched.
  const snapshot: FixtureRow[] = [];
  let translations: FixtureRow[] = [];

  // The CAT editor loads translations from /api/jobs/:id/translations (see
  // listJobTranslations) — NOT /api/files/:id/translations, which is a different
  // endpoint. That is the route to read, or the stub finds nothing.
  //
  // Intercepted rather than observed via page.on("response"): the handler must
  // populate `translations` BEFORE the spellcheck request fires, and an event
  // listener resolves asynchronously — it loses that race. Fulfilled from the
  // raw body string rather than by passing the fetched `response` object, which
  // is disposed once its body has been read ("Fetch response has been disposed").
  await page.route("**/api/jobs/*/translations*", async route => {
    const response = await route.fetch();
    const body = await response.text();
    try {
      const parsed = JSON.parse(body);
      if (parsed?.data) {
        translations = parsed.data;
        // First read wins: the pre-correction state, used to restore the fixture.
        if (snapshot.length === 0) snapshot.push(...parsed.data);
      }
    } catch {
      // Not JSON — pass it through untouched.
    }
    await route.fulfill({
      status: response.status(),
      contentType: "application/json",
      body,
    });
  });

  // Every check request body, so a test can assert on the scope it carried.
  const checks: { translation_ids: number[]; include_locked?: boolean }[] = [];
  const stub = { snapshot, checks };
  // `failed` counts segments, not responses: a run spans multiple chunked
  // requests (SPELLCHECK_CHUNK_SIZE), so report the failures once, on the
  // first chunk, the way a real run with N broken segments would.
  let failedRemaining = opts.failed ?? 0;
  await page.route("**/api/jobs/*/spellcheck", async route => {
    const payload = route.request().postDataJSON() ?? {};
    checks.push(payload);
    const requested: number[] = payload.translation_ids ?? [];
    const results = [];
    for (const id of requested) {
      const text = translations.find(t => t.id === id)?.active_text ?? "";
      const offset = text.indexOf(word);
      if (offset < 0) continue;
      results.push({
        translation_id: id,
        matches: [{
          message: "Possible spelling mistake found.",
          offset,
          length: word.length,
          replacements: [{ value: fix }],
          rule: { id: "GERMAN_SPELLER_RULE", description: "Typo", category: { id: "TYPOS", name: "Possible Typo" } },
        }],
      });
    }
    const failed = failedRemaining;
    failedRemaining = 0;
    await route.fulfill({
      json: {
        data: { results, failed },
      },
    });
  });

  return stub;
}

/**
 * Stub the live single-segment endpoint (POST /api/spellcheck): flag `TYPO`
 * wherever it appears. Records each request body so a test can assert on what
 * the editor sent.
 */
async function stubLiveCheck(page: Page) {
  const requests: { text?: string; language?: string; job_id?: number }[] = [];
  await page.route("**/api/spellcheck", async route => {
    const body = route.request().postDataJSON() ?? {};
    requests.push(body);
    const text: string = body.text ?? "";
    const matches = [];
    let idx = text.indexOf(TYPO);
    while (idx >= 0) {
      matches.push({
        message: "Possible spelling mistake found.",
        offset: idx,
        length: TYPO.length,
        replacements: [{ value: FIX }],
        rule: { id: "GERMAN_SPELLER_RULE", description: "Typo", category: { id: "TYPOS", name: "Possible Typo" } },
      });
      idx = text.indexOf(TYPO, idx + 1);
    }
    await route.fulfill({ json: { matches } });
  });
  return requests;
}

async function runCheck(page: Page) {
  await page.getByTestId("open-spellcheck").click();
  await expect(page.getByTestId("spellcheck-dialog")).toBeVisible();
  await page.getByTestId("spellcheck-start").click();
}

test.describe("Spell check review dialog", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("walks findings one at a time, showing source and target", async ({ page }) => {
    await stubLanguageTool(page);
    await openGermanJob(page);
    await runCheck(page);

    // Review phase: counter, the flagged word in context, and a suggestion.
    await expect(page.getByTestId("spellfix-context")).toContainText(TYPO);
    await expect(page.getByTestId("spellfix-suggestions").getByRole("radio", { name: FIX }))
      .toBeVisible();
    // The top suggestion prefills the editable correction field.
    await expect(page.getByTestId("spellcheck-replacement")).toHaveValue(FIX);
    // The source must be reproduced inside the modal — it covers the grid.
    await expect(page.getByTestId("spellfix-source")).not.toBeEmpty();
    await expect(page.getByTestId("spellcheck-dialog")).toContainText(/Error 1 of \d+/);
  });

  // Own typo per write test: these run in parallel against a shared database, so
  // two tests correcting the same word race — the loser finds nothing left to fix.
  test("Change applies the correction and drops the segment to needs review", async ({ page }) => {
    const stub = await stubLanguageTool(page, { word: "Zahlnug", fix: "Zahlung" });
    let appliedIds: number[] = [];
    try {
      await openGermanJob(page);
      await runCheck(page);

      await expect(page.getByTestId("spellfix-context")).toContainText("Zahlnug");

      // Await the real write — never a timeout.
      const write = page.waitForResponse(r =>
        r.url().includes("/translations/batch-draft") && r.request().method() === "PUT");
      await page.getByTestId("spellcheck-change").click();
      const response = await write;
      expect(response.ok()).toBe(true);

      const body = await response.json();
      appliedIds = body.data.applied.map((t: { id: number }) => t.id);
      expect(body.data.applied.length).toBeGreaterThan(0);
      // A spell fix is an edit, never a confirmation.
      for (const t of body.data.applied) {
        expect(t.status).toBe("needs_review");
      }
      expect(body.data.conflicts).toHaveLength(0);
    } finally {
      // Put the shared fixture back — the DB is reset per suite, not per test.
      await restoreFixture(page, stub.snapshot, appliedIds);
    }
  });

  test("a typed correction is applied verbatim; selecting a suggestion applies nothing", async ({ page }) => {
    const stub = await stubLanguageTool(page, { word: "Bestätiugng", fix: "Bestätigung" });
    let appliedIds: number[] = [];
    try {
      await openGermanJob(page);
      await runCheck(page);

      await expect(page.getByTestId("spellfix-context")).toContainText("Bestätiugng");

      // Clicking a suggestion chip only selects it — the finding must still be
      // under review afterwards, with no write fired. (The word occurs in one
      // fixture segment, so an apply-on-click regression would end the review.)
      await page.getByTestId("spellfix-suggestions").getByRole("radio", { name: "Bestätigung" }).click();
      await expect(page.getByTestId("spellcheck-dialog")).toContainText(/Error 1 of \d+/);

      // A typed fix overrides the suggestions entirely.
      await page.getByTestId("spellcheck-replacement").fill("Empfangsbestätigung");
      const write = page.waitForResponse(r =>
        r.url().includes("/translations/batch-draft") && r.request().method() === "PUT");
      await page.getByTestId("spellcheck-change").click();
      const body = await (await write).json();
      appliedIds = body.data.applied.map((t: { id: number }) => t.id);

      expect(body.data.applied.length).toBeGreaterThan(0);
      expect(body.data.applied[0].active_text).toContain("Empfangsbestätigung");
    } finally {
      // Put the shared fixture back — the DB is reset per suite, not per test.
      await restoreFixture(page, stub.snapshot, appliedIds);
    }
  });

  test("Change all fixes every occurrence in one request", async ({ page }) => {
    // "Liferung" appears in two fixture segments.
    const stub = await stubLanguageTool(page, { word: "Liferung", fix: "Lieferung" });
    let appliedIds: number[] = [];
    try {
      await openGermanJob(page);
      await runCheck(page);

      await expect(page.getByTestId("spellfix-context")).toContainText("Liferung");

      const write = page.waitForResponse(r =>
        r.url().includes("/translations/batch-draft") && r.request().method() === "PUT");
      await page.getByTestId("spellcheck-change-all").click();
      const response = await write;

      const body = await response.json();
      appliedIds = body.data.applied.map((t: { id: number }) => t.id);
      // Both occurrences must go in ONE request, not one request per segment.
      expect(body.data.applied.length).toBeGreaterThanOrEqual(2);
      await expect(page.getByTestId("spellcheck-dialog")).toContainText(/Nothing left to review/i);
    } finally {
      // Put the shared fixture back — the DB is reset per suite, not per test.
      await restoreFixture(page, stub.snapshot, appliedIds);
    }
  });

  test("the tagged segment's correction preserves its inline tag", async ({ page }) => {
    // "rechnung" appears in three fixture segments, one of which wraps a {ref}
    // placeholder in `<ph id="3">` markup.
    const stub = await stubLanguageTool(page, { word: "rechnung", fix: "Rechnung" });
    let appliedIds: number[] = [];
    try {
      await openGermanJob(page);
      await runCheck(page);

      const write = page.waitForResponse(r =>
        r.url().includes("/translations/batch-draft") && r.request().method() === "PUT");
      await page.getByTestId("spellcheck-change-all").click();
      const body = await (await write).json();
      appliedIds = body.data.applied.map((t: { id: number }) => t.id);

      // The tagged fixture row wraps a {ref} placeholder in `<ph id="3">` markup,
      // with the typo alongside it. The tag must survive the correction — an
      // offset-based rich-text splice would have eaten it.
      const tagged = body.data.applied.find((t: { active_rich_text?: string }) =>
        t.active_rich_text?.includes("<ph"));
      expect(tagged, "expected one applied row to carry inline tag markup").toBeTruthy();
      expect(tagged.active_rich_text).toContain('<ph id="3">{ref}</ph>');
      expect(tagged.active_rich_text).toContain("Rechnung");
    } finally {
      // Put the shared fixture back — the DB is reset per suite, not per test.
      await restoreFixture(page, stub.snapshot, appliedIds);
    }
  });

  test("Ignore all persists to the project dictionary and survives a reload", async ({ page }) => {
    await stubLanguageTool(page);
    await openGermanJob(page);
    const projectId = new URL(page.url()).pathname.split("/")[2];

    try {
      await runCheck(page);

      await expect(page.getByTestId("spellfix-context")).toContainText(TYPO);

      const saved = page.waitForResponse(r =>
        r.url().includes("/spellcheck-dictionary") && r.request().method() === "POST");
      await page.getByRole("button", { name: /ignore all/i }).click();
      expect((await saved).ok()).toBe(true);

      // The point of "Ignore all" is that it outlives the run, so assert the row
      // is actually readable back from the API rather than trusting the POST.
      const listed = await page.request.get(
        `/api/projects/${projectId}/spellcheck-dictionary?language=de`,
      );
      expect(listed.ok()).toBe(true);
      const words = (await listed.json()).data.map((e: { word: string }) => e.word);
      expect(words).toContain(TYPO);
    } finally {
      await removeTypoFromDictionary(page, projectId);
    }
  });

  // A run that couldn't check some segments must say so rather than presenting
  // itself as a clean bill of health.
  test("reports segments it could not check", async ({ page }) => {
    await stubLanguageTool(page, { failed: 2 });
    await openGermanJob(page);
    await runCheck(page);

    await expect(page.getByTestId("spellcheck-notes"))
      .toContainText(/2 segments could not be checked/i);
  });

  // The panel is portaled to <body>, outside the segment editor's DOM subtree.
  // CATEditor's click-outside-deactivates handler must exempt it, or any
  // mousedown on a non-<button> part of the panel (the source text, the
  // "Change to" input…) deactivates the segment, unmounting the editor and the
  // panel with it.
  test("inline fix panel survives clicks on its non-interactive parts", async ({ page }) => {
    await stubLiveCheck(page);

    await openGermanJob(page);
    await page.getByText("Bitte bestätigen Sie die adresse für die Lieferung.").click();

    // The live check underlines the typo, and clicking the underline opens the panel.
    const underline = page.locator('[data-testid="segment-editor"] .spellcheck-error').first();
    await underline.click();
    const panel = page.getByTestId("spellcheck-fix-menu");
    await expect(panel).toBeVisible();

    // A click on a non-interactive part of the panel must not dismiss it —
    // and must not deactivate the segment editor underneath.
    await page.getByTestId("spellfix-source").click();
    await expect(panel).toBeVisible();
    await expect(page.getByTestId("segment-editor")).toBeVisible();

    // Same for the "Change to" input — an <input>, not a <button>.
    await page.getByTestId("spellcheck-replacement").click();
    await expect(panel).toBeVisible();
  });

  // The dictionary write is only half of "Ignore in this project": the
  // underline the user clicked has to go away too, and the live check has to
  // tell the server which job it's for so the dictionary applies on re-check.
  test("Ignore in this project removes the inline underline and scopes the live check to the job", async ({ page }) => {
    const liveRequests = await stubLiveCheck(page);
    await openGermanJob(page);
    const projectId = new URL(page.url()).pathname.split("/")[2];
    const jobId = Number(new URL(page.url()).pathname.match(/\/jobs\/(\d+)/)?.[1]);
    expect(jobId).toBeGreaterThan(0);

    try {
      await page.getByText("Bitte bestätigen Sie die adresse für die Lieferung.").click();
      const underlines = page.locator('[data-testid="segment-editor"] .spellcheck-error');
      await expect(underlines).toHaveCount(1);
      expect(liveRequests.at(-1)?.job_id).toBe(jobId);

      await underlines.first().click();
      const saved = page.waitForResponse(r =>
        r.url().includes("/spellcheck-dictionary") && r.request().method() === "POST");
      await page.getByTestId("spellfix-ignore-project").click();
      expect((await saved).ok()).toBe(true);

      await expect(page.getByTestId("spellcheck-fix-menu")).toBeHidden();
      await expect(underlines).toHaveCount(0);
      // The editor stays open — ignoring a word is not a reason to leave the segment.
      await expect(page.getByTestId("segment-editor")).toBeVisible();
    } finally {
      await removeTypoFromDictionary(page, projectId);
    }
  });

  // The flagged word lives only in the locked fixture row (own typo, like every
  // write test above), so the default scope must find nothing, and only the All
  // scope may both find it and write the fix.
  test("opens on non-locked segments; only All segments checks locked ones", async ({ page }) => {
    const stub = await stubLanguageTool(page, { word: "geändert", fix: "angepasst" });
    let appliedIds: number[] = [];
    try {
      await openGermanJob(page);
      const scope = (value: string) => page.locator(`input[name="spellcheck-scope"][value="${value}"]`);
      const lockedIds = stub.snapshot.filter(r => r.locked).map(r => r.id);
      expect(lockedIds.length, "the fixture has a locked row").toBeGreaterThan(0);
      expect(
        stub.snapshot.filter(r => r.active_text?.includes("geändert")).map(r => r.id),
        "the flagged word lives only in the locked row",
      ).toEqual(lockedIds);
      const requestedIds = () => stub.checks.flatMap(c => c.translation_ids);

      await page.getByTestId("open-spellcheck").click();
      await expect(scope("nonlocked")).toBeChecked();
      await expect(scope("all")).toBeEnabled();
      await expect(scope("all")).not.toBeChecked();
      // Disabled until the segment filter bar has an active filter.
      await expect(scope("filtered")).toBeDisabled();

      // Default scope: the locked row is not requested, the server is not asked
      // to check locked rows, so the locked row's typo is never found.
      await page.getByTestId("spellcheck-start").click();
      await expect(page.getByTestId("spellcheck-dialog")).toContainText(/No spelling issues found/i);
      expect(stub.checks.length).toBeGreaterThan(0);
      for (const id of lockedIds) expect(requestedIds()).not.toContain(id);
      expect(stub.checks.every(c => c.include_locked === undefined)).toBe(true);

      // All segments: the locked row is requested with include_locked.
      await page.keyboard.press("Escape");
      await expect(page.getByTestId("spellcheck-dialog")).toBeHidden();
      stub.checks.length = 0;
      await page.getByTestId("open-spellcheck").click();
      await scope("all").check();
      await page.getByTestId("spellcheck-start").click();
      await expect(page.getByTestId("spellcheck-dialog")).toContainText(/Error 1 of 1/);
      expect(stub.checks.length).toBeGreaterThan(0);
      expect(requestedIds()).toEqual(expect.arrayContaining(lockedIds));
      expect(stub.checks.every(c => c.include_locked === true)).toBe(true);

      // The fix of the locked row goes through: the write carries include_locked
      // and the server applies the row instead of listing it under `locked`.
      await expect(page.getByTestId("spellfix-context")).toContainText("geändert");
      const write = page.waitForResponse(r =>
        r.url().includes("/translations/batch-draft") && r.request().method() === "PUT");
      await page.getByTestId("spellcheck-change-all").click();
      const response = await write;
      expect(response.ok()).toBe(true);
      expect(response.request().postDataJSON().include_locked).toBe(true);
      const body = await response.json();
      appliedIds = body.data.applied.map((t: { id: number }) => t.id);
      expect(appliedIds).toEqual(expect.arrayContaining(lockedIds));
      expect(body.data.locked).toEqual([]);
      await expect(page.getByTestId("spellcheck-dialog")).toContainText(/Nothing left to review/i);
    } finally {
      // Put the shared fixture back — the DB is reset per suite, not per test.
      await restoreFixture(page, stub.snapshot, appliedIds);
    }
  });
});
