import { Browser, BrowserContext, Page, Locator, Response, expect, request, test } from "@playwright/test";

const API_BASE = "";

/**
 * Locator for the TipTap segment editor (contenteditable div).
 * Use this instead of page.locator("textarea") for the CAT editor translation input.
 */
export const segmentEditor = (page: Page): Locator =>
  page.locator('[data-testid="segment-editor"] .ProseMirror');

/**
 * fill() until the editor holds exactly `text`. fill's select-all is a DOM
 * selection that ProseMirror reads a task later; a redraw in between (a late
 * spell-check result on a freshly opened editor) or a refetch that loads a
 * different text puts the caret back at the end, and the text is appended.
 * A retried fill replaces it all.
 */
export async function fillEditorExactly(editor: Locator, text: string): Promise<void> {
  await expect(async () => {
    await editor.fill(text);
    await expect(editor).toHaveText(text, { timeout: 500 });
  }).toPass({ timeout: 5000 });
}

/**
 * Land on the authenticated app as the seed owner.
 *
 * Auth is injected once per run via Playwright storageState (see
 * global-setup.ts) — the tenant-scoped seed-org JWT is already in
 * localStorage before the test starts, so this just navigates to the app and
 * confirms the authed shell rendered. No per-test dev-login/org-select.
 *
 * Tests that need a different identity (vendors, j9/j23/j26) overlay their own
 * token via addInitScript/setItem, which wins over the storageState token.
 */
export async function login(page: Page) {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: /projects/i })).toBeVisible();
}

/**
 * Store `token` as the signed-in identity. Dev auto-login fires when /login
 * mounts and would overwrite the token (DEV_AUTO_LOGIN is on in the test env),
 * so it is blocked for the rest of the test before /login is opened. Navigate
 * afterwards to load the app as that identity.
 */
export async function signInWithToken(page: Page, token: string) {
  await page.route("**/api/auth/dev-login", (route) => route.abort());
  await page.goto("/login");
  await page.evaluate((t) => localStorage.setItem("token", t), token);
}

/**
 * Sign `page` in as a seeded user: sign-in is dev or Google only, so this
 * dev-logs in AS that email (the dev-login endpoint takes an optional email).
 */
export async function loginAs(page: Page, email: string) {
  const res = await page.request.post("/api/auth/dev-login", { data: { email } });
  expect(res.ok(), `dev-login as ${email}: ${res.status()}`).toBe(true);
  await signInWithToken(page, (await res.json()).token);
}

/**
 * A new browser session signed in as `email`, beside the test's own page.
 * Dev-login also sets the session's auth cookie, which event streams sign in
 * with (EventSource cannot send a header), so they run as that user too. The
 * caller closes `context`.
 */
export async function sessionAs(browser: Browser, email: string): Promise<{ page: Page; context: BrowserContext }> {
  const context = await browser.newContext();
  const page = await context.newPage();
  await loginAs(page, email);
  return { page, context };
}

/** `sessionAs` for a test that leaves the session open until the run ends. */
export async function pageAs(browser: Browser, email: string): Promise<Page> {
  return (await sessionAs(browser, email)).page;
}

/**
 * The element's own computed background, and the background of the first
 * ancestor that paints one. Compare the two instead of hard-coding a colour:
 * a palette repaints every surface.
 */
export async function backgroundAndBehind(locator: Locator): Promise<{ self: string; behind: string }> {
  return locator.evaluate((el) => {
    let behind = "";
    for (let p = el.parentElement; p; p = p.parentElement) {
      const bg = getComputedStyle(p).backgroundColor;
      if (bg !== "rgba(0, 0, 0, 0)" && bg !== "transparent") { behind = bg; break; }
    }
    return { self: getComputedStyle(el).backgroundColor, behind };
  });
}

/**
 * Click "Create project" on the New Project wizard and return the published
 * project. The wizard autosaves a draft while the form is filled,
 * so the click sends PUT /draft + POST /publish — the
 * POST /api/projects that creates the row has usually already happened on the
 * first blur. Publish is the response that carries the final project.
 */
export async function submitProjectWizard(page: Page): Promise<{ response: Response; id: number }> {
  const [response] = await Promise.all([
    page.waitForResponse(
      r => /\/api\/projects\/\d+\/publish$/.test(r.url()) && r.request().method() === "POST",
      { timeout: 10000 },
    ),
    page.getByRole("button", { name: /^create project$/i }).click(),
  ]);
  const body = await response.json().catch(() => ({}));
  return { response, id: body?.data?.id };
}

/**
 * Wait for async job pipeline to finish by polling the job status.
 * Returns when job status changes from "processing" or when timeout.
 */
export async function waitForPipeline(page: Page, { timeout = 15000 } = {}) {
  // Wait for any "processing" indicators to disappear
  await expect(
    page.getByText(/processing|running pipeline/i).first()
  ).toBeHidden({ timeout });
}

/**
 * Upload a file to a project via the API directly (faster than UI upload).
 */
export async function uploadFileViaAPI(
  page: Page,
  projectId: number,
  filename: string,
  content: string,
  contentType = "text/plain"
) {
  const response = await page.request.post(
    `${API_BASE}/api/projects/${projectId}/files`,
    {
      headers: await authHeaders(page),
      multipart: {
        file: {
          name: filename,
          mimeType: contentType,
          buffer: Buffer.from(content),
        },
      },
    }
  );
  expect(response.ok()).toBe(true);
  return response.json();
}

/**
 * Pick a language in a language Combobox using stable testIds + cmdk keyboard nav.
 *
 * cmdk renders options inside a Base UI Popover portal, and a popover that
 * is still animating closed keeps its options queryable by `getByTestId`. A
 * global `getByTestId('lang-option-en')` can match that STALE popover instead
 * of the one this click opened, so the field stays empty.
 *
 * So this helper:
 *   1. Always wait for any previous popover-content to be GONE before opening
 *      the new one (`expect(page.locator(...)).toHaveCount(0)`).
 *   2. Click the trigger unconditionally — never try to detect "already open".
 *   3. Scope the option lookup to the popover content that opened as a result
 *      of this trigger click, by chaining off the `[data-slot="popover-content"]`
 *      that appears AFTER the click.
 *   4. Wait for the popover to fully unmount after Enter before returning so
 *      the next caller starts from a clean DOM.
 * This avoids the "stale popover masquerades as the next popover" failure.
 */
