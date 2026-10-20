import { test, expect } from "@playwright/test";
import { login } from "./helpers";

/**
 * Built-in AI chat — UI behavior against a route-mocked backend.
 *
 * The send path is mocked at the network boundary (page.route) with a
 * byte-realistic SSE body: the seed connects a FAKE Claude key, so an unmocked
 * send would call the real Anthropic API from CI — nondeterministic and slow.
 * What stays real: the panel, the fetch+ReadableStream parser, markdown
 * rendering, tool chips. The loop/tools chain is covered by Go integration
 * tests against the real MCP server.
 */

const SSE_BODY = [
  'event: tool_start\ndata: {"type":"tool_start","tool":"openloc_list_jobs"}',
  'event: tool_end\ndata: {"type":"tool_end","tool":"openloc_list_jobs","ok":true}',
  'event: text\ndata: {"type":"text","text":"You have **2 jobs** in progress."}',
  'event: done\ndata: {"conversation_id":123,"input_tokens":50,"output_tokens":10}',
  "",
].join("\n\n");

test.describe("Built-in chat", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("launcher opens the panel with the empty state; close restores launcher", async ({ page }) => {
    await expect(page.getByTestId("chat-launcher")).toBeVisible();
    await page.getByTestId("chat-launcher").click();

    await expect(page.getByTestId("chat-panel")).toBeVisible();
    await expect(page.getByText(/Ask about your projects/)).toBeVisible();
    // Mode badge defaults to read-only; switching via the picker
    // flips it to write and back. Scoped to the badge testid — the picker
    // button also says "Read-only", which trips strict mode on a bare text
    // locator.
    await expect(page.getByTestId("chat-mode-badge")).toHaveText("read-only");
    await page.getByTestId("chat-mode-picker").click();
    await page.getByTestId("chat-mode-write").click();
    await expect(page.getByTestId("chat-mode-badge")).toHaveText("write");
    await page.getByTestId("chat-mode-picker").click();
    await page.getByTestId("chat-mode-read").click();
    await expect(page.getByTestId("chat-mode-badge")).toHaveText("read-only");

    await page.getByTestId("chat-close").click();
    await expect(page.getByTestId("chat-panel")).not.toBeVisible();
    await expect(page.getByTestId("chat-launcher")).toBeVisible();
  });

  test("send streams an answer: tool chip + markdown text render", async ({ page }) => {
    await page.route("**/api/chat", (route) =>
      route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: SSE_BODY,
      }),
    );

    await page.getByTestId("chat-launcher").click();
    await page.getByTestId("chat-input").fill("how many jobs are running?");
    await page.getByTestId("chat-send").click();

    // User bubble immediately; assistant content after the stream parses.
    await expect(page.getByText("how many jobs are running?")).toBeVisible();
    await expect(page.getByTestId("chat-tool-chip")).toContainText("list_jobs");
    // **2 jobs** must have become <strong> — markdown rendered, not echoed.
    await expect(page.getByTestId("chat-messages").locator("strong")).toHaveText("2 jobs");

    // Minimize keeps the conversation: panel hides, launcher returns, and
    // reopening shows the same exchange (nothing unmounted, nothing refetched).
    await page.getByTestId("chat-minimize").click();
    await expect(page.getByTestId("chat-panel")).toBeHidden();
    await page.getByTestId("chat-launcher").click();
    await expect(page.getByTestId("chat-messages").locator("strong")).toHaveText("2 jobs");
  });

  test("client_action navigate moves the app to the server-built path", async ({ page }) => {
    const NAV_SSE = [
      'event: tool_start\ndata: {"type":"tool_start","tool":"navigate_to"}',
      'event: client_action\ndata: {"type":"client_action","action":"navigate","path":"/tasks","tool":"navigate_to"}',
      'event: tool_end\ndata: {"type":"tool_end","tool":"navigate_to","ok":true}',
      'event: text\ndata: {"type":"text","text":"I opened your Tasks page."}',
      'event: done\ndata: {"conversation_id":5}',
      "",
    ].join("\n\n");
    await page.route("**/api/chat", (route) =>
      route.fulfill({ status: 200, contentType: "text/event-stream", body: NAV_SSE }),
    );

    await page.getByTestId("chat-launcher").click();
    await page.getByTestId("chat-input").fill("take me to my tasks");
    await page.getByTestId("chat-send").click();

    // Real router navigation, panel still open on top of the new page.
    await expect(page).toHaveURL(/\/tasks$/);
    await expect(page.getByText("I opened your Tasks page.")).toBeVisible();
    await expect(page.getByTestId("chat-panel")).toBeVisible();
  });

  test("render_chart client_action draws a real chart", async ({ page }) => {
    const CHART = {
      type: "client_action",
      action: "render_chart",
      tool: "render_chart",
      chart: {
        title: "MT usage by engine",
        type: "bar",
        x_key: "mt_engine",
        series: ["words"],
        data: [
          { mt_engine: "DeepL", words: 120 },
          { mt_engine: "Claude", words: 80 },
        ],
      },
    };
    const CHART_SSE = [
      'event: tool_start\ndata: {"type":"tool_start","tool":"openloc_query_metrics"}',
      'event: tool_end\ndata: {"type":"tool_end","tool":"openloc_query_metrics","ok":true}',
      'event: tool_start\ndata: {"type":"tool_start","tool":"render_chart"}',
      `event: client_action\ndata: ${JSON.stringify(CHART)}`,
      'event: tool_end\ndata: {"type":"tool_end","tool":"render_chart","ok":true}',
      'event: text\ndata: {"type":"text","text":"DeepL leads."}',
      'event: done\ndata: {"conversation_id":7}',
      "",
    ].join("\n\n");
    await page.route("**/api/chat", (route) =>
      route.fulfill({ status: 200, contentType: "text/event-stream", body: CHART_SSE }),
    );

    await page.getByTestId("chat-launcher").click();
    await page.getByTestId("chat-input").fill("chart MT usage by engine");
    await page.getByTestId("chat-send").click();

    await expect(page.getByText("DeepL leads.")).toBeVisible();
    // A real browser gives ResponsiveContainer a size, so recharts must have
    // drawn actual SVG bars — jsdom can only pin the mount, this pins pixels.
    const chart = page.getByTestId("chat-chart");
    await expect(chart).toBeVisible();
    await expect(chart.getByText("MT usage by engine")).toBeVisible();
    await expect(chart.locator("svg .recharts-bar-rectangle")).toHaveCount(2);
  });

  test("412 no-key response shows the settings link, not an error blob", async ({ page }) => {
    await page.route("**/api/chat", (route) =>
      route.fulfill({
        status: 412,
        contentType: "application/json",
        // House envelope, matching httputil.ErrorCode — keep this byte-realistic.
        body: JSON.stringify({ error: { code: "no_claude_key", message: "No Claude API key is configured — connect one in AI settings → Providers." } }),
      }),
    );

    await page.getByTestId("chat-launcher").click();
    await page.getByTestId("chat-input").fill("hello");
    await page.getByTestId("chat-send").click();

    await expect(page.getByText("Connect one in AI settings → Providers.")).toBeVisible();
    await expect(page.getByTestId("chat-input")).toBeDisabled();
  });

  // --- UI guide and confirm cards ---

  const sse = (...events: string[]) => [...events, 'event: done\ndata: {"conversation_id":7}', ""].join("\n\n");
  const action = (payload: Record<string, unknown>) =>
    `event: client_action\ndata: ${JSON.stringify({ type: "client_action", ...payload })}`;

  async function ask(page: import("@playwright/test").Page, body: string, question: string) {
    await page.route("**/api/chat", (route) =>
      route.fulfill({ status: 200, contentType: "text/event-stream", body }),
    );
    await page.getByTestId("chat-launcher").click();
    await page.getByTestId("chat-input").fill(question);
    await page.getByTestId("chat-send").click();
  }

  test("highlight spotlights the real element with the caption", async ({ page }) => {
    await ask(
      page,
      sse(action({ action: "highlight", anchor: "projects.new", caption: "Start a new project here.", path: "/projects" })),
      "where do I create a project?",
    );

    const popover = page.locator(".driver-popover.openloc-guide");
    await expect(popover).toContainText("Start a new project here.");
    await expect(page.locator('[data-guide="projects.new"]')).toHaveClass(/driver-active-element/);

    await popover.locator(".driver-popover-close-btn").click();
    await expect(popover).toHaveCount(0);
  });

  test("a tour moves across pages and minimizes the panel", async ({ page }) => {
    await ask(
      page,
      sse(action({
        action: "tour",
        steps: [
          { anchor: "nav.projects", title: "Projects", body: "All your work lives here." },
          { anchor: "tms.new", title: "Memories", body: "Create a translation memory.", path: "/memories" },
        ],
      })),
      "show me around",
    );

    const popover = page.locator(".driver-popover.openloc-guide");
    await expect(popover).toContainText("All your work lives here.");
    await expect(popover).toContainText("1 of 2");
    await expect(page.getByTestId("chat-panel")).toBeHidden();

    await popover.getByRole("button", { name: "Next" }).click();
    await expect(page).toHaveURL(/\/memories$/);
    await expect(popover).toContainText("Create a translation memory.");
    await expect(page.locator('[data-guide="tms.new"]')).toHaveClass(/driver-active-element/);

    await popover.getByRole("button", { name: "Done" }).click();
    await expect(popover).toHaveCount(0);
  });

  test("a guarded tool shows a confirm card; only the user's click posts the token", async ({ page }) => {
    const confirm = {
      tool: "openloc_delete_custom_palette",
      args: { id: "ocean" },
      summary: 'Delete your custom palette "Ocean". The colours cannot be recovered.',
      token: "1790000000.test-token",
      expires_at: "2026-09-24T12:10:00Z",
    };
    let posted: Record<string, unknown> | null = null;
    await page.route("**/api/chat/confirm", async (route) => {
      posted = route.request().postDataJSON();
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ data: { ok: true, message: "done" } }) });
    });
    await ask(
      page,
      sse(
        `event: confirm_required\ndata: ${JSON.stringify({ type: "confirm_required", tool: confirm.tool, confirm })}`,
        'event: text\ndata: {"type":"text","text":"Confirm in the card to delete it."}',
      ),
      "delete my ocean palette",
    );

    const card = page.getByTestId("chat-confirm");
    await expect(card).toHaveAttribute("data-state", "pending");
    await expect(card).toContainText("Needs your confirmation");
    expect(posted).toBeNull();

    await page.getByTestId("chat-confirm-review").click();
    await page.getByRole("button", { name: "Delete" }).click();
    await expect(card).toHaveAttribute("data-state", "done");
    expect(posted).toEqual({ conversation_id: 7, tool: confirm.tool, args: { id: "ocean" }, confirm_token: confirm.token });
  });
});
