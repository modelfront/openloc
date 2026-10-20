import { test, expect, type Locator, type Page } from "@playwright/test";
import path from "path";
import { authHeaders, getSeedProjectId, openFirstJobInFile, getSeedTMId } from "../tests/helpers";

// Documentation screenshots (make docs-screenshots). Each `test` writes one PNG
// that a help page embeds as `assets/<name>.png`. Names are the contract with
// internal/productdocs/content — a page's `screenshots:` list must match.
//
// Runs against the seeded database plus the docs dataset (cmd/seed-docs), so
// every shot shows the same realistic customers, projects and memories.

// Names come from cmd/seed-docs.
const BOOKING_PROJECT = "Nordwind — Summer 2026 booking flow";
const BOOKING_FILE = "booking-flow-strings.json";
const IFU_PROJECT = "Helix — HX-200 instructions for use, rev. C";
const IFU_FILE = "HX-200-IFU-chapter-3.docx";
const NORDWIND_TM = "Nordwind — Marketing (EN→DE/FR)";
const AI_ENGINE = "Nordwind — brand review";
const AI_PROMPT = "Nordwind brand reviewer";

const OUT = path.resolve(__dirname, "..", "..", "internal", "productdocs", "content", "assets");

async function settle(page: Page) {
  // Dev-only chrome that must not appear in product screenshots.
  await page.addStyleTag({
    content: `
      [data-testid="role-switcher"], [data-role-switcher] { display: none !important; }
      [data-testid="chat-launcher"] { display: none !important; }
      * { caret-color: transparent !important; }
    `,
  });
  await page.evaluate(() => document.fonts.ready);
  // Let the last layout/transition frames land.
  await page.waitForTimeout(400);
}

async function shoot(page: Page, name: string) {
  await settle(page);
  await page.screenshot({ path: path.join(OUT, `${name}.png`), fullPage: false, animations: "disabled" });
}

// Breathing room around a cropped shot. An exact bounding box puts text hard
// against the image edge, which reads as a rendering fault in the help page.
const CROP_PAD = 14;

type Box = { x: number; y: number; width: number; height: number };

// Pads a box and clamps it to the document, in PAGE coordinates (not viewport):
// `fullPage` screenshots clip against the document, so an element taller than
// the window is captured whole instead of cut off at the fold.
async function paddedClip(page: Page, box: Box, padBottom = CROP_PAD): Promise<Box> {
  const doc = await page.evaluate(() => ({
    w: document.documentElement.scrollWidth,
    h: document.documentElement.scrollHeight,
  }));
  const x = Math.max(0, box.x - CROP_PAD);
  const y = Math.max(0, box.y - CROP_PAD);
  return {
    x,
    y,
    width: Math.min(doc.w - x, box.width + (box.x - x) + CROP_PAD),
    height: Math.min(doc.h - y, box.height + (box.y - y) + padBottom),
  };
}

/** The element's rectangle in page coordinates, scroll included. */
async function pageBox(el: Locator, name: string): Promise<Box> {
  const box = await el.evaluate(e => {
    const r = e.getBoundingClientRect();
    return { x: r.x + window.scrollX, y: r.y + window.scrollY, width: r.width, height: r.height };
  });
  if (!box || box.width === 0) throw new Error(`${name}: element has no size`);
  return box;
}

// Crop to one element when the page is about that part (a card, the sidebar):
// a full-page shot buries the subject in unrelated chrome.
// `padBottom` exists for an element that sits flush against something dark —
// the editor sidebar ends at the leverage status bar, and 14px of padding there
// catches a black strip that reads as a rendering fault in the help page.
async function shootElement(page: Page, selector: string, name: string, padBottom?: number) {
  await settle(page);
  const el = page.locator(selector).first();
  await expect(el).toBeVisible();
  await el.scrollIntoViewIfNeeded();
  const clip = await paddedClip(page, await pageBox(el, name), padBottom);
  await page.screenshot({ path: path.join(OUT, `${name}.png`), fullPage: true, clip, animations: "disabled" });
}

// Crop the first `count` rows of the segment grid. The rows are not wrapped in
// an element of their own, so the box is measured from the live layout rather
// than written down — a column resize moves the crop, it does not break it.
async function shootRows(page: Page, rows: Locator, count: number, name: string) {
  await settle(page);
  const first = await pageBox(rows.nth(0), name);
  const last = await pageBox(rows.nth(count - 1), name);
  const clip = await paddedClip(page, {
    x: first.x, y: first.y, width: first.width, height: last.y + last.height - first.y,
  });
  await page.screenshot({ path: path.join(OUT, `${name}.png`), fullPage: true, clip, animations: "disabled" });
}