async function pickLangInCmdk(page: Page, trigger: Locator, code: string) {
  const popoverContent = page.locator('[data-slot="popover-content"]');

  // Step 1: Wait for any previously-open popover content to be fully removed
  // from the DOM. Base UI unmounts the popup on close, so this is a hard
  // synchronization point: if zero match, we know no stale options can match
  // a getByTestId lookup.
  await expect(popoverContent).toHaveCount(0, { timeout: 3000 });

  // Step 2: Click the trigger to open a fresh popover.
  await trigger.click();

  // Step 3: Wait for exactly ONE popover to be mounted, then scope the
  // option lookup to that specific popover content.
  await expect(popoverContent).toHaveCount(1, { timeout: 3000 });
  const searchInput = popoverContent.getByPlaceholder(/search languages/i);
  const targetOption = popoverContent.getByTestId(`lang-option-${code}`);
  await expect(searchInput).toBeVisible({ timeout: 3000 });

  // Filter the list to narrow what's visible, but DO NOT rely on cmdk's
  // Enter-key selection. The language picker matches both `code` AND
  // `name`, so typing "en" highlights Armenian (hy, because "armenian"
  // contains "en"), Estonian, Slovenian, etc. cmdk then sorts by its
  // internal algorithm and the "first match" Enter selects is almost
  // never what the caller typed. Click the scoped option directly.
  await searchInput.fill('');
  await searchInput.type(code, { delay: 30 });
  await expect(targetOption).toBeVisible({ timeout: 2000 });
  await targetOption.click();

  // Step 4: Wait for the popover to fully unmount BEFORE returning, so the
  // next caller starts from a clean DOM.
  await expect(popoverContent).toHaveCount(0, { timeout: 3000 });

  // Verify the trigger updated to show the picked code (shown uppercase).
  await expect(trigger).toContainText(code.toUpperCase(), { timeout: 2000 });
}

/**
 * Select a language in a language Combobox via testId. Call with the trigger locator.
 * Opens the popover, clicks the option by data-testid.
 */
export async function selectLanguage(page: Page, trigger: Locator, code: string) {
  await pickLangInCmdk(page, trigger, code);
}

/**
 * A long unbreakable name must not widen its row past the layout.
 * The row is required: the app shell is `overflow-hidden`
 * with an `overflow-y-auto` main, so a document-level width check can never
 * fail here and would pass against the unfixed page.
 */
export async function expectNoOverflow(row: Locator) {
  await expect(row).toBeVisible();
  const rowOverflow = await row.evaluate((el) => el.scrollWidth - el.clientWidth);
  expect(rowOverflow, "row content wider than the row").toBeLessThanOrEqual(0);
}

/**
 * Runs `measure` with every column resize strip hidden. A strip straddles its
 * header's border by design, so it sticks out of its cell and counts in the
 * cell's `scrollWidth`. Use this for "is the content wider than its cell"
 * checks only; measure whether anything scrolls with the strips shown.
 */
export async function withResizeStripsHidden<T>(page: Page, measure: () => Promise<T>): Promise<T> {
  const style = await page.addStyleTag({
    content: '[role="separator"][aria-label^="Resize"] { display: none !important; }',
  });
  try {
    return await measure();
  } finally {
    await style.evaluate((el) => el.remove());
  }
}

/** A name with no spaces or hyphens — nothing for the browser to break on. */
export const UNBREAKABLE_NAME = "Supercalifragilisticexpialidocious".repeat(4);

/**
 * The New-project form's Source language trigger. Every language picker shares
 * `data-testid="lang"`, so scope through the field wrapper instead of taking
 * the first match on the page.
 */
export function sourceLanguageTrigger(page: Page): Locator {
  return page.locator('[data-field="source_language"]').getByTestId('lang');
}

/**
 * Drive a Combobox / MultiCombobox (components/ui/combobox.tsx):
 * open the trigger and click the option whose visible text matches `label`.
 * Uses the same popover-count sync as pickLangInCmdk so stale popovers can't
 * match. For fixed-value options prefer `pickComboboxValue` (testid-scoped).
 */
export async function pickComboboxOption(page: Page, trigger: Locator, label: string | RegExp) {
  const popover = page.locator('[data-slot="popover-content"]');
  await expect(popover).toHaveCount(0, { timeout: 3000 });
  await trigger.click();
  await expect(popover).toHaveCount(1, { timeout: 3000 });
  await popover.getByRole('option', { name: label }).first().click();
  await expect(popover).toHaveCount(0, { timeout: 3000 });
}

/**
 * Pick a ui/combobox option by value. The trigger has `data-testid={triggerTestId}`
 * and each option `data-testid={`${triggerTestId}-option-${value}`}`.
 */
export async function pickComboboxValue(page: Page, triggerTestId: string, value: string) {
  const popover = page.locator('[data-slot="popover-content"]');
  await expect(popover).toHaveCount(0, { timeout: 3000 });
  await page.getByTestId(triggerTestId).click();
  await expect(popover).toHaveCount(1, { timeout: 3000 });
  // Wait for the option before clicking it. The popover mounts as soon as the
  // trigger is clicked, but its list can still be filling from a query — and a
  // list that re-renders when that query lands detaches whatever the click had
  // already resolved. `toBeVisible` re-queries on every poll, so it survives
  // the remount and fails with "option never appeared" instead of a click
  // timeout that reads like a broken selector.
  const option = popover.getByTestId(`${triggerTestId}-option-${value}`);
  await expect(option).toBeVisible({ timeout: 10_000 });
  await option.click();
  await expect(popover).toHaveCount(0, { timeout: 3000 });
}

/**
 * Run a pinned combobox action — the "+ Create new…" rows under the search
 * field. The trigger has `data-testid={triggerTestId}` and each action
 * `data-testid={`${triggerTestId}-action-${key}`}`.
 */
export async function clickComboboxAction(page: Page, triggerTestId: string, key: string) {
  const popover = page.locator('[data-slot="popover-content"]');
  await expect(popover).toHaveCount(0, { timeout: 3000 });
  await page.getByTestId(triggerTestId).click();
  await expect(popover).toHaveCount(1, { timeout: 3000 });
  await popover.getByTestId(`${triggerTestId}-action-${key}`).click();
  await expect(popover).toHaveCount(0, { timeout: 3000 });
}

/**
 * Select one or more target languages in a MultiCombobox. The multi-combobox
 * keeps its popover OPEN between selections (continuous multi-select), so this
 * helper opens once, loops through the codes, then Escape-closes.
 *
 * Same popover-scoping discipline as pickLangInCmdk:
 *   1. Wait for any previous popover content to be gone.
 *   2. Open the trigger.
 *   3. Scope option lookups to the just-opened popover (never global testId).
 *   4. After each selection, wait for the option to disappear from the list
 *      (multi-combobox removes "taken" codes from `available`).
 *   5. Escape + wait for popover content to unmount before returning.
 */
