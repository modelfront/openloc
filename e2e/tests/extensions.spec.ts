import { test, expect, type Page } from "@playwright/test";
import { authHeaders, login } from "./helpers";
import { buildMockQEZip, zipStored } from "./extension-zip";

// Custom providers on the AI settings page: an owner adds the mock QE
// extension (built from internal/extension/testdata/mockqe — no binary is
// checked in) from the providers grid, its panel opens with its details, a key
// is added there, and the provider is removed again from the same panel.
// Serial: the steps share one installed extension in the shared seed tenant.
test.describe.configure({ mode: "serial" });

const EXT_ID = "mockqe";
const PROVIDER = `ext:${EXT_ID}`;

let zip: Buffer;

// Removes the extension; its keys and endpoints go with it.
async function removeMockQE(page: Page) {
  const res = await page.request.delete(`/api/ai/extensions/${EXT_ID}`, { headers: await authHeaders(page) });
  expect([204, 404]).toContain(res.status());
}

test.describe("AI custom providers", () => {
  test.beforeAll(async ({ browser }) => {
    // The first wasm build can take a while; Go caches it after that.
    test.setTimeout(180_000);
    zip = buildMockQEZip();
    // A run that died half-way leaves its extension behind.
    const page = await browser.newPage();
    await login(page);
    await removeMockQE(page);
    await page.close();
  });

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    await login(page);
    await removeMockQE(page);
    await page.close();
  });

  test.beforeEach(async ({ page }) => {
    await login(page);
    await page.goto("/ai-settings");
  });

  // The add control opens an explanation; the zip is picked from there.
  async function openAddDialog(page: Page) {
    await page.getByTestId("ai-custom-provider-add").click();
    await expect(page.getByTestId("ai-custom-provider-dialog")).toContainText("Add a custom provider");
  }

  test("a package without a manifest is refused with the server's reason", async ({ page }) => {
    await openAddDialog(page);
    const [res] = await Promise.all([
      page.waitForResponse((r) => r.url().endsWith("/api/ai/extensions") && r.request().method() === "POST"),
      page.getByTestId("ai-custom-provider-file").setInputFiles({
        name: "broken.zip",
        mimeType: "application/zip",
        buffer: zipStored({ "README.txt": Buffer.from("not an extension") }),
      }),
    ]);
    expect(res.status()).toBe(400);
    const body = await res.json();
    expect(body.error.field).toBe("file");
    await expect(page.getByTestId("ai-custom-provider-add-error")).toHaveText(body.error.message);
    await expect(page.getByTestId(`ai-connection-tile-new-${PROVIDER}`)).toHaveCount(0);
  });

  test("add the custom provider, then add a key in its panel", async ({ page }) => {
    // The upload and the key check each compile the WASM module once on a
    // fresh server, which alone can take several seconds.
    test.setTimeout(30_000);
    await openAddDialog(page);
    const [upload] = await Promise.all([
      page.waitForResponse((r) => r.url().endsWith("/api/ai/extensions") && r.request().method() === "POST"),
      page.getByTestId("ai-custom-provider-file").setInputFiles({ name: "mockqe.zip", mimeType: "application/zip", buffer: zip }),
    ]);
    expect(upload.status()).toBe(201);
    await expect(page.getByTestId("ai-custom-provider-dialog")).toHaveCount(0);

    // The provider joins the QE+APE row under its manifest name, and its
    // panel opens on its own.
    const tile = page.getByTestId("ai-connection-group-qe").getByTestId(`ai-connection-tile-new-${PROVIDER}`);
    await expect(tile).toContainText("Mock QE");
    await expect(tile).toHaveAttribute("aria-pressed", "true");
    const panel = page.getByTestId("ai-connection-panel");
    const details = panel.getByTestId("ai-custom-provider-details");
    await expect(details).toContainText("Version 1.0.0");
    await expect(details).toContainText("QE");
    await expect(details).toContainText("APE");
    await expect(details).toContainText("Sends data to: the endpoint set on its key");
    // The manifest has a base_url setting, so the endpoint field is there,
    // labelled by the manifest.
    await expect(panel.getByTestId("ai-connection-form")).toContainText("Endpoint");

    await panel.getByTestId("ai-token-add").click();
    const form = page.getByTestId("ai-token-form");
    await expect(form).toContainText("API token");
    await form.getByTestId("ai-token-api-key").fill("e2e-extension-key");
    const [created] = await Promise.all([
      page.waitForResponse((r) => r.url().includes(`/api/ai/vendors/${PROVIDER}/tokens`) && r.request().method() === "POST"),
      form.getByRole("button", { name: "Add" }).click(),
    ]);
    // With no endpoint set, mockqe's check_key accepts any key.
    expect(created.status()).toBe(201);
    const token = (await created.json()).data as { id: number };
    await expect(page.getByTestId(`ai-token-row-${token.id}`)).toBeVisible();

    // The key is stored against the extension's provider.
    const vendors = await page.request.get("/api/ai/vendors", { headers: await authHeaders(page) });
    const vendor = ((await vendors.json()).data as Array<{ provider: string; tokens: Array<{ id: number }> }>)
      .find((v) => v.provider === PROVIDER);
    expect(vendor?.tokens.map((t) => t.id)).toContain(token.id);
  });

  test("remove the custom provider after confirming", async ({ page }) => {
    // It has a key from the test above, so its tile is keyed by that
    // endpoint, not by the provider: find it by name.
    const tile = page.getByTestId("ai-connection-group-qe").getByRole("button", { name: /^Mock QE/ });
    await tile.click();
    await page.getByTestId("ai-connection-panel").getByTestId("ai-custom-provider-remove").click();
    const dialog = page.getByRole("alertdialog");
    await expect(dialog).toContainText("Remove Mock QE?");
    const [res] = await Promise.all([
      page.waitForResponse((r) => r.url().endsWith(`/api/ai/extensions/${EXT_ID}`) && r.request().method() === "DELETE"),
      dialog.getByRole("button", { name: "Remove" }).click(),
    ]);
    expect(res.status()).toBe(204);
    await expect(page.getByTestId("ai-connection-panel")).toHaveCount(0);
    await expect(tile).toHaveCount(0);
  });
});
