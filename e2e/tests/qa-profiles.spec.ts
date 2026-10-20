import { test, expect, type Page } from "@playwright/test";
import { backgroundAndBehind, deleteViaAPI, login } from "./helpers";

/**
 * Create a QA profile through the New QA profile dialog and return its id,
 * read from the POST response so the caller can delete it through the API.
 */
async function createProfileViaUI(page: Page, name: string, description?: string): Promise<number> {
  await page.getByRole("button", { name: /new qa profile/i }).click();
  await page.getByPlaceholder(/e\.g\. strict qa/i).fill(name);
  if (description) await page.getByPlaceholder(/optional description/i).fill(description);
  const [res] = await Promise.all([
    page.waitForResponse(r => /\/api\/qa-profiles\/?$/.test(r.url()) && r.request().method() === "POST"),
    page.getByRole("button", { name: /^create$/i }).click(),
  ]);
  expect(res.ok(), `create QA profile: ${res.status()}`).toBe(true);
  const id = (await res.json()).data.id as number;
  await expect(page.getByText(name).first()).toBeVisible();
  return id;
}

test.describe("QA profiles", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("QA profiles page shows seeded Standard QA profile", async ({ page }) => {
    await page.getByRole("link", { name: /qa profiles/i }).click();
    await expect(page).toHaveURL(/\/quality/);
    await expect(page.getByRole("heading", { name: /qa profiles/i })).toBeVisible();
    await expect(page.getByText(/standard qa/i).first()).toBeVisible();
    await expect(page.getByText(/all checks enabled/i).first()).toBeVisible();
  });

  test("expanding a profile shows QA rules with severity badges", async ({ page }) => {
    await page.getByRole("link", { name: /qa profiles/i }).click();

    // Click the Standard QA card to expand — click the profile name text
    await page.getByText(/standard qa/i).first().click();

    // Rules panel loads — the check types appear (wait for API)
    await expect(page.getByText(/empty target/i).first()).toBeVisible({ timeout: 10000 });
    await expect(page.getByText(/tag checks/i).first()).toBeVisible();
    await expect(page.getByText(/number consistency/i).first()).toBeVisible();
    await expect(page.getByText(/punctuation/i).first()).toBeVisible();

    // Each rule offers the four severity options as chip-shaped buttons.
    await expect(page.getByRole("button", { name: "Disabled", exact: true }).first()).toBeVisible();
    await expect(page.getByRole("button", { name: "Error", exact: true }).first()).toBeVisible();
    await expect(page.getByRole("button", { name: "Warning", exact: true }).first()).toBeVisible();
  });

  // The rules panel is a well in the page under the profile card: page-coloured
  // and bordered, in both modes.
  for (const mode of ["light", "dark"] as const) {
    test(`the rules panel is painted in the page colour (${mode})`, async ({ page }) => {
      await page.evaluate(m => localStorage.setItem("openloc-theme", m), mode);
      await page.goto("/quality");
      await page.getByText(/standard qa/i).first().click();
      const panel = page.getByTestId("qa-rules-panel");
      await expect(panel).toBeVisible({ timeout: 10000 });
      const shades = await backgroundAndBehind(panel);
      expect(shades.self, "the rules panel matches the page behind it").toBe(shades.behind);
    });
  }

  test("clicking the active severity disables a rule; a severity re-enables it", async ({ page }) => {
    await page.getByRole("link", { name: /qa profiles/i }).click();

    // Use a fresh profile so we don't mutate the shared demo data.
    const profileName = `E2E QA toggle ${Date.now()}`;
    const profileId = await createProfileViaUI(page, profileName);

    try {
      // Expand it
      await page.getByText(profileName).first().click();

      // The tag_checks sub-checks only render while the rule is enabled, so their
      // visibility is a clean observable signal of the enabled/disabled state.
      const tagRow = page.locator("div.px-4.py-3").filter({ hasText: "Tag checks" });
      await expect(tagRow).toBeVisible({ timeout: 10000 });

      const ruleSaved = () =>
        page.waitForResponse(r => /\/qa-profiles\/\d+\/rules$/.test(r.url()) && r.request().method() === "POST");

      // Normalize to a known state: disabled -> no sub-checks.
      await Promise.all([ruleSaved(), tagRow.getByRole("button", { name: "Disabled", exact: true }).click()]);
      await expect(page.getByText(/missing tags/i)).toHaveCount(0);

      // Clicking a severity while disabled re-enables the rule -> sub-checks appear.
      await Promise.all([ruleSaved(), tagRow.getByRole("button", { name: "Warning", exact: true }).click()]);
      await expect(page.getByText(/missing tags/i).first()).toBeVisible({ timeout: 5000 });

      // Clicking the now-active severity again disables the rule -> sub-checks vanish.
      await Promise.all([ruleSaved(), tagRow.getByRole("button", { name: "Warning", exact: true }).click()]);
      await expect(page.getByText(/missing tags/i)).toHaveCount(0);
    } finally {
      await deleteViaAPI(page, `/api/qa-profiles/${profileId}`, `QA profile ${profileId}`);
    }
  });

  test("can create and delete a QA profile", async ({ page }) => {
    await page.getByRole("link", { name: /qa profiles/i }).click();

    const profileName = `E2E QA ${Date.now()}`;
    const profileId = await createProfileViaUI(page, profileName, "Test profile");

    try {
      // Delete — hover the card and click Delete button
      const profileCard = page.locator("div.group").filter({ hasText: profileName }).first();
      await profileCard.hover();
      const deleteBtn = profileCard.getByRole("button", { name: /delete/i });
      await expect(deleteBtn).toBeVisible();
      await deleteBtn.click({ force: true });

      // Confirm dialog
      const confirmBtn = page.getByRole("alertdialog").getByRole("button", { name: /delete/i });
      await Promise.all([
        page.waitForResponse(r => r.url().endsWith(`/api/qa-profiles/${profileId}`) && r.request().method() === "DELETE"),
        confirmBtn.click(),
      ]);
      await expect(page.getByText(profileName)).not.toBeVisible({ timeout: 5000 });
    } finally {
      // 404 (already gone) when the UI delete above succeeded; removes the profile when it did not.
      await deleteViaAPI(page, `/api/qa-profiles/${profileId}`, `QA profile ${profileId}`);
    }
  });

  test("nav link navigates to QA profiles page", async ({ page }) => {
    const navLink = page.getByRole("link", { name: /qa profiles/i });
    await expect(navLink).toBeVisible();
    await navLink.click();
    await expect(page).toHaveURL(/\/quality/);
    await expect(page.getByRole("button", { name: /new qa profile/i })).toBeVisible();
  });

  // The QA profile rename goes through `useMutationWithToast`, so the
  // outcome surfaces as a toast and the list cache invalidates. This test is
  // the happy path; the next one covers the failure path.
  test("renaming a QA profile shows success toast and updates the list", async ({ page }) => {
    const initialName = `E2E QA Rename Happy ${Date.now()}`;
    const renamedName = `${initialName} (renamed)`;

    await page.getByRole("link", { name: /qa profiles/i }).click();

    // Create a profile we can rename + clean up.
    const profileId = await createProfileViaUI(page, initialName, "Rename test");

    try {
      // Hover the row to reveal the rename button (hover-action pattern).
      // Note: we have to re-resolve the row before each interaction because
      // `filter({ hasText })` matches via textContent — once the inline input
      // appears, the profile name moves into an `<input value="">` which no
      // longer satisfies the textContent filter.
      const profileCard = page.locator("div.group").filter({ hasText: initialName }).first();
      await profileCard.hover();
      const renameBtn = profileCard.getByRole("button", { name: /rename/i });
      await expect(renameBtn).toBeVisible();
      await renameBtn.click({ force: true });

      // Look up the rename input at page level — there's only one visible at
      // a time, and the `aria-label="Rename"` default from `InlineRenameField`
      // makes it role-locatable.
      const renameInput = page.getByRole("textbox", { name: /^rename$/i });
      await expect(renameInput).toBeVisible({ timeout: 3000 });
      await renameInput.fill(renamedName);
      await Promise.all([
        page.waitForResponse(
          resp => resp.url().includes("/qa-profiles/") && resp.request().method() === "PATCH",
          { timeout: 5000 },
        ),
        renameInput.press("Enter"),
      ]);

      // Success toast from useMutationWithToast.
      await expect(page.getByText(/QA profile renamed/i).first()).toBeVisible({ timeout: 3000 });

      // List reflects the new name (the mutation's invalidateKeys).
      await expect(page.getByText(renamedName).first()).toBeVisible({ timeout: 3000 });
    } finally {
      await deleteViaAPI(page, `/api/qa-profiles/${profileId}`, `QA profile ${profileId}`);
    }
  });

  test("rename failure surfaces a toast and does NOT silently swallow the error", async ({ page }) => {
    const initialName = `E2E QA Rename Fail ${Date.now()}`;

    await page.getByRole("link", { name: /qa profiles/i }).click();

    // Create the profile we'll attempt to rename.
    const profileId = await createProfileViaUI(page, initialName, "Failure test");

    try {
      // Intercept the PATCH and inject a 500: the frontend must surface it as
      // a toast.
      await page.route("**/api/qa-profiles/*", async (route) => {
        if (route.request().method() === "PATCH") {
          await route.fulfill({
            status: 500,
            contentType: "application/json",
            body: JSON.stringify({ error: { message: "simulated server failure" } }),
          });
        } else {
          await route.continue();
        }
      });

      const profileCard = page.locator("div.group").filter({ hasText: initialName }).first();
      await profileCard.hover();
      const renameBtn = profileCard.getByRole("button", { name: /rename/i });
      await expect(renameBtn).toBeVisible();
      await renameBtn.click({ force: true });

      const renameInput = page.getByRole("textbox", { name: /^rename$/i });
      await expect(renameInput).toBeVisible({ timeout: 3000 });
      await renameInput.fill(`${initialName} (rename attempt)`);
      await renameInput.press("Enter");

      // A failed rename shows an error toast.
      await expect(page.getByText(/Failed to rename QA profile/i).first()).toBeVisible({ timeout: 3000 });

      // The rename input should remain open after a failure so the user
      // doesn't lose their typed value (intentional UX — `useMutationWithToast`
      // only calls `onSuccess: cancelRename` on success). Verifies the input
      // is still in the DOM with the (rejected) attempted name.
      await expect(renameInput).toBeVisible();
      await expect(renameInput).toHaveValue(`${initialName} (rename attempt)`);

      // Stop intercepting so the Escape + cleanup paths can talk to the real backend.
      await page.unroute("**/api/qa-profiles/*");

      // Cancel the rename so the original name returns to the list.
      await renameInput.press("Escape");
      await expect(page.getByText(initialName).first()).toBeVisible({ timeout: 3000 });
    } finally {
      await deleteViaAPI(page, `/api/qa-profiles/${profileId}`, `QA profile ${profileId}`);
    }
  });
});