export async function selectTargetLanguages(page: Page, trigger: Locator, codes: string[]) {
  const popoverContent = page.locator('[data-slot="popover-content"]');

  // Hard sync: no stale popover in the DOM.
  await expect(popoverContent).toHaveCount(0, { timeout: 3000 });

  await trigger.click();
  await expect(popoverContent).toHaveCount(1, { timeout: 3000 });
  const searchInput = popoverContent.getByPlaceholder(/search languages/i);
  await expect(searchInput).toBeVisible({ timeout: 3000 });

  for (const code of codes) {
    const option = popoverContent.getByTestId(`lang-option-${code}`);
    await searchInput.fill('');
    await searchInput.type(code, { delay: 30 });
    await expect(option).toBeVisible({ timeout: 2000 });
    // Click the option directly instead of pressing Enter. cmdk's Enter
    // handler has proved unreliable inside Base UI's modal popover — the
    // filter function sometimes returns 0 for exact-code matches, so the
    // highlighted item isn't the one the test typed. A direct click on the
    // scoped option is deterministic.
    await option.click();
    // The multi-combobox is a toggle-list: selected options STAY in the list
    // (marked checked) rather than disappearing. Wait for the checked state so
    // the next iteration's search doesn't race with the re-render.
    await expect(option).toHaveAttribute('data-checked', 'true', { timeout: 2000 });
  }

  // Close the popover — verify unmount so the next caller starts clean.
  await page.keyboard.press('Escape');
  await expect(popoverContent).toHaveCount(0, { timeout: 3000 });
}

/**
 * Add an assignment (TM / MT Engine / Term base) via the add-row form.
 * Uses the data-testid on the add-row container for reliability.
 *
 * @param resourceLabel - "TM" | "MT Engine" | "Term base"
 * @param resourceName - Name of the resource to select from the dropdown
 * @param source - Source language code (e.g., "en")
 * @param target - Target language code (e.g., "fr")
 */
export async function addAssignment(
  page: Page,
  resourceLabel: 'TM' | 'Engine' | 'Term base',
  resourceName: string,
  source: string,
  target: string,
) {
  const testId = `assignment-add-row-${resourceLabel.toLowerCase().replace(/\s/g, '-')}`;
  const addRow = page.getByTestId(testId);
  await expect(addRow).toBeVisible({ timeout: 5000 });
  await addRow.scrollIntoViewIfNeeded();

  // Add-row pickers, in order: resource, source lang, target lang — every
  // picker is a Combobox, so they share the one trigger slot.
  const triggers = addRow.locator('[data-slot="combobox-trigger"]');

  await pickComboboxOption(page, triggers.nth(0), resourceName);
  await pickLangInCmdk(page, triggers.nth(1), source);
  await pickLangInCmdk(page, triggers.nth(2), target);

  const addBtn = addRow.getByRole('button', { name: /^Add$/ });
  await expect(addBtn).toBeEnabled({ timeout: 3000 });
  await addBtn.click();

  // Wait for the saving indicator to clear (auto-save completes)
  try {
    await expect(page.getByText(/^Saving\.\.\.$/)).toBeHidden({ timeout: 3000 });
  } catch {
    // ignore — saving indicator may not appear
  }

  // Wait briefly for React to re-render with the new row in the list
  await page.waitForTimeout(200);
}

/**
 * Assert that a project's settings match the expected shape, with EXACT
 * counts/IDs/names. A `toBeGreaterThan(0)` check passes even when
 * assignments are duplicated, partially clobbered, or point to the wrong TM/MT.
 *
 * Pass only the fields you care about — undefined fields are skipped.
 */
export async function assertProjectAssignments(
  page: Page,
  projectId: number,
  expected: {
    tmCount?: number;
    /** The MT router the project follows (`project.mt_router_id`); null = no MT. */
    mtRouterId?: number | null;
    /** The router's name as the settings read reports it (`project.mt_router_name`). */
    mtRouterName?: string | null;
    workflowStepCount?: number;
    /** Names that must appear in workflow_steps (subset, any order). */
    workflowStepNames?: string[];
    mtOnUpload?: boolean;
    fuzzyThreshold?: number;
    qaProfileId?: number | null;
    /** TM IDs each tm_assignment must reference (subset). */
    tmIds?: number[];
  },
): Promise<void> {
  const settings = await getProjectSettingsViaAPI(page, projectId);
  const data = settings.data;

  if (expected.tmCount !== undefined) {
    expect(data.tm_assignments?.length ?? 0, "tm_assignment count").toBe(expected.tmCount);
  }
  if (expected.mtRouterId !== undefined) {
    expect(data.project?.mt_router_id ?? null, "mt_router_id").toBe(expected.mtRouterId);
  }
  if (expected.mtRouterName !== undefined) {
    expect(data.project?.mt_router_name ?? null, "mt_router_name").toBe(expected.mtRouterName);
  }
  if (expected.workflowStepCount !== undefined) {
    expect(data.workflow_steps?.length ?? 0, "workflow_step count").toBe(expected.workflowStepCount);
  }
  if (expected.workflowStepNames) {
    const names = (data.workflow_steps ?? []).map((s: { name: string }) => s.name);
    for (const want of expected.workflowStepNames) {
      expect(names, `workflow_steps must contain "${want}", got ${JSON.stringify(names)}`).toContain(want);
    }
  }
  if (expected.mtOnUpload !== undefined) {
    expect(data.project?.mt_on_upload, "mt_on_upload").toBe(expected.mtOnUpload);
  }
  if (expected.fuzzyThreshold !== undefined) {
    expect(data.project?.fuzzy_threshold, "fuzzy_threshold").toBeCloseTo(expected.fuzzyThreshold, 2);
  }
  if (expected.qaProfileId !== undefined) {
    expect(data.project?.qa_profile_id, "qa_profile_id").toBe(expected.qaProfileId);
  }
  if (expected.tmIds) {
    const ids = (data.tm_assignments ?? []).map((a: { tm_id: number }) => a.tm_id);
    for (const want of expected.tmIds) {
      expect(ids, `tm_assignments must reference TM ${want}, got ${JSON.stringify(ids)}`).toContain(want);
    }
  }
}

/** Bearer header for `page.request` calls, from the logged-in page's token. */
export async function authHeaders(page: Page): Promise<Record<string, string>> {
  const token = await page.evaluate(() => localStorage.getItem("token"));
  return token ? { Authorization: `Bearer ${token}` } : {};
}

/**
 * Invite `email` into the inviter's org and join as them: a dev-login as the
 * invited email materializes the invite. `headers` is the inviter's auth and
 * defaults to the page's signed-in user. Returns the member's id and token.
 */
export async function inviteMember(
  page: Page, email: string, role = "project_manager", headers?: Record<string, string>,
): Promise<{ userId: number; token: string }> {
  const invite = await page.request.post(`${API_BASE}/api/invites`, {
    headers: headers ?? await authHeaders(page),
    data: { email, role },
  });
  expect(invite.ok(), `invite failed: ${invite.status()}`).toBe(true);
  expect((await invite.json()).data, "only the invite email carries the token").not.toHaveProperty("token");
  // The dev-login answers with the member's session cookie, so it runs in a
  // request context of its own: the inviter's page keeps its cookie, which
  // its event streams sign in with.
  const joinContext = await request.newContext({ baseURL: test.info().project.use.baseURL });
  try {
    const join = await joinContext.post(`${API_BASE}/api/auth/dev-login`, { data: { email } });
    expect(join.ok(), `join via dev-login failed: ${join.status()}`).toBe(true);
    const body = await join.json();
    expect(body.token, "the member's token").toBeTruthy();
    return { userId: body.user?.id, token: body.token };
  } finally {
    await joinContext.dispose();
  }
}