// The editor's Resources panel opens at 200px, which squeezes the TM, terminology
// and history panels the guides describe. The shots open it wider through the
// width the panel persists for its own resize handle.
const DOCS_SIDEBAR_WIDTH = 320;

test.describe("documentation screenshots", () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript((width) => {
      localStorage.setItem("cat-sidebar-width", String(width));
    }, DOCS_SIDEBAR_WIDTH);
  });

  test("projects-list", async ({ page }) => {
    await page.goto("/projects");
    await expect(page.getByRole("heading", { name: /projects/i })).toBeVisible();
    await expect(page.getByRole("link", { name: /Nordwind — Summer 2026/ })).toBeVisible();
    await shoot(page, "projects-list");
  });

  test("project-create", async ({ page }) => {
    await page.goto("/projects/new");
    await expect(page.getByRole("heading", { name: /new project/i })).toBeVisible();
    // The form only shows after a Start from choice.
    await page.getByRole("button", { name: "From scratch" }).click();
    await expect(page.getByPlaceholder(/project name/i)).toBeVisible();
    await shoot(page, "project-create");
  });

  test("project-create-template-picker", async ({ page }) => {
    await page.goto("/projects/new");
    await expect(page.getByRole("heading", { name: /new project/i })).toBeVisible();
    await page.getByRole("button", { name: "From template" }).click();
    const dialog = page.getByTestId("template-picker-dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("button", { name: /Nordwind|Default Template/ }).first()).toBeVisible();
    await shoot(page, "project-create-template-picker");
  });

  test("project tabs", async ({ page }) => {
    test.setTimeout(90_000);
    await page.goto("/projects");
    const id = await getSeedProjectId(page, BOOKING_PROJECT);
    await page.goto(`/projects/${id}/files`);
    await expect(page.getByText(BOOKING_FILE)).toBeVisible();
    await shoot(page, "project-files");

    // Every shot below waits for the data it pictures, never for a timer: a
    // slow query must fail the run, not ship a spinner into the help pages.
    //
    // The analysis shot shows one analysis opened, with its breakdown tables:
    // run one over the project's jobs, wait for the worker, then open its row.
    // It is deleted again once shot.
    const headers = await authHeaders(page);
    const jobsBody = await (await page.request.get(`/api/projects/${id}/jobs`, { headers })).json();
    const jobIds = (jobsBody.data ?? jobsBody).map((j: { id: number }) => j.id);
    const created = await page.request.post(`/api/projects/${id}/analyses`, {
      headers,
      data: { name: "Quote — summer booking flow", job_ids: jobIds },
    });
    expect(created.ok(), `create analysis: ${created.status()}`).toBe(true);
    const analysisId: number = (await created.json()).data.id;
    try {
      await expect.poll(async () => {
        const r = await page.request.get(`/api/analyses/${analysisId}`, { headers });
        return (await r.json()).data?.analysis?.status;
      }, { timeout: 45_000 }).toBe("completed");

      const analysesLoaded = page.waitForResponse(r => r.url().includes("/analyses") && r.ok());
      await page.getByRole("link", { name: /analys/i }).click();
      await expect(page.getByRole("link", { name: /analys/i })).toHaveAttribute("aria-current", "page");
      await analysesLoaded;
      await page.getByTestId(`analysis-row-${analysisId}`).click();
      await expect(page.getByTestId(`analysis-detail-${analysisId}`).getByRole("table").first()).toBeVisible();
      await shootElement(page, '[data-guide="project_analyses.list"]', "project-analysis");
    } finally {
      await page.request.delete(`/api/analyses/${analysisId}`, { headers });
    }

    // Workflow steps live on their own panel: the picker for a library workflow plus its steps,
    // read-only. Cropped to the panel. Waits for the steps rather than the
    // picker — the picker renders before the settings query resolves, so a
    // shot taken on it would picture an empty card.
    await page.getByRole("link", { name: /^workflow$/i }).click();
    await expect(page.getByRole("link", { name: /^workflow$/i })).toHaveAttribute("aria-current", "page");
    await expect(page.getByTestId("workflow-step-badge-0")).toBeVisible();
    await shootElement(page, '[data-testid="project-panel"]', "workflow-steps");

    await page.getByRole("link", { name: /settings/i }).click();
    await expect(page.getByRole("link", { name: /settings/i })).toHaveAttribute("aria-current", "page");
    await expect(page.locator('[data-testid="tm-section"]')).toBeVisible();
    await shoot(page, "project-settings");
    // Cropped: the translation-memory card the guides talk about on its own.
    await shootElement(page, '[data-testid="tm-section"]', "tm-assignments");
  });

  test("cat-editor", async ({ page }) => {
    await page.goto("/projects");
    const id = await getSeedProjectId(page, BOOKING_PROJECT);
    await page.goto(`/projects/${id}/files`);
    await openFirstJobInFile(page, BOOKING_FILE);
    await expect(page.locator("[data-segment-idx]").first()).toBeVisible({ timeout: 20_000 });
    await shoot(page, "cat-editor");
    // The "Free cancellation until 24 hours before check-in." segment is the
    // one that fills every sidebar panel at once: the fuzzy memory match, the
    // MT suggestion, two glossary terms, and the number findings the seeded QA
    // run stored (its target kept the match's "48"). The MT suggestion is a
    // debounced live call to the engine, so it must be waited for by name — it
    // renders nothing at all while in flight, and a shot taken before it lands
    // ships a blank panel into the machine-translation guide.
    const sidebar = page.getByTestId("editor-sidebar");
    await page.locator("[data-segment-idx]").nth(6).click();
    await expect(sidebar.getByText("Free cancellation until 48 hours before check-in.")).toBeVisible();
    await expect(page.getByTestId("terminology-section").getByText("free cancellation")).toBeVisible();
    await expect(sidebar.getByTestId("qa-finding-row").first()).toBeVisible();
    await expect(sidebar.getByTestId("mt-suggestion")).toBeVisible();
    // History opens with the sidebar, so the shot needs no interaction — just
    // wait for the rows. This segment's memory match is its one event.
    const history = page.getByTestId("segment-history-section");
    await expect(history.getByTestId("segment-history-row")).toHaveCount(1);
    await shootElement(page, '[data-testid="editor-sidebar"]', "editor-sidebar", 0);

    // Terminology in the editor: "Find your next getaway." hits the glossary
    // term getaway → Kurzurlaub, so the source shows the highlight and the
    // sidebar's Terminology section lists the term. Full shot: both matter.
    await page.locator("[data-segment-idx]").nth(0).click();
    await expect(page.getByTestId("terminology-section").getByText("Kurzurlaub")).toBeVisible();
    await shoot(page, "editor-terminology");

    // The History panel on its own, on "We sent the details to {email}.": an
    // MT row and the approved APE rewrite above it, marked against it — words
    // removed struck through in red, words added in yellow. Addressed by index,
    // not nth(): the grid is virtualized, so nth() counts rendered rows. Opened
    // from its source text, since a click on the row itself does not open it
    // here. The previous segment also has two rows, so the wait is on the
    // marks, which only this segment's rows carry.
    await page.locator('[data-segment-idx="15"]').getByText("We sent the details to {email}.").click();
    await expect(history.getByTestId("segment-history-row")).toHaveCount(2);
    await expect(history.locator('[title="Removed"]').first()).toBeVisible();
    await expect(history.locator('[title="Added"]').first()).toBeVisible();
    await shootElement(page, '[data-testid="segment-history-section"]', "segment-history", 0);
  });

  test("tm-list", async ({ page }) => {
    await page.goto("/memories");
    await expect(page.getByRole("heading", { name: /translation memories/i })).toBeVisible();
    await expect(page.getByText(NORDWIND_TM).first()).toBeVisible();
    await shoot(page, "tm-list");
  });

  test("tm-browser", async ({ page }) => {
    await page.goto("/memories");
    const id = await getSeedTMId(page, NORDWIND_TM);
    await page.goto(`/memories/${id}`);
    await expect(page.getByRole("navigation", { name: "Breadcrumb" })).toContainText("Nordwind");
    await expect(page.getByText("Finden Sie Ihren nächsten Kurzurlaub.")).toBeVisible();
    await shoot(page, "tm-browser");
  });

  test("terminology-browser", async ({ page }) => {
    await page.goto("/terminology");
    await expect(page.getByRole("heading", { name: /terminology/i })).toBeVisible();
    await page.getByRole("link", { name: /Nordwind brand glossary/ }).first().click();
    await expect(page.getByRole("navigation", { name: "Breadcrumb" })).toContainText(/Nordwind brand glossary/);
    await expect(page.getByText("Nordwind Miles")).toBeVisible();
    await shoot(page, "terminology-browser");
  });

  test("ai-routers", async ({ page }) => {
    await page.goto("/ai");
    // The seeded router defaults to the base seed's "OpenAI review" engine;
    // the page's first tab is the routers list.
    await expect(page.getByText("Default QE+APE router")).toBeVisible();
    await shootElement(page, '[data-testid="ai-routers-section"]', "ai-routers");
  });

  test("mt-routers", async ({ page }) => {
    await page.goto("/mt");
    // The seeded router defaults to the base seed's LibreTranslate engine; the
    // page's first tab is the routers list.
    await expect(page.getByText("Default MT router")).toBeVisible();
    await shootElement(page, '[data-testid="mt-routers-section"]', "mt-routers");
  });

  test("ai-page", async ({ page }) => {
    await page.goto("/ai/engines");
    // The base seed's engines plus the customer's LLM reviewer from the docs
    // dataset. Waiting on
    // the second one means the list is never shot half-loaded.
    await expect(page.getByText(AI_ENGINE)).toBeVisible();
    await shootElement(page, '[data-testid="ai-engines-section"]', "ai-engines");

    // The output-mode chip on each system prompt row is what the guide
    // explains — so the Prompts tab gets its own shot.
    await page.goto("/ai/prompts");
    await expect(page.getByText(AI_PROMPT)).toBeVisible();
    await shoot(page, "system-prompts");
  });

  test("tasks", async ({ page }) => {
    await page.goto("/tasks");
    await expect(page.getByRole("heading", { name: /tasks/i })).toBeVisible();
    await expect(page.getByText(BOOKING_FILE).first()).toBeVisible();
    await shoot(page, "tasks");
  });

  // The statuses reference is entirely about the Status and Match columns, so
  // it needs the columns themselves, not another shot of the whole editor.
  test("segment badges and QA tooltip", async ({ page }) => {
    await page.goto("/projects");
    const id = await getSeedProjectId(page, BOOKING_PROJECT);
    await page.goto(`/projects/${id}/files`);
    await openFirstJobInFile(page, BOOKING_FILE);

    const rows = page.locator("[data-segment-idx]");
    await expect(rows.first()).toBeVisible({ timeout: 20_000 });
    // A badge only means something next to a different one, so wait for the
    // seeded run's QA dot: it is the last thing the grid paints, and it proves
    // the rows carry more than one status before the crop is taken.
    const dot = page.locator('[data-testid="qa-issue-badge"]').first();
    await expect(dot).toBeVisible();
    await shootRows(page, rows, 8, "segment-badges");

    // Same page, one interaction later: hovering the dot lists the segment's
    // findings (read-only — ignoring happens in the sidebar).
    await dot.hover();
    await expect(page.getByTestId("qa-issue-tooltip")).toBeVisible();
    await shootElement(page, '[data-slot="tooltip-content"]', "qa-finding-tooltip");
  });

  // Expanded, not collapsed: the open row shows the page AND the rules editor
  // in one shot.
  test("qa-profile-rules", async ({ page }) => {
    await page.goto("/quality");
    await expect(page.getByRole("heading", { name: /qa profiles/i })).toBeVisible();
    await page.getByText("Marketing QA").click();
    await expect(page.getByText("Empty target")).toBeVisible();
    await expect(page.getByText("Number consistency")).toBeVisible();
    await shoot(page, "qa-profile-rules");
  });

  test("workflow-step-dialog", async ({ page }) => {
    await page.goto("/workflows/steps");
    await page.getByTestId("new-step-definition").click();
    const dialog = page.getByTestId("step-definition-dialog");
    await expect(dialog).toBeVisible();
    // Hybrid is what reveals the AI router picker the guide describes. Nothing
    // is saved.
    await dialog.getByTestId("step-definition-name").fill("Review");
    await dialog.getByTestId("step-type-option-hybrid").click();
    await expect(dialog.getByTestId("step-definition-router")).toBeVisible();
    await shootElement(page, '[data-testid="step-definition-dialog"]', "workflow-step-dialog");
  });

  // Shot on the instructions-for-use file, whose German draft carries a
  // deliberate typo ("Vorbereitug", see cmd/seed-docs). No other shot pictures that
  // file, so the typo cannot leak into the rest of the help pages.
  test("spell-check-dialog", async ({ page }) => {
    await page.goto("/projects");
    const id = await getSeedProjectId(page, IFU_PROJECT);
    await page.goto(`/projects/${id}/files`);
    await openFirstJobInFile(page, IFU_FILE);
    await expect(page.locator("[data-segment-idx]").first()).toBeVisible({ timeout: 20_000 });

    await page.getByTestId("open-spellcheck").click();
    const dialog = page.getByTestId("spellcheck-dialog");
    await expect(dialog).toBeVisible();
    await dialog.getByTestId("spellcheck-start").click();
    // A real LanguageTool round-trip: wait for the review phase to name an
    // error, never for a timer, so a dead checker fails the run instead of
    // shipping the empty scope screen into the guide.
    await expect(dialog.getByText(/Error \d+ of \d+/)).toBeVisible({ timeout: 30_000 });
    await expect(dialog.getByTestId("spellcheck-change")).toBeVisible();
    await shootElement(page, '[data-testid="spellcheck-dialog"]', "spell-check-dialog");
  });
});
