import { test, expect } from "@playwright/test";
import { login } from "./helpers";

// API documentation (/api-docs): guides and the endpoint reference in one
// sidebar. The endpoint pages are built from the served OpenAPI document, so
// this proves the whole path: spec endpoint → page → sidebar → search.

test.describe("API docs", () => {
  test("signed out, the API docs are public; the customer API is not", async ({ browser }) => {
    const ctx = await browser.newContext({ storageState: { cookies: [], origins: [] } });
    expect((await ctx.request.get("/api/api-docs")).status()).toBe(200);
    expect((await ctx.request.get("/api/api-docs/openapi.json")).status()).toBe(200);
    expect((await ctx.request.get("/api/v1/openapi.json")).status()).toBe(401);
    const page = await ctx.newPage();
    await page.goto("/api-docs/endpoints/getJob");
    await expect(page.getByTestId("api-endpoint")).toBeVisible();
    await expect(page.getByText("Open app", { exact: true })).toBeVisible();
    await expect(page.getByTestId("nav-rail")).toHaveCount(0);
    await ctx.close();
  });

  test("API is in the account menu and lists guides and endpoint groups", async ({ page: app, context }) => {
    await login(app);
    await app.getByRole("button", { name: /account menu/i }).click();
    // The API docs open in a new tab.
    const [page] = await Promise.all([
      context.waitForEvent("page"),
      app.getByRole("menuitem", { name: "API" }).click(),
    ]);
    await expect(page).toHaveURL(/\/api-docs$/);
    await expect(page.getByTestId("nav-rail")).toHaveCount(0);
    await expect(page.getByRole("heading", { name: "API", exact: true, level: 1 })).toBeVisible();

    const sidebar = page.getByTestId("docs-sidebar");
    await expect(sidebar.getByRole("link", { name: "Authentication" })).toBeVisible();
    const nav = sidebar.getByTestId("api-endpoint-nav");
    await expect(nav.getByRole("link", { name: "API documentation" })).toBeVisible();
    await nav.getByRole("button", { name: "Jobs" }).click();
    await nav.getByRole("link", { name: /Get a job/ }).click();
    await expect(page).toHaveURL(/\/api-docs\/endpoints\/getJob$/);
  });

  test("an endpoint page shows its contract and working samples", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/api-docs/endpoints/createProject");
    await expect(page.getByRole("heading", { level: 1, name: "Create a project" })).toBeVisible();
    await expect(page.getByTestId("api-request-bar")).toContainText("/api/v1/projects");
    await expect(page.getByRole("region", { name: "Body" })).toContainText("source_language");
    await expect(page.getByTestId("api-request-samples")).toContainText("--request POST");
    await page.getByTestId("api-request-samples").getByRole("tab", { name: "Python" }).click();
    await expect(page.getByTestId("api-request-samples")).toContainText("requests.post(");
    await expect(page.getByTestId("api-response-samples").getByRole("tab", { name: "201" })).toBeVisible();
  });

  test("search finds an endpoint", async ({ page }) => {
    await page.goto("/api-docs");
    await page.getByTestId("docs-search-trigger").click();
    await page.getByTestId("docs-search-input").fill("upload");
    await page.getByTestId("docs-search-results").getByText("Upload a file", { exact: true }).click();
    await expect(page).toHaveURL(/\/api-docs\/endpoints\/uploadFile$/);
  });
});