/**
 * Look up a seed project by name and return its ID. Never hardcode
 * `projectId = 1`: the auto-increment sequence diverges from the assumed ID
 * after a failed seed leaves orphan rows behind. Default seed: "Demo Project".
 */
export async function getSeedProjectId(page: Page, name = "Demo Project"): Promise<number> {
  const headers = await authHeaders(page);
  const res = await page.request.get(`${API_BASE}/api/projects`, { headers });
  expect(res.ok(), `list projects: ${res.status()}`).toBe(true);
  const projects = (await res.json()).data;
  const proj = projects.find((p: { name: string }) => p.name === name);
  expect(proj, `seed project "${name}" missing — run \`make seed\``).toBeDefined();
  return proj.id;
}

/**
 * Create a TM via the API and land on the /memories list with it visible. A TM
 * requires a source language + at least one target language; tests
 * that only need a TM to *exist* use this instead of driving the create dialog
 * (the create form itself is covered by the dedicated UI test). Returns the id.
 * `onCreated` gets the id before the navigation, so a caller can register it
 * for cleanup even when the navigation times out.
 */
export async function createTMViaAPI(
  page: Page,
  name: string,
  opts: { source?: string; targets?: string[]; customerId?: number; onCreated?: (id: number) => void } = {},
): Promise<number> {
  const res = await page.request.post(`${API_BASE}/api/tms`, {
    headers: await authHeaders(page),
    data: {
      name,
      source_language: opts.source ?? "en",
      target_languages: opts.targets ?? ["es"],
      customer_id: opts.customerId ?? null,
    },
  });
  expect(res.ok(), `create TM via API: ${res.status()} ${await res.text()}`).toBe(true);
  const id = (await res.json()).data.id as number;
  opts.onCreated?.(id);
  // Land on the list so callers can click the row.
  await page.goto("/memories");
  await expect(page.getByText(name).first()).toBeVisible({ timeout: 5000 });
  return id;
}

/** Import a TMX file into a TM; fails the test unless the upload is accepted. */
export async function importTMXViaAPI(
  page: Page,
  tmId: number,
  tmxContent: string,
  filename = "import.tmx",
): Promise<void> {
  const res = await page.request.post(`${API_BASE}/api/tms/${tmId}/import`, {
    headers: await authHeaders(page),
    multipart: { file: { name: filename, mimeType: "application/xml", buffer: Buffer.from(tmxContent) } },
  });
  expect(res.ok(), `TMX import into TM ${tmId}: ${res.status()}`).toBe(true);
}

/**
 * DELETE something a test created. Already gone (404) passes — the test
 * usually deleted it through the UI first; any other failure fails the test,
 * naming `what`.
 */
export async function deleteViaAPI(page: Page, path: string, what: string): Promise<void> {
  const res = await page.request.delete(`${API_BASE}${path}`, { headers: await authHeaders(page) });
  expect(res.ok() || res.status() === 404, `delete ${what}: ${res.status()} ${await res.text()}`).toBe(true);
}

/**
 * Delete a TM created by a test. Fails loudly: a refused delete (409 while any
 * settings row still assigns the TM) would otherwise leak the TM into every
 * later run. Empty a TM profile's list first with `emptyTMProfileViaAPI`.
 */
export async function deleteTMViaAPI(page: Page, tmId: number) {
  await deleteViaAPI(page, `/api/tms/${tmId}`, `TM ${tmId}`);
}

/** Remove every TM from a TM profile, so the profile no longer blocks deleting them. */
export async function emptyTMProfileViaAPI(page: Page, profileId: number) {
  const res = await page.request.put(`${API_BASE}/api/tm-profiles/${profileId}/tms`, {
    headers: await authHeaders(page),
    data: { assignments: [] },
  });
  expect(res.ok(), `empty TM profile ${profileId}: ${res.status()} ${await res.text()}`).toBe(true);
}

/**
 * Look up the seed "Default TM" by name. Used by tests that want to verify
 * project assignments reference the correct TM, since
 * "Default Template" pre-fill always points at this TM.
 */
export async function getSeedTMId(page: Page, name = "Default TM"): Promise<number> {
  const headers = await authHeaders(page);
  const res = await page.request.get(`${API_BASE}/api/tms`, { headers });
  expect(res.ok(), `list TMs: ${res.status()}`).toBe(true);
  const tms = (await res.json()).data;
  const tm = tms.find((t: { name: string }) => t.name === name);
  expect(tm, `seed TM "${name}" missing — run \`make seed\``).toBeDefined();
  return tm.id;
}

/**
 * The MT router the seed template follows. Projects created from "Default
 * Template" inherit its `mt_router_id`, so tests compare against this id and
 * name. Resolved through the template rather than by router name: a fresh
 * seed calls it "Default MT router" (cmd/seed/mt_router.go), a database
 * migrated from the per-profile model calls it "Default Template MT".
 */
export async function getSeedTemplateMTRouter(
  page: Page,
  templateName = "Default Template",
): Promise<{ id: number; name: string }> {
  const headers = await authHeaders(page);
  const list = await page.request.get(`${API_BASE}/api/templates`, { headers });
  expect(list.ok(), `list templates: ${list.status()}`).toBe(true);
  const row = ((await list.json()).data as { id: number; name: string }[]).find((t) => t.name === templateName);
  expect(row, `seed template "${templateName}" missing — run \`make seed\``).toBeDefined();
  // The list carries bare template rows; the settings-profile fields
  // (mt_router_id, mt_router_name) come with the detail read, under `template`.
  const detail = await page.request.get(`${API_BASE}/api/templates/${row!.id}`, { headers });
  expect(detail.ok(), `get template ${row!.id}: ${detail.status()}`).toBe(true);
  const tmpl = (await detail.json()).data.template as { mt_router_id: number | null; mt_router_name: string | null };
  expect(tmpl.mt_router_id, `seed template "${templateName}" has no MT router`).toEqual(expect.any(Number));
  expect(tmpl.mt_router_name, `seed template "${templateName}" router has no name`).toEqual(expect.any(String));
  return { id: tmpl.mt_router_id!, name: tmpl.mt_router_name! };
}

/**
 * Open every workflow-step section on the project page's Tasks tab. They
 * start closed; each holds the jobs whose current task is at that step.
 */
export async function openTaskSteps(page: Page): Promise<void> {
  await expect(page.getByTestId("task-step-sections")).toBeVisible();
  const closed = page.locator('[data-testid^="task-step-section-"] [aria-expanded="false"]');
  while ((await closed.count()) > 0) await closed.first().click();
}

/**
 * Open a job in the CAT editor from the project page's Tasks tab (each job
 * once, under its current step): pick the row by target language and/or file
 * name, and click it — a row click opens the job.
 *
 * Scoping by row keeps the locator deterministic — a global `.first()` row
 * races under parallel workers that add or remove files.
 *
 * Caller must already be on the project page (any tab).
 */
