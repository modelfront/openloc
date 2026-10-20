import { test, expect } from "@playwright/test";
import { login, deleteProjectViaAPI, expectNoOverflow, getProjectSettingsViaAPI, setProjectWorkflowStepsViaAPI, deleteStepDefinitionViaAPI, UNBREAKABLE_NAME } from "./helpers";

// AI prompts + engines, and QE+APE workflow steps.
//
// The prompt is created through the UI (plain inputs + mode cards — no cmdk),
// which proves the form + POST + list render + persistence. The editor is its
// own page, so "open the form" is a navigation: the list links to
// /ai/prompts/new. The engine and the workflow step are exercised through the
// API: their forms lean on cmdk comboboxes that are deliberately kept out of
// E2E, and the backend integration tests
// already cover the full validation matrix.
//
// A QE+APE step carries an `ai_router_id`: the router
// maps target languages to engines and the step reaches its engine through
// it. This test proves the end-to-end wiring: a prompt is created in the UI,
// an engine (with a row) and a router defaulting to it via API, and the router
// attaches to a workflow step, which persists.
test.describe("AI prompts, engines, and QE+APE steps", () => {
  const suffix = Date.now();
  const promptName = `E2E QE prompt ${suffix}`;
  const engineName = `E2E QE engine ${suffix}`;
  const projectName = `E2E AI project ${suffix}`;

  let token: string | null = null;
  let promptId = 0;
  let engineId = 0;
  let routerId = 0;
  let projectId = 0;

  const auth = () => ({ Authorization: `Bearer ${token}` });

  test.beforeEach(async ({ page }) => {
    await login(page);
    token = await page.evaluate(() => localStorage.getItem("token"));
  });

  test.afterAll(async ({ browser }) => {
    // Fresh context for cleanup (afterAll has no test page/token).
    const page = await browser.newPage();
    await login(page);
    if (projectId) await deleteProjectViaAPI(page, projectId);
    if (engineId) {
      // The hybrid step definition the workflow PUT created for this router,
      // then the router, then the engine — each delete is refused while the
      // next one still references it.
      const headers = { Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}` };
      const defs = await page.request.get(`/api/step-definitions`, { headers });
      const def = ((await defs.json()).data as { id: number; name: string }[]).find((d) => d.name === `AI review ${engineId}`);
      if (def) await deleteStepDefinitionViaAPI(page, def.id);
      if (routerId) await page.request.delete(`/api/ai/routers/${routerId}`, { headers });
      await page.request.delete(`/api/ai/engines/${engineId}`, {
        headers: { Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}` },
      });
    }
    if (promptId) {
      await page.request.delete(`/api/ai/prompts/${promptId}`, {
        headers: { Authorization: `Bearer ${await page.evaluate(() => localStorage.getItem("token"))}` },
      });
    }
    await page.close();
  });

  // An engine or prompt name is user text and can be one long unbreakable word.
  // Both rows show it through EditableTitle, which truncates.
  test("a long engine or prompt name stays inside its row", async ({ page }) => {
    await login(page);
    const token = await page.evaluate(() => localStorage.getItem("token"));
    const auth = () => ({ Authorization: `Bearer ${token}` });
    const stamp = Date.now();
    let promptId: number | undefined;
    let engineId: number | undefined;

    try {
      const promptRes = await page.request.post(`/api/ai/prompts`, {
        headers: auth(),
        data: {
          name: `${UNBREAKABLE_NAME}Prompt${stamp}`,
          prompt_text: "Translate faithfully.",
          out_status: true, out_text: true, out_note: false, out_comments: false,
        },
      });
      expect(promptRes.ok(), `create prompt: ${promptRes.status()} ${await promptRes.text()}`).toBe(true);
      promptId = (await promptRes.json()).data.id;

      const vendors = (await (await page.request.get(`/api/ai/vendors`, { headers: auth() })).json()).data as
        Array<{ provider: string; tokens: Array<{ id: number }> }>;
      const tokenId = vendors.find((v) => v.provider === "openai")?.tokens[0]?.id;
      expect(tokenId, "seed should provide an OpenAI token").toBeGreaterThan(0);

      const engineRes = await page.request.post(`/api/ai/engines`, {
        headers: auth(),
        data: {
          name: `${UNBREAKABLE_NAME}Engine${stamp}`,
          rows: [{
            source_lang: "en", target_langs: ["fr"], system_prompt_id: promptId,
            bad_output_action: "reject",
            token_id: tokenId, model: "gpt-4o", settings: {},
          }],
        },
      });
      expect(engineRes.ok(), `create engine: ${engineRes.status()} ${await engineRes.text()}`).toBe(true);
      engineId = (await engineRes.json()).data.id;

      await page.goto("/ai/prompts");
      await expectNoOverflow(page.getByTestId(`ai-prompt-${promptId}`));
      await page.goto("/ai/engines");
      await expectNoOverflow(page.getByTestId(`ai-engine-${engineId}`));
    } finally {
      if (engineId) await page.request.delete(`/api/ai/engines/${engineId}`, { headers: auth() });
      if (promptId) await page.request.delete(`/api/ai/prompts/${promptId}`, { headers: auth() });
    }
  });

  test("create a prompt in the UI, then wire an engine + workflow step", async ({ page }) => {
    // Ten sequential steps (UI form + five API round trips + two navigations);
    // 30s covers them under 8 parallel workers. Every step asserts its own
    // response, so a real hang still fails, just later.
    test.setTimeout(30_000);

    // 1. Create a system prompt through the UI (name + text + the 'status'
    // output mode). Provider/model live on model presets, not prompts.
    // Prompts sit on their own tab of the AI page.
    await page.goto("/ai/prompts");
    await expect(page.getByRole("heading", { name: /^AI$/ })).toBeVisible();
    // The editor is its own page: the button navigates to /ai/prompts/new.
    await page.getByTestId("ai-prompt-add").click();
    await expect(page).toHaveURL(/\/ai\/prompts\/new$/);

    const form = page.getByTestId("ai-prompt-form");
    await expect(form.getByTestId("ai-prompt-name")).toHaveCount(0);
    await page.getByTestId("ai-prompt-type-option-qe").click();
    await form.getByTestId("ai-prompt-name").fill(promptName);
    // The instructions field is a TipTap surface, not a textarea — it draws the
    // <source_lang> / <target_lang> tokens as chips inside the text. Playwright
    // fills a contenteditable the same way.
    await form.getByTestId("ai-prompt-instructions").fill("Rate this translation.");
    // Await the create POST before asserting on the list: the form has no
    // optimistic update, so the card only appears after POST → invalidate →
    // refetch → render. Asserting on a 5s budget races all three under load
    // ("assertion after a mutation races the invalidation refetch").
    const [createPromptRes] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/api/ai/prompts") && r.request().method() === "POST" && r.ok(),
        { timeout: 30_000 },
      ),
      form.getByTestId("ai-prompt-submit").click(),
    ]);
    // The id for the engine reference below, and for afterEach cleanup.
    promptId = (await createPromptRes.json()).data.id;
    expect(promptId).toBeGreaterThan(0);

    // Card renders + a save toast fires; assert the persisted card is visible.
    await expect(page.getByText(promptName)).toBeVisible({ timeout: 15_000 });

    // 2. Persistence: reload and the prompt is still there (proves POST + GET).
    await page.goto("/ai/prompts");
    await expect(page.getByText(promptName)).toBeVisible({ timeout: 5000 });

    // 3b. The vendor token an LLM engine row runs with. The row carries
    // {token_id, model, settings} directly.
    const vendorsRes = await page.request.get(`/api/ai/vendors`, { headers: auth() });
    expect(vendorsRes.ok()).toBe(true);
    const vendors = (await vendorsRes.json()).data as Array<{ provider: string; tokens: Array<{ id: number }> }>;
    const openaiTokenId = vendors.find((v) => v.provider === "openai")?.tokens[0]?.id;
    expect(openaiTokenId, "seed should provide an OpenAI token").toBeGreaterThan(0);

    // 4. Create an engine with a row routing en→fr through the system prompt,
    // its own token + model, segment prompt, and a bad-output action.
    const engRes = await page.request.post(`/api/ai/engines`, {
      headers: auth(),
      data: {
        name: engineName,
        rows: [{
          source_lang: "en",
          target_langs: ["fr"],
          system_prompt_id: promptId,
          token_id: openaiTokenId,
          model: "gpt-4o",
          settings: {},
          bad_output_action: "reject",
        }],
      },
    });
    expect(engRes.ok(), `create engine: ${engRes.status()} ${await engRes.text()}`).toBe(true);
    engineId = (await engRes.json()).data.id;

    // The engine list renders on the AI engines tab (proves GET + enriched row).
    await page.goto("/ai/engines");
    await expect(page.getByText(engineName)).toBeVisible({ timeout: 5000 });

    // 4b. A router that defaults to the engine — what a step points at.
    const routerRes = await page.request.post(`/api/ai/routers`, {
      headers: auth(),
      data: { name: `E2E router ${suffix}`, default_engine_id: engineId, rules: [] },
    });
    expect(routerRes.ok(), `create router: ${routerRes.status()} ${await routerRes.text()}`).toBe(true);
    routerId = (await routerRes.json()).data.id;

    // 5. Create a project and attach the router to a workflow step. The step
    // save checks the router is this tenant's, so a valid router attaches
    // successfully and persists.
    const projRes = await page.request.post(`/api/projects`, {
      headers: auth(),
      data: { name: projectName, source_language: "en", target_languages: ["es"] },
    });
    expect(projRes.ok(), `create project: ${projRes.status()}`).toBe(true);
    projectId = (await projRes.json()).data.id;

    // A hybrid library step carries the engine.
    await setProjectWorkflowStepsViaAPI(page, projectId, [
      { name: "translation", role_required: "linguist" },
      { name: `AI review ${engineId}`, role_required: "project_manager", ai_router_id: routerId },
    ]);

    // 6. The step persisted with the router attached (proves the FK wiring).
    const settings = await getProjectSettingsViaAPI(page, projectId);
    const steps = settings.data.workflow_steps as Array<{ name: string; ai_router_id: number | null }>;
    expect(steps.find((s) => s.name === `AI review ${engineId}`)?.ai_router_id, "router must persist on the step").toBe(routerId);
  });
});
