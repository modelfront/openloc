import { test, expect } from "@playwright/test";
import { deleteViaAPI, login } from "./helpers";

// The Engines tab of the MT page (/mt/engines). The sidebar item is "MT" and
// lands on the Routers tab; the tabs are a SubNav, one route each.
test.describe("MT engines", () => {
  let token: string | null = null;
  const auth = () => ({ Authorization: `Bearer ${token}` });

  test.beforeEach(async ({ page }) => {
    await login(page);
    token = await page.evaluate(() => localStorage.getItem("token"));
  });

  test("engines tab is reached from the MT nav item and shows LibreTranslate", async ({ page }) => {
    await page.getByRole("link", { name: /^MT$/ }).click();
    await expect(page).toHaveURL(/\/mt$/);
    await expect(page.getByRole("heading", { level: 1, name: /^MT$/ })).toBeVisible();

    await page.getByRole("link", { name: /^Engines$/ }).click();
    await expect(page).toHaveURL(/\/mt\/engines$/);
    await expect(page.getByRole("heading", { level: 2, name: /^MT$/ })).toBeVisible();
    await expect(page.getByText(/libretranslate/i).first()).toBeVisible();
  });

  test("the old /engines address redirects to the Engines tab", async ({ page }) => {
    await page.goto("/engines");
    await expect(page).toHaveURL(/\/mt\/engines$/);
    await expect(page.getByRole("heading", { level: 2, name: /^MT$/ })).toBeVisible();
  });

  // An engine provisioned with the org is the org's from then on: it opens,
  // renames, switches off and deletes like any other, and
  // its type is in the create catalog, so even its provider is a real choice.
  //
  // Deliberately READ-ONLY on the seeded engine. Switching one off removes it
  // from every router's engine picker (`combineMTEngines` filters on enabled),
  // so a spec that toggles a shared seeded row breaks mt-routers.spec whenever
  // the two overlap. The lifecycle below uses an engine this spec owns.
  test("a provisioned engine opens fully editable", async ({ page }) => {
    await page.goto("/mt/engines");

    // By testid rather than by row text: a `div` filtered on `^name$` matches
    // whatever the row happens to render today and breaks the moment anything
    // joins the name.
    const libreRow = page
      .locator('[data-testid^="mt-engine-row-"]')
      .filter({ hasText: "LibreTranslate" });
    await expect(libreRow, "LibreTranslate row should render").toBeVisible();
    // A provisioned engine is deletable like any other.
    await expect(libreRow.getByRole("button", { name: "Delete" })).toBeVisible();

    await libreRow.getByTestId(/^mt-engine-open-/).click();
    const form = page.getByTestId("mt-engine-form");
    await expect(form).toBeVisible();
    await expect(form.getByTestId("mt-engine-name")).toBeEnabled();
    await expect(form.getByTestId("mt-engine-enabled")).toBeChecked();
    await expect(form.getByTestId("mt-engine-provider")).toBeEnabled();
    await form.getByRole("button", { name: "Cancel" }).click();
  });

  // The full lifecycle, on an engine this spec creates and removes, so nothing
  // it does is visible to another spec sharing the database.
  test("an engine renames, switches off and deletes", async ({ page }) => {
    const name = `E2E engine ${Date.now()}`;
    const res = await page.request.post("/api/mt-engines", {
      headers: auth(),
      data: { name, engine_type: "mock" },
    });
    expect(res.ok(), `create engine: ${res.status()}`).toBe(true);
    const id = (await res.json()).data.id as number;

    try {
      await page.goto("/mt/engines");
      const row = page.getByTestId(`mt-engine-row-${id}`);
      await expect(row).toBeVisible();

      await row.getByTestId(/^mt-engine-open-/).click();
      const form = page.getByTestId("mt-engine-form");
      await form.getByTestId("mt-engine-name").fill(`${name} renamed`);
      await form.getByTestId("mt-engine-enabled").click();
      const saved = page.waitForResponse(
        (r) => r.url().includes(`/api/mt-engines/${id}`) && r.request().method() === "PATCH",
      );
      await form.getByTestId("mt-engine-submit").click();
      await saved;

      // The Off state shows on the row; the switch is in the dialog.
      await expect(row).toContainText(`${name} renamed`);
      await expect(row).toContainText("Off");

      const gone = page.waitForResponse(
        (r) => r.url().includes(`/api/mt-engines/${id}`) && r.request().method() === "DELETE",
      );
      await row.getByRole("button", { name: "Delete" }).click();
      await page.getByRole("alertdialog").getByRole("button", { name: /^Delete$/ }).click();
      await gone;
      await expect(row).toHaveCount(0);
    } finally {
      // 404 (already gone) when the UI delete above succeeded; removes the engine when it did not.
      await deleteViaAPI(page, `/api/mt-engines/${id}`, `engine ${id}`);
    }
  });

  // A provider engine IS configured here: it is a name, a
  // provider, a key from AI settings and that provider's options. The form must
  // ask for all four — a create form missing the key would make an engine that
  // cannot authenticate.
  test("builds a provider engine from a key and the provider's options", async ({ page }) => {
    await page.goto("/mt/engines");
    await expect(page.getByRole("heading", { level: 2, name: /^MT$/ })).toBeVisible();

    // Checked BEFORE the dialog opens: it makes the rest of the page inert, so
    // a link behind it is unreachable by role.
    const hint = page.getByTestId("mt-engines-hint");
    await expect(hint).toBeVisible();
    await expect(hint.getByRole("link", { name: /ai settings/i })).toHaveAttribute(
      "href",
      "/ai-settings",
    );

    await page.getByTestId("mt-engine-add").click();
    const form = page.getByTestId("mt-engine-form");
    await expect(form).toBeVisible();
    await expect(form.getByTestId("mt-engine-name")).toBeVisible();
    await expect(form.getByTestId("mt-engine-provider")).toBeVisible();
    await expect(form.getByTestId("mt-engine-key")).toBeVisible();
    // The options come from GET /api/mt-engines/catalog, so seeing DeepL's
    // proves the catalog reached the form rather than a hardcoded list.
    await expect(form.getByText("Formality")).toBeVisible();
    await expect(form.getByText("Glossary ID")).toBeVisible();
  });

});