export async function openJob(page: Page, { lang, fileName }: { lang?: string; fileName?: string } = {}): Promise<void> {
  // Callers often arrive by clicking a project link; wait for it to land.
  await page.waitForURL(/\/projects\/\d+/);
  const url = new URL(page.url());
  if (!/\/projects\/\d+$/.test(url.pathname)) {
    const id = url.pathname.match(/\/projects\/(\d+)/)?.[1];
    expect(id, "openJob: not on a project page").toBeTruthy();
    await page.goto(`/projects/${id}`);
  }
  await openTaskSteps(page);
  // The language and the file sit in separate cells, so match the whole row.
  let rows = page.locator('tr[data-testid^="task-row-job:"]');
  if (fileName) rows = rows.filter({ hasText: fileName });
  if (lang) rows = rows.filter({ has: page.locator('[data-testid^="task-row-language-"]', { hasText: new RegExp(`^${lang}$`, "i") }) });
  const row = rows.first();
  await expect(row).toBeVisible({ timeout: 10000 });
  // A row click opens the editor in a new tab; go to the row's job instead,
  // so the test keeps driving `page`. The click itself is covered in
  // task-matrix.spec.ts.
  const jobId = (await row.getAttribute("data-testid"))?.replace("task-row-job:", "");
  const projectId = new URL(page.url()).pathname.match(/\/projects\/(\d+)/)?.[1];
  expect(jobId && projectId, "openJob: row has no job, or not on a project page").toBeTruthy();
  await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
}

/**
 * Select the first task (top row, first step) in the Tasks tab and wait for the selection bar. Caller is on the project page.
 */
export async function selectFirstTask(page: Page): Promise<void> {
  const id = new URL(page.url()).pathname.match(/\/projects\/(\d+)/)?.[1];
  expect(id, "selectFirstTask: not on a project page").toBeTruthy();
  await page.goto(`/projects/${id}`);
  await openTaskSteps(page);
  await page.locator('[data-testid^="task-select-job:"]').first().click();
  await expect(page.getByTestId("bulk-actions-bar")).toBeVisible();
  await expect(page.getByTestId("bulk-selected-count")).toHaveText("1 task selected");
}

/** The first job of a file, opened in the CAT editor. See {@link openJob}. */
export async function openFirstJobInFile(page: Page, fileName: string): Promise<void> {
  await openJob(page, { fileName });
}

/**
 * Open the bulk bar's "Pre-translate" menu and return its Apply TM / Apply MT
 * item (`bulk-apply-tm-btn` / `bulk-apply-mt-btn`).
 *
 * Returns the item locator so callers can chain a `waitForResponse` race:
 * the menu is already open by the time this resolves.
 *
 * Caller must have at least one job selected in the bulk bar.
 */
export async function openBulkPretranslate(page: Page, which: "tm" | "mt"): Promise<Locator> {
  await page.getByTestId("bulk-pretranslate-btn").click();
  const item = page.getByTestId(`bulk-apply-${which}-btn`);
  // The menu is portalled and mounts a tick after the click — wait for the
  // item so the caller's click can't race an empty popup.
  await expect(item).toBeVisible({ timeout: 5000 });
  return item;
}

/**
 * Delete a list-page row by hovering to reveal the delete button, clicking
 * it, and confirming in the dialog. Asserts the row is gone afterwards.
 *
 * Hard-asserts the button: an `if (deleteBtn.isVisible())` guard silently
 * no-ops when the button never appears (render bug, CSS change, wrong
 * selector), passing green while leaving the resource in the DB.
 *
 * Works for any list page where rows are <a> elements with a hover-revealed
 * delete button (project list, template list, TM list).
 *
 * @param rowText - Text content uniquely identifying the row.
 */
export async function deleteRowViaHover(page: Page, rowText: string): Promise<void> {
  const row = page.locator("a").filter({ hasText: rowText }).first();
  await row.hover();
  const deleteBtn = row.locator('button[title*="Delete"]').or(
    row.locator("button").last(),
  );
  await expect(deleteBtn).toBeVisible({ timeout: 2000 });
  await deleteBtn.click();
  const confirmBtn = page.getByRole("button", { name: /delete/i }).last();
  await expect(confirmBtn).toBeVisible({ timeout: 2000 });
  await confirmBtn.click();
  await expect(page.getByText(rowText)).not.toBeVisible({ timeout: 3000 });
}

/**
 * Create a minimal en→de project via the API (extra fields merged into the
 * body, e.g. workflow_id). Returns its id; pair with deleteProjectViaAPI.
 */
export async function createProjectViaAPI(page: Page, name: string, extra: Record<string, unknown> = {}): Promise<number> {
  const res = await page.request.post(`${API_BASE}/api/projects`, {
    headers: await authHeaders(page),
    data: { name, source_language: "en", target_languages: ["de"], ...extra },
  });
  expect(res.ok(), `create project: ${res.status()} ${await res.text()}`).toBe(true);
  return (await res.json()).data.id;
}

/**
 * Delete the draft the wizard autosaved for this page, if any.
 * Any spec that types into the New Project form creates a
 * real `status='draft'` row on the first blur, whether or not it goes on to
 * publish — so a test that only asserts a validation error still has one to
 * clean up. Reads the id from `?draft=`, which the autosave pins into the URL.
 */
export async function deleteDraftFromURL(page: Page): Promise<void> {
  const match = page.url().match(/[?&]draft=(\d+)/);
  if (!match) return;
  await page.request.delete(`${API_BASE}/api/projects/${match[1]}`, {
    headers: await authHeaders(page),
  });
}

/** Delete a project; fails the test unless it is deleted or already gone. */
export async function deleteProjectViaAPI(page: Page, projectId: number): Promise<void> {
  await deleteViaAPI(page, `/api/projects/${projectId}`, `project ${projectId}`);
}

/**
 * Fetch project settings via API (for verifying that assignments were saved).
 */
export async function getProjectSettingsViaAPI(page: Page, projectId: number) {
  const response = await page.request.get(
    `${API_BASE}/api/projects/${projectId}/settings`,
    {
      headers: await authHeaders(page),
    }
  );
  expect(response.ok()).toBe(true);
  return response.json();
}

/**
 * Delete a file by ID via the API. Used for E2E test cleanup.
 */
export async function deleteFileViaAPI(
  page: Page,
  projectId: number,
  fileId: number
) {
  const response = await page.request.delete(
    `${API_BASE}/api/projects/${projectId}/files/${fileId}`,
    {
      headers: await authHeaders(page),
    }
  );
  // Don't assert — file may already be deleted
  return response.ok();
}

