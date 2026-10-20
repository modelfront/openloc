import { test, expect, Page } from "@playwright/test";
import { login, authHeaders, createTMViaAPI, importTMXViaAPI, selectLanguage, selectTargetLanguages } from "./helpers";
import path from "path";

test.describe("Translation memory", () => {
  // TMs the running test created. They are deleted in afterEach, not in the
  // test's finally: the hook has its own time budget, so a slow step reports
  // its own error instead of the cleanup's timeout. The delete uses headers
  // read right after login and the `request` fixture, so it works whatever
  // state the page is left in. Best-effort, so a failed cleanup never changes
  // a test's result.
  let tmIds: number[] = [];
  let headers: Record<string, string> = {};

  const createTM = (page: Page, name: string) =>
    createTMViaAPI(page, name, { onCreated: (id) => tmIds.push(id) });

  test.beforeEach(async ({ page }) => {
    await login(page);
    headers = await authHeaders(page);
  });

  test.afterEach(async ({ request }) => {
    for (const id of tmIds) await request.delete(`/api/tms/${id}`, { headers }).catch(() => {});
    tmIds = [];
  });

  test("TM list shows Default TM", async ({ page }) => {
    await page.getByRole("link", { name: /translation memor/i }).click();
    await expect(page).toHaveURL(/\/memories/);
    await expect(
      page.getByRole("heading", { name: /translation memories/i })
    ).toBeVisible();
    await expect(page.getByText(/default tm/i)).toBeVisible();
  });

  test("TM browser shows entries with language pairs", async ({ page }) => {
    await page.getByRole("link", { name: /translation memor/i }).click();

    // Click on Default TM to open browser. The link's accessible name on the
    // list page is `Default TM N entries` — match by role+name so we don't
    // also match orphan TM cards from prior failed runs (Bulk FR Test ...)
    // that contain "TM" in their text.
    await page.getByRole("link", { name: /^default tm/i }).click();
    await expect(page).toHaveURL(/\/memories\/\d+/);
    // Wait for the detail-view breadcrumb to render — this proves we left
    // the list page. Without this, the `getByText(/entries/i)` below strict-
    // mode-collides with list-page TM cards that haven't unmounted yet
    // (URL changes synchronously, the new view paints when its react-query
    // resolves — small race window but enough under parallel load).
    await expect(page.getByRole("navigation", { name: /breadcrumb/i })).toContainText(/default tm/i, { timeout: 5000 });

    // Should show entry count
    await expect(page.getByText(/entries/i)).toBeVisible();

    // Should show language pair filter buttons
    await expect(page.getByText(/all/i).first()).toBeVisible();

    // Should show actual TM entries from seed data
    // Seed has entries like "We thank you for your patience"
    await expect(
      page.getByText(/patience|verification code|wallet/i).first()
    ).toBeVisible();

    // Per-row language columns: SL (entry's own source locale — can differ
    // from the TM's declared one) and TL.
    await expect(page.getByRole("columnheader", { name: "SL" })).toBeVisible();
    await expect(page.getByRole("columnheader", { name: "TL" })).toBeVisible();
  });

  test("TM browser language pair filter narrows the entry count", async ({ page }) => {
    // Hard-assert the filter button exists, click it, and assert the count
    // actually narrows: "entries" text alone shows whatever the filter does.
    await page.getByRole("link", { name: /translation memor/i }).click();
    await page.getByText(/default tm/i).click();

    // Wait for the unfiltered count label to render. Default TM seed has
    // 23 entries per pair × 3 pairs (en→es/fr/de) = 69 total.
    // We don't hard-pin "23 of 69" because seed entries can grow over time —
    // we capture the current totals dynamically and assert that filtering
    // makes the count strictly smaller AND no larger than 1/3 of the total
    // (since filter restricts to one pair out of three).
    const entryCount = page.getByText(/\d+ of \d+ entries/);
    await expect(entryCount).toBeVisible({ timeout: 5000 });
    const unfilteredText = (await entryCount.textContent()) ?? "";
    const unfilteredMatch = unfilteredText.match(/of\s+([\d,]+)\s+entries/);
    expect(unfilteredMatch, `unfiltered count text should match "of N entries", got "${unfilteredText}"`).not.toBeNull();
    const totalEntries = parseInt(unfilteredMatch![1].replace(/,/g, ""), 10);
    expect(totalEntries, "Default TM should have at least one entry").toBeGreaterThan(0);

    // Click the ES target-language filter. Tabs show only the target language
    // (source is shown once above the list). Hard-assert button exists.
    const esFilter = page.getByRole("button", { name: /^ES$/i });
    await expect(esFilter).toBeVisible({ timeout: 5000 });
    await esFilter.click();

    // After filtering, count must be strictly less than the original total
    // (since seed has multiple pairs).
    await expect.poll(async () => {
      const text = (await entryCount.textContent()) ?? "";
      const m = text.match(/of\s+([\d,]+)\s+entries/);
      return m ? parseInt(m[1].replace(/,/g, ""), 10) : Infinity;
    }, { timeout: 5000, message: "filter should reduce entry count" }).toBeLessThan(totalEntries);
  });

  test("TM browser search options: scope, exact match, regex, and the hit tint", async ({ page }) => {
    test.setTimeout(30_000);
    // Real backend: the options compile to one Postgres regex, so this is the
    // layer that proves \b and exact matching work in the database.
    const tmName = `Search Options ${Date.now()}`;
    const tmxContent = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en" creationtool="ui-test" creationtoolversion="1"/>
  <body>
    <tu><tuv xml:lang="en"><seg>The server is down</seg></tuv><tuv xml:lang="es"><seg>El servidor no funciona</seg></tuv></tu>
    <tu><tuv xml:lang="en"><seg>Restart the servers</seg></tuv><tuv xml:lang="es"><seg>Reinicia los servidores</seg></tuv></tu>
    <tu><tuv xml:lang="en"><seg>Server</seg></tuv><tuv xml:lang="es"><seg>Servidor</seg></tuv></tu>
  </body>
</tmx>`;
    const tmId = await createTM(page, tmName);
    await importTMXViaAPI(page, tmId, tmxContent, "search.tmx");
    await page.goto(`/memories/${tmId}`);
    await expect(page.getByText("3 of 3 entries")).toBeVisible({ timeout: 10_000 });

    const search = page.getByTestId("tm-search");
    const searched = (q: string) => page.waitForResponse(r =>
      r.url().includes(`/api/tms/${tmId}/entries`) && new URL(r.url()).searchParams.get("q") === q);

    // Default: substring, any case, both fields — and the hit is tinted.
    let res = searched("server");
    await search.fill("server");
    await res;
    await expect(page.getByText("3 of 3 matching entries")).toBeVisible();
    // One tinted run per source hit: "server", "server"s, "Server".
    await expect(page.locator('span[class~="bg-yellow-200/60"]')).toHaveCount(3);

    // The options sit on the filter row, always shown.
    await expect(page.getByTestId("tm-search-advanced")).toBeVisible();

    // Exact: only the entry whose whole text is "Server".
    res = searched("server");
    await page.getByRole("checkbox", { name: "Exact", exact: true }).check();
    await res;
    await expect(page.getByText("1 of 1 matching entries")).toBeVisible();
    await expect(page.getByText("Servidor", { exact: true })).toBeVisible();
    await page.getByRole("checkbox", { name: "Exact", exact: true }).uncheck();

    // Regex word boundary: "servers" no longer matches.
    await page.getByRole("checkbox", { name: "Regex", exact: true }).check();
    res = searched("\\bserver\\b");
    await search.fill("\\bserver\\b");
    await res;
    await expect(page.getByText("2 of 2 matching entries")).toBeVisible();
    await expect(page.getByText("Restart the servers")).toBeHidden();

    // Target-only scope: the English pattern finds nothing in Spanish.
    res = searched("\\bserver\\b");
    await page.getByTestId("tm-search-scope-target").click();
    await res;
    await expect(page.getByText("No entries match your search.")).toBeVisible();

    // An invalid pattern is flagged and the table empties.
    await page.getByTestId("tm-search-scope-both").click();
    await search.fill("(server");
    await expect(page.getByTestId("tm-search-regex-error")).toHaveText("Invalid regular expression");
    await expect(page.getByText("Invalid regular expression.")).toBeVisible();
  });

  test("TM CRUD: create (name + source + targets) and delete a TM", async ({ page }) => {
    await page.getByRole("link", { name: /translation memor/i }).click();

    // Use a unique name to avoid collisions from prior failed runs
    const tmName = `E2E TM ${Date.now()}`;

    // Create new TM via the dialog — a TM requires a source language and at
    // least one target language. Customer is optional (left blank).
    await page.getByRole("button", { name: /new tm/i }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await dialog.getByPlaceholder(/tm name/i).fill(tmName);
    await selectLanguage(page, dialog.locator('[data-testid="tm-create-source"] button'), "en");
    await selectTargetLanguages(page, dialog.locator('[data-testid="tm-create-target"] button'), ["es", "fr"]);
    const [createResp] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().split("?")[0].endsWith("/api/tms") && r.request().method() === "POST",
        { timeout: 10_000 },
      ),
      dialog.getByRole("button", { name: /^create$/i }).click(),
    ]);
    expect(createResp.ok(), "create TM should succeed").toBe(true);
    const tmId: number = (await createResp.json()).data.id;
    // The UI delete below is the test; the afterEach catches a failure before it.
    tmIds.push(tmId);

    // Should appear in list with its language pair rendered.
    const row = page.locator("a").filter({ hasText: tmName }).first();
    await expect(row).toBeVisible();
    await expect(row).toContainText(/en\s*→\s*es, fr/i);

    // Delete it
    const deleteTarget = page.locator("a").filter({ hasText: tmName }).first();
    await deleteTarget.hover();
    const deleteBtn = deleteTarget.locator('button[title*="Delete"]').or(
      deleteTarget.locator("button").last()
    );
    await expect(deleteBtn).toBeVisible({ timeout: 2000 });
    await deleteBtn.click();
    // Confirm in the dialog, not any "Delete" button on the page.
    await page.getByRole("alertdialog").getByRole("button", { name: /^delete$/i }).click();
    // Row should be gone. Assert on the list link specifically — the create
    // success toast ("TM \"<name>\" created") also contains the name, so a bare
    // getByText(tmName) would strict-mode-collide with the lingering toast.
    await expect(page.locator("a").filter({ hasText: tmName })).toHaveCount(0, { timeout: 3000 });
  });

  test("TMX import: upload TMX file and verify entries appear", async ({
    page,
  }) => {
    // Create a temp TM for this test
    const tmName = `TMX Import Test ${Date.now()}`;
    await createTM(page, tmName);
    // Navigate to TM browser
    await page.getByText(tmName).first().click();
    await expect(page).toHaveURL(/\/memories\/\d+/);

    // Wait for Import TMX button to be visible
    await expect(
      page.getByRole("button", { name: /import tmx/i })
    ).toBeVisible();

    // Import TMX file via the hidden file input
    const fileInput = page.locator('input[type="file"][accept=".tmx"]');
    const tmxPath = path.resolve(__dirname, "fixtures/sample.tmx");
    await fileInput.setInputFiles(tmxPath);

    // Wait for entries to appear (import processes async, entries refresh after)
    await expect(page.getByText(/hello world/i).first()).toBeVisible({
      timeout: 15000,
    });
    await expect(page.getByText(/hola mundo/i).first()).toBeVisible();
  });

  test("TMX export: export button available for Default TM", async ({
    page,
  }) => {
    await page.getByRole("link", { name: /translation memor/i }).click();
    await page.getByText(/default tm/i).click();
    await expect(page).toHaveURL(/\/memories\/\d+/);

    // Wait for entries to load (non-zero count)
    await expect(page.getByText(/\d+ of \d+ entries|\d+ entries/i).first()).toBeVisible();
    await expect(page.getByText(/patience|verification code|wallet/i).first()).toBeVisible();

    // Export button should be enabled (not disabled) since there are entries
    const exportBtn = page.getByRole("button", { name: /export tmx/i });
    await expect(exportBtn).toBeVisible();
    await expect(exportBtn).toBeEnabled();

    // Click export and verify the API call succeeds (check via network)
    const responsePromise = page.waitForResponse(
      (r) => r.url().includes("/export") && r.status() === 200
    );
    await exportBtn.click();
    const response = await responsePromise;
    expect(response.headers()["content-type"]).toContain("application/xml");
  });

  test("TMX round-trip: import preserves multiple translations and metadata, export includes them", async ({
    page,
  }) => {
    test.setTimeout(30_000);

    // Create a temp TM
    const tmName = `Roundtrip Test ${Date.now()}`;
    await createTM(page, tmName);
    // Navigate to TM browser
    await page.getByText(tmName).first().click();
    await expect(page).toHaveURL(/\/memories\/\d+/);

    // Import the roundtrip.tmx fixture (4 entries: 2 with same source, different targets)
    const fileInput = page.locator('input[type="file"][accept=".tmx"]');
    const tmxPath = path.resolve(__dirname, "fixtures/roundtrip.tmx");
    await fileInput.setInputFiles(tmxPath);

    // Wait for import to complete — should show 4 entries (no deduplication)
    await expect(page.getByText(/4 entries|4 of 4/i).first()).toBeVisible({ timeout: 15000 });

    // Verify both translations for "Hello world" exist
    const holaEntries = page.getByText("Hola mundo");
    const holaTodosEntries = page.getByText("Hola a todos");
    await expect(holaEntries.first()).toBeVisible();
    await expect(holaTodosEntries.first()).toBeVisible();

    // Verify "Goodbye" → "Adiós" exists
    await expect(page.getByText("Adiós").first()).toBeVisible();

    // Export and verify metadata in the TMX output
    const responsePromise = page.waitForResponse(
      (r) => r.url().includes("/export") && r.status() === 200
    );
    await page.getByRole("button", { name: /export tmx/i }).click();
    const response = await responsePromise;
    const tmxContent = await response.text();

    // Metadata should be present in exported TMX
    expect(tmxContent).toContain('tuid="tu-001"');
    expect(tmxContent).toContain('creationid="translator1"');
    expect(tmxContent).toContain('creationdate="20260115T120000Z"');

    // Both translations for "Hello world" should be in the export
    const helloCount = (tmxContent.match(/Hello world/g) || []).length;
    expect(helloCount).toBeGreaterThanOrEqual(2); // at least 2 TUs with "Hello world"
  });

  // Pins that a backdrop click dismisses the confirm. Base UI
  // AlertDialog ignores backdrop clicks, so `web/src/components/ui/alert-dialog.tsx`
  // wires `onBackdropClick` onto the body-portaled overlay; only a real browser
  // proves the click reaches it.
  test("clicking the backdrop of the bulk F&R confirm dialog dismisses it (real browser)", async ({ page }) => {
    const tmName = `Backdrop Click ${Date.now()}`;
    const marker = `BACKDROP_${Date.now()}`;
    const tmxContent = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en" creationtool="ui-test" creationtoolversion="1"/>
  <body>
    <tu><tuv xml:lang="en"><seg>row with ${marker} inside</seg></tuv><tuv xml:lang="es"><seg>fila con ${marker} dentro</seg></tuv></tu>
  </body>
</tmx>`;

    const tmId = await createTM(page, tmName);

    await page.locator("a").filter({ hasText: tmName }).first().click();
    await expect(page).toHaveURL(/\/memories\/\d+/, { timeout: 5000 });

    const token = await page.evaluate(() => localStorage.getItem("token"));
    await importTMXViaAPI(page, tmId, tmxContent, "backdrop.tmx");
    await page.reload();
    await expect(page.getByText(`row with ${marker} inside`)).toBeVisible({ timeout: 5000 });

    // Open F&R modal, fill find, run preview, then click Replace all to open
    // the destructive ConfirmDialog (an AlertDialog under the hood).
    await page.getByTestId("tm-find-replace-btn").click();
    await expect(page.getByTestId("tm-find-replace-dialog")).toBeVisible();
    await page.getByTestId("tm-fr-find").fill(marker);
    await page.getByTestId("tm-fr-replace").fill(`${marker}_NOPE`);
    await page.getByTestId("tm-fr-find-btn").click();
    await expect(page.getByTestId("tm-fr-preview")).toBeVisible({ timeout: 3000 });
    await page.getByTestId("tm-fr-replace-all-btn").click();
    const confirm = page.getByRole("alertdialog");
    await expect(confirm).toBeVisible({ timeout: 3000 });

    // Click the backdrop at the top-left corner of the viewport. The popup is
    // centered, so (20, 20) lands on the overlay only — no popup, no buttons.
    // The overlay's onClick calls onBackdropClick → onOpenChange(false).
    await page.mouse.click(20, 20);

    // Invariant: the backdrop click MUST cancel — dialog gone, no replacement
    // applied. We verify both.
    await expect(confirm, "backdrop click must dismiss the confirm dialog").not.toBeVisible({ timeout: 2000 });

    // Anti-tautology: the entry must NOT have been replaced (no destructive
    // action fired from the dismiss). Cancel-on-backdrop is the contract.
    const entriesRes = await page.request.get(`/api/tms/${tmId}/entries?limit=10`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    const entries = (await entriesRes.json()).data as Array<{ source_text: string }>;
    expect(entries[0].source_text).toContain(marker);
    expect(entries[0].source_text).not.toContain("_NOPE");
  });

  test("TM Browser: bulk Find & Replace updates entries + writes history", async ({ page }) => {
    // ~5s unloaded (TMX import + reload + 6-step dialog flow + 3 API
    // round-trips); full-suite worker load overruns the
    // 10s default.
    test.setTimeout(30_000);
    // End-to-end UI proof of bulk F&R across a TM.
    // Catches what unit tests can't: Portal rendering of dialog + confirm,
    // real backend response shape vs frontend types, transactional history,
    // cache invalidation after the bulk apply.
    //
    // Strategy: dedicated TM with 3 marker entries so we can assert the
    // "2 occurrences in 2 entries" preview without depending on shared seed.
    const tmName = `Bulk FR Test ${Date.now()}`;
    const marker = `BULKFR_${Date.now()}`;
    const tmxContent = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en" creationtool="ui-test" creationtoolversion="1"/>
  <body>
    <tu><tuv xml:lang="en"><seg>need to ${marker} the system</seg></tuv><tuv xml:lang="es"><seg>hay que ${marker} el sistema</seg></tuv></tu>
    <tu><tuv xml:lang="en"><seg>second ${marker} entry</seg></tuv><tuv xml:lang="es"><seg>segunda entrada</seg></tuv></tu>
    <tu><tuv xml:lang="en"><seg>unrelated row</seg></tuv><tuv xml:lang="es"><seg>fila sin relación</seg></tuv></tu>
  </body>
</tmx>`;

    // 1. Create TM + import 3 entries (2 with the marker on source, 1 of which also has it on target).
    const tmId = await createTM(page, tmName);
    await page.locator("a").filter({ hasText: tmName }).first().click();
    await expect(page).toHaveURL(/\/memories\/\d+/, { timeout: 5000 });

    const token = await page.evaluate(() => localStorage.getItem("token"));
    await importTMXViaAPI(page, tmId, tmxContent, "bulk-fr.tmx");
    await page.reload();
    await expect(page.getByText(`need to ${marker} the system`)).toBeVisible({ timeout: 5000 });

    // 2. Open F&R modal.
    await page.getByTestId("tm-find-replace-btn").click();
    await expect(page.getByTestId("tm-find-replace-dialog")).toBeVisible();

    // 3. Type find + replace, scope=source so we only hit the source column
    //    (target also has the marker on row 1, but scope=source isolates it).
    await page.getByTestId("tm-fr-find").fill(marker);
    await page.getByTestId("tm-fr-replace").fill(`${marker}_REPLACED`);
    await page.getByTestId("tm-fr-scope-source").click();

    // 4. Find → preview shows "2 occurrences in 2 entries" (rows 1+2, source only).
    await page.getByTestId("tm-fr-find-btn").click();
    const preview = page.getByTestId("tm-fr-preview");
    await expect(preview).toBeVisible({ timeout: 3000 });
    await expect(preview).toContainText(/2 occurrences in 2 entries/i);

    // 5. Replace all → confirm dialog shows the count.
    await page.getByTestId("tm-fr-replace-all-btn").click();
    const confirm = page.getByRole("alertdialog");
    await expect(confirm).toBeVisible({ timeout: 3000 });
    await expect(confirm).toContainText(/Replace 2 occurrences/i);

    // 6. Click "Replace all" in the confirm.
    await confirm.getByRole("button", { name: /^Replace all$/i }).click();

    // 7. Success toast + dialog closes.
    await expect(page.getByText(/Replaced 2 occurrences in 2 entries/i)).toBeVisible({ timeout: 5000 });
    await expect(page.getByTestId("tm-find-replace-dialog")).not.toBeVisible({ timeout: 3000 });

    // 8. Source cells now show the new value (transaction landed AND cache
    //    invalidated → list refetched).
    await expect(page.getByText(`need to ${marker}_REPLACED the system`)).toBeVisible({ timeout: 5000 });
    await expect(page.getByText(`second ${marker}_REPLACED entry`)).toBeVisible({ timeout: 5000 });

    // 9. The unrelated row is unchanged (anti-tautology — proves we didn't
    //    just replace everything).
    await expect(page.getByText("unrelated row")).toBeVisible();

    // 10. Each affected entry has a history row with change_reason="find_replace".
    //     Verify via the history endpoint directly (UI walk-through is the
    //     domain of TMEntryHistoryDialog tests).
    const entriesRes = await page.request.get(`/api/tms/${tmId}/entries?limit=10`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    const entries = (await entriesRes.json()).data as Array<{ id: number; source_text: string }>;
    const replacedEntries = entries.filter(e => e.source_text.includes(`${marker}_REPLACED`));
    expect(replacedEntries.length).toBe(2);
    for (const e of replacedEntries) {
      const histRes = await page.request.get(`/api/tms/${tmId}/entries/${e.id}/history`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      const hist = (await histRes.json()).data as Array<{ change_reason: string; source_text: string }>;
      expect(hist.length).toBe(1);
      expect(hist[0].change_reason).toBe("find_replace");
      // BEFORE-snapshot must hold the original (un-replaced) value.
      expect(hist[0].source_text).toContain(marker);
      expect(hist[0].source_text).not.toContain("_REPLACED");
    }
  });

  test("TM Browser: edit a cell, then click history → dialog shows BEFORE value (full UI round-trip)", async ({ page }) => {
    // ~5s unloaded; same reason as the bulk-F&R test above — long UI round-trip
    // that overruns the 10s default only under full-suite worker contention.
    test.setTimeout(30_000);
    // End-to-end UI proof for the inline-edit + history-viewer slice.
    // Catches drift the unit tests can't:
    //   - Real backend response shape vs frontend `TMEntryHistory` type
    //   - Dialog Portal rendering + dismiss
    //   - Cache invalidation: after edit, dialog must show the new history row
    //
    // Strategy: create a dedicated TM with a single TMX entry so we don't
    // pollute the shared Default TM and don't race with other UI tests that
    // depend on its entry counts.
    const tmName = `History Viewer Test ${Date.now()}`;
    const marker = `HISTORY_${Date.now()}`;
    const tmxContent = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en" creationtool="ui-test" creationtoolversion="1"/>
  <body>
    <tu><tuv xml:lang="en"><seg>history viewer marker</seg></tuv><tuv xml:lang="es"><seg>marcador de historia</seg></tuv></tu>
  </body>
</tmx>`;

    // 1. Create TM via UI, then click into it to navigate to the browser
    //    (the create dialog stays on /memories — it doesn't auto-route).
    const tmId = await createTM(page, tmName);
    await page.locator("a").filter({ hasText: tmName }).first().click();
    await expect(page).toHaveURL(/\/memories\/\d+/, { timeout: 5000 });

    // Import the TMX via API — bulk-import via UI would work too but slower
    // and not what this test is exercising.
    await importTMXViaAPI(page, tmId, tmxContent, "history-test.tmx");

    await page.reload();

    // 2. Find the entry's target cell, click to enter edit mode.
    const targetCell = page.getByText("marcador de historia").first();
    await expect(targetCell).toBeVisible({ timeout: 5000 });
    await targetCell.click();

    // 3. Edit via the ProseMirror editor — the cell now has a contenteditable
    //    `.ProseMirror` element. Select all, type a new value, press Enter to commit.
    const editor = page.locator('[data-testid^="tm-cell-edit-"][data-testid$="-target"] .ProseMirror');
    await expect(editor).toBeVisible({ timeout: 3000 });
    await editor.press("ControlOrMeta+a");
    await editor.pressSequentially(`marcador editado ${marker}`);
    await editor.press("Enter");

    // 4. Wait for the success toast — proves the PATCH landed AND the cache invalidated.
    await expect(page.getByText(/TM entry updated/i)).toBeVisible({ timeout: 5000 });

    // 5. Open the history dialog via the clock button on the same row.
    //    The row's actions column has the history + delete buttons; the
    //    history button has data-testid `tm-history-btn-{entryID}`.
    //    We don't know the entry ID, so locate by testid prefix instead.
    const historyBtn = page.locator('[data-testid^="tm-history-btn-"]').first();
    await expect(historyBtn).toBeVisible();
    await historyBtn.click();

    // 6. Dialog opens, shows the BEFORE-snapshot of the edit we just made.
    //    The history row must contain "marcador de historia" — the original
    //    target text, BEFORE we overwrote it. This is the core invariant
    //    of the audit trail (matches j25 phase E1's BEFORE-fidelity check).
    const dialog = page.getByTestId("tm-history-dialog");
    await expect(dialog).toBeVisible({ timeout: 3000 });

    const historyList = page.getByTestId("tm-history-list");
    await expect(historyList).toBeVisible({ timeout: 3000 });
    await expect(historyList).toContainText("marcador de historia");

    // 7. Escape closes the dialog.
    await page.keyboard.press("Escape");
    await expect(dialog).not.toBeVisible({ timeout: 2000 });
  });

  // TM-side missing-tags bar, full user flow:
  //   1. Click TM target cell → editor opens with bpt+ept chips.
  //   2. User wipes content (Cmd+A → Backspace) and types plain text. The
  //      cell must stay mounted while typing, or it restores the chips and
  //      hides the bar. Mount-stability is its own test (above) — this one
  //      pins the user-facing behavior.
  //   3. Bar appears with both missing chips.
  //   4. Click first chip → bpt inserted as a real `xliffTag` atom; bar
  //      shrinks to 1.
  //   5. Press F8 → next missing tag (ept) inserted; bar disappears.
  //   6. Re-wipe + Alt+F8 → both chips inserted in one keystroke.
  test("missing-tags bar + click chip + F8 + Alt+F8 (full user flow)", async ({ page }) => {
    test.setTimeout(30_000);
    const tmName = `Missing tags TM Bar ${Date.now()}`;
    const tmxContent = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en" creationtool="ui-test" creationtoolversion="1"/>
  <body>
    <tu><tuv xml:lang="en"><seg>Click <bpt id="1">&lt;b&gt;</bpt>here<ept id="1">&lt;/b&gt;</ept> to continue</seg></tuv><tuv xml:lang="es"><seg>Pulsa <bpt id="1">&lt;b&gt;</bpt>aquí<ept id="1">&lt;/b&gt;</ept> para continuar</seg></tuv></tu>
  </body>
</tmx>`;

    const tmId = await createTM(page, tmName);
    await page.locator("a").filter({ hasText: tmName }).first().click();
    await expect(page).toHaveURL(/\/memories\/\d+/, { timeout: 5000 });

    const token = await page.evaluate(() => localStorage.getItem("token"));
    await importTMXViaAPI(page, tmId, tmxContent, "missing-tags-flow.tmx");
    await page.reload();
    await expect(page.getByText(/Pulsa/).first()).toBeVisible({ timeout: 5000 });

    const entriesRes = await page.request.get(`/api/tms/${tmId}/entries`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    const entriesBody = await entriesRes.json();
    const entryId = entriesBody.data?.[0]?.id;
    expect(entryId, "TM entry must exist after import").toBeTruthy();

    // Click target cell → wipe → type. The editor stays mounted throughout
    // typing (stable key + dataRef).
    await page.locator(`[data-testid="tm-cell-${entryId}-target"]`).click();
    const editor = page.locator(`[data-testid="tm-cell-edit-${entryId}-target"] .ProseMirror`);
    await expect(editor).toBeVisible({ timeout: 3000 });
    await editor.press("ControlOrMeta+a");
    await editor.pressSequentially("Pulsa aqui para continuar");

    // INVARIANT 1: bar appears at page level with both missing tags.
    const bar = page.locator('[data-testid="missing-tags-bar"]');
    await expect(bar,
      "bar must appear when target is missing source tags").toBeVisible({ timeout: 3000 });
    const chips = bar.locator('[data-testid="missing-tag-chip"]');
    await expect(chips,
      "bar must list one chip per missing source tag (bpt + ept = 2)").toHaveCount(2);
    await expect(chips.nth(0)).toHaveAttribute("data-tag-type", "bpt");
    await expect(chips.nth(1)).toHaveAttribute("data-tag-type", "ept");

    // INVARIANT 2: click bar chip → real chip atom in editor; bar shrinks.
    await chips.nth(0).click();
    await expect(editor.locator('span.xliff-tag[data-tag-type="bpt"][data-tag-id="1"]'),
      "clicking bar chip must insert real `xliffTag` atom in the editor").toHaveCount(1);
    await expect(chips,
      "bar must shrink to remaining missing tags after one is inserted").toHaveCount(1);

    // INVARIANT 3: F8 inserts the next missing (ept). Bar disappears.
    await page.keyboard.press("F8");
    await expect(editor.locator('span.xliff-tag[data-tag-type="ept"][data-tag-id="1"]'),
      "F8 must insert the next missing source tag").toHaveCount(1);
    await expect(bar,
      "bar must hide when target has all source tags").not.toBeVisible({ timeout: 1500 });

    // INVARIANT 4: re-wipe + Alt+F8 → both chips inserted in one keystroke.
    await editor.press("ControlOrMeta+a");
    await editor.pressSequentially("re-edit");
    await expect(chips,
      "bar must re-appear after target loses tags again (no stale memo)").toHaveCount(2);
    await page.keyboard.press("Alt+F8");
    await expect(editor.locator('span.xliff-tag[data-tag-type="bpt"][data-tag-id="1"]'),
      "Alt+F8 must insert the bpt").toHaveCount(1);
    await expect(editor.locator('span.xliff-tag[data-tag-type="ept"][data-tag-id="1"]'),
      "Alt+F8 must also insert the ept (single keystroke covers all)").toHaveCount(1);
    await expect(bar,
      "bar must hide after Alt+F8 inserts everything").not.toBeVisible({ timeout: 1500 });
  });

  // Pins that re-editing a TM cell right after
  // a save does not 409 against the user's own write. `startEdit` reads
  // `entry.updated_at` from the `tm-entries` cache, so
  // `advanceTMEntryInCache` must patch every such cache in
  // `updateMut.onSuccess` before the refetch lands. The GET is throttled so
  // that helper is the only thing keeping the baseline fresh; making it a
  // no-op turns this test RED.
  test("editing the same TM cell twice in a row does NOT show a false-positive conflict dialog", async ({ page }) => {
    test.setTimeout(30_000);
    const tmName = `TM double edit ${Date.now()}`;
    const tmxContent = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en" creationtool="ui-test" creationtoolversion="1"/>
  <body>
    <tu><tuv xml:lang="en"><seg>twice-edit source</seg></tuv><tuv xml:lang="es"><seg>twice-edit target original</seg></tuv></tu>
  </body>
</tmx>`;

    const tmId = await createTM(page, tmName);
    await page.locator("a").filter({ hasText: tmName }).first().click();
    await expect(page).toHaveURL(/\/memories\/\d+/, { timeout: 5000 });

    const token = await page.evaluate(() => localStorage.getItem("token"));
    await importTMXViaAPI(page, tmId, tmxContent, "twice-edit.tmx");

    await page.reload();
    await expect(page.getByText("twice-edit target original")).toBeVisible({ timeout: 5000 });

    // Select by entry ID, not by text: the edited text only renders after the
    // throttled refetch lands, and the second click must land DURING the
    // throttle window to race it.
    const entriesRes = await page.request.get(
      `/api/tms/${tmId}/entries`,
      { headers: { Authorization: `Bearer ${token}` } },
    );
    const entriesBody = await entriesRes.json();
    const entryId = entriesBody.data?.[0]?.id;
    expect(entryId, "TM entry must exist after import").toBeTruthy();
    const targetCellSelector = `[data-testid="tm-cell-${entryId}-target"]`;

    // THROTTLE the entries refetch AFTER initial page load. On localhost the
    // invalidate-driven GET /api/tms/{id}/entries lands in ~10ms — faster than
    // the user's re-click — masking whether the synchronous helper is doing
    // its job. With 1.5s of latency, the user's re-click can race the refetch
    // and we exercise the very window the helper exists to cover.
    await page.route(`**/api/tms/${tmId}/entries**`, async (route, req) => {
      if (req.method() === "GET") {
        await new Promise(r => setTimeout(r, 1500));
      }
      try {
        await route.continue();
      } catch (e) {
        // Playwright throws "Route is already handled!" if the same request
        // is matched twice (can happen when the page fires duplicate GETs
        // close together — useInfiniteQuery refetch on focus + the
        // invalidate-driven refetch race). The actual continue succeeded
        // the first time; the second invocation is a no-op on our side.
        if (!(e as Error).message?.includes("already handled")) throw e;
      }
    });

    // FIRST edit: click target cell → type → Enter (commits).
    await page.locator(targetCellSelector).click();
    const editor = page.locator(`[data-testid="tm-cell-edit-${entryId}-target"] .ProseMirror`);
    await expect(editor).toBeVisible({ timeout: 3000 });
    await editor.press("ControlOrMeta+a");
    await editor.pressSequentially("twice-edit first edit");
    await editor.press("Enter");
    await expect(page.getByText(/TM entry updated/i)).toBeVisible({ timeout: 5000 });

    // Re-click the SAME cell IMMEDIATELY using the testid (NOT text). The
    // throttled refetch is in flight (1.5s); the cell still shows the old
    // text "twice-edit target original" until refetch lands — but the testid
    // is stable across both states. Without `advanceTMEntryInCache`,
    // `startEdit` captures the stale pre-save `updated_at` from cache →
    // next save 409s.
    await page.locator(targetCellSelector).click();
    const editor2 = page.locator(`[data-testid="tm-cell-edit-${entryId}-target"] .ProseMirror`);
    await expect(editor2).toBeVisible({ timeout: 3000 });
    await editor2.press("ControlOrMeta+a");
    await editor2.pressSequentially("twice-edit second edit");

    // Wait for the second save's network round-trip to actually arrive. We
    // need the PUT response IN HAND before asserting on the dialog —
    // otherwise `not.toBeVisible` passes immediately at t=0 because the
    // dialog hasn't rendered yet, even though the 409 is in flight.
    const patchPromise = page.waitForResponse(
      (r) => r.url().includes(`/tms/${tmId}/entries/${entryId}`) && r.request().method() === "PATCH",
      { timeout: 10000 },
    );
    await editor2.press("Enter");
    const patchResponse = await patchPromise;

    // CORE INVARIANT: the second save must NOT 409. If status is 409, the
    // bug is reproducing — surfaced to the user as the conflict dialog.
    expect(
      patchResponse.status(),
      "second save in same cell must NOT return 409 — that's the user 409'ing against their own previous write",
    ).not.toBe(409);

    // Belt-and-braces: also assert no conflict UI surfaces — neither a modal
    // (guards against one appearing on this surface) nor the conflict banner.
    await expect(
      page.getByRole("alertdialog"),
      "a 'Segment edited by another user' dialog must NOT appear during a single-user back-to-back edit",
    ).not.toBeVisible({ timeout: 1500 });
    await expect(
      page.getByTestId("conflict-banner"),
      "conflict banner must NOT appear during a single-user back-to-back edit",
    ).not.toBeVisible({ timeout: 500 });

    // The second edit persisted (no silent drop): the save succeeded and the
    // server holds the second value, not the first.
    expect(patchResponse.ok(), `second save failed: ${patchResponse.status()}`).toBe(true);
    const finalRes = await page.request.get(`/api/tms/${tmId}/entries`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    const finalEntry = (await finalRes.json()).data?.find((e: { id: number }) => e.id === entryId);
    expect(finalEntry?.target_text).toBe("twice-edit second edit");
  });

  // TM Browser cell-edit conflict surfaces the same conflict banner
  // the CAT editor uses. This test runs in real Chromium against the
  // backend 409 body shape — proves the wiring
  // (frontend ApiError.body extraction → ConflictBanner) works end-to-end,
  // not just under JSDOM. Both saves use the SAME auth token (the seed
  // user) so the banner heading must use the same-user "another tab or
  // session" wording — NOT "{Name} saved a different version" which would
  // read as nonsense when the editor IS the same person.
  test("TM cell-edit conflict banner appears on 409 and 'Use theirs' reloads the server version", async ({ page }) => {
    test.setTimeout(30_000);

    // Dedicated TM with a single entry so we know exactly which row to edit
    // and don't pollute the shared Default TM (which `Use mine` would.)
    const tmName = `Banner Conflict ${Date.now()}`;
    const marker = `BANNER_${Date.now()}`;
    const tmxContent = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en" creationtool="ui-test" creationtoolversion="1"/>
  <body>
    <tu><tuv xml:lang="en"><seg>row with ${marker} source</seg></tuv><tuv xml:lang="es"><seg>fila con ${marker} target</seg></tuv></tu>
  </body>
</tmx>`;

    // Create the TM via UI, then import the single entry via API for speed.
    const tmId = await createTM(page, tmName);
    await page.locator("a").filter({ hasText: tmName }).first().click();
    await expect(page).toHaveURL(/\/memories\/\d+/, { timeout: 5000 });

    const token = await page.evaluate(() => localStorage.getItem("token"));
    await importTMXViaAPI(page, tmId, tmxContent, "banner-conflict.tmx");
    await page.reload();
    await expect(page.getByText(`row with ${marker} source`)).toBeVisible({ timeout: 5000 });

    // Get the entry id so we can both target the right cell AND issue the
    // out-of-band stale-write that triggers the 409.
    const entriesRes = await page.request.get(`/api/tms/${tmId}/entries`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    const entryId = (await entriesRes.json()).data?.[0]?.id as number;
    expect(entryId, "imported entry must exist").toBeTruthy();

    // Enter edit mode on the target cell. The UI captures the row's
    // updated_at as the optimistic-lock baseline at this moment.
    await page.locator(`[data-testid="tm-cell-${entryId}-target"]`).click();
    const editor = page.locator(`[data-testid="tm-cell-edit-${entryId}-target"] .ProseMirror`);
    await expect(editor).toBeVisible({ timeout: 3000 });

    // OUT-OF-BAND save advances the server's updated_at → user A's baseline
    // is now stale. Same auth token so the banner must show the same-user
    // heading (not "{Name} saved a different version").
    await page.request.patch(
      `/api/tms/${tmId}/entries/${entryId}`,
      {
        headers: { Authorization: `Bearer ${token}` },
        data: {
          source_text: `row with ${marker} source`,
          target_text: "their canonical target",
          target_rich_text: "",
          change_reason: "edit",
        },
      },
    );

    // Type a conflicting edit + commit (Enter is the TM cell-edit commit
    // shortcut per the editor keymap).
    await editor.press("ControlOrMeta+a");
    await editor.pressSequentially("my conflicting target");
    await editor.press("Enter");

    // Banner must appear with same-user heading + TARGET field label +
    // both versions side-by-side + both action buttons.
    await expect(page.getByTestId("conflict-banner")).toBeVisible({ timeout: 5000 });
    await expect(page.getByTestId("conflict-banner-title")).toContainText(/another tab or session/i);
    const theirs = page.getByTestId("conflict-banner-theirs");
    await expect(theirs).toContainText(/their target/i);  // field label, lower-cased
    await expect(theirs).toContainText("their canonical target");
    await expect(page.getByTestId("conflict-banner-mine")).toContainText("my conflicting target");
    await expect(page.getByTestId("conflict-banner-use-theirs")).toBeVisible();
    await expect(page.getByTestId("conflict-banner-use-mine")).toBeVisible();

    // "Use theirs" → banner clears, cache invalidates, the cell shows the
    // server version (their canonical target). The local edit is gone.
    await page.getByTestId("conflict-banner-use-theirs").click();
    await expect(page.getByTestId("conflict-banner")).not.toBeVisible({ timeout: 3000 });
    await expect(page.locator(`[data-testid="tm-cell-${entryId}-target"]`)).toContainText("their canonical target", { timeout: 3000 });

    // Anti-tautology: confirm via API that "Use theirs" did NOT push the
    // local edit through. The server should still have the canonical text.
    const finalRes = await page.request.get(`/api/tms/${tmId}/entries`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    const finalEntry = (await finalRes.json()).data?.find((e: { id: number }) => e.id === entryId);
    expect(finalEntry?.target_text).toBe("their canonical target");
  });

  // The TM browser page container clips overflow (fixed-layout page), so it
  // must leave internal headroom above the breadcrumb rename input, or the
  // input's 3px focus ring is cut off at the top.
  test("TM rename input's focus ring is not clipped by the page container", async ({ page }) => {
    await page.getByRole("link", { name: /translation memor/i }).click();
    await page.getByRole("link", { name: /^default tm/i }).click();
    await expect(page).toHaveURL(/\/memories\/\d+/);

    await page.getByTestId("rename-tm").click();
    const input = page.getByRole("textbox", { name: /rename/i });
    await expect(input).toBeFocused();

    const container = page.locator('[data-slot="page-container"]');
    const inputBox = (await input.boundingBox())!;
    const containerBox = (await container.boundingBox())!;
    // focus-visible:ring-3 extends 3px above the input; anything less than
    // 3px of headroom inside the overflow-hidden container clips it.
    expect(
      inputBox.y - containerBox.y,
      "rename input needs ≥3px headroom inside the clipping container for its focus ring",
    ).toBeGreaterThanOrEqual(3);

    // Escape so the rename is abandoned, not committed.
    await page.keyboard.press("Escape");
  });

  // The virtualizer measures rows (`measureRows`), so the editing row's real
  // height pushes subsequent rows down instead of painting under them.
  test("a long TM entry wraps without overlapping the rows below, read-only and while editing", async ({ page }) => {
    test.setTimeout(30_000);
    const tmName = `Row Overlap ${Date.now()}`;
    const longTarget = Array(12).fill("una frase bastante larga que se envuelve").join(" ");
    const tmxContent = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en" creationtool="ui-test" creationtoolversion="1"/>
  <body>
    <tu><tuv xml:lang="en"><seg>long wrapping source</seg></tuv><tuv xml:lang="es"><seg>${longTarget}</seg></tuv></tu>
    <tu><tuv xml:lang="en"><seg>row below marker</seg></tuv><tuv xml:lang="es"><seg>marcador de fila inferior</seg></tuv></tu>
  </body>
</tmx>`;

    const tmId = await createTM(page, tmName);

    await page.locator("a").filter({ hasText: tmName }).first().click();
    await expect(page).toHaveURL(/\/memories\/\d+/, { timeout: 5000 });
    const token = await page.evaluate(() => localStorage.getItem("token"));

    await importTMXViaAPI(page, tmId, tmxContent, "overlap.tmx");
    await page.reload();
    await expect(page.getByText("marcador de fila inferior")).toBeVisible({ timeout: 5000 });

    const entriesRes = await page.request.get(`/api/tms/${tmId}/entries`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    const entriesBody = await entriesRes.json();
    const longEntry = entriesBody.data.find((e: { target_text: string }) => e.target_text.startsWith("una frase"));
    expect(longEntry, "long TM entry must exist after import").toBeTruthy();

    // Read-only cells already wrap (up to 10 lines), and the measured rows
    // keep the next row below the wrapped one.
    const readOnly = page.locator(`[data-testid="tm-cell-${longEntry.id}-target"]`);
    const readOnlyBox = (await readOnly.boundingBox())!;
    expect(readOnlyBox.height, "long read-only target must wrap past one line").toBeGreaterThan(30);
    const readOnlyRow = page.locator("div[data-index]").filter({ has: readOnly });
    const readOnlyIdx = parseInt((await readOnlyRow.getAttribute("data-index"))!, 10);
    const rowBelowReadOnly = page.locator(`div[data-index="${readOnlyIdx + 1}"]`);
    await expect
      .poll(async () => {
        const cBox = await readOnly.boundingBox();
        const bBox = await rowBelowReadOnly.boundingBox();
        if (!cBox || !bBox) return -Infinity;
        return bBox.y - (cBox.y + cBox.height);
      }, { timeout: 3000, message: "row below must sit below the wrapped read-only cell (no overlap)" })
      .toBeGreaterThanOrEqual(-1);

    // The editor shows the entry in full, with no line cap.
    await readOnly.click();
    const editor = page.locator(`[data-testid="tm-cell-edit-${longEntry.id}-target"]`);
    await expect(editor).toBeVisible({ timeout: 3000 });

    const editorBox = (await editor.boundingBox())!;
    expect(editorBox.height, "long target must actually wrap to multiple lines").toBeGreaterThan(60);

    // The virtualized row wrapper right below the editing one must start at
    // or below the editor's bottom edge. Poll: row measurement is async
    // (ResizeObserver) so the layout settles a frame or two after mount.
    const editingRow = page.locator("div[data-index]").filter({ has: editor });
    const editIdx = parseInt((await editingRow.getAttribute("data-index"))!, 10);
    const rowBelow = page.locator(`div[data-index="${editIdx + 1}"]`);
    await expect
      .poll(async () => {
        const eBox = await editor.boundingBox();
        const bBox = await rowBelow.boundingBox();
        if (!eBox || !bBox) return -Infinity;
        return bBox.y - (eBox.y + eBox.height);
      }, { timeout: 3000, message: "row below must sit below the expanded editor (no overlap)" })
      .toBeGreaterThanOrEqual(-1);
  });
});
