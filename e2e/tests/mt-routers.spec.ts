import { test, expect } from "@playwright/test";
import {
  createProjectViaAPI,
  deleteProjectViaAPI,
  getProjectSettingsViaAPI,
  login,
  pickComboboxValue,
  getSeedTemplateMTRouter,
  selectTargetLanguages,
} from "./helpers";

// MT routers: the create form is driven through the UI —
// name, the prefilled "All languages" row pointed at a classic MT engine (the
// default), plus a German row pointed at the seeded LLM translation engine —
// and the saved router is checked in the list, after a reload, and through
// the API. A project then picks it on its settings tab, and the router cannot
// be deleted while that project follows it. Both comboboxes go through the
// shared helpers ("cmdk popover" pattern).
//
// Engine option keys are `mt:<id>` (classic MT engine) and `ai:<id>` (LLM
// engine); the API splits them back into mt_engine_id / ai_engine_id.
type EngineRef = { mt_engine_id: number | null; ai_engine_id: number | null };
type RouterResponse = {
  id: number;
  name: string;
  default_mt_engine_id: number | null;
  default_ai_engine_id: number | null;
  rules: ({ id: number; target_lang: string } & EngineRef)[];
};

test.describe("MT routers", () => {
  // Named per test run, not per file, so repeats (--repeat-each, retries)
  // never trip the unique-name constraint on a router a previous run left.
  let routerName = "";
  let token: string | null = null;
  let routerId = 0;
  let projectId = 0;
  const auth = () => ({ Authorization: `Bearer ${token}` });

  test.beforeEach(async ({ page }) => {
    await login(page);
    token = await page.evaluate(() => localStorage.getItem("token"));
  });

  // Clear the project's router before deleting anything: the delete refuses
  // (409) while any settings profile — a soft-deleted project's included —
  // still points at the router.
  test.afterAll(async ({ browser }) => {
    if (!routerId && !projectId) return;
    const page = await browser.newPage();
    await login(page);
    const headers = { Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}` };
    if (projectId) {
      await page.request.patch(`/api/projects/${projectId}/settings`, { headers, data: { mt_router_id: null } });
      await deleteProjectViaAPI(page, projectId);
    }
    if (routerId) await page.request.delete(`/api/mt-routers/${routerId}`, { headers });
    await page.close();
  });

  test("create a router in the UI, pick it on a project, and it is held by that project", async ({ page }) => {
    test.setTimeout(30_000);
    routerName = `E2E MT router ${Date.now()}-${Math.floor(Math.random() * 1e6)}`;

    // The default row → a seeded classic MT engine.
    const mtRes = await page.request.get(`/api/mt-engines`, { headers: auth() });
    expect(mtRes.ok()).toBe(true);
    const classic = ((await mtRes.json()).data as Array<{ id: number; name: string; enabled: boolean }>)
      .find((e) => e.enabled);
    expect(classic, "seed must provide an enabled MT engine").toBeTruthy();

    // The German row → the seeded LLM translation engine when the seed has one
    // (it needs an OpenAI token), otherwise the same classic engine. Which one
    // was picked is pinned below, so the assertion is exact either way.
    const aiRes = await page.request.get(`/api/ai/engines`, { headers: auth() });
    expect(aiRes.ok()).toBe(true);
    const llm = ((await aiRes.json()).data as Array<{ id: number; name: string; purpose: string }>)
      .find((e) => e.purpose === "mt");
    const ruleKey = llm ? `ai:${llm.id}` : `mt:${classic!.id}`;
    const ruleRef: EngineRef = llm
      ? { mt_engine_id: null, ai_engine_id: llm.id }
      : { mt_engine_id: classic!.id, ai_engine_id: null };

    await page.goto("/mt");
    await expect(page.getByRole("heading", { name: /^MT$/ })).toBeVisible();
    // The seeded router (the one Default Template follows) is listed before
    // anything is created.
    const seedRouter = await getSeedTemplateMTRouter(page);
    await expect(page.getByTestId(`mt-router-${seedRouter.id}`)).toContainText(seedRouter.name);

    await page.getByTestId("mt-router-add").click();
    const form = page.getByTestId("mt-router-form");
    await form.getByTestId("mt-router-name").fill(routerName);

    // Row 0 opens prefilled: "All languages" → the first engine, and as the
    // only route it cannot be removed. Point it at the classic engine.
    await expect(form.getByTestId("mt-router-rule-0").getByTestId("lang")).toContainText("All");
    await expect(form.getByTestId("mt-router-rule-0-remove")).toBeDisabled();
    await pickComboboxValue(page, "mt-router-rule-0-engine", `mt:${classic!.id}`);

    await form.getByTestId("mt-router-rule-add").click();
    await selectTargetLanguages(page, form.getByTestId("mt-router-rule-1").getByTestId("lang"), ["de"]);
    await pickComboboxValue(page, "mt-router-rule-1-engine", ruleKey);

    const [res] = await Promise.all([
      page.waitForResponse((r) => r.url().endsWith("/api/mt-routers") && r.request().method() === "POST"),
      form.getByTestId("mt-router-submit").click(),
    ]);
    expect(res.status(), await res.text()).toBe(201);
    const created = (await res.json()).data as RouterResponse;
    routerId = created.id;
    expect(created.name).toBe(routerName);
    expect(created.default_mt_engine_id).toBe(classic!.id);
    expect(created.default_ai_engine_id).toBeNull();
    expect(created.rules).toEqual([{ id: expect.any(Number), target_lang: "de", ...ruleRef }]);

    // The card lists the router by name; it survives a reload.
    await expect(page.getByTestId(`mt-router-${routerId}`)).toContainText(routerName);
    await page.reload();
    await expect(page.getByTestId(`mt-router-${routerId}`)).toContainText(routerName);

    // The API reads back exactly what was saved.
    const getRes = await page.request.get(`/api/mt-routers/${routerId}`, { headers: auth() });
    expect(getRes.ok()).toBe(true);
    const fetched = (await getRes.json()).data as RouterResponse;
    expect(fetched.name).toBe(routerName);
    expect(fetched.default_mt_engine_id).toBe(classic!.id);
    expect(fetched.default_ai_engine_id).toBeNull();
    expect(fetched.rules).toEqual(created.rules);

    // A project picks the router on its settings tab. A fresh project, not
    // Demo Project: other specs read Demo Project's settings in parallel.
    projectId = await createProjectViaAPI(page, `E2E MT router project ${Date.now()}`);
    const before = await getProjectSettingsViaAPI(page, projectId);
    expect(before.data.project.mt_router_id, "a project made without a template has no router").toBeNull();

    await page.goto(`/projects/${projectId}/settings`);
    const mtSection = page.getByTestId("mt-section");
    await expect(mtSection).toBeVisible();
    const select = mtSection.getByTestId("mt-router-select");
    await expect(select).toContainText("No machine translation");
    // Nothing to clear yet, so no X.
    await expect(mtSection.getByTestId("mt-router-select-clear")).toHaveCount(0);
    const [patch] = await Promise.all([
      page.waitForResponse((r) => r.url().includes(`/api/projects/${projectId}/settings`) && r.request().method() === "PATCH"),
      pickComboboxValue(page, "mt-router-select", String(routerId)),
    ]);
    expect(patch.ok(), await patch.text()).toBe(true);
    await expect(mtSection.getByTestId("mt-router-select")).toContainText(routerName);

    const after = await getProjectSettingsViaAPI(page, projectId);
    expect(after.data.project.mt_router_id).toBe(routerId);
    expect(after.data.project.mt_router_name).toBe(routerName);

    // While the project follows it, the router cannot be deleted.
    const del = await page.request.delete(`/api/mt-routers/${routerId}`, { headers: auth() });
    expect(del.status(), await del.text()).toBe(409);
    const still = await page.request.get(`/api/mt-routers/${routerId}`, { headers: auth() });
    expect(still.ok(), "a refused delete leaves the router in place").toBe(true);

    // The X inside the picker clears the router without opening the list.
    const [clearPatch] = await Promise.all([
      page.waitForResponse((r) => r.url().includes(`/api/projects/${projectId}/settings`) && r.request().method() === "PATCH"),
      mtSection.getByTestId("mt-router-select-clear").click(),
    ]);
    expect(clearPatch.ok(), await clearPatch.text()).toBe(true);
    await expect(select).toContainText("No machine translation");
    await expect(page.getByRole("listbox")).toHaveCount(0);
    const cleared = await getProjectSettingsViaAPI(page, projectId);
    expect(cleared.data.project.mt_router_id).toBeNull();
    expect(cleared.data.project.mt_router_name).toBeNull();
  });
});