/**
 * Create a fully isolated CAT-editor playground: a fresh project (with
 * pre-translate-on-upload DISABLED), a single uploaded file, and the
 * resulting job — ready for the test to navigate to and exercise.
 *
 * **Why this exists.** CAT/TM tests that share the seed Demo Project trip
 * over each other under parallel workers: its uploads enqueue pipeline jobs
 * that serialise on the asynq workers, the Default TM carries entries from
 * earlier runs into new segments, and `.last()` file-list selectors match
 * leftovers from other runs. `createIsolatedWorkflowJob` is the same pattern
 * for workflow tests.
 *
 * **Each isolated project carries:**
 *   - Its own settings_profile with `mt_on_upload: false` —
 *     uploads do NOT enqueue pipeline jobs. The test's segments stay
 *     untranslated until the test itself decides otherwise (matches what
 *     90% of CAT lifecycle tests want — they exercise edit/save/conflict
 *     invariants, not pre-translate output).
 *   - A unique target language (caller can override).
 *   - No TM assignment (so the TM-pollution accumulation across runs
 *     doesn't leak into this test's segments).
 *
 * **Cleanup contract.** The returned `cleanup` is idempotent and
 * cascade-deletes via `DELETE /api/projects/{id}`, so file/job/segment/
 * translation rows all go with the project. It reads the token through the
 * page, so an `afterEach` deletes `projectId` (from `onCreated`) with the
 * `request` fixture instead.
 *
 * **What it does NOT cover.** Tests that genuinely want to exercise the
 * pre-translate pipeline path can pass
 * `preTranslate: true` to opt back in. Or for tests that don't need a CAT
 * editor at all (TM-only, project list, settings UI), don't use this.
 */
export async function createIsolatedCATJob(
  page: Page,
  opts: {
    /** Filename of the uploaded source file. Should be unique per run. */
    fileName: string;
    /** Source file contents. */
    content: string;
    /** MIME type. Default "text/plain". */
    contentType?: string;
    /** Target languages. Default ["es"]. */
    targetLanguages?: string[];
    /** Source language. Default "en". */
    sourceLanguage?: string;
    /** Project name. Default derived from fileName. */
    projectName?: string;
    /** Set true to KEEP pre-translate-on-upload (default: false). Pass true
     *  for tests that explicitly exercise the pipeline. */
    preTranslate?: boolean;
    /** Pass `null` to skip auto-assigning a QA profile. Default: assigns the
     *  first profile from `/api/qa-profiles` so `Run QA` works. */
    qaProfile?: null;
    /** Sets "Strings contain HTML" before the upload. Default: the project's. */
    inlineHTML?: boolean;
    /** Gets the project id right after the create, so an `afterEach` can
     *  delete it even when a later setup step fails. */
    onCreated?: (projectId: number) => void;
  },
): Promise<{
  projectId: number;
  fileId: number;
  jobId: number;
  /** Bearer token for direct API calls (page.request). */
  token: string | null;
  /** Click into the editor URL for this job. Awaits `Hello world` or
   *  whatever the source file's first line text is being visible. */
  navigateToEditor: (firstLineText?: RegExp) => Promise<void>;
  /** Cascade-delete the project (and everything under it). Idempotent. */
  cleanup: () => Promise<void>;
}> {
  const targetLanguages = opts.targetLanguages ?? ["es"];
  const sourceLanguage = opts.sourceLanguage ?? "en";
  const projectName = opts.projectName ?? `iso ${opts.fileName} ${Date.now()}`;
  const contentType = opts.contentType ?? "text/plain";

  const token = await page.evaluate(() => localStorage.getItem("token"));
  const headers = await authHeaders(page);

  // 1. Create project — fresh settings_profile with template defaults.
  const projRes = await page.request.post(`${API_BASE}/api/projects`, {
    headers,
    data: { name: projectName, source_language: sourceLanguage, target_languages: targetLanguages },
  });
  expect(projRes.ok(), `create project: ${projRes.status()} ${await projRes.text()}`).toBe(true);
  const projectId: number = (await projRes.json()).data.id;
  opts.onCreated?.(projectId);

  // 2. Disable pre-translate-on-upload (the template default may be true).
  // Without this, the upload at step 3 enqueues a pipeline job that races
  // against test code — see the "Why this exists" comment above.
  // Also assign the first available QA profile so `Run QA` works. The freshly
  // created project's settings_profile may not inherit a qa_profile_id from
  // the default template; tests that exercise Run QA get a
  // 400 "project has no QA profile assigned" without this. Pass `qaProfile:
  // null` to opt out (rare).
  const settingsPatch: Record<string, unknown> = {};
  if (!opts.preTranslate) {
    settingsPatch.mt_on_upload = false;
    settingsPatch.tm_on_upload = false;
  }
  if (opts.inlineHTML !== undefined) settingsPatch.inline_html = opts.inlineHTML;
  if (opts.qaProfile !== null) {
    const profilesRes = await page.request.get(`${API_BASE}/api/qa-profiles`, { headers });
    if (profilesRes.ok()) {
      const profiles: { id: number; name: string }[] = (await profilesRes.json()).data ?? [];
      // Pin to "Standard QA" (seed default — has empty_target/tag_checks/etc
      // all enabled). The list endpoint returns profiles sorted by name, so
      // orphan E2E test profiles ("E2E QA Rename ...") often sort BEFORE
      // "Standard QA" — picking [0] would land on an empty orphan with no
      // rules enabled, and Run QA would silently return 0 issues. Look up
      // by name explicitly; fall back to [0] only if Standard QA is missing
      // (caller can override with `qaProfile: null` to opt out entirely).
      const standard = profiles.find(p => p.name === "Standard QA");
      const picked = standard ?? profiles[0];
      if (picked) settingsPatch.qa_profile_id = picked.id;
    }
  }
  if (Object.keys(settingsPatch).length > 0) {
    const settingsRes = await page.request.patch(
      `${API_BASE}/api/projects/${projectId}/settings`,
      { headers, data: settingsPatch },
    );
    expect(settingsRes.ok(),
      `patch settings: ${settingsRes.status()} ${await settingsRes.text()}`,
    ).toBe(true);
  }

  // 3. Upload the file. With pre-translate disabled, this commits
  // synchronously (file + segments + translations + job) and returns.
  const upload = await uploadFileViaAPI(page, projectId, opts.fileName, opts.content, contentType);
  const fileId: number = upload.data?.id ?? upload.id;

  // 4. Get the job for the (single) target language. With one target lang
  // there's exactly one job; multiple target langs create multiple jobs and
  // this returns the first.
  const jobsRes = await page.request.get(`${API_BASE}/api/projects/${projectId}/jobs`, { headers });
  expect(jobsRes.ok(), `list jobs: ${jobsRes.status()}`).toBe(true);
  const jobs = (await jobsRes.json()).data;
  expect(jobs.length, `expected ≥1 job after upload, got ${jobs.length}`).toBeGreaterThan(0);
  const jobId: number = jobs[0].id;

  return {
    projectId,
    fileId,
    jobId,
    token,
    navigateToEditor: async (firstLineText) => {
      await page.goto(`/projects/${projectId}/jobs/${jobId}/edit`);
      // Most callers verify the source file's first line is visible to
      // assert the editor mounted. Default to the first line of `content`
      // (up to 30 chars), but caller can override.
      const expected = firstLineText ?? new RegExp(
        opts.content.split("\n")[0].slice(0, 30).replace(/[.*+?^${}()|[\]\\]/g, "\\$&"),
      );
      await expect(page.getByText(expected).first()).toBeVisible({ timeout: 10000 });
    },
    cleanup: async () => {
      // Cascade-delete via DELETE /api/projects/{id}. Don't assert: the test
      // may have failed before reaching us (after a timeout the page is
      // already closed), and an error here would hide that failure.
      if (page.isClosed()) return;
      await page.request.delete(`${API_BASE}/api/projects/${projectId}`, { headers: await authHeaders(page) })
        .catch(() => {});
    },
  };
}

