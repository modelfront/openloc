// Labels page E2E.
//
// Customer and Domain are built-in labels; this page manages every label. Covers the create-form happy
// path (every create form has a submit+verify E2E test), the case-insensitive
// duplicate guard (the partial unique index catches "Country" vs "country"
// within a tenant), adding a value by typing, the built-in labels having no
// bin, and deleting a value a project uses: it asks first, unsets the value on
// the project, and frees the label.
//
// Self-cleanup: each test deletes what it made via the API in afterEach.

import { test, expect } from "@playwright/test";
import { login, createProjectViaAPI, deleteProjectViaAPI, projectLabelsViaAPI } from "./helpers";

// Unique per worker process: fullyParallel workers load this module at the same
// moment, so a bare Date.now() can repeat across them, and one worker's afterEach
// sweep would then delete the label another worker is still testing.
const TS = `${Date.now()}${process.pid}`;
const NAME = `E2E Label ${TS}`;
const NAME_VARIANT_CASE = `e2e label ${TS}`; // case-flipped duplicate

test.describe("Labels page", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test.afterEach(async ({ page }) => {
    // Best-effort: the test may already have deleted it.
    const res = await page.request.get("/api/labels");
    if (!res.ok()) return;
    const body = await res.json();
    for (const l of (body.data ?? []).filter((l: { name: string }) => l.name.startsWith(`E2E Label ${TS}`))) {
      await page.request.delete(`/api/labels/${l.id}`);
    }
  });

  test("create → appears → add a value → duplicate name refused → delete", async ({ page }) => {
    // Through the nav rather than page.goto, so the role-load race the page
    // guards against is exercised the way a user hits it.
    await page.getByRole("link", { name: /^labels$/i }).click();
    await expect(page).toHaveURL(/\/labels$/);
    await expect(page.getByTestId("label-manager")).toBeVisible();

    // Create.
    await page.getByTestId("add-label-button").click();
    await page.getByTestId("new-label-name").fill(NAME);
    const [created] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith("/api/labels") && r.request().method() === "POST"),
      page.getByTestId("new-label-submit").click(),
    ]);
    expect(created.status()).toBe(201);
    const id = (await created.json()).data.id;
    await expect(page.getByTestId(`label-row-${id}`)).toContainText(NAME);

    // Add a value by typing it: the values ARE free text, so the picker offers
    // to add whatever matches nothing.
    await page.getByTestId(`label-values-${id}`).click();
    await page.getByPlaceholder("Type to add a new option").fill("Germany");
    const [valueAdded] = await Promise.all([
      page.waitForResponse(r => r.url().includes(`/api/labels/${id}/values`) && r.request().method() === "POST"),
      page.getByTestId(`label-values-${id}-action-create-option`).click(),
    ]);
    expect(valueAdded.status()).toBe(201);
    await page.keyboard.press("Escape");
    await expect(page.getByTestId(`label-values-${id}`)).toContainText("Germany");

    // A case-flipped duplicate is refused by the partial unique index.
    await page.getByTestId("add-label-button").click();
    await page.getByTestId("new-label-name").fill(NAME_VARIANT_CASE);
    const [dup] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith("/api/labels") && r.request().method() === "POST"),
      page.getByTestId("new-label-submit").click(),
    ]);
    expect(dup.status()).toBe(409);

    // Delete the one nothing uses.
    await page.reload();
    const [deleted] = await Promise.all([
      page.waitForResponse(r => r.url().includes(`/api/labels/${id}`) && r.request().method() === "DELETE"),
      page.getByTestId(`label-delete-${id}`).click(),
    ]);
    expect(deleted.status()).toBe(204);
    await expect(page.getByTestId(`label-row-${id}`)).toHaveCount(0);
  });

  test("in-use value: bin explains, delete asks, project loses it, label frees up", async ({ page }) => {
    // Its own name, outside the NAME prefix the afterEach sweeps, so a parallel
    // worker's cleanup cannot delete it mid-test; the finally removes it.
    const created = await page.request.post("/api/labels", { data: { name: `E2E In-use Label ${TS}`, values: ["Germany", "Japan"] } });
    expect(created.status()).toBe(201);
    const label = (await created.json()).data as { id: number; values: { id: number; value: string }[] };
    const germany = label.values.find(v => v.value === "Germany")!;
    await page.goto("/labels");
    const projectId = await createProjectViaAPI(page, `E2E Label project ${TS}`);
    try {
      const picked = await page.request.put(`/api/projects/${projectId}/labels`, {
        data: { labels: [{ label_definition_id: label.id, label_value_id: germany.id }] },
      });
      expect(picked.ok()).toBe(true);

      await page.reload();
      const bin = page.getByTestId(`label-delete-${label.id}`);
      await expect(bin).toBeDisabled();
      // The span around the disabled bin is the tooltip trigger.
      await bin.locator("..").hover();
      await expect(page.getByTestId(`label-delete-tooltip-${label.id}`)).toContainText("Germany (1 project) is still in use");

      // Unticking the in-use value asks first, then unsets it on the project.
      await page.getByTestId(`label-values-${label.id}`).click();
      await page.getByTestId(`label-values-${label.id}-option-${germany.id}`).click();
      await expect(page.getByText("Germany is set on 1 project. Deleting it removes it from them.")).toBeVisible();
      const [deletedValue] = await Promise.all([
        page.waitForResponse(r => r.url().includes(`/api/labels/${label.id}/values/${germany.id}`) && r.request().method() === "DELETE"),
        page.getByRole("button", { name: "Delete", exact: true }).click(),
      ]);
      expect(deletedValue.status()).toBe(204);
      expect(await projectLabelsViaAPI(page, projectId)).toEqual({});

      // Japan is left and unused, so the label can go now.
      await page.keyboard.press("Escape");
      await expect(bin).toBeEnabled();
      const [deletedLabel] = await Promise.all([
        page.waitForResponse(r => r.url().endsWith(`/api/labels/${label.id}`) && r.request().method() === "DELETE"),
        bin.click(),
      ]);
      expect(deletedLabel.status()).toBe(204);
      await expect(page.getByTestId(`label-row-${label.id}`)).toHaveCount(0);
    } finally {
      await deleteProjectViaAPI(page, projectId);
      await page.request.delete(`/api/labels/${label.id}`);
    }
  });

  test("Customer and Domain are built in: listed first, and no bin", async ({ page }) => {
    await page.goto("/labels");
    await expect(page.getByTestId("label-manager")).toBeVisible();

    const res = await page.request.get("/api/labels");
    const labels = (await res.json()).data as { id: number; name: string; system: boolean }[];
    const builtIn = labels.filter(l => l.system).map(l => l.name);
    expect(builtIn).toEqual(["Customer", "Domain"]);

    // Ordered ahead of the tenant's own labels, and undeletable — the bin is
    // not rendered at all, because the API refuses the call it would make.
    expect(labels.slice(0, 2).every(l => l.system)).toBe(true);
    for (const l of labels.filter(l => l.system)) {
      await expect(page.getByTestId(`label-delete-${l.id}`)).toHaveCount(0);
    }

    // And the API agrees, not just the UI.
    const customer = labels.find(l => l.system && l.name === "Customer")!;
    const refused = await page.request.delete(`/api/labels/${customer.id}`);
    expect(refused.status()).toBe(409);
  });
});
