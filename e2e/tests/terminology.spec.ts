import { test, expect, type Page } from "@playwright/test";
import { deleteViaAPI, login, openJob, segmentEditor } from "./helpers";

/**
 * Open the seeded en→de term base from the Terminology list.
 *
 * Scoped to a term-base row, never page-wide text: the list is reached by
 * clicking the nav link, and until it renders the projects list is still
 * mounted — where a project's "EN → DE" language pair matches the same words
 * and navigates to the project instead (the "`.first()` / `.last()` /
 * `.nth()` on a global locator" trap).
 */
async function openDeTermBase(page: Page) {
  await expect(page).toHaveURL(/\/terminology$/);
  const row = page
    .locator('[data-testid^="term-base-row-"]')
    .filter({ hasText: /en→de/i })
    .first();
  await expect(row).toBeVisible();
  await row.click();
  await expect(page).toHaveURL(/\/terminology\/\d+/);
}

test.describe("Term base", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("term base list shows seeded term bases", async ({ page }) => {
    await page.getByRole("link", { name: /terminology/i }).click();
    await expect(page).toHaveURL(/\/terminology/);
    await expect(
      page.getByRole("heading", { name: /terminology/i })
    ).toBeVisible();
    // Seed creates Default Term base (en→de), (en→es), (en→fr)
    await expect(page.getByText(/default term base/i).first()).toBeVisible();
    await expect(page.getByText(/\(en→de\)/i).first()).toBeVisible();
  });

  test("term base browser shows entries and search works", async ({ page }) => {
    await page.getByRole("link", { name: /terminology/i }).click();

    // Click on the DE term base
    await openDeTermBase(page);

    // Should show concept count. The view opens filtered to the source language,
    // so the label reads "N of M matching concepts".
    await expect(page.getByText(/\d+ of \d+( matching)? concepts/i)).toBeVisible();

    // Should show seeded terms (e.g. "password", "email address")
    await expect(page.getByText(/password/i).first()).toBeVisible();

    // Search narrows the list: "email" stays, "password" goes.
    const searchInput = page.getByPlaceholder(/search/i);
    const [searchResp] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/concepts") && r.url().includes("q=email"),
        { timeout: 30_000 },
      ),
      searchInput.fill("email"),
    ]);
    expect(searchResp.ok(), "term search should succeed").toBe(true);
    await expect(page.getByText(/email/i).first()).toBeVisible();
    await expect(page.getByText(/password/i)).toHaveCount(0);
  });

  test("term base CRUD: create and delete", async ({ page }) => {
    await page.getByRole("link", { name: /terminology/i }).click();

    const terminologyName = `E2E Term base ${Date.now()}`;
    let terminologyId: number | undefined;

    try {
      // Create
      await page.getByRole("button", { name: /new term base/i }).click();
      await page.getByPlaceholder(/term base name/i).fill(terminologyName);
      // Select source language via combobox popover (target languages are derived
      // from terms, so the create dialog only asks for a source).
      const srcTrigger = page.locator('button').filter({ hasText: "Source" }).first();
      await srcTrigger.click();
      // The popover opens a dialog with a combobox search input
      const srcSearch = page.locator('dialog input[role="combobox"], [role="dialog"] input[role="combobox"]').first();
      await srcSearch.waitFor({ state: "visible" });
      await srcSearch.pressSequentially("english", { delay: 50 });
      await page.getByRole("option", { name: /english/i }).first().waitFor({ state: "visible" });
      await page.getByRole("option", { name: /english/i }).first().click();

      const [createResp] = await Promise.all([
        page.waitForResponse(
          (r) => r.url().split("?")[0].endsWith("/api/terminology") && r.request().method() === "POST",
          { timeout: 10_000 },
        ),
        page.getByRole("button", { name: /^create$/i }).click(),
      ]);
      expect(createResp.ok(), "create term base should succeed").toBe(true);
      terminologyId = (await createResp.json()).data.id;

      // Creating a base opens it directly.
      await expect(page).toHaveURL(new RegExp(`/terminology/${terminologyId}$`));
      await expect(
        page.getByRole("navigation", { name: /breadcrumb/i })
      ).toContainText(terminologyName, { timeout: 5000 });

      // Back to the list to delete it.
      await page.getByRole("link", { name: /^terminology$/i }).first().click();
      await expect(page).toHaveURL(/\/terminology$/);
      await expect(page.getByRole("link", { name: terminologyName })).toBeVisible();

      // Delete
      const deleteTarget = page
        .locator("a")
        .filter({ hasText: terminologyName })
        .first();
      await deleteTarget.hover();
      // Click the last button (delete) that appears on hover
      const deleteBtn = deleteTarget.locator("button").last();
      await expect(deleteBtn).toBeVisible({ timeout: 2000 });
      await deleteBtn.click();
      // Confirm in the dialog, not any "Delete" button on the page.
      const [deleteResp] = await Promise.all([
        page.waitForResponse(
          (r) => r.url().split("?")[0].endsWith(`/api/terminology/${terminologyId}`) && r.request().method() === "DELETE",
          { timeout: 10_000 },
        ),
        page.getByRole("alertdialog").getByRole("button", { name: /^delete$/i }).click(),
      ]);
      expect(deleteResp.ok(), "delete term base should succeed").toBe(true);
      // Scope to the list row (a link) — a lingering "created" toast also matches
      // the name, which would trip strict-mode on a bare getByText.
      await expect(page.getByRole("link", { name: terminologyName })).not.toBeVisible({
        timeout: 3000,
      });
    } finally {
      // The UI delete above is the test; this catches a failure before it.
      if (terminologyId !== undefined) await deleteViaAPI(page, `/api/terminology/${terminologyId}`, `term base ${terminologyId}`);
    }
  });

  // TermBaseBrowser's inline name and description editors are both
  // `InlineRenameField` (with `placeholder` / `maxLength` / `aria-label`). This
  // test exercises both inline-edit paths end-to-end on the term base detail page.
  test("TermBaseBrowser inline name + description editors save changes", async ({ page }) => {
    test.setTimeout(20_000);
    const initialName = `E2E F2 Term base ${Date.now()}`;
    const renamedName = `${initialName} (renamed)`;
    const description = `E2E description ${Date.now()}`;

    // Create a fresh term base via API so the test doesn't depend on cmdk
    // popovers in the create form (those are flaky — the "cmdk fuzzy filter"
    // pattern).
    const token = await page.evaluate(() => localStorage.getItem("token")) ?? "";
    if (!token) {
      // Need to log in first (the beforeEach login already populated localStorage,
      // but we need to actually navigate so it's accessible).
      await page.goto("/terminology");
    }
    const tokenAfterNav = (await page.evaluate(() => localStorage.getItem("token"))) ?? "";
    const createResp = await page.request.post("/api/terminology", {
      headers: { Authorization: `Bearer ${tokenAfterNav}` },
      data: { name: initialName, source_language: "en", target_language: "fr", description: "" },
    });
    expect(createResp.ok(), `create term base: ${createResp.status()}`).toBe(true);
    const createBody = await createResp.json();
    const terminologyId: number = createBody.data?.id ?? createBody.id;

    try {
      // Navigate to the new term base's browser page.
      await page.goto(`/terminology/${terminologyId}`);
      await expect(page.getByRole("navigation", { name: /breadcrumb/i })).toContainText(initialName, { timeout: 5000 });

      // ── Rename via inline name editor ─────────────────────────────────
      // Click the pencil (EditableTitle) to enter rename mode.
      await page.getByTestId("rename-term-base").click();
      const nameInput = page.getByRole("textbox", { name: /^rename$/i });
      await expect(nameInput).toBeVisible({ timeout: 3000 });
      await nameInput.fill(renamedName);
      await Promise.all([
        page.waitForResponse(
          resp => resp.url().includes(`/terminology/${terminologyId}`) && resp.request().method() === "PATCH",
          { timeout: 5000 },
        ),
        nameInput.press("Enter"),
      ]);
      // The current breadcrumb item should now show the new name.
      await expect(page.getByRole("navigation", { name: /breadcrumb/i })).toContainText(renamedName, { timeout: 3000 });

      // ── Edit description via inline editor ─────────────────────────────
      // The placeholder text "Add a description..." is the click target when empty.
      await page.getByText(/add a description/i).first().click();
      const descInput = page.getByRole("textbox", { name: /edit term base description/i });
      await expect(descInput).toBeVisible({ timeout: 3000 });
      await expect(descInput).toHaveAttribute("placeholder", "Add a description...");
      await descInput.fill(description);
      await Promise.all([
        page.waitForResponse(
          resp => resp.url().includes(`/terminology/${terminologyId}`) && resp.request().method() === "PATCH",
          { timeout: 5000 },
        ),
        descInput.press("Enter"),
      ]);
      // Description should now appear in the page (no longer the placeholder).
      await expect(page.getByText(description).first()).toBeVisible({ timeout: 3000 });
    } finally {
      // Clean up the test term base via API.
      await page.request.delete(`/api/terminology/${terminologyId}`, {
        headers: { Authorization: `Bearer ${tokenAfterNav}` },
      }).catch(() => {});
    }
  });

  test("concept inline editing: create, edit term text + definition, delete concept", async ({ page }) => {
    await page.getByRole("link", { name: /terminology/i }).click();
    await openDeTermBase(page);

    const uniqueSuffix = `${Date.now().toString().slice(-6)}${test
      .info()
      .workerIndex}${Math.floor(Math.random() * 1000)}`;
    const term = `database${uniqueSuffix}`;

    // Create a concept via the dialog (first term row defaults to the base source
    // language — no cmdk language picking).
    await page.getByTestId("new-concept").click();
    const dialogTermInput = page.getByPlaceholder("Term").first();
    await expect(dialogTermInput).toBeVisible();
    await dialogTermInput.fill(term);
    const [createResp] = await Promise.all([
      page.waitForResponse(
        (r) => /\/terminology\/\d+\/concepts$/.test(r.url().split("?")[0]) && r.request().method() === "POST",
        { timeout: 30_000 },
      ),
      page.getByRole("button", { name: /^Save$/ }).click(),
    ]);
    // The base is the shared seed one, so the concept must not outlive the test.
    const conceptPath = `${new URL(createResp.url()).pathname}/${(await createResp.json()).data.id}`;

    try {
      // Find it via search (?q=).
      await Promise.all([
        page.waitForResponse(
          (r) => r.url().includes("/concepts") && r.url().includes(`q=${term}`),
          { timeout: 30_000 },
        ),
        page.getByPlaceholder(/search/i).fill(term),
      ]);
      await expect(page.getByText(term).first()).toBeVisible({ timeout: 8000 });

      // INLINE edit the term text: click the term → input → Enter → PATCH /terms/{id}.
      const edited = `edited${uniqueSuffix}`;
      await page.getByText(term).first().click();
      const inlineTerm = page.getByRole("textbox", { name: /edit term/i });
      await expect(inlineTerm).toHaveValue(term);
      await inlineTerm.fill(edited);
      await expect(inlineTerm).toHaveValue(edited);
      const [termPatch] = await Promise.all([
        page.waitForResponse(
          (r) => /\/terminology\/\d+\/concepts\/\d+\/terms\/\d+$/.test(r.url().split("?")[0]) && r.request().method() === "PATCH",
          { timeout: 30_000 },
        ),
        inlineTerm.press("Enter"),
      ]);
      expect(termPatch.ok(), "inline term PATCH should succeed").toBe(true);
      // Re-search for the edited text so we're on a stable row.
      await Promise.all([
        page.waitForResponse(
          (r) => r.url().includes("/concepts") && r.url().includes(`q=${edited}`),
          { timeout: 30_000 },
        ),
        page.getByPlaceholder(/search/i).fill(edited),
      ]);
      await expect(page.getByText(edited).first()).toBeVisible({ timeout: 10_000 });

      // INLINE edit the definition: click the "Add definition…" affordance → type →
      // blur → PATCH /concepts/{id}.
      await page.getByText(/add definition/i).first().click();
      const inlineDef = page.getByRole("textbox", { name: /edit definition/i });
      await inlineDef.fill(`def ${uniqueSuffix}`);
      const [defPatch] = await Promise.all([
        page.waitForResponse(
          (r) => /\/terminology\/\d+\/concepts\/\d+$/.test(r.url().split("?")[0]) && r.request().method() === "PATCH",
          { timeout: 30_000 },
        ),
        page.getByPlaceholder(/search/i).click(), // blur the textarea
      ]);
      expect(defPatch.ok(), "inline definition PATCH should succeed").toBe(true);

      // Delete the concept via the Info pencil → dialog → confirm.
      await page.getByRole("button", { name: /edit concept/i }).first().click();
      await page.getByRole("button", { name: /delete concept/i }).click();
      const [del] = await Promise.all([
        page.waitForResponse(
          (r) => /\/terminology\/\d+\/concepts\/\d+$/.test(r.url().split("?")[0]) && r.request().method() === "DELETE",
          { timeout: 30_000 },
        ),
        page.getByRole("button", { name: /^Delete$/ }).click(),
      ]);
      expect(del.ok(), "concept delete DELETE should succeed").toBe(true);
      await expect(page.getByText(edited)).not.toBeVisible({ timeout: 10_000 });
    } finally {
      // The UI delete above is the test; this catches a failure before it.
      await deleteViaAPI(page, conceptPath, `concept ${conceptPath}`);
    }
  });

  test("concept browser language filter (multi-select dropdown) refetches with ?lang= and clears", async ({ page }) => {
    await page.getByRole("link", { name: /terminology/i }).click();
    await openDeTermBase(page);

    // Open the searchable language filter and toggle German → refetch scoped to
    // ?lang=de. The filter is a cmdk combobox, so items are role=option.
    await page.getByRole("button", { name: /filter by language/i }).click();
    const [resp] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/concepts") && /[?&]lang=de(&|$)/.test(r.url()),
        { timeout: 30_000 },
      ),
      page.getByRole("option", { name: /german/i }).click(),
    ]);
    expect(resp.ok(), "language-filtered concept list should load").toBe(true);

    // Selecting keeps the popover open (multi-select); "All languages" resets.
    const [all] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/concepts") && !r.url().includes("lang="),
        { timeout: 30_000 },
      ),
      page.getByRole("option", { name: /all languages/i }).click(),
    ]);
    expect(all.ok()).toBe(true);
  });

  test("term row expand icon opens the concept dialog scrolled to that term", async ({ page }) => {
    await page.getByRole("link", { name: /terminology/i }).click();
    await openDeTermBase(page);

    // Reveal (hover) + click the first term row's "Open in concept view" icon.
    const expand = page.getByRole("button", { name: /open in concept view/i }).first();
    await expand.hover();
    const [conceptGet] = await Promise.all([
      page.waitForResponse(
        (r) => /\/terminology\/\d+\/concepts\/\d+$/.test(r.url().split("?")[0]) && r.request().method() === "GET",
        { timeout: 30_000 },
      ),
      expand.click(),
    ]);
    expect(conceptGet.ok(), "concept fetch for the dialog should succeed").toBe(true);

    // The concept dialog (full term view) opens...
    await expect(page.getByText("Edit concept")).toBeVisible();
    // ...scrolled to the expanded term — its card gets the attention ring,
    // which lasts about two seconds, so poll for it rather than wait long.
    await expect(
      page.locator('[data-testid="concept-term-row"][data-attention]'),
    ).toHaveCount(1, { timeout: 2000 });
  });

  test("search key-swap keeps rows on screen — no empty flash", async ({ page }) => {
    // The entries query key includes debouncedSearch. Without placeholderData,
    // the key swap renders data=undefined → the table flashes EMPTY until the
    // search response lands. Users see a flicker; the CRUD test's edit row
    // unmounted mid-fill and reverted the typed value. Delaying the search
    // response widens the gap enough to observe deterministically.
    await page.getByRole("link", { name: /terminology/i }).click();
    await openDeTermBase(page);
    // The view opens filtered to the source language, which hides the German
    // target term this test asserts on — switch to "All languages" first, and
    // wait for the filter popover to fully close (its own "Search languages…"
    // input would otherwise linger over the concepts search box).
    await page.getByRole("button", { name: /filter by language/i }).click();
    await page.getByRole("option", { name: /all languages/i }).click();
    await page.keyboard.press("Escape");
    await expect(page.getByPlaceholder("Search languages...")).not.toBeVisible();
    await expect(page.getByText("Passwort").first()).toBeVisible();

    await page.route(/\/api\/terminology\/\d+\/concepts\?.*q=/, async (route) => {
      await new Promise((r) => setTimeout(r, 1200));
      await route.continue();
    });
    await page.getByPlaceholder(/search/i).fill("password");

    // 300ms debounce + 1200ms delayed response: at ~700ms the swap has fired
    // and the response is still pending. With keepPreviousData the old rows
    // must still be on screen; without it this catches the empty flash.
    // Assert on the TARGET term: the empty-state message echoes the searched
    // source term ("No terms match 'password'"), so asserting on "password"
    // would pass even while the table is empty. "Passwort" exists only in a
    // real row.
    await page.waitForTimeout(700);
    await expect(
      page.getByText("Passwort").first(),
      "rows must stay visible while the search refetch is in flight",
    ).toBeVisible({ timeout: 100 });
  });

  test("terms appear in CAT editor when segment is active", async ({
    page,
  }) => {
    // Demo Project → demo.txt → its DE job, picked by row so a file another
    // worker uploads cannot take its place.
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { lang: "de", fileName: "demo.txt" });

    // Wait for editor to load
    await expect(
      page.getByText(/reset your password/i).first()
    ).toBeVisible({ timeout: 10000 });

    // Click on the translation of "Reset your password." to activate the row
    // The German translation is "Setzen Sie Ihr Passwort zurück."
    await page.getByText(/Setzen Sie Ihr Passwort/i).first().click();

    // Should see the editor (segment activated)
    await expect(segmentEditor(page).first()).toBeVisible({ timeout: 5000 });

    // The terminology panel lists the DE term for "password". Scoped to the
    // panel: "Passwort" also sits in the segment text and the source highlight.
    const termSection = page.getByTestId("terminology-section");
    await expect(termSection).toBeVisible({ timeout: 10000 });
    await expect(termSection).toContainText("Passwort", { timeout: 10000 });

    // Source text should have "password" highlighted with underline decoration
    // Title format is "targetTerm (status)" e.g. "Passwort (approved)"
    const underlined = page.locator(
      'span[class*="underline"][title*="Passwort"]'
    );
    await expect(underlined.first()).toBeVisible();
  });

  test("clicking outside active segment deactivates it", async ({ page }) => {
    // Navigate to Demo Project → DE job
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { lang: "de" });

    await expect(
      page.getByText(/reset your password/i).first()
    ).toBeVisible({ timeout: 10000 });

    // Click on the translation to activate a segment
    await page.getByText(/Setzen Sie Ihr Passwort/i).first().click();

    // Should see the editor (edit mode active)
    await expect(segmentEditor(page).first()).toBeVisible({ timeout: 2000 });

    // Click outside — on the header area
    await page.locator("h1, .text-2xl, nav").first().click({ force: true });

    // Editor should disappear (segment deactivated)
    await expect(segmentEditor(page)).not.toBeVisible({ timeout: 2000 });
  });
});