/**
 * Pretranslate a whole file by looping over its jobs and calling the
 * production phase-split endpoints (tm-apply then mt-apply per target
 * language). Mirrors what the CAT editor's "Apply TM" + "Apply MT" buttons
 * do in sequence.
 *
 * If a test cares about QE+APE outcomes, attach a QE+APE engine to the job's
 * workflow step and call POST /api/jobs/{id}/ai-step separately.
 */
export async function pretranslateFileViaAPI(
  page: Page,
  projectId: number,
  fileId: number,
  // Explicit token for multi-user scenarios (j23 acts as a specific owner);
  // defaults to the logged-in page's session.
  token?: string,
): Promise<void> {
  const headers = token ? { Authorization: `Bearer ${token}` } : await authHeaders(page);
  const jobsResp = await page.request.get(`${API_BASE}/api/projects/${projectId}/jobs`, { headers });
  expect(jobsResp.ok(), `list jobs failed: ${jobsResp.status()}`).toBe(true);
  const jobs = (await jobsResp.json()).data as Array<{ id: number; file_id: number }>;
  const fileJobs = jobs.filter((j) => j.file_id === fileId);
  // A phase with nothing configured (no TM / no MT engine for the lang pair)
  // returns 400 "No … configured" — a legitimate pretranslate no-op, not a
  // failure (e.g. J10 deliberately creates a TM-less, MT-less project to test
  // fallback/error-recovery). Accept 2xx (applied) or that specific 400;
  // anything else — especially a 5xx crash — is a real failure.
  const okOrNoConfig = async (resp: import("@playwright/test").APIResponse, phase: string, jobId: number) => {
    if (resp.ok()) return;
    const body = await resp.json().catch(() => ({} as { error?: { message?: string } }));
    const msg = body?.error?.message ?? "";
    expect(
      resp.status() === 400 && /no .*configured/i.test(msg),
      `${phase} job=${jobId} failed unexpectedly: ${resp.status()} ${msg}`,
    ).toBe(true);
  };
  // The upload hook auto-enqueues a pipeline run when the project has TM/MT
  // configured, and the phase handlers' double-start claim
  // answers 409 while that run is active. Callers rely on the phase having
  // ACTUALLY been applied when this helper returns, so on a 409 we wait for
  // the running pipeline to finish and retry once; a second 409 fails loudly
  // via okOrNoConfig.
  const isRunning = (j: { pipeline_status?: string; current_step?: string | null }) =>
    (j.pipeline_status === "queued" || j.pipeline_status === "in_progress") &&
    !(j.current_step ?? "").toLowerCase().endsWith("complete");
  const waitForJobIdle = async (jobId: number) => {
    for (let i = 0; i < 60; i++) {
      const resp = await page.request.get(`${API_BASE}/api/projects/${projectId}/jobs`, { headers });
      const job = ((await resp.json()).data as Array<{ id: number; pipeline_status?: string; current_step?: string | null }>)
        .find((j) => j.id === jobId);
      if (!job || !isRunning(job)) return;
      await page.waitForTimeout(500);
    }
    expect(false, `job ${jobId} pipeline still running after 30s`).toBe(true);
  };
  const applyPhase = async (phase: "tm-apply" | "mt-apply", jobId: number) => {
    let resp = await page.request.post(`${API_BASE}/api/jobs/${jobId}/${phase}`, { headers });
    if (resp.status() === 409) {
      await waitForJobIdle(jobId);
      resp = await page.request.post(`${API_BASE}/api/jobs/${jobId}/${phase}`, { headers });
    }
    await okOrNoConfig(resp, phase, jobId);
  };
  for (const job of fileJobs) {
    await applyPhase("tm-apply", job.id);
    await applyPhase("mt-apply", job.id);
  }
}

/**
 * Create a TM profile via the API (name + optional TMs / threshold). Returns
 * its id. Pair with deleteTMProfileViaAPI in a finally block.
 */
export async function createTMProfileViaAPI(
  page: Page,
  name: string,
  opts: { tmIds?: number[]; fuzzyThreshold?: number } = {},
): Promise<number> {
  const res = await page.request.post(`${API_BASE}/api/tm-profiles`, {
    headers: await authHeaders(page),
    data: {
      name,
      fuzzy_threshold: opts.fuzzyThreshold,
      tms: (opts.tmIds ?? []).map((id, i) => ({ tm_id: id, mode: "read_write", priority: i })),
    },
  });
  expect(res.ok(), `create TM profile via API: ${res.status()} ${await res.text()}`).toBe(true);
  return (await res.json()).data.id as number;
}

/** Delete a TM profile via the API. Cleanup helper; ignores failures. */
export async function deleteTMProfileViaAPI(page: Page, profileId: number) {
  const res = await page.request.delete(`${API_BASE}/api/tm-profiles/${profileId}`, {
    headers: await authHeaders(page),
  });
  return res.ok();
}

// ─── Step + workflow libraries ───────────────────────────

/** A step as tests describe it: by name, with optional gate / AI router /
 *  assignee / deadline. Resolved to a library reference before the PUT. */
export interface StepSpec {
  name: string;
  role_required?: string;
  ai_router_id?: number | null;
  default_assignee?: number | null;
  deadline?: string | null;
  step_type?: "human" | "hybrid" | "ai";
}

/** Resolve a library step definition by name (case-insensitive), creating it
 *  when missing. Returns its id. */
export async function ensureStepDefinitionViaAPI(page: Page, spec: StepSpec): Promise<number> {
  const headers = await authHeaders(page);
  const list = await page.request.get(`${API_BASE}/api/step-definitions`, { headers });
  expect(list.ok(), `list step definitions: ${list.status()}`).toBe(true);
  const existing = ((await list.json()).data as { id: number; name: string }[])
    .find((d) => d.name.toLowerCase() === spec.name.toLowerCase());
  if (existing) return existing.id;
  const res = await page.request.post(`${API_BASE}/api/step-definitions`, {
    headers,
    data: {
      name: spec.name,
      step_type: spec.step_type ?? (spec.ai_router_id ? "hybrid" : "human"),
      role_required: spec.role_required ?? "linguist",
      ai_router_id: spec.ai_router_id ?? null,
    },
  });
  expect(res.ok(), `create step definition ${spec.name}: ${res.status()} ${await res.text()}`).toBe(true);
  return (await res.json()).data.id;
}

