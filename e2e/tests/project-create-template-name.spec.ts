import { test, expect } from "@playwright/test";
import { deleteDraftFromURL, login, startFromTemplate } from "./helpers";

// A template's settings load after the pick. A name typed while they load is
// the user's, and the template's own project name must not replace it.
test.describe("New project from a template", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test.afterEach(async ({ page }) => {
    await deleteDraftFromURL(page);
  });

  test("a name typed while the template loads is kept", async ({ page }) => {
    let release: () => void = () => {};
    const held = new Promise<void>(resolve => { release = resolve });
    await page.route(/\/api\/templates\/\d+$/, async route => {
      await held;
      await route.continue();
    });

    await page.goto("/projects/new");
    // The picker steps without startFromTemplate's final wait, which needs
    // the (held) template response.
    await page.getByTestId("start-from-template").click();
    const dialog = page.getByTestId("template-picker-dialog");
    await dialog.getByRole("button", { name: "Default Template" }).click();
    await expect(dialog).toHaveCount(0);
    const nameInput = page.getByPlaceholder(/project name/i);
    const name = `Typed while loading ${Date.now()}`;
    await nameInput.fill(name);

    release();
    // The caption names the template in the same render that applies the
    // template's metadata, so from here on the name has had its chance to move...
    await expect(page.getByTestId("start-from-source")).toHaveText("Using template Default Template");
    // ...and the typed name is still there.
    await expect(nameInput).toHaveValue(name);
  });

  test("an empty name takes the template's project name", async ({ page }) => {
    await page.goto("/projects/new");
    await startFromTemplate(page, "Default Template");
    await expect(page.getByPlaceholder(/project name/i)).toHaveValue("Default Template");
  });
});
