import { test, expect } from "@playwright/test";
import { login, pickComboboxValue, selectTargetLanguages } from "./helpers";

// AI routers: the create form is driven through the UI —
// name, the prefilled "All languages" → first engine row (the default), plus
// a German row — and the saved router is
// checked in the list, after a reload, and through the API. The engine it
// routes to is the seeded QE+APE engine. Both comboboxes go through the
// shared helpers ("cmdk popover" pattern).
test.describe("AI routers", () => {
  // Named per test run, not per file, so repeats (--repeat-each, retries)
  // never trip the unique-name constraint on a router a previous run left.
  let routerName = "";
  let token: string | null = null;
  let routerId = 0;
  const auth = () => ({ Authorization: `Bearer ${token}` });

  test.beforeEach(async ({ page }) => {
    await login(page);
    token = await page.evaluate(() => localStorage.getItem("token"));
  });

  test.afterAll(async ({ browser }) => {
    if (!routerId) return;
    const page = await browser.newPage();
    await login(page);
    const headers = { Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}` };
    await page.request.delete(`/api/ai/routers/${routerId}`, { headers });
    await page.close();
  });

  test("create a router in the UI with a default engine and a language rule", async ({ page }) => {
    test.setTimeout(20_000);
    routerName = `E2E UI router ${Date.now()}-${Math.floor(Math.random() * 1e6)}`;
    const enginesRes = await page.request.get(`/api/ai/engines`, { headers: auth() });
    expect(enginesRes.ok()).toBe(true);
    const engine = ((await enginesRes.json()).data as Array<{ id: number; name: string; purpose: string }>)
      .find((e) => e.purpose === "qe_ape");
    expect(engine, "seed must provide a QE+APE engine").toBeTruthy();

    await page.goto("/ai");
    await expect(page.getByRole("heading", { name: /^AI$/ })).toBeVisible();
    // The seeded router is listed before anything is created.
    await expect(page.getByText("Default QE+APE router")).toBeVisible();

    await page.getByTestId("ai-router-add").click();
    const form = page.getByTestId("ai-router-form");
    await form.getByTestId("ai-router-name").fill(routerName);

    // Row 0 opens prefilled: "All languages" → the first QE+APE engine, and
    // as the only route it cannot be removed.
    await expect(form.getByTestId("ai-router-rule-0").getByTestId("lang")).toContainText("All");
    await expect(form.getByTestId("ai-router-rule-0-remove")).toBeDisabled();
    await form.getByTestId("ai-router-rule-add").click();
    await selectTargetLanguages(page, form.getByTestId("ai-router-rule-1").getByTestId("lang"), ["de"]);
    await pickComboboxValue(page, "ai-router-rule-1-engine", String(engine!.id));

    const [res] = await Promise.all([
      page.waitForResponse((r) => r.url().includes("/api/ai/routers") && r.request().method() === "POST"),
      form.getByRole("button", { name: /^Create$/ }).click(),
    ]);
    expect(res.status(), await res.text()).toBe(201);
    const created = (await res.json()).data as { id: number; name: string; default_engine_id: number; rules: { target_lang: string; engine_id: number }[] };
    routerId = created.id;
    expect(created.name).toBe(routerName);
    expect(created.default_engine_id).toBe(engine!.id);
    expect(created.rules).toEqual([{ id: expect.any(Number), target_lang: "de", engine_id: engine!.id }]);

    // The card lists the router by name; it survives a reload.
    const card = page.getByTestId(`ai-router-${routerId}`);
    await expect(card).toContainText(routerName);
    await page.reload();
    await expect(page.getByTestId(`ai-router-${routerId}`)).toContainText(routerName);

    // The step dialog offers it.
    await page.goto("/workflows/steps");
    await page.getByTestId("new-step-definition").click();
    const dialog = page.getByTestId("step-definition-dialog");
    await dialog.getByTestId("step-type-option-hybrid").click();
    await dialog.getByTestId("step-definition-router").click();
    const popover = page.locator('[data-slot="popover-content"]');
    await expect(popover.getByRole("option", { name: new RegExp(routerName) })).toBeVisible();
    await page.keyboard.press("Escape");
  });
});