/** Workflow-steps PUT body items for the given specs. */
export async function libraryStepsPayload(page: Page, steps: StepSpec[]) {
  const out = [];
  for (const s of steps) {
    out.push({
      step_definition_id: await ensureStepDefinitionViaAPI(page, s),
      default_assignee: s.default_assignee ?? null,
      deadline: s.deadline ?? null,
    });
  }
  return out;
}

/** PUT a project's workflow steps from name-based specs. */
export async function setProjectWorkflowStepsViaAPI(page: Page, projectId: number, steps: StepSpec[]) {
  const res = await page.request.put(`${API_BASE}/api/projects/${projectId}/workflow-steps`, {
    headers: await authHeaders(page),
    data: { steps: await libraryStepsPayload(page, steps) },
  });
  expect(res.ok(), `set workflow steps ${projectId}: ${res.status()} ${await res.text()}`).toBe(true);
  return (await res.json()).data;
}

export async function createWorkflowViaAPI(page: Page, name: string, steps: StepSpec[]): Promise<number> {
  const items = [];
  for (const s of steps) {
    items.push({ step_definition_id: await ensureStepDefinitionViaAPI(page, s), default_assignee: s.default_assignee ?? null });
  }
  const res = await page.request.post(`${API_BASE}/api/workflows`, {
    headers: await authHeaders(page),
    data: { name, steps: items },
  });
  expect(res.ok(), `create workflow ${name}: ${res.status()} ${await res.text()}`).toBe(true);
  return (await res.json()).data.id;
}

export async function deleteWorkflowViaAPI(page: Page, id: number) {
  const res = await page.request.delete(`${API_BASE}/api/workflows/${id}`, { headers: await authHeaders(page) });
  expect([204, 404]).toContain(res.status());
}

export async function deleteStepDefinitionViaAPI(page: Page, id: number) {
  const res = await page.request.delete(`${API_BASE}/api/step-definitions/${id}`, { headers: await authHeaders(page) });
  expect([204, 404]).toContain(res.status());
}

/**
 * New project page: click the "From scratch" start button. The form
 * (name, languages, settings…) only renders after this choice.
 */
export async function startFromScratch(page: Page) {
  await page.getByTestId("start-from-scratch").click();
  await expect(page.getByTestId("project-create-form")).toBeVisible();
}

/**
 * New project page: answer the TM picker with "From scratch". A new project
 * starts with no answer, and the match scoring + memory list only render once
 * one is given.
 */
export async function chooseTMFromScratch(page: Page) {
  await pickComboboxValue(page, "tm-profile-select", "custom");
  await expect(page.getByTestId("tm-assignments")).toBeVisible();
}

/**
 * New project page: choose "From template", pick the template named `name`
 * in the dialog, and wait for the form to show with that template applied.
 */
export async function startFromTemplate(page: Page, name: string | RegExp) {
  await page.getByTestId("start-from-template").click();
  const dialog = page.getByTestId("template-picker-dialog");
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByTestId("project-create-form")).toBeVisible();
  await expect(page.getByTestId("start-from-source")).toContainText("Using template");
}

/**
 * The tenant's built-in labels. Every tenant has both, so
 * this never creates anything — it just resolves the ids a test needs to add a
 * value or assert a project's pick.
 */
export async function systemLabelsViaAPI(page: Page): Promise<{ customer: number; domain: number }> {
  const res = await page.request.get(`${API_BASE}/api/labels`);
  expect(res.ok(), `list labels: ${res.status()}`).toBe(true);
  const labels = (await res.json()).data as { id: number; name: string; system: boolean }[];
  const byKey = (name: string) => {
    const found = labels.find(l => l.system && l.name === name);
    expect(found, `built-in label ${name} missing — every tenant should have it`).toBeDefined();
    return found!.id;
  };
  return { customer: byKey("Customer"), domain: byKey("Domain") };
}

/**
 * Adds a value to a label and returns its id, or resolves the existing one.
 * Idempotent on purpose: values are never cleaned up between local runs (a
 * value a project used cannot be retired), so a second run would otherwise
 * fail on the 409 the unique index raises.
 */
export async function addLabelValueViaAPI(page: Page, labelId: number, value: string): Promise<number> {
  const res = await page.request.post(`${API_BASE}/api/labels/${labelId}/values`, { data: { value } });
  if (res.status() === 409) {
    const list = await page.request.get(`${API_BASE}/api/labels`);
    expect(list.ok(), `list labels: ${list.status()}`).toBe(true);
    const labels = (await list.json()).data as { id: number; values: { id: number; value: string }[] }[];
    const existing = labels.find(l => l.id === labelId)?.values
      .find(v => v.value.toLowerCase() === value.toLowerCase());
    expect(existing, `label ${labelId} reported a duplicate ${value} it does not have`).toBeDefined();
    return existing!.id;
  }
  expect(res.ok(), `add label value: ${res.status()} ${await res.text()}`).toBe(true);
  return (await res.json()).data.id as number;
}

/** A project's label picks, as `{ "Domain": "Patents" }`. */
export async function projectLabelsViaAPI(page: Page, projectId: number): Promise<Record<string, string>> {
  const res = await page.request.get(`${API_BASE}/api/projects/${projectId}/labels`);
  expect(res.ok(), `list project labels: ${res.status()}`).toBe(true);
  const rows = (await res.json()).data as { label_name: string; label_value: string }[];
  return Object.fromEntries(rows.map(r => [r.label_name, r.label_value]));
}

/**
 * Set a deadline through its DeadlinePicker (chip or field): open the popover,
 * page the calendar to the month, click the day, type the time, press Done —
 * Done is when the picker commits. `value` is "yyyy-MM-ddTHH:mm" in the
 * browser's zone; the picker sends it to the server as UTC.
 */
export async function setDeadline(page: Page, trigger: Locator, value: string) {
  const [day, time] = value.split("T");
  const panel = page.locator('[data-slot="popover-content"]');
  await expect(panel).toHaveCount(0, { timeout: 3000 });
  await trigger.click();
  await expect(panel).toHaveCount(1, { timeout: 3000 });

  const cell = panel.locator(`[data-day="${day}"]:not([data-outside])`);
  for (let i = 0; i < 60 && (await cell.count()) === 0; i++) {
    const shown = await panel.locator("[data-day]:not([data-outside])").first().getAttribute("data-day");
    await panel.getByRole("button", { name: day > shown! ? /next month/i : /previous month/i }).click();
  }
  await cell.getByRole("button").click();
  await panel.getByLabel("Time", { exact: true }).fill(time);
  // Typing leaves the time list open, and an open list hides the rest of the
  // popover from assistive tech — so Done is found with includeHidden. The
  // click itself is a real one, as a mouse user would make it.
  await panel.getByRole("button", { name: "Done", includeHidden: true }).click();
  await expect(panel).toHaveCount(0, { timeout: 3000 });
}
