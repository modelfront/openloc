import { test, expect } from "@playwright/test";
import { authHeaders, login, segmentEditor, fillEditorExactly, uploadFileViaAPI, deleteFileViaAPI, createIsolatedCATJob, openJob, importTMXViaAPI, deleteTMViaAPI } from "./helpers";

// Clicks Run QA and returns the /run-qa response and the report the editor
// reads after it. The report is matched by start time: an older GET of the
// same URL (the page load's, or the refetch a confirm starts) can answer after
// the click with the counts from before the run.
async function runQAAndReadReport(page: import("@playwright/test").Page, button: import("@playwright/test").Locator) {
  let runAnswered = Number.POSITIVE_INFINITY;
  const [runResp, reportResp] = await Promise.all([
    page.waitForResponse(r => {
      if (!(r.url().includes("/run-qa") && r.request().method() === "POST")) return false;
      const t = r.request().timing();
      runAnswered = t.startTime + t.responseStart;
      return true;
    }, { timeout: 10000 }),
    page.waitForResponse(r => r.url().includes("/qa-report") && r.request().method() === "GET"
      && r.request().timing().startTime >= runAnswered, { timeout: 10000 }),
    button.click(),
  ]);
  return { runResp, reportResp };
}

test.describe("CAT Editor", () => {
  // Projects a test registers through `onCreated` are deleted here, with
  // headers read right after login and the `request` fixture: the hook has
  // its own time budget and reads nothing from the page. Best-effort, so a
  // failed cleanup never changes a test's result.
  let projectIds: number[] = [];
  let headers: Record<string, string> = {};

  test.beforeEach(async ({ page }) => {
    await login(page);
    headers = await authHeaders(page);
  });

  test.afterEach(async ({ request }) => {
    for (const id of projectIds) await request.delete(`/api/projects/${id}`, { headers }).catch(() => {});
    projectIds = [];
  });

  test("can open a job in the CAT editor", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();

    await openJob(page, { fileName: "demo.txt" });

    // Should see the CAT editor — source text from demo.txt should be visible
    await expect(page.getByText("We thank you for your patience.")).toBeVisible({ timeout: 10000 });
  });

  // On a slow connection the editor stays on one loading screen until its data
  // is in, instead of showing the header, toolbar and filters piece by piece.
  // One run per request the screen waits for, each held back on its own.
  const slowRequests: { name: string; path: (job: { jobId: number; fileId: number }) => string }[] = [
    { name: "translations", path: j => `/api/jobs/${j.jobId}/translations` },
    { name: "workflow steps", path: j => `/api/jobs/${j.jobId}/workflow/steps` },
    { name: "segments", path: j => `/api/files/${j.fileId}/segments` },
  ];
  for (const slow of slowRequests) test(`shows one loading screen until the ${slow.name} load`, async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      fileName: `slow-load-${Date.now()}.txt`,
      content: "slow load first line\n",
    });

    let release!: () => void;
    const held = new Promise<void>(r => { release = r; });
    await page.route(`**${slow.path(job)}`, async route => {
      await held;
      await route.continue();
    });

    try {
      const jobLoaded = page.waitForResponse(r => r.url().endsWith(`/api/jobs/${job.jobId}`) && r.ok());
      await page.goto(`/projects/${job.projectId}/jobs/${job.jobId}/edit`);
      await jobLoaded;

      await expect(page.getByText("Loading editor…")).toBeVisible();
      await expect(page.getByTestId("editor-download-btn")).toHaveCount(0);
      await expect(page.getByRole("navigation", { name: "Breadcrumb" })).toHaveCount(0);

      release();
      await expect(page.getByText("slow load first line").first()).toBeVisible();
      await expect(page.getByTestId("editor-download-btn")).toBeVisible();
      await expect(page.getByText("Loading editor…")).toHaveCount(0);
    } finally {
      release();
      await job.cleanup();
    }
  });

  // A long project name and a long file name must truncate, not run under the
  // chips and buttons that share the breadcrumb line. Layout only exists in a
  // real browser, hence E2E.
  test("long project and file names truncate in the breadcrumb and never overlap its buttons", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      fileName: `an_extremely_long_file_name_without_any_spaces_v12_final_FINAL_${Date.now()}.txt`,
      // Longer than the whole line on its own, with no spaces to wrap at: only
      // a project crumb that may shrink keeps this from overflowing.
      projectName: `${"A_very_long_project_name_".repeat(6)}${Date.now()}`,
      content: "first source line\n",
    });

    try {
      await job.navigateToEditor(/first source line/);
      const nav = page.getByRole("navigation", { name: "Breadcrumb" });

      const overflow = await nav.locator("ol").evaluate(el => el.scrollWidth - el.clientWidth);
      expect(overflow, "breadcrumb list must not spill past its nav").toBeLessThanOrEqual(1);

      const chips = (await page.getByTestId("cat-current-step").or(page.getByTestId("cat-target-language")).last().boundingBox())!;
      const download = (await page.getByTestId("editor-download-btn").boundingBox())!;
      expect(chips.x + chips.width, "chips must end before the Download button").toBeLessThanOrEqual(download.x);

      // The "Projects" crumb is fixed text and keeps its width; only the
      // user-named crumbs give way.
      const projects = (await nav.getByRole("link", { name: "Projects", exact: true }).boundingBox())!;
      expect(projects.width, '"Projects" crumb must not be squeezed').toBeGreaterThan(40);
    } finally {
      await job.cleanup();
    }
  });

  // Inactive rows cap at 10 lines without reserving them; activating a row
  // keeps its height and its text in place (the boxes are pulled into the cell
  // padding — index.css segment-source-box / segment-editor-frame); and source
  // and translation line up in both states. Layout only exists in a real
  // browser, hence E2E.
  test("rows cap at 10 lines, activating keeps height and text position, source and translation line up", async ({ page }) => {
    // One sentence (clauses joined by commas), so the line stays one segment.
    const longLine = "A long clause that keeps going to fill many lines of the source column, ".repeat(40).trim();
    const job = await createIsolatedCATJob(page, {
      fileName: `row-geometry-${Date.now()}.txt`,
      content: `short source line\n${longLine}\n`,
    });

    try {
      await job.navigateToEditor(/short source line/);
      const rows = page.locator("[data-translation-cell]").locator("..");
      const sourceCell = (i: number) => rows.nth(i).locator(":scope > div").nth(1);
      const targetCell = (i: number) => page.locator("[data-translation-cell]").nth(i);
      const lineHeight = 20; // text-sm
      // The clamped element sits inside the padded preview.
      const sourceClamp = (i: number) => sourceCell(i).locator(":scope > div > div").first();

      // A one-line row is one line tall — the clamp caps, it doesn't reserve.
      const shortPreview = (await sourceClamp(0).boundingBox())!;
      expect(shortPreview.height, "short inactive row must not reserve 10 lines").toBeLessThan(2 * lineHeight);
      // A long row stops at 10 lines.
      const longPreview = (await sourceClamp(1).boundingBox())!;
      expect(longPreview.height, "long inactive row must stop at 10 lines").toBeLessThanOrEqual(10 * lineHeight + 1);
      expect(longPreview.height, "long inactive row must show 10 lines").toBeGreaterThan(9 * lineHeight);

      // First visible text, relative to its cell: { x, y }.
      const textPos = (cell: import("@playwright/test").Locator) => cell.evaluate(el => {
        const w = document.createTreeWalker(el, NodeFilter.SHOW_TEXT, { acceptNode: n => (n.textContent?.trim() ? 1 : 3) });
        const r = document.createRange();
        r.selectNodeContents(w.nextNode()!);
        const t = r.getClientRects()[0], c = el.getBoundingClientRect();
        return { x: t.left - c.left, y: t.top - c.top };
      });
      const firstTextTop = async () => (await textPos(sourceCell(0))).y;

      // Inactive: the translation's text starts where the source's does.
      const src = await textPos(sourceCell(0)), tgt = await textPos(targetCell(0));
      expect(Math.abs(tgt.y - src.y), "inactive source and translation text must share a top").toBeLessThanOrEqual(0.5);
      expect(Math.abs(tgt.x - src.x), "inactive source and translation text must share an indent").toBeLessThanOrEqual(0.5);

      const before = { height: (await rows.nth(0).boundingBox())!.height, textTop: await firstTextTop() };

      await page.locator("[data-translation-cell]").first().click();
      await expect(segmentEditor(page).first()).toBeVisible({ timeout: 5000 });
      await expect(page.getByTestId("active-source-box")).toBeVisible();

      const after = { height: (await rows.nth(0).boundingBox())!.height, textTop: await firstTextTop() };
      expect(Math.abs(after.height - before.height), "activating a row must not change its height").toBeLessThanOrEqual(0.5);
      expect(Math.abs(after.textTop - before.textTop), "activating a row must not move its source text").toBeLessThanOrEqual(0.5);

      // Active: the dashed source box and the editor box sit at the same
      // place in their cells.
      const boxIn = async (box: import("@playwright/test").Locator, cell: import("@playwright/test").Locator) => {
        const b = (await box.boundingBox())!, c = (await cell.boundingBox())!;
        return { x: b.x - c.x, y: b.y - c.y };
      };
      const srcBox = await boxIn(page.getByTestId("active-source-box"), sourceCell(0));
      const editorBox = await boxIn(segmentEditor(page).first(), targetCell(0));
      expect(Math.abs(editorBox.y - srcBox.y), "source box and editor must share a top").toBeLessThanOrEqual(0.5);
      expect(Math.abs(editorBox.x - srcBox.x), "source box and editor must share an indent").toBeLessThanOrEqual(0.5);
    } finally {
      await job.cleanup();
    }
  });

  // The active row's two boxes share one frame: the editor's border is the
  // source box's dashed grey one, with no focus ring on top.
  for (const theme of ["light", "dark"] as const) {
    test(`the active editor's border matches the source box (${theme})`, async ({ page }) => {
      await page.addInitScript(t => localStorage.setItem("openloc-theme", t), theme);
      const job = await createIsolatedCATJob(page, {
        fileName: `editor-border-${theme}-${Date.now()}.txt`,
        content: "first source line\n",
      });
      try {
        await job.navigateToEditor(/first source line/);
        await expect(page.locator("html")).toHaveClass(theme === "dark" ? /\bdark\b/ : /^(?!.*\bdark\b)/);
        await page.locator("[data-translation-cell]").first().click();
        const editor = segmentEditor(page).first();
        await expect(editor).toBeFocused({ timeout: 5000 });

        const frame = (el: Element) => {
          const cs = getComputedStyle(el);
          return { style: cs.borderTopStyle, width: cs.borderTopWidth, color: cs.borderTopColor, shadow: cs.boxShadow };
        };
        const source = await page.getByTestId("active-source-box").evaluate(frame);
        expect(source.style).toBe("dashed");
        expect(await editor.evaluate(frame)).toEqual(source);
      } finally {
        await job.cleanup();
      }
    });
  }

  // The header's column borders show in dark mode too, and each border's
  // resize strip is centred on the line, not hanging off its left side.
  test("column borders show in dark mode, and each resize strip is centred on its line", async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("openloc-theme", "dark"));
    const job = await createIsolatedCATJob(page, {
      fileName: `column-borders-${Date.now()}.txt`,
      content: "first source line\nsecond source line\n",
    });
    projectIds.push(job.projectId);

    await job.navigateToEditor(/first source line/);
    await expect(page.locator("html")).toHaveClass(/\bdark\b/);

    const handles = page.locator('[data-testid^="segment-column-resize-"]');
    expect(await handles.count()).toBeGreaterThan(0);
    for (const handle of await handles.all()) {
      const cell = handle.locator("..");
      // WCAG contrast of the border against the header behind it, on the
      // colours the browser actually paints (a canvas resolves oklch too).
      const contrast = await cell.evaluate((el) => {
        const ctx = document.createElement("canvas").getContext("2d")!;
        const rgb = (css: string) => {
          ctx.clearRect(0, 0, 1, 1);
          ctx.fillStyle = css;
          ctx.fillRect(0, 0, 1, 1);
          return Array.from(ctx.getImageData(0, 0, 1, 1).data.slice(0, 3));
        };
        const lum = (c: number[]) => {
          const [r, g, b] = c.map((v) => {
            const s = v / 255;
            return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
          });
          return 0.2126 * r + 0.7152 * g + 0.0722 * b;
        };
        const line = lum(rgb(getComputedStyle(el).borderRightColor));
        const bg = lum(rgb(getComputedStyle(el.parentElement!).backgroundColor));
        return (Math.max(line, bg) + 0.05) / (Math.min(line, bg) + 0.05);
      });
      expect(contrast, "the column border is visible against the dark header").toBeGreaterThan(1.25);

      const line = await cell.evaluate((el) => {
        const box = el.getBoundingClientRect();
        return box.right - parseFloat(getComputedStyle(el).borderRightWidth) / 2;
      });
      const strip = (await handle.boundingBox())!;
      expect(Math.abs(strip.x + strip.width / 2 - line), "the resize strip is centred on the line").toBeLessThanOrEqual(1);
    }
  });

  // Dragging a header border moves width between the two neighbours, the body
  // rows follow the header, and double-click restores the defaults. Nothing is
  // saved (unit-tested in useColumnBorderDrag.test.tsx); this pins the wiring.
  test("dragging a column border resizes header and rows together; double-click resets", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      fileName: `column-resize-${Date.now()}.txt`,
      content: "first source line\nsecond source line\n",
    });

    try {
      await job.navigateToEditor(/first source line/);
      const handle = page.getByTestId("segment-column-resize-1"); // Source | Translation
      const headerSource = handle.locator("..");
      const headerTarget = headerSource.locator("xpath=following-sibling::div[1]");
      const bodySource = page.locator("[data-translation-cell]").first().locator("xpath=preceding-sibling::div[1]");
      const bodyTarget = page.locator("[data-translation-cell]").first();
      const width = async (l: import("@playwright/test").Locator) => (await l.boundingBox())!.width;

      const before = { source: await width(headerSource), target: await width(headerTarget) };
      expect(await width(bodySource)).toBeCloseTo(before.source, 0);

      const box = (await handle.boundingBox())!;
      await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
      await page.mouse.down();
      await page.mouse.move(box.x + box.width / 2 + 100, box.y + box.height / 2, { steps: 5 });
      await page.mouse.up();

      const after = { source: await width(headerSource), target: await width(headerTarget) };
      expect(after.source - before.source, "Source grows by the drag").toBeCloseTo(100, 0);
      expect(before.target - after.target, "Translation gives up the same width").toBeCloseTo(100, 0);
      expect(await width(bodySource), "body rows follow the header").toBeCloseTo(after.source, 0);
      expect(await width(bodyTarget), "body rows follow the header").toBeCloseTo(after.target, 0);

      await handle.dblclick();
      expect(await width(headerSource), "double-click restores the default").toBeCloseTo(before.source, 0);
      expect(await width(bodySource), "double-click restores the default").toBeCloseTo(before.source, 0);
    } finally {
      await job.cleanup();
    }
  });

  // #, Match and Status stop at the width their header label and content
  // need, so a drag never pushes their text into the next column. The limits
  // are unit-tested in segment-columns.test.ts; this checks them against the
  // real font.
  test("dragging the #, Match and Status borders never pushes header text into the next column", async ({ page }) => {
    const job = await createIsolatedCATJob(page, {
      fileName: `column-min-${Date.now()}.txt`,
      content: "first source line\nsecond source line\n",
    });

    try {
      await job.navigateToEditor(/first source line/);
      const header = page.getByTestId("segment-column-resize-0").locator("../..");
      const cell = (i: number) => header.locator(`xpath=./div[${i + 1}]`);
      const width = async (i: number) => (await cell(i).boundingBox())!.width;
      const drag = async (border: number, dx: number) => {
        const box = (await page.getByTestId(`segment-column-resize-${border}`).boundingBox())!;
        await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
        await page.mouse.down();
        await page.mouse.move(box.x + box.width / 2 + dx, box.y + box.height / 2, { steps: 5 });
        await page.mouse.up();
      };
      const defaults = { index: await width(0), match: await width(3), status: await width(4) };

      // Match and Status start at their minimums, so give Match room first.
      await drag(2, -100);
      expect(await width(3), "Match takes the width Translation gave up").toBeCloseTo(defaults.match + 100, 0);
      await drag(3, -300);
      expect(await width(3), "Match stops at its default").toBeCloseTo(defaults.match, 0);
      await drag(4, -300);
      expect(await width(4), "Status stops at its default").toBeCloseTo(defaults.status, 0);
      await drag(0, -100);
      expect(await width(0), "# stops at its default").toBeCloseTo(defaults.index, 0);

      // How far each header cell's text runs past the cell's right edge.
      const spill = await header.evaluate(h => Array.from(h.children).map(c => {
        let textRight = -Infinity;
        const walker = document.createTreeWalker(c, NodeFilter.SHOW_TEXT);
        for (let n = walker.nextNode(); n; n = walker.nextNode()) {
          if (!n.textContent?.trim()) continue;
          const range = document.createRange();
          range.selectNodeContents(n);
          textRight = Math.max(textRight, range.getBoundingClientRect().right);
        }
        return Math.max(0, Math.round(textRight - c.getBoundingClientRect().right));
      }));
      expect(spill).toEqual([0, 0, 0, 0, 0, 0]);
    } finally {
      await job.cleanup();
    }
  });

  test("CAT editor shows segment content", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });

    await expect(page.getByText(/patience|password|protect/i).first()).toBeVisible({
      timeout: 10000,
    });
  });

  test("CAT editor toolbar has all buttons", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });

    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    await expect(page.getByRole("button", { name: /concordance/i })).toBeVisible();
    await expect(page.getByRole("button", { name: /find and replace/i })).toBeVisible();
    // Export + Bilingual XLIFF collapsed into one Download menu.
    await expect(page.getByTestId("editor-download-btn")).toBeVisible();
    // By testid: the label flips between "Run QA" and "QA (n)" with the job's
    // open findings, so a name matcher would assert the seed's QA state.
    await expect(page.getByTestId("run-qa-btn")).toBeVisible();

    // Hidden characters toggle (¶ button)
    const pilcrowBtn = page.getByRole("button", { name: "¶" });
    await expect(pilcrowBtn).toBeVisible();
  });

  test("hidden characters toggle shows whitespace markers", async ({ page }) => {
    // Clear localStorage to ensure clean state
    await page.evaluate(() => localStorage.removeItem("cat-show-whitespace"));

    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });

    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    // Whitespace dot spans should NOT be present initially
    const wsDots = page.locator("span.ws-dot");
    await expect(wsDots.first()).not.toBeAttached({ timeout: 2000 });

    const pilcrowBtn = page.getByRole("button", { name: "¶" });
    await pilcrowBtn.click();

    // Should show ws-dot spans for spaces in source text
    await expect(wsDots.first()).toBeAttached({ timeout: 3000 });

    // Click a segment to edit — dots should also appear in the editor
    await page.getByText(/patience/i).first().click();
    const editorDots = page.locator('[data-testid="segment-editor"] .ws-dot');
    await expect(editorDots.first()).toBeAttached({ timeout: 3000 });

    await pilcrowBtn.click();

    // Whitespace dots should disappear from both source and editor
    await expect(wsDots.first()).not.toBeAttached({ timeout: 3000 });
    await expect(editorDots.first()).not.toBeAttached({ timeout: 3000 });
  });

  test("segments with newlines render as multiline in editor", async ({ page }) => {
    test.setTimeout(15_000);
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    // Scroll the segment list to bottom to find the multiline segment
    // Use the inner scroll container (not <main> which also has overflow-y-auto)
    const container = page.locator('.overflow-y-auto.overflow-x-hidden').first();
    for (let i = 0; i < 10; i++) {
      await container.evaluate(el => { el.scrollTop = el.scrollHeight });
      await page.waitForTimeout(150);
    }

    // The multiline segment source text should be visible
    const multilineSource = page.getByText(/Please review/).first();
    await expect(multilineSource).toBeVisible({ timeout: 5000 });
    await multilineSource.click();
    await page.waitForTimeout(300);

    // The editor should render multiline (height > single line)
    const editor = page.locator('[data-testid="segment-editor"] .ProseMirror');
    await expect(editor).toBeVisible({ timeout: 3000 });
    const height = await editor.evaluate(el => el.getBoundingClientRect().height);
    expect(height).toBeGreaterThan(40);
  });

  test("lock toggle visible when segment is active", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    await page.locator("[data-translation-cell]").first().click();
    await expect(page.locator('[data-testid="segment-editor"]').first()).toBeVisible({ timeout: 5000 });

    // Lock toggle should be visible (either "Lock" or "Unlock")
    const lockBtn = page.getByText(/🔒 Unlock|🔓 Lock/).first();
    await expect(lockBtn).toBeVisible({ timeout: 2000 });
  });

  test("no console errors on page load", async ({ page }) => {
    const errors: string[] = [];
    page.on("pageerror", (err) => errors.push(err.message));

    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });

    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    // Wait a bit for any deferred errors
    await page.waitForTimeout(2000);

    expect(errors, `Console errors found: ${errors.join(", ")}`).toHaveLength(0);
  });

  test("Tab advances to next segment", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    await page.locator("[data-translation-cell]").first().click();
    await expect(segmentEditor(page).first()).toBeVisible({ timeout: 5000 });
    const firstText = await segmentEditor(page).first().textContent();

    // Press Tab → should move to next segment
    await page.keyboard.press("Tab");
    await expect(segmentEditor(page).first()).toBeVisible({ timeout: 3000 });
    const secondText = await segmentEditor(page).first().textContent();
    expect(secondText).not.toBe(firstText);
  });

  test("Escape deactivates the segment", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    await page.locator("[data-translation-cell]").first().click();
    await expect(segmentEditor(page).first()).toBeVisible({ timeout: 5000 });

    // Press Escape → editor should disappear
    await page.keyboard.press("Escape");
    await expect(segmentEditor(page)).not.toBeVisible({ timeout: 3000 });
  });

  test("Cmd+Shift+S copies source to target", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    // Activate first segment (source: "We thank you for your patience.")
    await page.locator("[data-translation-cell]").first().click();
    await expect(segmentEditor(page).first()).toBeVisible({ timeout: 5000 });

    // Press Cmd+Shift+S → editor should contain the source text
    await page.keyboard.press("ControlOrMeta+Shift+s");
    const text = await segmentEditor(page).first().textContent();
    expect(text).toContain("We thank you for your patience");
  });

  test("formatting toolbar shows B/I/X²/X₂ when segment is active", async ({
    page,
  }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });

    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    await page.locator("[data-translation-cell]").first().click();
    await expect(segmentEditor(page).first()).toBeVisible({ timeout: 5000 });

    // Formatting toolbar should be visible with B, I, X², X₂
    await expect(page.getByTitle("Bold (Ctrl+B)")).toBeVisible();
    await expect(page.getByTitle("Italic (Ctrl+I)")).toBeVisible();
    await expect(page.getByTitle("Superscript")).toBeVisible();
    await expect(page.getByTitle("Subscript")).toBeVisible();
  });

  test("TM/TB sidebar shows matches when segment is active", async ({
    page,
  }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt", lang: "de" });

    await expect(
      page.getByText(/reset your password/i).first()
    ).toBeVisible({ timeout: 10000 });

    // Click a segment with a known TM match
    await page.getByText(/Setzen Sie Ihr Passwort/i).first().click();
    await expect(segmentEditor(page).first()).toBeVisible({
      timeout: 5000,
    });

    // Scoped to the sidebar: "101%" and "Passwort" also appear in the grid.
    const sidebar = page.getByTestId("editor-sidebar");
    await expect(sidebar).toBeVisible({ timeout: 5000 });

    // The seed makes this segment a genuine in-context match, so the card
    // reads 101%, as its Match column does.
    await expect(
      sidebar.getByTestId("tm-match-card").filter({ hasText: "101%" }).first(),
    ).toBeVisible({ timeout: 5000 });

    await expect(sidebar.getByTestId("terminology-section")).toContainText("Passwort", {
      timeout: 5000,
    });
  });

  test("fuzzy TM hit highlights its difference from the segment source, and hovering re-targets it", async ({
    page,
  }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt", lang: "de" });

    // Seeded fuzzy pair: this segment reads "…immediate steps." while the TM
    // holds both "…the immediate steps." and "…immediate action.", so there is
    // a difference to see in each direction.
    const source = page.getByText("We have taken immediate steps.").first();
    await expect(source).toBeVisible({ timeout: 10000 });
    await source.click();
    await expect(segmentEditor(page).first()).toBeVisible({ timeout: 5000 });

    const cards = page.getByTestId("tm-match-card");
    await expect(cards.first()).toBeVisible({ timeout: 10000 });

    // Each hit's source line marks the words it has that this segment lacks.
    const cardHighlights = cards.locator("span.bg-yellow-300");
    await expect(cardHighlights.first()).toBeVisible({ timeout: 10000 });

    // The mirror image: pointing at the "…immediate action." hit marks the
    // words THIS segment has that the hit lacks.
    const activeSource = page.getByTestId("active-source-box");
    const actionCard = cards.filter({ hasText: "immediate action" }).first();
    await expect(actionCard).toBeVisible();
    await actionCard.hover();

    const sourceHighlight = activeSource.locator("span.bg-yellow-300").first();
    await expect(sourceHighlight).toBeVisible({ timeout: 5000 });
    await expect(sourceHighlight).toContainText(/step/i);

    // The source half is active-row only: no other row carries a highlight,
    // and deactivating clears this one. Without this the isActive gate could
    // be deleted and every assertion above would still pass.
    // Scoped to the grid, so the sidebar's own card highlights don't count.
    const strayHighlights = page.locator("[data-segment-idx] span.bg-yellow-300");
    await expect(strayHighlights).toHaveCount(1);

    await page.keyboard.press("Escape");
    await expect(page.getByTestId("active-source-box")).toHaveCount(0);
    await expect(strayHighlights).toHaveCount(0);
  });

  test("Cmd+1 does not insert the previous segment's TM hit while the new lookup is in flight", async ({
    page,
  }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt", lang: "de" });

    // Land on a segment with a known hit and let its matches arrive.
    const first = page.getByText("We have taken immediate steps.").first();
    await expect(first).toBeVisible({ timeout: 10000 });
    await first.click();
    await expect(page.getByTestId("tm-match-card").first()).toBeVisible({ timeout: 10000 });
    const staleHitText = await page.getByTestId("tm-match-card").first().innerText();

    // Hold the next segment's lookup open, then switch. The panel still shows
    // the previous segment's hits — they must be inert to the shortcut, which
    // is what CATEditor's `usable` (not `matches`) feed buys.
    await page.route("**/tm-matches**", async (route) => {
      await new Promise((r) => setTimeout(r, 4000));
      await route.continue();
    });

    const second = page.getByText("Following actions.").first();
    await second.click();
    await expect(segmentEditor(page).first()).toBeVisible({ timeout: 5000 });

    await page.keyboard.press(process.platform === "darwin" ? "Meta+1" : "Control+1");

    // Nothing from the previous segment's hit may land in this cell.
    const editor = segmentEditor(page).first();
    const targetText = (await editor.innerText()).trim();
    const staleTarget = staleHitText.split("\n").pop()!.trim();
    expect(targetText).not.toContain(staleTarget);

    await page.unroute("**/tm-matches**");
  });

  test("sidebar does not crash on segment with no TM matches", async ({
    page,
  }) => {
    const errors: string[] = [];
    page.on("pageerror", (err) => errors.push(err.message));

    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt", lang: "de" });

    await expect(
      page.getByText(/patience/i).first()
    ).toBeVisible({ timeout: 10000 });

    // Click a segment unlikely to have a TM match (pre-translated, no exact source in TM)
    await page.getByText(/Klicken Sie auf/i).first().click();
    await expect(segmentEditor(page).first()).toBeVisible({ timeout: 5000 });

    // Sidebar should still render without crashing
    await expect(page.getByText("Resources").first()).toBeVisible({ timeout: 5000 });
    await expect(page.getByText("TM matches").first()).toBeVisible();
    // "No matches" text should appear (not a crash)
    await expect(page.getByText(/no matches/i).first()).toBeVisible({ timeout: 5000 });

    // No uncaught errors
    expect(errors, `Console errors: ${errors.join(", ")}`).toHaveLength(0);
  });

  test("Resources panel width: drag, persist, reset, drag shut, and drag back open", async ({ page }) => {
    // Five scenarios and three page loads in one test: 6.9s alone, 10.9s under
    // a full 8-worker run, against a 10s default it misses by under a second.
    // 20s is roughly twice the measured worst case — enough headroom for a
    // busier machine, short enough that a real hang still reports quickly.
    test.setTimeout(20_000);
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    const sidebar = page.getByTestId("editor-sidebar");
    const handle = page.getByTestId("resize-sidebar");
    const sidebarWidth = async () => Math.round((await sidebar.boundingBox())!.width);
    // SIDEBAR_CLICK_WIDTH in CATEditor.tsx: the width a click opens the panel
    // at, and the one a double-click on the handle restores.
    const CLICK_WIDTH = 200;

    try {
      await page.evaluate(() => localStorage.removeItem("cat-sidebar-width"));
      await page.reload();
      await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });
      // Poll, don't measure once: after a reload the panel's width lands a
      // frame or two behind the segment text under parallel load.
      await expect.poll(sidebarWidth).toBe(CLICK_WIDTH);
      // Drag the handle left → the panel widens by roughly the drag distance.
      const hb = (await handle.boundingBox())!;
      const y = hb.y + hb.height / 2;
      await page.mouse.move(hb.x + hb.width / 2, y);
      await page.mouse.down();
      await page.mouse.move(hb.x + hb.width / 2 - 120, y, { steps: 8 });
      await page.mouse.up();
      await expect.poll(sidebarWidth).toBeGreaterThan(CLICK_WIDTH + 80);

      // The width is in localStorage and comes back after a reload.
      const widened = await sidebarWidth();
      expect(Number(await page.evaluate(() => localStorage.getItem("cat-sidebar-width")))).toBe(widened);
      await page.reload();
      await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });
      await expect.poll(sidebarWidth).toBe(widened);

      // Double-click restores the default width.
      await page.getByTestId("resize-sidebar").dblclick();
      await expect.poll(sidebarWidth).toBe(CLICK_WIDTH);

      // Dragging the handle all the way right closes the panel instead of
      // leaving a sliver, and reopening returns the width it had.
      const shut = (await page.getByTestId("resize-sidebar").boundingBox())!;
      await page.mouse.move(shut.x + shut.width / 2, shut.y + shut.height / 2);
      await page.mouse.down();
      await page.mouse.move(shut.x + 900, shut.y + shut.height / 2, { steps: 10 });
      await page.mouse.up();
      await expect(sidebar).toHaveCount(0);
      const reopen = page.getByTestId("open-sidebar");
      await expect(reopen).toBeVisible();

      // Dragging the closed handle leftwards pulls the panel back out at the
      // width the drag asks for — no click needed.
      const rb = (await reopen.boundingBox())!;
      const ry = rb.y + rb.height / 2;
      await page.mouse.move(rb.x + rb.width / 2, ry);
      await page.mouse.down();
      await page.mouse.move(rb.x + rb.width / 2 - 260, ry, { steps: 10 });
      await page.mouse.up();
      await expect(sidebar).toBeVisible();
      await expect.poll(sidebarWidth).toBeGreaterThan(CLICK_WIDTH + 40);

      // A plain click on that tab opens it narrow instead, whatever the last
      // dragged width was.
      await page.getByTitle(/Close sidebar/i).click();
      await expect(sidebar).toHaveCount(0);
      await page.getByTestId("open-sidebar").click();
      await expect(sidebar).toBeVisible();
      await expect.poll(sidebarWidth).toBe(CLICK_WIDTH);
    } finally {
      await page.evaluate(() => {
        localStorage.removeItem("cat-sidebar-width");
        localStorage.setItem("cat-sidebar-open", "true");
      });
    }
  });

  test("sidebar collapses and reopens via the Resources handle", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    // Sidebar is open by default.
    await expect(page.getByTestId("editor-sidebar")).toBeVisible();

    // Close it → the sidebar unmounts and the collapsed reopen handle appears.
    await page.getByTitle("Close sidebar").click();
    await expect(page.getByTestId("editor-sidebar")).toHaveCount(0);
    await expect(page.getByTestId("open-sidebar")).toBeVisible();

    // Clicking the handle restores the sidebar.
    await page.getByTestId("open-sidebar").click();
    await expect(page.getByTestId("editor-sidebar")).toBeVisible();
    await expect(page.getByTestId("open-sidebar")).toHaveCount(0);
  });

  test("Resources panel: sections can be hidden and reordered, and the layout survives a reload", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });
    // Start from the default layout no matter what an earlier run left behind.
    await page.evaluate(() => localStorage.removeItem("cat-sidebar-sections"));
    await page.reload();
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    const sidebar = page.getByTestId("editor-sidebar");
    const sectionOrder = () =>
      sidebar.locator("[data-sidebar-section]").evaluateAll(els => els.map(el => (el as HTMLElement).dataset.sidebarSection));
    expect(await sectionOrder()).toEqual(["tm", "mt", "terminology", "history", "qa", "comments"]);

    try {
      // Hide Comments through the customize popover.
      await page.getByTestId("sidebar-customize-trigger").click();
      const popover = page.getByTestId("sidebar-customize-popover");
      await expect(popover).toBeVisible();
      await popover.getByTestId("sidebar-section-toggle-comments").uncheck();
      await expect(sidebar.getByTestId("comments-section")).toHaveCount(0);

      // Drag TM matches below MT suggestion. Same pointer choreography as the TM
      // rows test: cross the 4px activation distance, wait for dnd-kit to mark
      // the row as dragging, then land centre-on-centre.
      const tmRow = popover.getByTestId("sidebar-section-row-tm");
      const mtRow = popover.getByTestId("sidebar-section-row-mt");
      const handle = popover.getByTestId("sidebar-section-drag-tm");
      const hb = (await handle.boundingBox())!;
      const mb = (await mtRow.boundingBox())!;
      const x = hb.x + hb.width / 2;
      const startY = hb.y + hb.height / 2;
      await page.mouse.move(x, startY);
      await page.mouse.down();
      await page.mouse.move(x, startY + 6);
      await expect(tmRow).toHaveAttribute("data-dragging", "true");
      const targetY = mb.y + mb.height / 2;
      for (let i = 1; i <= 8; i++) {
        await page.mouse.move(x, startY + 6 + ((targetY - startY - 6) * i) / 8);
      }
      await page.mouse.up();
      await expect.poll(sectionOrder).toEqual(["mt", "tm", "terminology", "history", "qa"]);

      // The arrangement is in localStorage and comes back after a reload.
      const stored = await page.evaluate(() => JSON.parse(localStorage.getItem("cat-sidebar-sections") ?? "null"));
      expect(stored).toEqual({ order: ["mt", "tm", "terminology", "references", "history", "qa", "comments"], hidden: ["comments"] });
      await page.reload();
      await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });
      expect(await sectionOrder()).toEqual(["mt", "tm", "terminology", "history", "qa"]);
    } finally {
      await page.evaluate(() => localStorage.removeItem("cat-sidebar-sections"));
    }
  });

  test("segment counter and progress bar visible", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    // Segment counter renders "N segments" (CountMeta).
    await expect(page.getByText(/\d+ segments?/).first()).toBeVisible();

    await expect(page.locator('[title*="approved"]').first()).toBeVisible();
  });

  test("Ctrl+G opens jump to segment dialog", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    await page.keyboard.press("ControlOrMeta+g");
    await expect(page.getByText("Jump to segment")).toBeVisible({ timeout: 3000 });

    await page.locator('input[type="number"]').fill("5");
    await page.getByRole("button", { name: "Go" }).click();

    // Dialog closes and segment 5 (index 4) is the active row: its editor is mounted.
    await expect(page.locator('[data-segment-idx="4"]').getByTestId("segment-editor")).toBeVisible({ timeout: 3000 });
  });

  // Clicking inside a portaled dialog must NOT deactivate the active segment
  // via the document-level mousedown handler in CATEditor. The handler bails
  // out for ancestors matching `[data-slot$="-portal"]`, covering Jump, Step
  // Completion, Step Rejection, Keyboard shortcuts and Analysis.
  test("clicking inside a portaled dialog does not deactivate the active segment", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    await page.locator("[data-translation-cell]").first().click();
    await expect(segmentEditor(page).first()).toBeVisible({ timeout: 5000 });

    // Open the Jump dialog (a portaled Radix Dialog).
    await page.keyboard.press("ControlOrMeta+g");
    await expect(page.getByText("Jump to segment")).toBeVisible({ timeout: 3000 });

    // Click the dialog title — content lives in a body-portaled subtree, so
    // mousedown bubbles up to the document handler that deactivates segments.
    // Without the data-slot$="-portal" exemption, this click would trip the
    // handler and unmount the active row.
    await page.getByText("Jump to segment").click();

    // Active segment must remain active — editor still rendered.
    await expect(segmentEditor(page).first()).toBeVisible({ timeout: 1000 });
  });

  test("segment filter bar is visible", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    await expect(page.getByText("Untranslated").first()).toBeVisible();
    await expect(page.getByText("Approved").first()).toBeVisible();

    await expect(page.getByPlaceholder("Search segments...")).toBeVisible();
  });

  test("segment filter by status shows filtered results", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    // Status pills carry their segment count: "Approved (3)".
    await page.locator("button").filter({ hasText: /^Approved \(\d+\)$/ }).click();

    // Should show filter count badge (total may vary with seed data)
    await expect(page.getByText(/\/ \d+/).first()).toBeVisible();

    // Clear filter (scoped: the toolbar also has a "Clear translations" button
    // when the job has clearable content, so target the filter-bar Clear by testid).
    await page.getByTestId("segment-filter-clear").click();
  });

  test("segment search 'x' clears the term and unfilters", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    const search = page.getByPlaceholder("Search segments...");
    await search.fill("patience");
    await search.press("Enter");
    await expect(page.getByText(/\/ \d+/).first()).toBeVisible();

    await page.getByTestId("segment-search-clear").click();
    await expect(search).toHaveValue("");
    // No filter active → the count badge + Clear link disappear.
    await expect(page.getByTestId("segment-filter-clear")).toBeHidden();
  });

  // A search moves segments to new positions in the list. Each row must be
  // placed by its own height, not by the height of whatever segment held that
  // position before, or tall rows are drawn over the next ones.
  test("search results never overlap when tall rows move to new positions", async ({ page }) => {
    const tall = (n: number) => `needle ${n} ` + "a long sentence that wraps over several lines of the source column. ".repeat(6).trim();
    const lines = Array.from({ length: 8 }, (_, i) => (i % 2 ? tall(i) : `short line ${i}`));
    const job = await createIsolatedCATJob(page, {
      fileName: `search-overlap-${Date.now()}.txt`,
      content: lines.join("\n") + "\n",
      onCreated: (id) => projectIds.push(id),
    });
    await job.navigateToEditor(/short line 0/);
    // Every row is laid out (and measured) before the search.
    await expect(page.locator("[data-segment-idx]")).toHaveCount(lines.length);

    const search = page.getByPlaceholder("Search segments...");
    await search.fill("needle");
    await search.press("Enter");
    const rows = page.locator("[data-segment-idx]");
    await expect(rows).toHaveCount(4);

    // Largest overlap between a row and the one under it, in px.
    const worstOverlap = () => rows.evaluateAll(els => {
      const boxes = els.map(e => e.getBoundingClientRect()).sort((a, b) => a.top - b.top);
      let worst = 0;
      for (let i = 0; i + 1 < boxes.length; i++) worst = Math.max(worst, boxes[i].bottom - boxes[i + 1].top);
      return worst;
    });
    await expect.poll(worstOverlap, { message: "a search result row overlaps the next one", timeout: 3000 }).toBeLessThanOrEqual(0.5);
  });

  test("advanced search: regex option filters and flags invalid patterns", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    await page.getByTestId("segment-search-advanced-toggle").click();
    await expect(page.getByTestId("segment-search-advanced")).toBeVisible();
    await page.getByRole("checkbox", { name: /regex/i }).check();

    // Invalid pattern → inline error + honest 0-matches badge.
    const search = page.getByPlaceholder("Search segments...");
    await search.fill("(");
    await search.press("Enter");
    await expect(page.getByTestId("segment-search-regex-error")).toBeVisible();
    await expect(page.getByText(/^0 \/ \d+/).first()).toBeVisible();

    // Valid match-all pattern → error gone, filtered count equals total.
    await search.fill(".*");
    await search.press("Enter");
    await expect(page.getByTestId("segment-search-regex-error")).toBeHidden();
    await expect(page.getByText(/^(\d+) \/ \1$/).first()).toBeVisible();

    // Reset everything for later tests.
    await page.getByTestId("segment-filter-clear").click();
  });

  test("comments section visible in sidebar", async ({ page }) => {
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt", lang: "de" });

    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    // Click a segment
    await page.locator("[data-translation-cell]").first().click();
    await expect(segmentEditor(page).first()).toBeVisible({ timeout: 5000 });

    // Sidebar should show Comments section
    await expect(page.getByText("Comments").first()).toBeVisible({ timeout: 5000 });
    await expect(page.getByPlaceholder("Add a comment...")).toBeVisible();
  });

  // Owns its project/file/job so every count below is scoped to data this test
  // created. Borrowing the seeded demo project would make the zero-baseline an
  // assertion about the whole database, which no parallel `make e2e` run can
  // honour.
  test("comment indicator and Commented filter", async ({ page }) => {
    test.setTimeout(60_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `comments-e2e-${Date.now()}.txt`,
      content: "First sentence here.\nSecond sentence here.\nThird sentence here.",
    });

    try {
      await job.navigateToEditor(/First sentence/);

      const indicator = page.getByTestId("segment-comment-indicator");
      const commentedFilter = page.getByTestId("segment-filter-comments");
      const rows = page.locator("[data-translation-cell]");

      // Baseline: a freshly uploaded file has no comments anywhere.
      await expect(indicator).toHaveCount(0);
      await expect(rows).toHaveCount(3);

      // Comment on the first segment. The Add button is disabled until the
      // draft is non-empty, so wait for enabled rather than racing it.
      await rows.first().click();
      await expect(segmentEditor(page).first()).toBeVisible({ timeout: 5000 });
      const addBtn = page.getByTestId("comment-add");
      await page.getByTestId("comment-input").fill("Check this term");
      await expect(addBtn).toBeEnabled();
      const created = page.waitForResponse(
        (r) => r.url().includes("/comments") && r.request().method() === "POST" && r.ok(),
      );
      await addBtn.click();
      await created;

      // Indicator appears on exactly one row, showing the count.
      await expect(indicator).toHaveCount(1, { timeout: 5000 });
      await expect(indicator).toHaveAttribute("data-comment-count", "1");

      // The Commented filter narrows the grid to that single segment.
      await commentedFilter.click();
      await expect(rows).toHaveCount(1);
      await expect(indicator).toHaveCount(1);

      // Turning it off restores all three rows.
      await commentedFilter.click();
      await expect(rows).toHaveCount(3);

      // Deleting the comment retires the indicator.
      const deleted = page.waitForResponse(
        (r) => r.url().includes("/comments/") && r.request().method() === "DELETE",
      );
      await page.getByTestId("comment-delete").click();
      await deleted;
      await expect(indicator).toHaveCount(0, { timeout: 5000 });
    } finally {
      // Cascade-deletes the project, so any comment created above goes with it
      // even if an assertion above threw.
      await job.cleanup();
    }
  });

  // --- XLIFF Tag Tests (upload-based, no scroll dependency) ---
  // All tag tests upload a small XLIFF file and clean up after themselves.

  const TAG_XLIFF = `<?xml version="1.0" encoding="utf-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
  <file source-language="en" target-language="es" datatype="plaintext" original="tag-test.xliff">
    <body>
      <trans-unit id="1">
        <source>Click <bpt id="1">&lt;b&gt;</bpt>here<ept id="1">&lt;/b&gt;</ept> to continue</source>
        <target>Haga clic <bpt id="1">&lt;b&gt;</bpt>aquí<ept id="1">&lt;/b&gt;</ept> para continuar</target>
      </trans-unit>
      <trans-unit id="2">
        <source>Your order is <ph id="2">{orderNumber}</ph>.</source>
      </trans-unit>
    </body>
  </file>
</xliff>`;

  /** Upload the tag XLIFF into a fresh isolated project (no pre-translate),
   *  navigate to the editor, return cleanup. The project (and everything
   *  under it) is cascade-deleted via `cleanup()`. */
  async function setupTagTest(page: import("@playwright/test").Page) {
    const job = await createIsolatedCATJob(page, {
      fileName: `tag-e2e-${Date.now()}.xliff`,
      content: TAG_XLIFF,
      contentType: "application/xliff+xml",
    });
    await job.navigateToEditor(/Click/);
    return job;
  }

  test("XLIFF tag chips visible in source and target columns", async ({ page }) => {
    test.setTimeout(60_000);
    const job = await setupTagTest(page);

    try {
      // Source column should show tag chips
      const tagChips = page.locator('span.xliff-tag');
      await expect(tagChips.first()).toBeVisible({ timeout: 5000 });
      const chipCount = await tagChips.count();
      expect(chipCount).toBeGreaterThanOrEqual(3); // bpt + ept + ph across 2 segments

      // Click segment to activate editor, then copy source to get tags
      await page.locator("[data-translation-cell]").first().click();
      const editor = segmentEditor(page).first();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await page.keyboard.press("ControlOrMeta+Shift+s");
      await page.waitForTimeout(500);

      // Editor should contain xliff-tag chips (from source_rich_text via copy-source)
      const editorTags = page.locator('[data-testid="segment-editor"] .xliff-tag');
      await expect(editorTags.first()).toBeVisible({ timeout: 3000 });
      expect(await editorTags.count()).toBeGreaterThanOrEqual(2);

      // Tags should be non-editable
      const editable = await editorTags.first().getAttribute('contenteditable');
      expect(editable).toBe('false');
    } finally {
      await job.cleanup();
    }
  });

  // Chip labels: chips are numbered by default and switch to
  // element names under the ¶ toggle, with the element form always on hover.
  // A <g ctype> chip shows the element name, never the raw OASIS ctype
  // (x-html-sup).
  const CTYPE_XLIFF = `<?xml version="1.0" encoding="utf-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
  <file source-language="en" target-language="es" datatype="plaintext" original="ctype-test.xliff">
    <body>
      <trans-unit id="1">
        <source>The G7<g id="1" ctype="x-html-sup">m</g> analyzer</source>
      </trans-unit>
    </body>
  </file>
</xliff>`;

  test("tag chips show numbers by default, element names under ¶, markup on hover", async ({ page }) => {
    test.setTimeout(60_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `ctype-e2e-${Date.now()}.xliff`,
      content: CTYPE_XLIFF,
      contentType: "application/xliff+xml",
    });

    try {
      await job.navigateToEditor(/analyzer/);

      const openChip = page.locator('span.xliff-tag[data-ctype="x-html-sup"]').first();
      await expect(openChip).toBeVisible({ timeout: 5000 });

      // Chips are numbered by default.
      await expect(openChip).toHaveText("<1>");
      // Hover reveals the original markup without changing mode.
      await expect(openChip).toHaveAttribute("title", "<sup>");

      const pilcrowBtn = page.getByRole("button", { name: "¶" });
      await pilcrowBtn.click();

      // Full-tags mode: the element name, not the OASIS ctype.
      await expect(openChip).toHaveText("<sup>");
      const closeChip = page.locator('span.xliff-tag[data-ctype="x-html-sup"]').last();
      await expect(closeChip).toHaveText("</sup>");

      // Toggling back restores the numbers.
      await pilcrowBtn.click();
      await expect(openChip).toHaveText("<1>");
    } finally {
      await job.cleanup();
    }
  });

  test("tag chips inside the editor follow the ¶ toggle too", async ({ page }) => {
    // The source column renders chips as React spans; the editor renders them
    // as ProseMirror node views. They're separate code paths and only this
    // test covers the second one — the mode has to reach an atom node without
    // touching the document.
    test.setTimeout(60_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `ctype-editor-${Date.now()}.xliff`,
      content: CTYPE_XLIFF,
      contentType: "application/xliff+xml",
    });

    try {
      await job.navigateToEditor(/analyzer/);

      // Copy source into the target so the editor actually holds chips.
      await page.locator("[data-translation-cell]").first().click();
      const editor = segmentEditor(page).first();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await page.keyboard.press("ControlOrMeta+Shift+s");

      const editorChip = page
        .locator('[data-testid="segment-editor"] .xliff-tag[data-ctype="x-html-sup"]')
        .first();
      await expect(editorChip).toBeVisible({ timeout: 5000 });
      await expect(editorChip).toHaveText("<1>");
      await expect(editorChip).toHaveAttribute("title", "<sup>");

      await page.getByRole("button", { name: "¶" }).click();
      await expect(editorChip).toHaveText("<sup>");

      await page.getByRole("button", { name: "¶" }).click();
      await expect(editorChip).toHaveText("<1>");
    } finally {
      await job.cleanup();
    }
  });

  test("chips render full names when ¶ was already on before the segment opened", async ({ page }) => {
    // Order matters. Toggling ¶ while a segment is open dispatches a
    // transaction that re-renders the chips; turning it on FIRST and then
    // opening a segment relies on the node view reading the mode it is handed
    // on its initial render.
    test.setTimeout(60_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `ctype-mount-${Date.now()}.xliff`,
      content: CTYPE_XLIFF,
      contentType: "application/xliff+xml",
    });

    try {
      await job.navigateToEditor(/analyzer/);

      // 1. Toggle full tag names ON with no segment active.
      await page.getByRole("button", { name: "¶" }).click();
      await expect(
        page.locator('span.xliff-tag[data-ctype="x-html-sup"]').first(),
      ).toHaveText("<sup>");

      // 2. NOW open the segment and pull the tags into the editor.
      await page.locator("[data-translation-cell]").first().click();
      const editor = segmentEditor(page).first();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await page.keyboard.press("ControlOrMeta+Shift+s");

      const editorChip = page
        .locator('[data-testid="segment-editor"] .xliff-tag[data-ctype="x-html-sup"]')
        .first();
      await expect(editorChip).toBeVisible({ timeout: 5000 });
      // The editor must agree with the source column it sits next to.
      await expect(editorChip).toHaveText("<sup>");
    } finally {
      await job.cleanup();
    }
  });

  test("term underline composes with tag chips in the active source", async ({ page }) => {
    // A segment with inline tags keeps its term underline: terms are painted
    // per text node over the flattened rich text.
    test.setTimeout(60_000);

    // Source wraps the term in a <b> pair so the segment takes the chip path.
    const TERM_TAG_XLIFF = `<?xml version="1.0" encoding="utf-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
  <file source-language="en" target-language="es" datatype="plaintext" original="term-tag-test.xliff">
    <body>
      <trans-unit id="1">
        <source>Reset your <bpt id="1">&lt;b&gt;</bpt>password<ept id="1">&lt;/b&gt;</ept> now</source>
      </trans-unit>
    </body>
  </file>
</xliff>`;

    const job = await createIsolatedCATJob(page, {
      fileName: `term-tag-e2e-${Date.now()}.xliff`,
      content: TERM_TAG_XLIFF,
      contentType: "application/xliff+xml",
    });
    const headers = { Authorization: `Bearer ${job.token}` };
    let termBaseId: number | null = null;
    try {
      // Hermetic terminology: own term base + entry + assignment, so the test
      // doesn't depend on whatever the default template happens to assign.
      const baseResp = await page.request.post("/api/terminology", {
        headers,
        data: {
          name: `term-tag-e2e ${Date.now()}`,
          description: "term tag regression",
          source_language: "en",
        },
      });
      expect(baseResp.ok(), `create term base: ${baseResp.status()}`).toBe(true);
      termBaseId = (await baseResp.json()).data.id;
      // Concept-native: a concept with an en source term + an es target term.
      const entryResp = await page.request.post(`/api/terminology/${termBaseId}/concepts`, {
        headers,
        data: {
          terms: [
            { language: "en", text: "password", status: "approved" },
            { language: "es", text: "contraseña", status: "approved" },
          ],
        },
      });
      expect(entryResp.ok(), `create term concept: ${entryResp.status()}`).toBe(true);
      const assignResp = await page.request.put(`/api/projects/${job.projectId}/terminology`, {
        headers,
        data: {
          assignments: [
            { term_base_id: termBaseId, source_language: "en", target_language: "es", priority: 0 },
          ],
        },
      });
      expect(assignResp.ok(), `assign term base: ${assignResp.status()}`).toBe(true);

      await job.navigateToEditor(/Reset your/);

      // Activate the segment and await the term lookup (never poll with timeouts).
      const lookupResp = page.waitForResponse(
        r => r.url().includes("/api/terminology/lookup") && r.status() === 200,
        { timeout: 15_000 },
      );
      await page.locator("[data-translation-cell]").first().click();
      const lookup = await lookupResp;
      const lookedUp = (await lookup.json()).data;
      expect(
        lookedUp.some((t: { source_term: string }) => t.source_term === "password"),
        "precondition: server lookup must find the term",
      ).toBe(true);

      const sourceBox = page.getByTestId("active-source-box");
      // Chips still render — composing must not degrade the chip path…
      await expect(sourceBox.locator("span.xliff-tag")).toHaveCount(2);
      // …and the term is underlined despite the tags (red: target lacks the
      // term; the exact color is unit-tested, here we pin underline presence).
      const underlined = sourceBox.locator("span.underline");
      await expect(underlined).toHaveCount(1);
      await expect(underlined).toHaveText("password");
    } finally {
      if (termBaseId !== null) {
        await page.request.delete(`/api/terminology/${termBaseId}`, { headers });
      }
      await job.cleanup();
    }
  });

  test("applying a term in a target with tag chips keeps the chips", async ({ page }) => {
    test.setTimeout(60_000);

    const TERM_TAG_XLIFF = `<?xml version="1.0" encoding="utf-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
  <file source-language="en" target-language="es" datatype="plaintext" original="term-apply-tag-test.xliff">
    <body>
      <trans-unit id="1">
        <source>Reset your <bpt id="1">&lt;b&gt;</bpt>password<ept id="1">&lt;/b&gt;</ept> now</source>
      </trans-unit>
    </body>
  </file>
</xliff>`;

    const job = await createIsolatedCATJob(page, {
      fileName: `term-apply-tag-e2e-${Date.now()}.xliff`,
      content: TERM_TAG_XLIFF,
      contentType: "application/xliff+xml",
    });
    const headers = { Authorization: `Bearer ${job.token}` };
    let termBaseId: number | null = null;
    try {
      const baseResp = await page.request.post("/api/terminology", {
        headers,
        data: { name: `term-apply-tag-e2e ${Date.now()}`, description: "", source_language: "en" },
      });
      expect(baseResp.ok(), `create term base: ${baseResp.status()}`).toBe(true);
      termBaseId = (await baseResp.json()).data.id;
      const entryResp = await page.request.post(`/api/terminology/${termBaseId}/concepts`, {
        headers,
        data: {
          terms: [
            { language: "en", text: "password", status: "approved" },
            { language: "es", text: "contraseña", status: "approved" },
          ],
        },
      });
      expect(entryResp.ok(), `create term concept: ${entryResp.status()}`).toBe(true);
      const assignResp = await page.request.put(`/api/projects/${job.projectId}/terminology`, {
        headers,
        data: {
          assignments: [
            { term_base_id: termBaseId, source_language: "en", target_language: "es", priority: 0 },
          ],
        },
      });
      expect(assignResp.ok(), `assign term base: ${assignResp.status()}`).toBe(true);

      await job.navigateToEditor(/Reset your/);
      const lookupResp = page.waitForResponse(
        r => r.url().includes("/api/terminology/lookup") && r.status() === 200,
        { timeout: 15_000 },
      );
      await page.locator("[data-translation-cell]").first().click();
      await lookupResp;

      // Copy source puts the tag chips and the untranslated term into the target.
      const editor = segmentEditor(page).first();
      await page.keyboard.press("ControlOrMeta+Shift+s");
      await expect(editor.locator(".xliff-tag")).toHaveCount(2);

      // A term row takes clicks only while it is NOT applied, and it learns
      // that from the editor's text one render later. Wait for the clickable
      // row before each apply, or the mousedown can hit the inert one.
      const applicableTerm = page.getByTestId("terminology-section").getByTitle("Click to replace selection");
      await expect(applicableTerm).toBeVisible();
      await applicableTerm.dispatchEvent("mousedown");

      await expect(editor).toContainText("contraseña");
      await expect(editor).not.toContainText("password");
      await expect(editor.locator(".xliff-tag")).toHaveCount(2);

      // Empty the tag pair, then apply the term with the cursor between the tags:
      // it must land inside the pair, not after the closing tag.
      await editor.getByText("contraseña").dblclick();
      await page.keyboard.press("Backspace");
      await expect(editor).not.toContainText("contraseña");
      await expect(applicableTerm).toBeVisible();
      await applicableTerm.dispatchEvent("mousedown");
      await expect(editor).toContainText("contraseña");
      const order = await editor.evaluate(el =>
        [...(el.querySelector("p")?.childNodes ?? [])].map(n =>
          n instanceof HTMLElement && n.classList.contains("xliff-tag") ? "TAG" : (n.textContent ?? "").trim(),
        ).filter(Boolean),
      );
      expect(order).toEqual(["Reset your", "TAG", "contraseña", "TAG", "now"]);
    } finally {
      if (termBaseId !== null) {
        await page.request.delete(`/api/terminology/${termBaseId}`, { headers });
      }
      await job.cleanup();
    }
  });

  test("deleting the last character leaves a saved segment empty", async ({ page }) => {
    test.setTimeout(30_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `empty-refill-e2e-${Date.now()}.txt`,
      content: "Hello world\n",
      contentType: "text/plain",
    });
    try {
      await job.navigateToEditor(/Hello world/);
      await page.locator("[data-translation-cell]").first().click();
      const editor = segmentEditor(page).first();
      await expect(editor).toBeVisible({ timeout: 5000 });

      // Save a target, so the segment has saved text to fall back to.
      await editor.click();
      await page.keyboard.type("ab");
      const saved = page.waitForResponse(
        r => /\/api\/files\/\d+\/translations\//.test(r.url()) && r.request().method() === "PUT" && r.ok(),
        { timeout: 15_000 },
      );
      await page.keyboard.press("ControlOrMeta+Enter");
      await saved;

      // Reload so the saved text comes from the server, as on a normal visit.
      await page.reload();
      await page.locator("[data-translation-cell]").first().click();
      await expect(editor).toHaveText("ab");
      await editor.click();
      await page.keyboard.press("End");
      await page.keyboard.press("Backspace");
      await page.keyboard.press("Backspace");

      await expect(editor).toHaveText("");
      // Give any re-sync a render cycle to happen before re-checking.
      await page.waitForTimeout(300);
      await expect(editor).toHaveText("");
    } finally {
      await job.cleanup();
    }
  });

  test("leaving an emptied segment clears its translation", async ({ page }) => {
    test.setTimeout(30_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `empty-clear-e2e-${Date.now()}.txt`,
      content: "Hello world\n\nSecond line\n",
      contentType: "text/plain",
    });
    try {
      await job.navigateToEditor(/Hello world/);
      const firstCell = page.locator("[data-translation-cell]").first();
      await firstCell.click();
      const editor = segmentEditor(page).first();
      await expect(editor).toBeVisible({ timeout: 5000 });

      await editor.click();
      await page.keyboard.type("ab");
      const saved = page.waitForResponse(
        r => /\/api\/files\/\d+\/translations\//.test(r.url()) && r.request().method() === "PUT" && r.ok(),
        { timeout: 15_000 },
      );
      await page.keyboard.press("ControlOrMeta+Enter");
      await saved;

      await page.reload();
      await firstCell.click();
      await expect(editor).toHaveText("ab");
      await editor.click();
      await page.keyboard.press("End");
      await page.keyboard.press("Backspace");
      await page.keyboard.press("Backspace");

      const cleared = page.waitForResponse(
        r => /\/translations\/\d+\/clear$/.test(r.url().split("?")[0]) && r.request().method() === "POST" && r.ok(),
        { timeout: 15_000 },
      );
      await page.keyboard.press("Tab");
      await cleared;

      await page.reload();
      await expect(page.getByText(/Second line/).first()).toBeVisible({ timeout: 10_000 });
      await expect(firstCell).not.toContainText("ab");
    } finally {
      await job.cleanup();
    }
  });

  // Clear stays on a segment with nothing saved. With saved text it asks
  // first and clears it on the server; with nothing saved it only empties the
  // editor.
  test("Clear stays after a clear, and empties unsaved text without a server call", async ({ page }) => {
    test.setTimeout(30_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `clear-button-e2e-${Date.now()}.txt`,
      content: "Hello world\n\nSecond line\n",
      contentType: "text/plain",
    });
    try {
      await job.navigateToEditor(/Hello world/);
      const firstCell = page.locator("[data-translation-cell]").first();
      await firstCell.click();
      const editor = segmentEditor(page).first();
      const clearButton = page.getByTestId("clear-translation-btn");
      const isClear = (url: string) => /\/translations\/\d+\/clear$/.test(url.split("?")[0]);
      await expect(editor).toBeVisible({ timeout: 5000 });
      await expect(clearButton, "offered on a segment nobody translated").toBeVisible();

      // Save a target, then reload so the saved text comes from the server.
      await editor.click();
      await page.keyboard.type("ab");
      const saved = page.waitForResponse(
        r => /\/api\/files\/\d+\/translations\//.test(r.url()) && r.request().method() === "PUT" && r.ok(),
        { timeout: 15_000 },
      );
      await page.keyboard.press("ControlOrMeta+Enter");
      await saved;
      await page.reload();
      await firstCell.click();
      await expect(editor).toHaveText("ab");

      // Saved text: Clear asks first, then clears it on the server.
      const cleared = page.waitForResponse(r => isClear(r.url()) && r.request().method() === "POST" && r.ok(), { timeout: 15_000 });
      await clearButton.click();
      await page.getByRole("alertdialog").getByRole("button", { name: "Clear" }).click();
      await cleared;
      await expect(editor).toHaveText("");
      await expect(page.getByRole("alertdialog")).toHaveCount(0);
      await expect(clearButton, "still offered once the segment is empty").toBeVisible();

      // Nothing saved: Clear empties the editor, with no dialog and no request.
      let clearRequests = 0;
      page.on("request", r => { if (isClear(r.url())) clearRequests++; });
      await editor.click();
      await page.keyboard.type("cd");
      await expect(editor).toHaveText("cd");
      await clearButton.click();
      await expect(editor).toHaveText("");
      await expect(page.getByRole("alertdialog")).toHaveCount(0);
      expect(clearRequests).toBe(0);
    } finally {
      await job.cleanup();
    }
  });

  test("toggling ¶ mid-edit does not dirty the segment or trigger a save", async ({ page }) => {
    // The label mode lives in ProseMirror plugin state, never in the document.
    // If it were a node attribute, flipping the toggle would rewrite every
    // chip, mark the doc dirty and — under the editor save architecture —
    // fire a save the translator never asked for, clobbering their draft.
    test.setTimeout(60_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `ctype-nodirty-${Date.now()}.xliff`,
      content: CTYPE_XLIFF,
      contentType: "application/xliff+xml",
    });

    try {
      await job.navigateToEditor(/analyzer/);

      let saveCount = 0;
      page.on("request", req => {
        if (req.method() === "PUT" && /\/api\/files\/\d+\/translations\//.test(req.url())) {
          saveCount++;
        }
      });

      await page.locator("[data-translation-cell]").first().click();
      const editor = segmentEditor(page).first();
      await expect(editor).toBeVisible({ timeout: 5000 });

      // Copy source so the editor holds real chips, then type an unsaved edit.
      await page.keyboard.press("ControlOrMeta+Shift+s");
      const editorChip = page
        .locator('[data-testid="segment-editor"] .xliff-tag[data-ctype="x-html-sup"]')
        .first();
      await expect(editorChip).toBeVisible({ timeout: 5000 });
      await page.keyboard.press("End");
      await page.keyboard.type(" entwurf");
      await expect(editor).toHaveText(/entwurf/);

      const before = saveCount;
      await page.getByRole("button", { name: "¶" }).click();
      // Assert the toggle actually took effect, so this can't pass merely
      // because nothing happened.
      await expect(editorChip).toHaveText("<sup>");

      expect(saveCount, "toggling the label mode must not save").toBe(before);
      // The in-progress draft survives untouched.
      await expect(editor).toHaveText(/entwurf/);
    } finally {
      await job.cleanup();
    }
  });

  test("cursor visible adjacent to XLIFF tag chips", async ({ page }) => {
    test.setTimeout(60_000);
    const job = await setupTagTest(page);

    try {
      // Click segment and copy source to get tags in editor
      await page.locator("[data-translation-cell]").first().click();
      await expect(segmentEditor(page).first()).toBeVisible({ timeout: 5000 });
      await page.keyboard.press("ControlOrMeta+Shift+s");
      await page.waitForTimeout(500);

      const editorTags = page.locator('[data-testid="segment-editor"] .xliff-tag');
      await expect(editorTags.first()).toBeVisible({ timeout: 3000 });

      // ArrowLeft into a tag — should show ProseMirror-selectednode highlight
      await page.keyboard.press("End");
      await page.waitForTimeout(100);

      let selectedNodeFound = false;
      for (let i = 0; i < 30; i++) {
        await page.keyboard.press("ArrowLeft");
        await page.waitForTimeout(100);
        const count = await page.locator('[data-testid="segment-editor"] .xliff-tag.ProseMirror-selectednode').count();
        if (count > 0) { selectedNodeFound = true; break; }
      }
      expect(selectedNodeFound, "tag should get ProseMirror-selectednode when selected via ArrowLeft").toBe(true);

      // Verify visible outline
      const selectedTag = page.locator('[data-testid="segment-editor"] .xliff-tag.ProseMirror-selectednode').first();
      const outline = await selectedTag.evaluate(el => getComputedStyle(el).outlineStyle);
      expect(outline, "selected tag should have a visible outline").not.toBe('none');
    } finally {
      await job.cleanup();
    }
  });

  test("no false draft saved on tag segments", async ({ page }) => {
    test.setTimeout(30_000);
    const job = await setupTagTest(page);

    // Every draft save is a PUT …/translations/{id}/draft. Counted, not waited
    // for: the assertion is that none is sent.
    const isDraftPut = (r: import("@playwright/test").Request) =>
      r.method() === "PUT" && /\/translations\/\d+\/draft$/.test(r.url());
    let draftPuts = 0;
    page.on("request", r => { if (isDraftPut(r)) draftPuts++; });

    try {
      // Click segment and copy source to get tags in editor. Copying IS an
      // edit, so its own autosave (3s debounce) must land before counting.
      await page.locator("[data-translation-cell]").first().click();
      await expect(segmentEditor(page).first()).toBeVisible({ timeout: 5000 });
      await Promise.all([
        page.waitForResponse(r => isDraftPut(r.request()), { timeout: 10000 }),
        page.keyboard.press("ControlOrMeta+Shift+s"),
      ]);
      draftPuts = 0;

      // Move cursor around without changing text
      await page.keyboard.press("Home");
      await page.keyboard.press("ArrowRight");
      await page.keyboard.press("ArrowRight");
      await page.keyboard.press("ArrowLeft");

      // A negative assertion needs the debounce to pass: 3s + margin.
      await page.waitForTimeout(4000);
      expect(draftPuts, "moving the cursor over tags must not save a draft").toBe(0);
    } finally {
      await job.cleanup();
    }
  });

  test("XLIFF tag chips remain visible when hidden chars toggled on", async ({ page }) => {
    test.setTimeout(60_000);
    const job = await setupTagTest(page);

    try {
      // Verify source tags visible initially
      const sourceTagChips = page.locator('span.xliff-tag');
      await expect(sourceTagChips.first()).toBeVisible({ timeout: 5000 });
      const initialCount = await sourceTagChips.count();
      expect(initialCount).toBeGreaterThanOrEqual(3);

      // Toggle hidden chars on (¶ button)
      const hiddenCharsBtn = page.getByRole('button', { name: '¶' });
      await hiddenCharsBtn.click();

      // Source tags should STILL be visible after toggling hidden chars
      await expect(sourceTagChips.first()).toBeVisible({ timeout: 3000 });
      const afterCount = await sourceTagChips.count();
      expect(afterCount).toBe(initialCount);

      // Toggle hidden chars off
      await hiddenCharsBtn.click();

      // Tags still there
      await expect(sourceTagChips.first()).toBeVisible({ timeout: 3000 });
    } finally {
      // Reset hidden chars state
      try { await page.evaluate(() => localStorage.removeItem('cat-show-whitespace')); } catch {}
      await job.cleanup();
    }
  });

  test("HTML file shows inline formatting as tag chips", async ({ page }) => {
    test.setTimeout(60_000);

    const htmlContent = `<!DOCTYPE html><html><body>
      <p>Click <b>here</b> to <a href="https://example.com">continue</a>.</p>
      <p>Plain text without formatting.</p>
    </body></html>`;

    const job = await createIsolatedCATJob(page, {
      fileName: `html-tags-${Date.now()}.html`,
      content: htmlContent,
      contentType: "text/html",
    });

    try {
      await job.navigateToEditor(/Click/);

      // Source column should show tag chips for <b> and <a>
      const tagChips = page.locator('span.xliff-tag');
      await expect(tagChips.first()).toBeVisible({ timeout: 5000 });

      // Should have at least 3 tag chips (bold open/close + link open/close from <g> tags)
      const chipCount = await tagChips.count();
      expect(chipCount).toBeGreaterThanOrEqual(3);
    } finally {
      await job.cleanup();
    }
  });

  // Optimistic locking on translation save, FRONTEND side: user A is
  // editing a segment, user B (simulated via a direct PUT) saves the same
  // segment in the meantime, user A then confirms — the inline conflict banner
  // must appear with a side-by-side choice instead of silently overwriting
  // user B's work.
  test("optimistic-lock conflict banner appears when another user edits the same segment mid-edit", async ({ page }) => {
    test.setTimeout(60_000);

    // Isolated project + file so we don't pollute Demo Project segments and
    // so cleanup is one API call. createIsolatedCATJob keeps pre-translate
    // off, so the shared Default TM cannot pre-populate the editor.
    // qaProfile: null — this test exercises optimistic-locking semantics, not
    // QA. Standard QA enforces number_consistency at error severity;
    // source contains a timestamp (lots of digits) while user-A/B edits don't,
    // so the seed profile would 422 the PUTs and mask the real assertions.
    const uniqueSrc = `conflict-source-${Date.now()}`;
    const job = await createIsolatedCATJob(page, {
      fileName: `conflict-${Date.now()}.txt`,
      content: `${uniqueSrc}\n`,
      qaProfile: null,
    });

    try {
      await job.navigateToEditor(new RegExp(uniqueSrc));

      // Click into edit mode on the first segment.
      await page.locator("[data-translation-cell]").first().click();
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      await expect(editor).toBeVisible({ timeout: 5000 });

      // Capture the translation ID via the listJobTranslations API.
      const translationsRes = await page.request.get(
        `/api/jobs/${job.jobId}/translations`,
        { headers: { Authorization: `Bearer ${job.token}` } },
      );
      const translationId = (await translationsRes.json()).data?.[0]?.id ?? null;
      expect(translationId, "translation id should be exposed by the listJobTranslations API").not.toBeNull();

      // Simulate "user B" saving this same translation OUT-OF-BAND, BEFORE
      // user A's expected_updated_at gets advanced. We omit expected_updated_at
      // entirely (find-replace path) so the backend accepts the write — the
      // resulting `updated_at` is newer than what user A captured when they
      // clicked into the segment.
      const userBResp = await page.request.put(
        `/api/files/${job.fileId}/translations/${translationId}`,
        {
          headers: { Authorization: `Bearer ${job.token}` },
          data: { human_text: "User B wrote this first", editing_time_ms: 0 },
        },
      );
      expect(userBResp.ok(), "user B's out-of-band save should succeed").toBe(true);

      // Now user A finishes typing and confirms (Ctrl/Cmd+Enter triggers the
      // saveMut path in SegmentRow → updateTranslationHuman with the STALE
      // expected_updated_at user A captured on activate). `editor.fill()`
      // atomically select-all-then-types so MT-pre-translated content from
      // upload doesn't get appended to (every other CAT lifecycle test in
      // this file uses fill for the same reason — keyboard.type APPENDS at
      // cursor and produces non-deterministic content under pre-translate
      // races).
      await editor.click();
      await editor.fill("User A's edit");
      const isMac = process.platform === "darwin";
      await page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter");

      // The conflict banner must appear with both versions side-by-side and
      // both action buttons. (We do NOT click "Use theirs" / "Use mine" —
      // dedicated tests below exercise those paths.) Both saves used the
      // SAME auth token (User A and "User B" are the same account here),
      // so the heading must use the same-user "another tab or session"
      // wording — NOT "Saved by Olivia while you were editing" which reads
      // as nonsense when YOU are Olivia.
      await expect(page.getByTestId("conflict-banner")).toBeVisible({ timeout: 5000 });
      await expect(page.getByTestId("conflict-banner-title")).toContainText(/another tab or session/i);
      await expect(page.getByTestId("conflict-banner-theirs")).toContainText("User B wrote this first");
      await expect(page.getByTestId("conflict-banner-mine")).toContainText("User A's edit");
      await expect(page.getByTestId("conflict-banner-use-theirs")).toBeVisible();
      await expect(page.getByTestId("conflict-banner-use-mine")).toBeVisible();
    } finally {
      await job.cleanup();
    }
  });

  // Clicking "Use theirs" discards the local edit and
  // reloads the server's version. The banner clears, the editor shows the
  // OTHER user's text. Anti-tautology: if "Use theirs" silently kept user A's
  // text instead of pulling user B's, this test would fail.
  test("conflict banner: 'Use theirs' discards local edits and shows the server version", async ({ page }) => {
    test.setTimeout(60_000);
    // qaProfile: null — see note on the sibling "optimistic-lock" test above.
    const uniqueSrc = `usetheirs-source-${Date.now()}`;
    const job = await createIsolatedCATJob(page, {
      fileName: `usetheirs-${Date.now()}.txt`,
      content: `${uniqueSrc}\n`,
      qaProfile: null,
    });

    try {
      await job.navigateToEditor(new RegExp(uniqueSrc));
      await page.locator("[data-translation-cell]").first().click();
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      await expect(editor).toBeVisible({ timeout: 5000 });

      const translationsRes = await page.request.get(
        `/api/jobs/${job.jobId}/translations`,
        { headers: { Authorization: `Bearer ${job.token}` } },
      );
      const translationId = (await translationsRes.json()).data?.[0]?.id ?? null;

      // User B saves first (out-of-band, server-side state advances).
      await page.request.put(
        `/api/files/${job.fileId}/translations/${translationId}`,
        {
          headers: { Authorization: `Bearer ${job.token}` },
          data: { human_text: "their canonical version", editing_time_ms: 0 },
        },
      );

      // User A types and confirms → 409 → banner. fill() for atomic content
      // replacement (see "banner appears" test for rationale).
      await editor.click();
      await editor.fill("my conflicting edit");
      const isMac = process.platform === "darwin";
      await page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter");
      await expect(page.getByTestId("conflict-banner")).toBeVisible({ timeout: 5000 });

      // Click "Use theirs" → banner clears, editor shows server version.
      await page.getByTestId("conflict-banner-use-theirs").click();
      await expect(page.getByTestId("conflict-banner")).not.toBeVisible({ timeout: 3000 });

      // Editor reflects "their canonical version" — user A's local edit was discarded.
      await expect(editor).toContainText("their canonical version", { timeout: 3000 });
      await expect(editor).not.toContainText("my conflicting edit");
    } finally {
      await job.cleanup();
    }
  });

  // Clicking "Use mine" re-issues the save with
  // expected_updated_at = null (last-writer-wins), succeeds, and persists the
  // local edit. Anti-tautology: if "Use mine" silently fell back to user B's
  // text or no-op'd, the API check at the end would fail.
  //
  // Pre-translate is OFF on the isolated project so the editor starts empty
  // and `editor.fill()` can plant a deterministic edit (no MT pre-fill to
  // append onto), and no Default TM entries from other runs leak in.
  test("conflict banner: 'Use mine' overwrites the server version with the local edit", async ({ page }) => {
    test.setTimeout(60_000);
    // qaProfile: null — see note on the sibling "optimistic-lock" test above.
    const uniqueSrc = `usemine-source-${Date.now()}`;
    const job = await createIsolatedCATJob(page, {
      fileName: `usemine-${Date.now()}.txt`,
      content: `${uniqueSrc}\n`,
      qaProfile: null,
    });

    try {
      await job.navigateToEditor(new RegExp(uniqueSrc));
      await page.locator("[data-translation-cell]").first().click();
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      await expect(editor).toBeVisible({ timeout: 5000 });

      const translationsRes = await page.request.get(
        `/api/jobs/${job.jobId}/translations`,
        { headers: { Authorization: `Bearer ${job.token}` } },
      );
      const translationId = (await translationsRes.json()).data?.[0]?.id ?? null;

      // User B saves first.
      await page.request.put(
        `/api/files/${job.fileId}/translations/${translationId}`,
        {
          headers: { Authorization: `Bearer ${job.token}` },
          data: { human_text: "their version (will be overwritten)", editing_time_ms: 0 },
        },
      );

      // fill() for atomic content replacement (see "banner appears" test).
      await editor.click();
      await editor.fill("my edit wins");
      const isMac = process.platform === "darwin";
      await page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter");
      await expect(page.getByTestId("conflict-banner")).toBeVisible({ timeout: 5000 });

      // Click "Use mine" → banner clears, the local edit is persisted.
      await page.getByTestId("conflict-banner-use-mine").click();
      await expect(page.getByTestId("conflict-banner")).not.toBeVisible({ timeout: 3000 });

      // API check: server now holds user A's text. Polled because the banner
      // clears as soon as the save's `.then` fires — the DB commit and the
      // listing endpoint's read can settle a hair late under parallel load.
      // Anti-tautology — if "Use mine" were a no-op, the server would still
      // hold "their version" after the timeout.
      await expect.poll(async () => {
        const detailRes = await page.request.get(
          `/api/files/${job.fileId}/translations/${translationId}`,
          { headers: { Authorization: `Bearer ${job.token}` } },
        );
        return (await detailRes.json()).data?.active_text;
      }, { timeout: 5000, message: "'Use mine' must overwrite the server with the local edit" }).toBe("my edit wins");
    } finally {
      await job.cleanup();
    }
  });

  // Pins that editing the SAME segment twice in a row (single user)
  // does not 409 against the user's own previous write. The next isActive
  // flip re-captures `translation.updated_at` from the cache, so
  // `advanceTranslationInCache` must patch it synchronously in
  // `saveMut.onSuccess`; removing that call from SegmentRow fails this test.
  test("editing the same segment twice in a row does NOT show a false-positive conflict dialog", async ({ page }) => {
    test.setTimeout(60_000);

    // MULTI-segment file: Cmd+Enter advances activeIdx → first segment goes
    // isActive=false. Re-clicking it re-flips isActive=true and re-captures
    // baselineRef from `translation?.updated_at` in the cache. THIS is the
    // path `advanceTranslationInCache` protects. A
    // single-segment file would leave isActive stuck on true and the bug
    // wouldn't reproduce — the test would be tautological.
    //
    // Pre-translate is OFF so the pipeline never writes: a pipeline write
    // bumping `updated_at` could 409 the FIRST user save and mask the
    // invariant entirely.
    const job = await createIsolatedCATJob(page, {
      fileName: `inc050-${Date.now()}.txt`,
      content: "Hello world\nSecond line\nThird line\n",
    });

    // HOLD the translations refetch instead of letting it race. This is the
    // realism switch: in localhost the invalidate-driven refetch always
    // outraces the user's re-activation, so the synchronous
    // `advanceTranslationInCache` helper is bypassed and the test passes
    // whether or not the helper works. With the refetch pinned open, the
    // user's click-back + second save happen entirely inside the window where
    // only the helper can keep the cache fresh. If the helper is broken
    // (silent no-op), the second save 409s.
    //
    // A fixed sleep here makes the window a race the test can lose: if the
    // refetch resolves between `editor.fill`'s select-all and its insert,
    // React re-syncs the editor and the fill appends instead of replacing —
    // "first editsecond edit". A gate the test opens explicitly removes the
    // timing dependency without weakening the invariant.
    //
    // Declared outside the try so `finally` can release it even when an
    // assertion throws mid-window — otherwise cleanup blocks on the handler.
    let releaseRefetch: () => void = () => {};
    const refetchGate = new Promise<void>(resolve => { releaseRefetch = resolve; });
    let holdRefetch = false;

    try {
      await job.navigateToEditor(/Hello world/);

      await page.route("**/api/jobs/*/translations", async (route, req) => {
        if (req.method() === "GET" && holdRefetch) {
          await refetchGate;
        }
        await route.continue();
      });

      const cell = page.locator("[data-translation-cell]").first();
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      const isMac = process.platform === "darwin";

      // Cmd+Enter fires the confirm save: PUT /api/files/{id}/translations/{id}
      // (bare — NOT /draft, NOT /lock). Await the response, not a fixed
      // timeout: under full-suite load the round trip regularly exceeds any
      // hardcoded wait, and this test ALSO holds refetches on purpose
      const confirmSave = /\/api\/files\/\d+\/translations\/\d+$/;

      // FIRST edit + Cmd/Ctrl+Enter — this is the "approval".
      await cell.click();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await expect(editor).toBeFocused();
      await fillEditorExactly(editor, "first edit");
      // Arm the gate now: the save's invalidation is the refetch we want to
      // hold. The page's own initial fetch already completed above.
      holdRefetch = true;
      const [firstSave] = await Promise.all([
        page.waitForResponse(r => confirmSave.test(r.url().split("?")[0]) && r.request().method() === "PUT", { timeout: 15_000 }),
        page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
      ]);
      expect(firstSave.ok(), "first confirm save should succeed").toBe(true);

      // SECOND edit on the SAME (first) segment — click back to segment 1.
      // isActive flips false→true → useEffect re-captures baselineRef from
      // `translation?.updated_at`, which must be the post-first-save value
      // or the save 409s against our own previous write.
      await cell.click();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await expect(editor).toBeFocused();
      await fillEditorExactly(editor, "second edit");
      const [secondSave] = await Promise.all([
        page.waitForResponse(r => confirmSave.test(r.url().split("?")[0]) && r.request().method() === "PUT", { timeout: 15_000 }),
        page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
      ]);
      // The INVARIANT under test: this save must NOT 409 against our own
      // previous write. Asserted on the response, not after a fixed sleep.
      expect(secondSave.status(), "second save must not conflict with our own first save").not.toBe(409);
      expect(secondSave.ok(), "second confirm save should succeed").toBe(true);

      // CORE INVARIANT: NO conflict banner. The user is editing alone — any
      // 409 here is a false-positive against their own previous write.
      await expect(
        page.getByTestId("conflict-banner"),
        "editing the same segment twice in a row must NOT trigger the 'Segment edited by another user' banner",
      ).not.toBeVisible({ timeout: 1000 });

      // Both saves are done — let the held refetch through so nothing in the
      // page (or the cleanup below) waits on it.
      holdRefetch = false;
      releaseRefetch();

      // Defensive: verify the second edit actually persisted on segment 1
      // (not silently dropped, not written to a different row).
      const translationsRes = await page.request.get(
        `/api/jobs/${job.jobId}/translations`,
        { headers: { Authorization: `Bearer ${job.token}` } },
      );
      const translationsBody = await translationsRes.json();
      const firstSegmentId = translationsBody.data?.[0]?.id;
      const detailRes = await page.request.get(
        `/api/files/${job.fileId}/translations/${firstSegmentId}`,
        { headers: { Authorization: `Bearer ${job.token}` } },
      );
      const body = await detailRes.json();
      expect(body.data?.active_text,
        "second edit must have persisted on segment 1 (proves no silent failure under the false-409)").toBe("second edit");
    } finally {
      // Release unconditionally: an assertion failing above must not leave the
      // route handler blocked, or cleanup hangs until the test timeout.
      holdRefetch = false;
      releaseRefetch();
      await job.cleanup();
    }
  });

  // Missing-tags bar + F8 / Alt+F8.
  //
  // Tags are inserted from source via UI affordance, never reconstructed from
  // typed display shorthand like `<1>`. F8 inserts the next missing source
  // tag; Alt+F8 inserts all. The bar shows above the active editor
  // whenever target is missing one or more source tags; clicking a chip
  // inserts that one, "Insert all" inserts the rest.
  //
  // Anti-tautology guarantee: the test deletes the editor's full content
  // (including bpt/ept chips), types plain text, and asserts on the bar's
  // presence + chip count + insertion behavior. If any layer regressed
  // (helper shape, keymap wiring, render conditional, atom-only
  // recomputation via docTick), the test fails.
  // Helper: wipe the editor entirely + type plain text. After pre-translate
  // the target may carry the source's bpt/ept chips already; we want a
  // clean "user has deleted all tags" baseline so the missing-tags bar has
  // something to do.
  async function wipeEditorAndType(
    page: import("@playwright/test").Page,
    editor: import("@playwright/test").Locator,
    text: string,
  ) {
    const isMac = process.platform === "darwin";
    await editor.click();
    await page.keyboard.press(isMac ? "ControlOrMeta+a" : "Control+a");
    await page.keyboard.press("Backspace");
    await editor.type(text);
  }

  test("missing-tags bar appears when target lacks source tags + click chip / F8 insert real chip atoms", async ({ page }) => {
    test.setTimeout(60_000);
    const job = await setupTagTest(page);

    try {
      // Activate the FIRST trans-unit (paired bpt+ept). Pre-translate may
      // have copied the chips into target — wipe to a known-empty baseline.
      const cell = page.locator("[data-translation-cell]").first();
      await cell.click();
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await wipeEditorAndType(page, editor, "Pulsa aqui para continuar.");

      // INVARIANT 1: bar appears with both missing tags, in source order.
      const bar = page.locator('[data-testid="missing-tags-bar"]');
      await expect(bar,
        "missing-tags bar must appear when target is missing source tags").toBeVisible({ timeout: 3000 });
      const chips = bar.locator('[data-testid="missing-tag-chip"]');
      await expect(chips,
        "bar must list one chip per missing source tag (bpt+ept = 2)").toHaveCount(2);
      await expect(chips.nth(0)).toHaveAttribute("data-tag-type", "bpt");
      await expect(chips.nth(1)).toHaveAttribute("data-tag-type", "ept");

      // INVARIANT 2: clicking a bar chip inserts it as a real atom (not plain
      // text). Bar shrinks to the still-missing tag.
      await chips.nth(0).click();
      await expect(editor.locator('span.xliff-tag[data-tag-type="bpt"][data-tag-id="1"]'),
        "clicking bar chip must insert a real `xliffTag` atom in the editor").toHaveCount(1);
      await expect(chips,
        "bar must shrink to remaining missing tags after one is inserted").toHaveCount(1);

      // INVARIANT 3: F8 inserts the next missing tag at cursor.
      await editor.click();
      await page.keyboard.press("F8");
      await expect(editor.locator('span.xliff-tag[data-tag-type="ept"][data-tag-id="1"]'),
        "F8 must insert the next missing source tag").toHaveCount(1);
      await expect(bar,
        "bar must hide when target has all source tags").not.toBeVisible({ timeout: 1500 });
    } finally {
      await job.cleanup();
    }
  });

  // Unified active-segment lifecycle. The SAVE LOGIC is centralized (one
  // `serializeEditor`, one `saveDraft`), and so is the TRANSITION LOGIC: every path that changes `activeSegmentIdx` (row click,
  // Tab, Jump dialog, F&R navigate, click-outside-list, Cmd+Enter) goes
  // through the `switchActiveSegment(newIdx, opts)` chokepoint, which flushes
  // the previous draft before transitioning.
  //
  // These three tests pin that invariant from three different transition
  // triggers. They guard against a new feature calling setActiveSegmentIdx
  // directly and skipping save-on-exit — the lint rule pins it at the syntax
  // level, the tests pin it at the behavior level.
  //
  // Test fixture: 5-line plain TXT file. Plain text means no XLIFF parser
  // dependency, no rich-text edge cases. Each line gets distinct content so the
  // F&R navigate test can target a specific later segment.
  //
  // Uses createIsolatedCATJob (pre-translate OFF by default) so no pipeline
  // writes race user saves and there is nothing to wait for.
  async function uploadInc057File(page: import("@playwright/test").Page) {
    const job = await createIsolatedCATJob(page, {
      fileName: `inc057-${Date.now()}.txt`,
      content: "alpha needs edit\nbeta decoy\nmarker_three findable\ndelta decoy\nepsilon decoy\n",
    });
    await job.navigateToEditor(/alpha needs edit/);
    return job;
  }

  /** Fetch segment 1's stored active_text via the API. Independent of UI re-render. */
  async function getSeg1ActiveText(page: import("@playwright/test").Page, jobId: number, token: string | null): Promise<string | null | undefined> {
    const res = await page.request.get(`/api/jobs/${jobId}/translations`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    const body = await res.json();
    return body.data?.[0]?.active_text;
  }

  /**
   * Resolves with the next draft-save PUT. The 2500ms cap sits under the 3s
   * auto-save debounce, so a response inside it came from the chokepoint flush,
   * not from the auto-save timer.
   */
  function waitForDraftFlush(page: import("@playwright/test").Page) {
    return page.waitForResponse(
      r => /\/api\/files\/\d+\/translations\/\d+\/draft$/.test(r.url().split("?")[0]) && r.request().method() === "PUT",
      { timeout: 2500 },
    );
  }

  // Editing a confirmed segment downgrades it. Flow: approve a
  // segment with Cmd+Enter → edit it → click another row → the status badge
  // and API status show "no longer confirmed" ('needs_review'), so a mutated
  // row never keeps the green badge.
  test("editing a pre-approved segment then clicking another row downgrades status to needs_review", async ({ page }) => {
    test.setTimeout(60_000);

    // Combo test:
    //   1. Cmd+Enter on segment 1 → status='approved'
    //   2. Re-edit segment 1 without re-confirming
    //   3. Click another row → the chokepoint flushes saveDraft
    //   4. SaveTranslationDraft downgrades 'approved' → 'needs_review'
    //
    // Step 1 relies on `confirmAndAdvance.afterSave`, which calls
    // onNavigate('next') → switchActiveSegment → flushDraftRef → saveDraft,
    // which would set status='needs_review', so saveMut.onSuccess clears
    // `dirtyRef.current=false` synchronously. No API-seed shortcut.
    //
    // Anti-tautology layer: if that synchronous clear regresses, the
    // precondition assertion at the end of step 1 (`status === 'approved'`)
    // fails BEFORE we even get to the downgrade invariant.
    const job = await createIsolatedCATJob(page, {
      fileName: `inc058-${Date.now()}.txt`,
      content: "alpha needs edit\nbeta decoy\nmarker_three findable\ndelta decoy\nepsilon decoy\n",
    });

    try {
      await job.navigateToEditor(/alpha needs edit/);

      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      const cells = page.locator("[data-translation-cell]");
      const isMac = process.platform === "darwin";

      // Step 1: pre-approve segment 1 via Cmd+Enter so it starts as 'approved'.
      await cells.nth(0).click();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.fill("first approval");
      // Await BOTH the confirm PUT and the invalidation-driven refetch of the
      // translations list ("Assertion after a mutation races the invalidation
      // refetch" pattern).
      const confirmSave = /\/api\/files\/\d+\/translations\/\d+$/;
      const [confirmResp] = await Promise.all([
        page.waitForResponse(r => confirmSave.test(r.url().split("?")[0]) && r.request().method() === "PUT", { timeout: 15_000 }),
        page.waitForResponse(r => r.url().split("?")[0].endsWith(`/api/jobs/${job.jobId}/translations`) && r.request().method() === "GET", { timeout: 15_000 }),
        page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
      ]);
      expect(confirmResp.ok(), "confirm PUT should succeed").toBe(true);

      // Verify the precondition LANDED via API. Doubles as the anti-
      // tautology check — if the dirty flag stays set this reads 'needs_review'.
      const trsRes = await page.request.get(
        `/api/jobs/${job.jobId}/translations`,
        { headers: { Authorization: `Bearer ${job.token}` } },
      );
      const seg1Id = (await trsRes.json()).data?.[0]?.id;
      expect(seg1Id, "precondition: seg 1 translation id resolves").toBeTruthy();
      const preEditRes = await page.request.get(
        `/api/files/${job.fileId}/translations/${seg1Id}`,
        { headers: { Authorization: `Bearer ${job.token}` } },
      );
      expect(
        (await preEditRes.json()).data?.status,
        "precondition (anti-tautology): seg 1 must STAY 'approved' after Cmd+Enter — the immediate flushDraft must not downgrade it to 'needs_review'",
      ).toBe("approved");

      // Step 2: re-activate segment 1, edit without re-confirming.
      await cells.nth(0).click();
      await expect(editor).toBeVisible({ timeout: 5000 });
      // When re-activating a segment, TipTap loads existing content
      // asynchronously (the editor mounts → React Query hydrates →
      // setContent fires). If editor.fill races that load, fill's atomic
      // select-all-then-type runs on an empty editor and the prior content
      // arrives AFTER, producing concatenated text instead of replaced text.
      // Wait for the previous active_text to actually be inside the editor
      // before filling so the select-all hits the right content.
      await expect(editor).toContainText("first approval", { timeout: 3000 });
      await fillEditorExactly(editor, "second edit no confirm");

      // Step 3: click another row. The chokepoint flushes the draft right
      // away (well inside the 3s auto-save), and SaveTranslationDraft sets
      // status to 'needs_review'.
      const flushSave = /\/api\/files\/\d+\/translations\/\d+\/draft$/;
      await Promise.all([
        page.waitForResponse(r => flushSave.test(r.url().split("?")[0]) && r.request().method() === "PUT", { timeout: 2500 }),
        cells.nth(2).click(),
      ]);

      // CORE INVARIANT: status downgraded from 'approved' to 'needs_review'.
      const postEditRes = await page.request.get(
        `/api/files/${job.fileId}/translations/${seg1Id}`,
        { headers: { Authorization: `Bearer ${job.token}` } },
      );
      const postEditBody = await postEditRes.json();
      expect(
        postEditBody.data?.status,
        "editing a pre-approved segment without re-confirming must downgrade status to 'needs_review'",
      ).toBe("needs_review");

      expect(
        postEditBody.data?.active_text,
        "the edit itself must persist, not just the status change",
      ).toBe("second edit no confirm");

      const seg1Row = cells.nth(0).locator("..");
      await expect(
        seg1Row,
        "row's status badge must show 'draft' (UI label for the needs_review DB status)",
      ).toContainText(/draft/i, { timeout: 3000 });
    } finally {
      await job.cleanup();
    }
  });

  test("clicking another row flushes the in-progress edit on the previous row", async ({ page }) => {
    test.setTimeout(60_000);
    const job = await uploadInc057File(page);

    try {
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      const cells = page.locator("[data-translation-cell]");

      // Activate segment 1, type without confirming.
      await cells.nth(0).click();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.fill("row→row edit");

      // Click segment 2's cell. The document mousedown handler bails for
      // targets inside `segmentScrollRef`, so the flush must come from
      // `switchActiveSegment`, called by the row's onClick.
      await Promise.all([waitForDraftFlush(page), cells.nth(1).click()]);

      // Segment 1's stored `active_text` must reflect the edit.
      expect(
        await getSeg1ActiveText(page, job.jobId, job.token),
        "segment 1's edit must be flushed when the user clicks another row (before auto-save would fire)",
      ).toBe("row→row edit");
    } finally {
      await job.cleanup();
    }
  });

  test("Tab navigation flushes the in-progress edit", async ({ page }) => {
    test.setTimeout(60_000);
    const job = await uploadInc057File(page);

    try {
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      const cells = page.locator("[data-translation-cell]");

      await cells.nth(0).click();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.fill("Tab edit");

      // Tab routes through `goToSegment` → `switchActiveSegment(filteredIndices[idx+1])`,
      // which flushes first.
      await Promise.all([waitForDraftFlush(page), page.keyboard.press("Tab")]);

      expect(
        await getSeg1ActiveText(page, job.jobId, job.token),
        "segment 1's edit must be flushed when the user Tabs to the next segment (before auto-save would fire)",
      ).toBe("Tab edit");
    } finally {
      await job.cleanup();
    }
  });

  test("Find&Replace navigation flushes the in-progress edit on the previously-active segment", async ({ page }) => {
    test.setTimeout(60_000);
    const job = await uploadInc057File(page);

    try {
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      const cells = page.locator("[data-translation-cell]");
      const isMac = process.platform === "darwin";

      // F&R searches `translations.active_text`. Pre-translate is OFF (per
      // createIsolatedCATJob default — keeps the upload pipeline race out of
      // this test), which means active_text starts NULL. Populate seg 2 via
      // API so F&R's "decoy" search has something to match. Doing this BEFORE
      // user A activates seg 1 keeps the test logic linear: this write isn't
      // racing seg 1's save, and seg 1's baseline timestamp is independent.
      const trsRes = await page.request.get(
        `/api/jobs/${job.jobId}/translations`,
        { headers: { Authorization: `Bearer ${job.token}` } },
      );
      const seg2Id = (await trsRes.json()).data?.[1]?.id;
      await page.request.put(
        `/api/files/${job.fileId}/translations/${seg2Id}`,
        {
          headers: { Authorization: `Bearer ${job.token}` },
          data: { human_text: "beta decoy in target", editing_time_ms: 0 },
        },
      );

      await cells.nth(0).click();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.fill("F&R edit");
      // Listen from the edit onward: the flush may fire as focus leaves the
      // editor or when Find navigates, and either must land inside the
      // auto-save debounce that starts with this edit.
      const flushed = waitForDraftFlush(page);

      // Open Find & Replace (Cmd+H), search for the unique source token in
      // segment 2, click Find → `scrollToTranslation` → `switchActiveSegment(1)`,
      // which flushes first.
      await page.keyboard.press(isMac ? "ControlOrMeta+h" : "Control+h");
      const findInput = page.getByPlaceholder("Find...");
      await expect(findInput).toBeVisible({ timeout: 3000 });
      await findInput.fill("decoy");
      await page.getByRole("button", { name: /^Find$/ }).click();

      // The flush response arrives inside the auto-save debounce, so the edit
      // reached the backend via the chokepoint (mousedown-on-input or
      // scrollToTranslation), not via the auto-save timer.
      await flushed;

      expect(
        await getSeg1ActiveText(page, job.jobId, job.token),
        "segment 1's edit must be flushed when F&R navigates to a different segment (before the auto-save timer would fire)",
      ).toBe("F&R edit");
    } finally {
      await job.cleanup();
    }
  });

  // The DRAFT-save twin of the edit-twice test. Pins that a blur-flush draft
  // save advances the cached updated_at (`saveDraft` calls
  // `advanceTranslationInCache`), so re-activating the same segment captures
  // a fresh baseline and the next Cmd+Enter does not 409 against the user's
  // own write. Run QA sits in the flow because it is what sends the user back
  // into the segment; it does not itself touch the translations cache.
  test("blur-flush + Run QA + re-edit on same segment does NOT show false-positive conflict dialog", async ({ page }) => {
    test.setTimeout(60_000);

    // Pre-translate OFF (createIsolatedCATJob default) — there's no pipeline
    // mt_text/tm_origin write to race the user's saves.
    const job = await createIsolatedCATJob(page, {
      onCreated: (id) => projectIds.push(id),
      fileName: `inc056-${Date.now()}.txt`,
      content: "Hello world\nSecond line\nThird line\n",
    });

    let releaseRefetch: () => void = () => {};
    const refetchGate = new Promise<void>(resolve => { releaseRefetch = resolve; });
    let holdRefetch = false;
    // Every API GET, not just the translations one: the QA report's own fetch
    // patches the cache too, and any of those re-renders can re-seed the
    // editor mid-`fill`. Nothing in the held window needs to read.
    await page.route("**/api/**", async (route, req) => {
      if (req.method() === "GET" && holdRefetch) await refetchGate;
      await route.continue();
    });

    try {
      await job.navigateToEditor(/Hello world/);

      const cell = page.locator("[data-translation-cell]").first();
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      const isMac = process.platform === "darwin";

      // STEP 1: activate segment 1, type without confirming (no Cmd+Enter).
      await cell.click();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.fill("first draft");

      // STEP 2: click OUTSIDE the editor on a non-button, non-sidebar, non-segment-list
      // element so CATEditor's mousedown-outside handler fires. This is the path that
      // calls `flushDraftRef.current()` → `saveDraft` → backend write (T0→T1). The
      // target-language chip in the breadcrumb matches none of the handler's
      // bail-out predicates.
      const outsideTarget = page.getByTestId("cat-target-language");
      await expect(outsideTarget).toBeVisible({ timeout: 3000 });
      // Wait for the saveDraft PUT to actually land before moving on — otherwise the
      // backend hasn't bumped to T1 yet and the rest of the test races the network.
      const draftSaveResp = page.waitForResponse(r =>
        /\/api\/files\/\d+\/translations\/\d+\/draft/.test(r.url()) && r.request().method() === "PUT",
      );
      await outsideTarget.click();
      await draftSaveResp;
      await expect(editor, "deactivate must hide the editor (segment becomes inactive)").not.toBeVisible({ timeout: 3000 });

      // STEP 3: Run QA — user-flow fidelity. Doesn't itself touch the translations
      // cache or `translations.updated_at`, but it's the action that, in the user's
      // real-world flow, surfaces the QA error indicator that motivates the re-edit.
      // Wait for the run-qa POST + the follow-up qa-report GET to settle, instead
      // of polling the button label. With pre-translate OFF, all segments have
      // null active_text → QA returns 0 issues → button label becomes "QA (0)".
      // Polling for `QA \(\d+\)` matches that, but the label transition is racy
      // under load (Checking... → Run QA briefly → QA (0)). The two API responses
      // are deterministic.
      // Select by testid, not label: after the blur-flush autosave the button
      // label may already read "QA (N)" (per-segment QA seeds the
      // report), so a /run qa/ name match would find nothing.
      const { reportResp: qaReport } = await runQAAndReadReport(page, page.getByTestId("run-qa-btn"));
      expect(qaReport.ok(), `qa-report fetch failed: ${qaReport.status()}`).toBe(true);

      // STEP 4: click back into segment 1. `setActiveSegmentIdx(0)` → activate
      // useEffect re-captures `baselineUpdatedAtRef` from the cached
      // `translation.updated_at`, which must already be T1.
      //
      // Hold the translations refetch across the re-activate + fill window.
      // `fill` is atomic against the caret, not against a late `setContent`: a
      // refetch landing between its select-all and its insert re-syncs the
      // editor and the two edits concatenate (testing.md, "CAT/TM segment-editor
      // test types via keyboard.type"). The gate is a promise this test releases,
      // not a sleep, so the window is bounded by these statements at any worker
      // count. Declared outside the try so `finally` always releases it.
      holdRefetch = true;
      await cell.click();
      await expect(editor).toBeVisible({ timeout: 5000 });
      // As in the status-downgrade test — wait for the prior saveDraft content to
      // appear in the editor before fill, otherwise fill races the load and
      // concatenates "first draft" + "second edit attempt".
      await expect(editor).toContainText("first draft", { timeout: 3000 });

      // STEP 5: edit + Cmd+Enter. Succeeds only if baselineRef matches the backend;
      // otherwise it 409s against the user's own flush write.
      await fillEditorExactly(editor, "second edit attempt");
      holdRefetch = false;
      releaseRefetch();
      // Await the confirm PUT rather than sleeping: under full-suite load a
      // fixed sleep lets the persistence assertion below read the pre-edit row,
      // which looks like a lost save rather than a test race. Same regex as
      // the edit-twice test.
      const confirmSaveResp = page.waitForResponse(
        r => /\/api\/files\/\d+\/translations\/\d+$/.test(r.url().split("?")[0]) &&
          r.request().method() === "PUT",
        { timeout: 15_000 },
      );
      await page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter");
      await confirmSaveResp;

      // CORE INVARIANT: NO conflict banner. The user is editing alone — a 409 here
      // is a false-positive against their own prior blur-flush write.
      await expect(
        page.getByTestId("conflict-banner"),
        "a deactivate-flush + re-edit on the same segment must NOT trigger 'Segment edited by another user'",
      ).not.toBeVisible({ timeout: 1000 });

      // Defensive: verify the second edit actually persisted on segment 1.
      const detailRes = await page.request.get(
        `/api/jobs/${job.jobId}/translations`,
        { headers: { Authorization: `Bearer ${job.token}` } },
      );
      const detailBody = await detailRes.json();
      const seg1 = detailBody.data?.[0];
      expect(seg1?.active_text,
        "the second edit must have persisted (proves no silent failure under the false-409)").toBe("second edit attempt");
    } finally {
      holdRefetch = false;
      releaseRefetch();
    }
  });

  // An unsaved tag deletion in the active segment survives scrolling
  // it out of `react-virtual`'s overscan window. Auto-save is debounced 3s, so
  // an unmount would destroy the in-memory edit and a remount would re-read
  // the original tags from the cache. The virtualizer's `rangeExtractor`
  // always includes the active index, so the active row stays mounted.
  test("deleting a tag in the active segment survives virtualization unmount/remount", async ({ page }) => {
    test.setTimeout(45_000);

    // 50 trans-units — far more than overscan=10 so scrolling actually unmounts
    // segment 1's SegmentRow if the active row were not kept. Segment 1 holds a
    // paired bpt/ept in BOTH source AND target so the editor renders a chip
    // immediately on activation (no pre-translate dependency).
    const SEG_COUNT = 50;
    const transUnits: string[] = [];
    transUnits.push(`      <trans-unit id="1">
        <source>Click <bpt id="1">&lt;b&gt;</bpt>here<ept id="1">&lt;/b&gt;</ept> to continue</source>
        <target>Haga clic <bpt id="1">&lt;b&gt;</bpt>aqui<ept id="1">&lt;/b&gt;</ept> para continuar</target>
      </trans-unit>`);
    for (let i = 2; i <= SEG_COUNT; i++) {
      transUnits.push(`      <trans-unit id="${i}"><source>Filler segment number ${i} for virtualization scroll.</source></trans-unit>`);
    }
    const xliff = `<?xml version="1.0" encoding="utf-8"?>
<xliff version="1.2" xmlns="urn:oasis:names:tc:xliff:document:1.2">
  <file source-language="en" target-language="es" datatype="plaintext" original="inc055.xliff">
    <body>
${transUnits.join("\n")}
    </body>
  </file>
</xliff>`;

    const job = await createIsolatedCATJob(page, {
      fileName: `inc055-${Date.now()}.xliff`,
      content: xliff,
      contentType: "application/xliff+xml",
    });
    try {
      await job.navigateToEditor(/Click/);

      // Activate segment 1 — the only one with target tags.
      const cell = page.locator("[data-translation-cell]").first();
      await cell.click();
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      await expect(editor).toBeVisible({ timeout: 5000 });

      // Precondition: editor target shows at least one xliff-tag chip from the
      // file's `<target>`. This pins the test's setup — without a chip there's
      // nothing to delete and the test would silently pass tautologically.
      const editorChips = editor.locator('span.xliff-tag');
      await expect(editorChips.first(),
        "setup: editor must show at least one tag chip before deletion (else the test is a no-op)").toBeVisible({ timeout: 5000 });
      const beforeChipCount = await editorChips.count();
      expect(beforeChipCount, "setup: at least the bpt chip must be present").toBeGreaterThanOrEqual(1);

      // Delete: select-all + Backspace + replacement plain text. Atomic chips
      // are part of the doc — Meta+a covers them, Backspace clears them.
      const isMac = process.platform === "darwin";
      await editor.click();
      await page.keyboard.press(isMac ? "ControlOrMeta+a" : "Control+a");
      await page.keyboard.press("Backspace");
      await editor.type("Plain replacement no chips", { delay: 5 });
      await expect(editorChips,
        "delete worked: editor has no xliff-tag chips after select-all+backspace").toHaveCount(0, { timeout: 2000 });

      // Scroll the segment list to the bottom — segment 1 leaves the overscan
      // window (10) and React unmounts it.
      const container = page.locator('.overflow-y-auto.overflow-x-hidden').first();
      await container.evaluate(el => { el.scrollTop = el.scrollHeight; });
      // Wait for the scroll to settle so the virtualizer prunes off-screen rows.
      // We assert via segment 50's source text appearing — proves we scrolled
      // far enough that the bottom rows are mounted (and by the same token,
      // segment 1 is well outside the overscan window).
      await expect(page.getByText(`Filler segment number ${SEG_COUNT}`),
        "scroll precondition: bottom segment must be visible (proves scroll moved past overscan)").toBeVisible({ timeout: 5000 });

      // Scroll back to top — segment 1 remounts.
      await container.evaluate(el => { el.scrollTop = 0; });
      const remountedEditor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      await expect(remountedEditor).toBeVisible({ timeout: 5000 });

      // CORE INVARIANT: the deleted tag must STAY deleted across the scroll.
      // The cached `translation.active_rich_text` still holds bpt+ept (autosave
      // is debounced, no save fired).
      await expect(
        remountedEditor.locator('span.xliff-tag'),
        "tag deleted before scroll must remain deleted after virtualization remount of the active row",
      ).toHaveCount(0, { timeout: 3000 });
    } finally {
      await job.cleanup();
    }
  });

  test("Alt+F8 inserts ALL remaining missing source tags in one shot", async ({ page }) => {
    test.setTimeout(60_000);
    const job = await setupTagTest(page);

    try {
      const cell = page.locator("[data-translation-cell]").first();
      await cell.click();
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await wipeEditorAndType(page, editor, "Pulsa aqui para continuar.");

      const bar = page.locator('[data-testid="missing-tags-bar"]');
      await expect(bar.locator('[data-testid="missing-tag-chip"]'),
        "precondition — bar shows both missing tags").toHaveCount(2);

      // Alt+F8 → all missing tags inserted, bar disappears.
      await editor.click();
      await page.keyboard.press("Alt+F8");
      await expect(editor.locator('span.xliff-tag[data-tag-type="bpt"][data-tag-id="1"]'),
        "Alt+F8 must insert the bpt").toHaveCount(1);
      await expect(editor.locator('span.xliff-tag[data-tag-type="ept"][data-tag-id="1"]'),
        "Alt+F8 must insert the ept too (single keystroke covers all)").toHaveCount(1);
      await expect(bar,
        "bar must hide after Alt+F8 inserts everything").not.toBeVisible({ timeout: 1500 });
    } finally {
      await job.cleanup();
    }
  });

  // Run QA — full user flow.
  //
  // The translator's canonical pre-ship loop:
  //   1. Run QA on a job → see issue count + per-row badge dots.
  //   2. Open a flagged segment, fix it, Cmd+Enter.
  //   3. Run QA again → issue count drops by 1, fixed row's badge is gone.
  //
  // What this test pins (the parts that have NO other coverage):
  //   - POST /run-qa + GET /qa-report wired correctly (toolbar → toast → state).
  //   - Button label transitions: "Run QA" → "Checking..." → "QA (N)".
  //   - The circular severity badge (`[data-testid="qa-issue-badge"]`) renders
  //     on each flagged row, with `data-severity` matching the rule's severity.
  //   - Re-running QA after a real fix actually reduces the count (anti-tautology
  //     proof: if the fix didn't take or the second Run QA was a no-op, both
  //     counts would be equal — assertion catches it).
  //
  // Test fixture:
  //   - 3-segment plain-text file, pre-translate OFF (createIsolatedCATJob default)
  //     → all targets start empty → `empty_target` rule (Standard QA seed,
  //     severity=error) fires for all 3.
  //   - Fix one segment via Cmd+Enter → that segment's `empty_target` clears
  //     → second Run QA shows 2 issues, not 3.
  test("Run QA: shows issue count + circular badges; fixing a segment + re-running drops the count and clears that row's badge", async ({ page }) => {
    test.setTimeout(60_000);

    const job = await createIsolatedCATJob(page, {
      onCreated: (id) => projectIds.push(id),
      fileName: `qa-flow-${Date.now()}.txt`,
      content: "alpha source line\nbeta source line\ngamma source line\n",
    });

    await job.navigateToEditor(/alpha source line/);

    const runQABtn = page.getByRole("button", { name: /^run qa$/i });
    await expect(runQABtn,
      "precondition: Run QA button shows initial 'Run QA' label (no qaReport yet)",
    ).toBeVisible({ timeout: 5000 });

    // STEP 1 — first Run QA. Capturing both responses is more deterministic
    // than polling the button label, which transitions "Run QA" →
    // "Checking..." → "QA (N)" within ~100ms under load.
    const { runResp: runQAResp1, reportResp: qaReportResp1 } = await runQAAndReadReport(page, runQABtn);
    expect(runQAResp1.ok(), `first /run-qa: ${runQAResp1.status()}`).toBe(true);
    expect(qaReportResp1.ok(), `first /qa-report: ${qaReportResp1.status()}`).toBe(true);

    // 3 empty targets → 3 `empty_target` issues. Standard QA profile has
    // empty_target enabled with severity=error (cmd/seed).
    const firstReport = await qaReportResp1.json();
    expect(
      firstReport.data?.summary?.error,
      "first Run QA: 3 untranslated segments must produce 3 empty_target errors",
    ).toBe(3);

    // Button label flips to "QA (3)" — CATEditor sums error + warning. All 3
    // are errors, 0 warnings.
    await expect(
      page.getByRole("button", { name: /^QA \(3\)$/ }),
      "first Run QA: button label must reflect total count (3 errors)",
    ).toBeVisible({ timeout: 3000 });

    // Toast confirmation (the user's primary feedback signal).
    await expect(
      page.getByText(/QA completed: 3 issue/i),
      "first Run QA: toast must announce 3 issues",
    ).toBeVisible({ timeout: 3000 });

    // CIRCULAR BADGES: every row must show one error-severity dot. The badge
    // selector is `[data-testid="qa-issue-badge"][data-severity="error"]`
    // (SegmentRow's `<span class="rounded-full ...">` dot).
    // Anti-tautology: if the badge testid was missing or the qaResultsByTranslation
    // mapping broke, count would be 0 instead of 3.
    const errorBadges = page.locator('[data-testid="qa-issue-badge"][data-severity="error"]');
    await expect(
      errorBadges,
      "first Run QA: must render 3 red circular badges (one per flagged row)",
    ).toHaveCount(3, { timeout: 5000 });

    // STEP 2 — fix segment 1 via Cmd+Enter (the canonical "approve" gesture).
    // This test does not catch a status regression (Cmd+Enter immediately
    // followed by a draft-flush downgrading status): empty_target only checks
    // text presence, not status. It pins the QA loop.
    const cells = page.locator("[data-translation-cell]");
    await cells.nth(0).click();
    const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
    await expect(editor).toBeVisible({ timeout: 5000 });
    await editor.fill("alpha translated");
    const isMac = process.platform === "darwin";
    // The confirm also re-checks seg 1 on its own (POST /translations/{id}/qa)
    // and stores the result. That call is dropped until the re-run is done,
    // so only the second Run QA can clear seg 1's finding.
    let recheckDropped = 0;
    await page.route("**/api/translations/*/qa", route => { recheckDropped++; return route.abort(); });
    // Cmd+Enter saves seg 1 and advances to seg 2. Awaiting the save is what
    // step 3 needs: its full Run QA reads the stored text.
    const [confirmResp] = await Promise.all([
      page.waitForResponse(
        r => /\/api\/files\/\d+\/translations\/\d+$/.test(r.url().split("?")[0]) && r.request().method() === "PUT",
        { timeout: 15_000 },
      ),
      page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
    ]);
    expect(confirmResp.ok(), `confirm save of seg 1: ${confirmResp.status()}`).toBe(true);
    await expect.poll(() => recheckDropped, { message: "the confirm's re-check of seg 1 was dropped" }).toBeGreaterThan(0);
    const storedBefore = await page.request.get(`/api/jobs/${job.jobId}/qa-report`, { headers: await authHeaders(page) });
    expect((await storedBefore.json()).data?.summary?.error,
      "before the re-run, the stored report still counts seg 1",
    ).toBe(3);

    // STEP 3 — second Run QA. Same wiring as step 1.
    const runQABtn2 = page.getByRole("button", { name: /^QA \(\d+\)$/ });
    await expect(runQABtn2,
      "step 3: button label still shows previous QA count before re-run",
    ).toBeVisible({ timeout: 3000 });
    const { reportResp: qaReportResp2 } = await runQAAndReadReport(page, runQABtn2);
    const secondReport = await qaReportResp2.json();
    await page.unroute("**/api/translations/*/qa");

    // CORE INVARIANT: the count dropped from 3 → 2, and only the job-wide
    // run can have done it: if the fix didn't persist, or the run changed
    // nothing, this would still be 3.
    expect(
      secondReport.data?.summary?.error,
      "second Run QA: fixed seg 1 → 2 errors remain (was 3 before fix)",
    ).toBe(2);

    // Button label updates: "QA (2)".
    await expect(
      page.getByRole("button", { name: /^QA \(2\)$/ }),
      "second Run QA: button label must reflect updated count",
    ).toBeVisible({ timeout: 3000 });

    // Circular badges count drops to 2.
    await expect(
      errorBadges,
      "second Run QA: only 2 red badges should remain (seg 1 cleared)",
    ).toHaveCount(2, { timeout: 5000 });

    // CIRCULAR BADGE on seg 1 specifically must be GONE. Scope by walking
    // up from the seg 1 [data-translation-cell] to its parent row, then
    // looking for the badge inside. Anti-tautology: if `qaResultsByTranslation`
    // is keyed wrong or stale, seg 1 might still show a badge despite the
    // count-2 assertion above (e.g., if a different segment's badge was
    // removed by accident).
    const seg1Row = cells.nth(0).locator("..");
    await expect(
      seg1Row.locator('[data-testid="qa-issue-badge"]'),
      "seg 1 row's badge must be cleared after fix + re-run (the user-visible 'I fixed it' signal)",
    ).toHaveCount(0, { timeout: 3000 });
  });

  // QA runs after autosave (edited segment only), no manual Run QA
  // needed; and the bidirectional number check flags a number
  // that appears ONLY in the translation (as a non-blocking WARNING).
  //
  // Flow: source line has no numbers. Type a target that introduces "999",
  // DON'T press Cmd+Enter, DON'T click Run QA — just wait for the 3s autosave.
  // The draft save's onSuccess POSTs /translations/{id}/qa (recompute+persist),
  // which returns a `number_added` warning and paints the row's warning badge.
  //
  // Anti-tautology: the badge appears only if (a) the draft save re-runs QA
  // and (b) CheckNumbers flags target-only numbers.
  test("autosave re-runs QA for the edited segment and flags a target-only number (no manual Run QA)", async ({ page }) => {
    test.setTimeout(60_000);

    const job = await createIsolatedCATJob(page, {
      fileName: `autosave-qa-${Date.now()}.txt`,
      content: "alpha source line\nbeta source line\n",
    });

    try {
      await job.navigateToEditor(/alpha source line/);

      // No QA has run yet → no badges on the grid.
      const warnBadges = page.locator('[data-testid="qa-issue-badge"][data-severity="warning"]');
      await expect(warnBadges, "precondition: no QA badges before any run").toHaveCount(0, { timeout: 5000 });

      const cells = page.locator("[data-translation-cell]");
      await cells.nth(0).click();
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      await expect(editor).toBeVisible({ timeout: 5000 });

      // Arm the per-segment QA POST wait BEFORE typing so we can't miss it.
      const qaPost = page.waitForResponse(
        r => /\/translations\/\d+\/qa$/.test(r.url()) && r.request().method() === "POST",
        { timeout: 20000 },
      );

      // Type a target with a number the source doesn't have. Do NOT confirm.
      await editor.fill("alpha traducido 999");

      // The 3s autosave fires → PUT /draft → onSuccess POSTs the QA recompute.
      const resp = await qaPost;
      expect(resp.ok(), `per-segment QA POST: ${resp.status()}`).toBe(true);
      const body = await resp.json();
      const checks: string[] = (body.data ?? []).map((r: { check_type: string }) => r.check_type);
      expect(
        checks,
        "autosave QA must flag the target-only number via number_added",
      ).toContain("number_added");

      // The warning badge paints on the edited row — WITHOUT clicking Run QA.
      await expect(
        warnBadges,
        "edited row shows a QA warning badge purely from autosave",
      ).toHaveCount(1, { timeout: 5000 });
      // The toolbar count reflects the autosave-discovered issue even though the
      // user never clicked Run QA — proof the per-segment refresh seeds the
      // report (the button starts at "Run QA" on a fresh job).
      await expect(
        page.getByRole("button", { name: /^QA \(1\)$/ }),
        "Run QA button count updates from autosave alone (no manual run)",
      ).toBeVisible({ timeout: 3000 });
    } finally {
      await job.cleanup();
    }
  });

  // Concurrent saveDraft race. The 3s auto-save timer can put one
  // PUT /draft in flight; if the user clicks another row mid-roundtrip,
  // switchActiveSegment fires a second saveDraft via flushDraftRef. Both see
  // `dirtyRef.current === true` (the dirty gate doesn't help) and would send
  // the same `expected_updated_at`, so the second would 409 with a false
  // "Saved from another tab or session" banner.
  //
  // The test forces the race by injecting latency on the first PUT /draft.
  // The in-flight guard makes the second call await the first's promise,
  // re-check dirty (false after the first success) and no-op.
  test("concurrent saveDraft (auto-save in flight + click another row) does NOT show false-positive conflict banner", async ({ page }) => {
    test.setTimeout(60_000);

    const job = await createIsolatedCATJob(page, {
      fileName: `concurrent-draft-${Date.now()}.txt`,
      content: "alpha source\nbeta source\ngamma source\n",
    });

    try {
      await job.navigateToEditor(/alpha source/);

      // Slow the FIRST PUT /draft so the SECOND has time to start while it's
      // in flight. After the first, network resumes at normal speed so the
      // second can complete cleanly (we want to observe the race, not block
      // forever). 2500ms is comfortably longer than the typical inter-request
      // delay (~50–100ms under parallel load) and shorter than the test's
      // overall budget.
      let draftCount = 0;
      await page.route("**/translations/*/draft", async (route, req) => {
        if (req.method() === "PUT") {
          draftCount++;
          if (draftCount === 1) {
            await new Promise(r => setTimeout(r, 2500));
          }
        }
        await route.continue();
      });

      const cells = page.locator("[data-translation-cell]");
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();

      // Activate seg 1, type. dirtyRef=true; auto-save timer set for t≈3000ms.
      await cells.nth(0).click();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.fill("first edit text");

      // Wait for the auto-save timer to fire (3s debounce): exactly one PUT
      // /draft has reached the route handler, so it is the one held in flight.
      await expect.poll(() => draftCount, { timeout: 5000 }).toBe(1);

      // The first PUT /draft is now in flight (artificially delayed by the
      // route handler). User clicks another row → switchActiveSegment →
      // flushDraftRef → saveDraft (the SECOND call). Without the in-flight
      // guard, this second saveDraft would fire its own PUT racing the first.
      await cells.nth(1).click();

      // Allow both PUTs (delayed first + immediate second if guard is
      // missing) to land server-side and the 409 response to flow back.
      // ConflictBanner is gated on `isActive` in SegmentRow, so a 409 sets
      // conflictPayload on Row 1 silently while Row 1 is inactive. It only
      // surfaces when the user comes BACK to Row 1.
      await page.waitForTimeout(3500);

      // STEP 4 — user comes back to Row 1. isActive=true, conflictPayload
      // set by the 409 from the racing first saveDraft → banner renders.
      await cells.nth(0).click();

      // CORE INVARIANT: no conflict banner. The user is alone — every 409
      // here is a false-positive caused by the saveDraft self-race.
      // Anti-tautology: with the guard removed, the SECOND PUT fires at
      // the same baseline as the first → server returns 409 → banner
      // appears as soon as the user activates the row again.
      await expect(
        page.getByTestId("conflict-banner"),
        "concurrent saveDraft race must NOT trigger a false 'tab or session' banner",
      ).not.toBeVisible({ timeout: 2000 });

      // Defensive: at least the auto-save PUT must have fired (otherwise
      // the test never reached the race window — pure tautology). With the
      // guard working, only 1 PUT fires (the second saveDraft from
      // switchActiveSegment awaits the in-flight one + re-checks dirty +
      // returns early). Without the guard, 2 fire and the second 409s.
      // We only assert ≥ 1 here because asserting === 2 would *require*
      // the bug to exist for the test to pass — the banner-not-visible
      // assertion above is the real invariant.
      expect(draftCount,
        "anti-tautology: at least the auto-save PUT must have fired (otherwise we never even tried the race)",
      ).toBeGreaterThanOrEqual(1);

      // Defensive: the seg 1 edit actually persisted.
      const trsRes = await page.request.get(
        `/api/jobs/${job.jobId}/translations`,
        { headers: { Authorization: `Bearer ${job.token}` } },
      );
      const seg1 = (await trsRes.json()).data?.[0];
      expect(seg1?.active_text,
        "seg 1 edit must have persisted (the first PUT, even racing the second)",
      ).toBe("first edit text");
    } finally {
      await job.cleanup();
    }
  });

  // Cross-race — Cmd+Enter (saveMut) racing an in-flight saveDraft. The shared
  // `useInFlightGuard` primitive serialises saveDraft-vs-saveDraft (test
  // above); THIS test pins the OTHER cross-race the primitive must cover: an auto-save's PUT /draft is
  // in flight, the user presses Cmd+Enter (saveMut → PUT /human) before it
  // returns, both share `expected_updated_at = T0`, and the second 409s.
  //
  // Repro shape mirrors the concurrent saveDraft test: inject latency on PUT /draft, type, wait
  // for the auto-save timer to fire, then Cmd+Enter (saveMut). Without the
  // shared guard saveMut would race; with it, saveMut awaits saveDraft's
  // sentinel, baseline advances to T1, saveMut sends T1, server matches, no
  // 409. After Cmd+Enter advances to the next segment we click back so any
  // 409-set conflictPayload would render (banner is gated on isActive —
  // exactly the user-observable surface of the bug).
  test("concurrent saveMut (Cmd+Enter) + saveDraft (auto-save in flight) does NOT show false-positive conflict banner", async ({ page }) => {
    test.setTimeout(60_000);

    const job = await createIsolatedCATJob(page, {
      fileName: `cross-race-${Date.now()}.txt`,
      content: "alpha source\nbeta source\ngamma source\n",
    });

    try {
      await job.navigateToEditor(/alpha source/);

      // Slow the FIRST PUT /draft by 2.5s. The auto-save timer fires it; we
      // then issue Cmd+Enter (saveMut → PUT /human) BEFORE the draft returns.
      // Without the shared guard, both PUTs share baseline=T0 → second 409s.
      let draftCount = 0;
      await page.route("**/translations/*/draft", async (route, req) => {
        if (req.method() === "PUT") {
          draftCount++;
          if (draftCount === 1) {
            await new Promise(r => setTimeout(r, 2500));
          }
        }
        await route.continue();
      });

      const cells = page.locator("[data-translation-cell]");
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      const isMac = process.platform === "darwin";

      // Activate seg 1, type → dirty=true, auto-save timer set for t≈3000ms.
      await cells.nth(0).click();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.fill("first edit text");

      // Wait for the auto-save timer to fire (3s debounce): exactly one PUT
      // /draft has reached the route handler, which holds it in flight.
      await expect.poll(() => draftCount, { timeout: 5000 }).toBe(1);

      // While saveDraft's PUT is in flight, the user presses Cmd+Enter →
      // saveMut. The shared guard makes saveMut await saveDraft's sentinel
      // before issuing PUT /human.
      await page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter");

      // Allow both PUTs (delayed first /draft + the /human that follows)
      // plus any 409 response + banner render time. Cmd+Enter advances to
      // seg 2 → seg 1 deactivates → conflict banner is hidden by the
      // isActive gate even if conflictPayload was set. Click back to seg 1
      // so a hypothetical false-409 surfaces.
      await page.waitForTimeout(3500);
      await cells.nth(0).click();

      // CORE INVARIANT: no banner. Same anti-tautology rationale as the
      // saveDraft-vs-saveDraft test: route saveMut around `guardSave` (a
      // direct mutationFn) and this test fails RED with the banner visible.
      await expect(
        page.getByTestId("conflict-banner"),
        "saveMut+saveDraft cross-race must NOT trigger a false 'tab or session' banner",
      ).not.toBeVisible({ timeout: 2000 });

      // Defensive: at least the auto-save's PUT actually fired. Without
      // this, the test is a tautology (race never even attempted).
      expect(draftCount,
        "anti-tautology: at least the auto-save PUT /draft must have fired",
      ).toBeGreaterThanOrEqual(1);
    } finally {
      await job.cleanup();
    }
  });

  // Stress loop covering the FULL save surface:
  //   gesture 0 — row-click away (blur-flush via switchActiveSegment).
  //   gesture 1 — Cmd+Enter (saveMut → propagation → onNavigate to next).
  //   gesture 2 — click + come-back-without-confirm (the user's exact symptom).
  //
  // False-409 classes covered here and what guards each:
  //   - concurrent saveDraft race      → guardSave
  //   - dirtyRef stale on Cmd+Enter    → applySegmentSaveSuccess clears synchronously
  //   - empty PUT /draft on flush      → dirty gate
  //   - propagation cache desync       → patchPropagatedSiblings
  //   - saveMut+saveDraft cross-race            → shared guardSave across both
  //
  // File contains a DUPLICATE source line ("dup source") on rows 1+5 so the
  // Cmd+Enter cycles actually exercise the propagation path (otherwise propagation
  // no-ops and the patchPropagatedSiblings helper is never called). That makes
  // this test a regression net for propagation cache desync too.
  test("stress: 20 cycles across all save gestures (Cmd+Enter + blur-flush + come-back) must never trigger false-409 banner", async ({ page }) => {
    test.setTimeout(120_000);

    const job = await createIsolatedCATJob(page, {
      fileName: `stress-full-${Date.now()}.txt`,
      // Rows 0 and 4 share the same source so Cmd+Enter on either fires
      // propagation onto the other — exercises the patchPropagatedSiblings
      // path. Rows 1, 2, 3 have unique sources so propagation no-ops on those
      // cycles; that variation is by design.
      content: "dup source\nbeta source\ngamma source\ndelta source\ndup source\n",
    });

    try {
      await job.navigateToEditor(/dup source/);

      const cells = page.locator("[data-translation-cell]");
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      const banner = page.getByTestId("conflict-banner");
      const isMac = process.platform === "darwin";

      // Activate a row by clicking its SOURCE cell, never the translation cell.
      // The whole inactive row has onClick=onActivate, but on activation the
      // translation cell sprouts a toolbar whose first control is the "🔓 Lock"
      // toggle. Clicking the translation cell to activate is a Playwright
      // fast-click hazard: the click point is computed on the still-inactive
      // (toolbar-less) cell, but the toolbar renders under the cursor before
      // the click lands, so the click occasionally hits Lock → the segment
      // locks server-side → the editor goes read-only → the next fill throws
      // "not an editable element". The source cell is read-only context with
      // no interactive controls, so activating through it can't misfire. (A
      // real user never hits this: their click completes on the static cell
      // before the toolbar exists.)
      const activate = (idx: number) =>
        cells.nth(idx).locator("xpath=preceding-sibling::div[1]").click();

      // Each cycle activates a row, types, and uses one of three exit gestures.
      // Mod operators rotate src/dst/gesture deterministically so the loop
      // exercises adjacent + far navigations and all three paths.
      //
      // Sonner toaster is bottom-right (web/src/main.tsx). Cmd+Enter on a
      // duplicate-source row fires "Propagated to N segments" toasts which
      // stack in the same screen region as the bottom segment cells and
      // intercept clicks. Make toasts non-interactive via `pointer-events:none`
      // injected once at the start of the test — they remain visible (so the
      // user-facing UX is unchanged) but Playwright's actionability check
      // can hit-test through them. Less invasive than removing DOM nodes
      // (which interferes with React reconciliation) and doesn't require
      // changing Sonner's prod config.
      await page.addStyleTag({
        content: '[data-sonner-toaster], [data-sonner-toast] { pointer-events: none !important; }',
      });

      // 20 cycles is the regression-net sweet spot: covers each (gesture,
      // srcIdx) permutation at least once, exercises propagation 3+ times,
      // stays inside the test budget, and keeps parallel-load flake rate to
      // background noise. Higher counts (30+) occasionally surface rare
      // microtask-ordering races worth investigating but those have their
      // own targeted tests; the stress loop's job is to catch *gross*
      // regressions in the save lifecycle, not to be the densest possible
      // probe.
      const CYCLES = 20;

      // The banner only renders on the active row, so a 409 answering a row the
      // loop already left would never show. Count the responses themselves.
      const conflicts: string[] = [];
      page.on("response", r => {
        if (r.status() === 409 && r.url().includes("/translations/")) {
          conflicts.push(`${r.request().method()} ${r.url()}`);
        }
      });

      for (let i = 0; i < CYCLES; i++) {
        const srcIdx = i % 5;
        const dstIdx = (i + 1 + (i % 3)) % 5;
        const gesture = i % 3;

        await activate(srcIdx);
        // Editable, not merely visible: under load the editor can be momentarily
        // present-but-contenteditable=false while the activated row's ProseMirror
        // finishes mounting. Filling then throws "not an editable element".
        await expect(editor).toBeEditable({ timeout: 5000 });
        await editor.fill(`cycle ${i} edit`);

        if (gesture === 0) {
          await activate(dstIdx);
        } else if (gesture === 1) {
          await page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter");
        } else {
          await activate(dstIdx);
          await page.waitForTimeout(150);
          await activate(srcIdx);
        }

        // Banner invariant. Tight timeout — a 409 surfaces within ~500ms.
        await expect(
          banner,
          `cycle ${i} (gesture ${gesture}, src=${srcIdx} dst=${dstIdx}): conflict banner must NOT appear in single-tab session`,
        ).not.toBeVisible({ timeout: 500 });
      }

      // Extra paranoia: come back to seg 0 once more — that's the row whose
      // baseline gets bumped by every Cmd+Enter cycle on seg 4 (and vice
      // versa) via propagation. If patchPropagatedSiblings ever drifted,
      // re-activating it last would still be at risk; assert clean.
      await activate(0);
      await expect(editor).toBeVisible({ timeout: 5000 });
      await expect(
        banner,
        "post-stress: re-activating seg 0 (a propagation target) must still NOT show banner",
      ).not.toBeVisible({ timeout: 1000 });

      expect(conflicts, "no save in the stress loop may be answered with 409").toEqual([]);
    } finally {
      await job.cleanup();
    }
  });

  // Auto-propagation cache desync (cross-row variant of the
  // single-row false-409 cases above).
  //
  // Repro: file has TWO segments sharing the same source text. User edits
  // seg 1, Cmd+Enter — saveMut.onSuccess fires `propagateTranslation` which
  // bumps seg 2's `updated_at` server-side (but NOT `edited_by`, which stays
  // at whatever it was). The invalidation refetch is async, so if the user
  // clicks seg 2 before it lands, seg 2's activate effect captures
  // `baselineRef` from the cache — which the propagate response must already
  // have patched (patchPropagatedSiblings), or the next save 409s.
  //
  // Propagation does not change edited_by, so a false banner's copy varies
  // ("Server has a different version" or "tab or session"); the test
  // asserts only that no banner appears.
  //
  // Repro is widened by injecting latency on `/translations` GET so the
  // refetch lands AFTER the user's re-activation — same pattern as the
  // edit-twice test for the single-row variant.
  test("auto-propagation does NOT leave sibling rows with stale baseline → false-409 banner", async ({ page }) => {
    test.setTimeout(60_000);

    // Two segments with the SAME source text — propagation will fire and
    // bump seg 2 when seg 1 is approved. The third segment is just filler
    // so the editor doesn't auto-advance off the file.
    const job = await createIsolatedCATJob(page, {
      fileName: `propagate-race-${Date.now()}.txt`,
      content: "duplicate source line\nduplicate source line\nfiller line\n",
    });

    try {
      await job.navigateToEditor(/duplicate source line/);

      // Inject latency on the translations GET so the propagate-driven
      // invalidation refetch lands AFTER the user re-activates seg 2 — the
      // race window. The edit-twice test uses the same trick on
      // `/api/jobs/*/translations` for the single-row case.
      await page.route("**/api/jobs/*/translations", async (route, req) => {
        if (req.method() === "GET") {
          await new Promise(r => setTimeout(r, 1500));
        }
        await route.continue();
      });

      const cells = page.locator("[data-translation-cell]");
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      const isMac = process.platform === "darwin";

      // Step 1: activate seg 1, type, Cmd+Enter → saveMut → onSuccess
      // fires propagateTranslation → backend bumps seg 2's updated_at.
      // Cmd+Enter advances to seg 2 (next index).
      await cells.nth(0).click();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.fill("propagated translation");
      await page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter");

      // Wait for the propagate request to settle so the backend HAS bumped
      // seg 2 by the time we re-activate it. The frontend invalidate fires
      // here too, but the throttled GET (1500ms above) ensures the refetch
      // lands AFTER our re-activation — keeping the cache stale.
      await page.waitForResponse(
        r => r.url().includes("/propagate") && r.request().method() === "POST",
        { timeout: 5000 },
      );

      // Step 2: click seg 2 (which is what Cmd+Enter advanced to anyway,
      // but be explicit). isActive flips → activate effect captures
      // baselineRef from the stale cache. The throttled GET window is what
      // keeps this stale.
      await cells.nth(1).click();
      await expect(editor).toBeVisible({ timeout: 5000 });

      // Step 3: edit seg 2 + Cmd+Enter. The propagate response patches seg 2's
      // cache row synchronously, so re-activation's baselineRef captures the
      // FRESH updated_at and the save matches.
      await editor.fill("user edit on propagated row");
      const [siblingSave] = await Promise.all([
        page.waitForResponse(
          r => /\/translations\/\d+$/.test(r.url().split("?")[0]) && r.request().method() === "PUT",
          { timeout: 10000 },
        ),
        page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
      ]);

      // CORE INVARIANT: the save is not rejected as a conflict, and no banner.
      expect(
        siblingSave.status(),
        "seg 2's save must not 409 against the user's OWN propagation",
      ).not.toBe(409);
      await expect(
        page.getByTestId("conflict-banner"),
        "auto-propagation must not leave seg 2 with a stale baseline (false-409)",
      ).not.toBeVisible({ timeout: 1500 });
    } finally {
      await job.cleanup();
    }
  });

  // In-flight window of propagation: patchPropagatedSiblings runs on the /propagate
  // RESPONSE, but the server commits the sibling's updated_at bump BEFORE the
  // response lands. Activating + saving a propagated sibling inside that
  // in-flight window captures the pre-bump (stale) baseline → the save 409s
  // against the user's OWN propagation → false banner. This is the residual
  // window the stress loop hits probabilistically under load; here we force it
  // deterministically by holding the /propagate response open.
  test("in-flight propagation: editing the sibling before the propagate response lands must NOT false-409", async ({ page }) => {
    test.setTimeout(60_000);

    const job = await createIsolatedCATJob(page, {
      fileName: `propagate-inflight-${Date.now()}.txt`,
      content: "duplicate source line\nduplicate source line\nfiller line\n",
    });

    try {
      await job.navigateToEditor(/duplicate source line/);

      // Signal the instant the server has committed the sibling bump, but hold
      // the RESPONSE so the client-side cache patch (patchPropagatedSiblings)
      // can't run — keeping the sibling's cached baseline stale.
      let serverCommittedPropagate: () => void;
      const committed = new Promise<void>((res) => { serverCommittedPropagate = res; });
      await page.route("**/propagate", async (route) => {
        const resp = await route.fetch();
        serverCommittedPropagate();
        await new Promise((r) => setTimeout(r, 3000));
        await route.fulfill({ response: resp });
      });
      // Hold the post-save translations refetch too, so an incidental refetch
      // can't freshen the baseline on its own — the propagation reconcile must
      // be the ONLY thing that prevents the false-409 (deterministic guard).
      await page.route("**/api/jobs/*/translations", async (route, req) => {
        if (req.method() === "GET") await new Promise((r) => setTimeout(r, 6000));
        await route.continue();
      });

      const cells = page.locator("[data-translation-cell]");
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      const isMac = process.platform === "darwin";

      await cells.nth(0).click();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.fill("propagated translation");
      await page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter");

      // Wait until the server has bumped the sibling, then — with the response
      // still held (cache stale) — edit + confirm that sibling.
      await committed;
      await cells.nth(1).click();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.fill("user edit on in-flight propagated sibling");
      const siblingSave = page.waitForResponse(
        (r) => /\/translations\/\d+$/.test(r.url()) && r.request().method() === "PUT",
        { timeout: 10000 },
      );
      await page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter");
      const resp = await siblingSave;

      expect(
        resp.status(),
        "sibling save must not 409 against the user's OWN in-flight propagation",
      ).not.toBe(409);
      await expect(
        page.getByTestId("conflict-banner"),
        "in-flight propagation must not surface as a false-409 on the sibling",
      ).not.toBeVisible({ timeout: 1500 });
    } finally {
      // Both routes above hold their response for seconds. Tear them down
      // before the page closes, or a handler still awaiting route.fetch()
      // throws "Target page has been closed" and fails a test whose
      // assertions already passed.
      await page.unrouteAll({ behavior: 'ignoreErrors' });
      await job.cleanup();
    }
  });

  // Same in-flight window, but via the draft path: a dirty sibling flushed on
  // deactivate (saveDraft) must also reconcile, not just Cmd+Enter (saveMut).
  test("in-flight propagation: a draft flush on the sibling must NOT false-409 (saveDraft path)", async ({ page }) => {
    test.setTimeout(60_000);

    const job = await createIsolatedCATJob(page, {
      fileName: `propagate-inflight-draft-${Date.now()}.txt`,
      content: "duplicate source line\nduplicate source line\nfiller line\n",
    });

    try {
      await job.navigateToEditor(/duplicate source line/);

      let serverCommittedPropagate: () => void;
      const committed = new Promise<void>((res) => { serverCommittedPropagate = res; });
      await page.route("**/propagate", async (route) => {
        const resp = await route.fetch();
        serverCommittedPropagate();
        await new Promise((r) => setTimeout(r, 3000));
        await route.fulfill({ response: resp });
      });
      // Hold the post-save translations refetch too, so an incidental refetch
      // can't freshen the baseline on its own — the propagation reconcile must
      // be the ONLY thing that prevents the false-409 (deterministic guard).
      await page.route("**/api/jobs/*/translations", async (route, req) => {
        if (req.method() === "GET") await new Promise((r) => setTimeout(r, 6000));
        await route.continue();
      });

      const cells = page.locator("[data-translation-cell]");
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      const isMac = process.platform === "darwin";

      await cells.nth(0).click();
      await expect(editor).toBeEditable({ timeout: 5000 });
      await editor.fill("propagated translation");
      await page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter");

      await committed;
      // Activate the sibling, dirty it, then switch away — the deactivate flush
      // fires saveDraft (NOT saveMut), which must reconcile the same way.
      await cells.nth(1).click();
      await expect(editor).toBeEditable({ timeout: 5000 });
      await editor.fill("draft edit on in-flight propagated sibling");
      const draftSave = page.waitForResponse(
        (r) => /\/translations\/\d+\/draft$/.test(r.url()) && r.request().method() === "PUT",
        { timeout: 10000 },
      );
      await cells.nth(2).click();
      const resp = await draftSave;

      expect(
        resp.status(),
        "draft flush on a propagated sibling must not 409 against the user's OWN propagation",
      ).not.toBe(409);
      await expect(
        page.getByTestId("conflict-banner"),
        "draft flush during in-flight propagation must not surface a false-409",
      ).not.toBeVisible({ timeout: 1500 });
    } finally {
      // Both routes above hold their response for seconds. Tear them down
      // before the page closes, or a handler still awaiting route.fetch()
      // throws "Target page has been closed" and fails a test whose
      // assertions already passed.
      await page.unrouteAll({ behavior: 'ignoreErrors' });
      await job.cleanup();
    }
  });


  // The sibling is opened and typed into while the propagation is in flight,
  // and the propagate response lands before the sibling is saved. The row
  // keeps its older baseline (it never replaces unsaved typing) and the
  // propagation is no longer in flight at save time; the draft flush must
  // still not 409 against the user's own propagation.
  test("propagation settling while the sibling holds unsaved typing must NOT false-409 its draft flush", async ({ page }) => {
    test.setTimeout(60_000);

    const job = await createIsolatedCATJob(page, {
      fileName: `propagate-settled-draft-${Date.now()}.txt`,
      content: "duplicate source line\nduplicate source line\nfiller line\n",
      onCreated: (id) => projectIds.push(id),
    });
    await job.navigateToEditor(/duplicate source line/);
    const apiHeaders = { Authorization: `Bearer ${job.token}` };
    const listed = await page.request.get(`/api/jobs/${job.jobId}/translations`, { headers: apiHeaders });
    const siblingId: number = (await listed.json()).data[1].id;

    let serverCommittedPropagate: () => void;
    const committed = new Promise<void>((res) => { serverCommittedPropagate = res; });
    let releasePropagate: () => void;
    const release = new Promise<void>((res) => { releasePropagate = res; });
    // The editor's refetch of the rows is held until the draft has answered:
    // the propagation reconcile must be the only thing that can prevent a 409.
    let releaseRefetch: () => void;
    const refetchGate = new Promise<void>((res) => { releaseRefetch = res; });
    await page.route("**/propagate", async (route) => {
      const resp = await route.fetch();
      serverCommittedPropagate();
      await release;
      await route.fulfill({ response: resp });
    });
    await page.route("**/api/jobs/*/translations", async (route, req) => {
      if (req.method() === "GET") await refetchGate;
      await route.continue();
    });
    // Taking the segment hold returns the row's newest version, which could
    // also refresh the sibling before the typing: held until the propagate
    // response lands, as a slow hold would be.
    await page.route(`**/api/jobs/*/holds/${siblingId}`, async (route, req) => {
      if (req.method() === "PUT") await release;
      await route.continue();
    });

    try {
      const cells = page.locator("[data-translation-cell]");
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      const isMac = process.platform === "darwin";

      await cells.nth(0).click();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.fill("propagated translation");
      await page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter");

      // Open the sibling and type while the propagate response is held...
      await committed;
      await cells.nth(1).click();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.fill("user edit typed while propagation was in flight");
      // ...then let the response land, and only then leave the row.
      const propagated = page.waitForResponse(r => r.url().endsWith("/propagate"));
      releasePropagate!();
      await propagated;

      const draft = page.waitForResponse(
        (r) => r.url().endsWith(`/translations/${siblingId}/draft`) && r.request().method() === "PUT",
        { timeout: 10000 },
      );
      await cells.nth(2).click();
      const resp = await draft;
      expect(
        resp.status(),
        "a draft saved after the user's own propagation settled must not 409 against it",
      ).toBe(200);
      const saved = await page.request.get(`/api/files/${job.fileId}/translations/${siblingId}`, { headers: apiHeaders });
      expect((await saved.json()).data?.active_text).toBe("user edit typed while propagation was in flight");
    } finally {
      releasePropagate!();
      releaseRefetch!();
      await page.unrouteAll({ behavior: 'ignoreErrors' });
    }
  });
  // ============================================================
  // The row virtualizer measures rows (measureElement), so an active
  // row whose multi-line translation grows past the 120px estimateSize pushes
  // the next row down instead of painting over it.
  //
  // The active source cell renders inside a softly-outlined box
  // (mirrors the target editor's border, dialled down for read-only). Tested
  // separately below.
  // ============================================================
  test("active row with multi-line translation does not overlap next row", async ({ page }) => {
    test.setTimeout(20_000);
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    await page.locator("[data-translation-cell]").first().click();
    const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
    await expect(editor).toBeVisible({ timeout: 5000 });

    // Type a long translation that forces the editor to wrap to many lines.
    // We need enough content that the editor's actual height substantially
    // exceeds the virtualizer's 120px estimateSize (a few hundred pixels).
    await editor.click();
    const longChunk = "Esta es una traducción intencionalmente larga para forzar el editor a envolverse a múltiples líneas. ";
    // insertText dispatches a single `insertText` input event (one ProseMirror
    // transaction) instead of 500 individual keydowns — same wrapped content,
    // but fast enough not to blow the test budget on a loaded machine.
    await page.keyboard.insertText(longChunk.repeat(5));
    // Give the editor a beat to flush layout + the virtualizer to react.
    // Wait out the transition so the assertion isn't racing the animation.
    await page.waitForTimeout(400);

    // Active row's actual visual bottom = the editor's getBoundingClientRect.
    // Next row's top = the .nth(1) data-translation-cell's getBoundingClientRect.
    // If the virtualizer correctly measured the active row, next row sits
    // BELOW the editor. If it didn't, next row's top is INSIDE the editor.
    const [editorBottom, nextRowTop] = await Promise.all([
      editor.evaluate(el => el.getBoundingClientRect().bottom),
      page.locator('[data-translation-cell]').nth(1).evaluate(el => el.getBoundingClientRect().top),
    ]);

    expect(
      nextRowTop,
      `next row should start at or below active editor's bottom — editor bottom=${editorBottom}, next row top=${nextRowTop}. ` +
      `If nextRowTop < editorBottom, the row virtualizer hasn't measured the active row dynamically.`
    ).toBeGreaterThanOrEqual(editorBottom - 4); // 4px tolerance for sub-pixel rounding
  });

  // A segment is one paragraph: plain Enter adds no
  // break, Shift+Enter is the line break. With hidden characters on, the line
  // break (a hardBreak node, not an inline char) shows as ¶, the same marker
  // read-only uses for the saved '\n', never as a separate ↵.
  test("plain Enter adds no break; Shift+Enter shows ¶ in edit mode", async ({ page }) => {
    test.setTimeout(30_000);
    // Use an isolated job so the autosave that fires during this test (3s
    // draft debounce) doesn't pollute Demo Project's segment 1 — the
    // existing "hidden characters toggle shows whitespace markers" test
    // activates segment 1 and asserts ws-dot for the source spaces, which
    // breaks if the target carries leftover content from this test.
    const job = await createIsolatedCATJob(page, {
      fileName: `ws-markers-e2e-${Date.now()}.txt`,
      content: "Hello world.",
    });
    try {
      await job.navigateToEditor(/Click/);

      // Enable the hidden-character toggle. Idempotent — fine if already on.
      const toggle = page.getByRole('button', { name: '¶' });
      const isOn = await toggle.evaluate(el => el.classList.contains('bg-blue-600') || el.getAttribute('data-state') === 'on' || el.classList.contains('bg-primary'));
      if (!isOn) await toggle.click();

      await page.locator("[data-translation-cell]").first().click();
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.click();
      await page.keyboard.press("ControlOrMeta+a");
      await page.keyboard.press("Delete");

      // "a" + Enter (no-op) + "b" + Shift+Enter (hardBreak) + "c"
      await page.keyboard.type("a");
      await page.keyboard.press("Enter");
      await page.keyboard.type("b");
      await page.keyboard.press("Shift+Enter");
      await page.keyboard.type("c");

      // ProseMirror flush.
      await page.waitForTimeout(200);

      // One paragraph, one ¶: the Shift+Enter break only.
      await expect(editor.locator('p')).toHaveCount(1);
      expect(await editor.evaluate(el => el.textContent ?? '')).toBe("ab¶c");
      const pilcrows = await editor.locator('.ws-pilcrow').count();
      expect(pilcrows, "expected ¶ for the Shift+Enter break only").toBe(1);

      // No ↵ widget: the line break shows as ¶, as read-only shows the saved
      // '\n'.
      const softBreaks = await editor.locator('.ws-soft-break').count();
      expect(softBreaks, "↵ marker should NOT be used — hardBreak should render as ¶ for consistency with read-only").toBe(0);
    } finally {
      await job.cleanup();
    }
  });

  // Tabs and runs of whitespace survive deactivate → reactivate.
  // `textToContent` (run when the editor mounts with saved text) builds
  // TipTap JSON directly for plain text, because ProseMirror's HTML parser
  // collapses whitespace runs to a single space.
  test("whitespace runs and tabs survive deactivate → reactivate", async ({ page }) => {
    test.setTimeout(40_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `ws-roundtrip-${Date.now()}.txt`,
      content: "first.\nsecond.\n",
    });
    try {
      await job.navigateToEditor(/Click/);

      await page.locator("[data-translation-cell]").first().click();
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.click();
      await page.keyboard.press("ControlOrMeta+a");
      await page.keyboard.press("Delete");

      // Paste a tricky payload — three spaces, a tab, then two tabs — so the
      // test catches both run-collapse and tab-destroying bugs at once.
      const payload = "x   y\ta\t\tz";
      await editor.evaluate((el, txt) => {
        const dt = new DataTransfer();
        dt.setData('text/plain', txt);
        const ev = new ClipboardEvent('paste', { bubbles: true, cancelable: true, clipboardData: dt });
        el.dispatchEvent(ev);
      }, payload);
      await page.waitForTimeout(200);

      // Sanity: pre-save state has the payload.
      const preSave = await editor.evaluate(el => el.textContent ?? '');
      expect(preSave, "payload should land in editor unchanged").toBe(payload);

      // Save (Cmd+Enter) — advances to next segment, deactivating segment 1.
      await page.keyboard.press("ControlOrMeta+Enter");
      await page.waitForTimeout(600);

      await page.locator("[data-translation-cell]").first().click();
      const reactivated = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      await expect(reactivated).toBeVisible({ timeout: 5000 });

      // The whole payload must round-trip exactly. No tab loss, no run-collapse.
      const reText = await reactivated.evaluate(el => el.textContent ?? '');
      expect(reText, `expected payload preserved through deactivate/reactivate. ` +
        `got=${JSON.stringify(reText)}, want=${JSON.stringify(payload)}`).toBe(payload);
    } finally {
      await job.cleanup();
    }
  });

  // The terminology sidebar shows only the active segment's terms.
  //   1. Click a segment with a term match → terms appear.
  //   2. Click a segment with NO term match → "no terms", not the previous
  //      segment's terms greyed out.
  test("terminology sidebar clears between segments with vs. without term matches", async ({ page }) => {
    test.setTimeout(20_000);
    await page.getByRole("link", { name: /demo project/i }).click();
    // en→de job — seeded with the term "password → Passwort".
    await openJob(page, { lang: "de" });
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    // Step 1: activate the segment "Reset your password." — term lookup
    // should return { source_term: "password", target_term: "Passwort" }.
    await page.getByText(/Reset your password/i).first().click();
    await expect(page.locator('[data-testid="segment-editor"]').first()).toBeVisible({ timeout: 5000 });

    // Scope to the Terminology section's data-testid so the assertion
    // can never collide with TM target strings that happen to contain the
    // term as a substring (e.g. "Setzen Sie Ihr Passwort zurück.").
    const termSection = page.getByTestId('terminology-section');
    await expect(termSection.getByText('Passwort')).toBeVisible({ timeout: 5000 });

    // Step 2: activate a segment that has NO term match — "We thank you for
    // your patience." has neither "password" nor any other seeded term.
    await page.getByText(/We thank you for your patience/i).first().click();
    await expect(page.locator('[data-testid="segment-editor"]').first()).toBeVisible({ timeout: 5000 });

    await expect(termSection.getByText(/no terms/i)).toBeVisible({ timeout: 3000 });
    await expect(termSection.getByText('Passwort')).not.toBeVisible();

    // A reset of `activeTerms` in CATEditor on activeSegmentIdx change would
    // race SegmentRow's lookup effect (React runs child useEffects before
    // parent) and wipe the terms on a cache hit. Clicking back to "Reset your
    // password." must still show Passwort.
    await page.getByText(/Reset your password/i).first().click();
    await expect(termSection.getByText('Passwort')).toBeVisible({ timeout: 3000 });
  });

  // A Shift+Enter line break saves as one '\n'. Paragraphs read back with
  // one '\n' too: SegmentEditor.test.tsx "getText separates paragraphs…".
  test("a Shift+Enter line break saves as a single newline", async ({ page }) => {
    test.setTimeout(40_000);
    const job = await createIsolatedCATJob(page, {
      fileName: `linebreak-double-${Date.now()}.txt`,
      content: 'Source segment for break test.\n',
    });
    try {
      await job.navigateToEditor(/Click/);

      // Activate, clear, type "A" + Shift+Enter + "B" + save.
      await page.locator("[data-translation-cell]").first().click();
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.click();
      await page.keyboard.press("ControlOrMeta+a");
      await page.keyboard.press("Delete");
      await page.keyboard.type("A");
      await page.keyboard.press("Shift+Enter");
      await page.keyboard.type("B");
      await page.waitForTimeout(150);

      // Toggle whitespace on, count ¶ before save (= 1 break).
      const toggle = page.getByRole('button', { name: '¶' });
      const isOn = await toggle.evaluate(el => el.classList.contains('bg-blue-600') || el.classList.contains('bg-primary'));
      if (!isOn) await toggle.click();
      await page.waitForTimeout(150);
      const beforeSave = await editor.locator('.ws-pilcrow').count();
      expect(beforeSave, "expected exactly 1 ¶ before save").toBe(1);

      // Capture the wire-format save body so we can assert single \n (not \n\n).
      const sent: string[] = [];
      page.on('request', req => {
        if (req.method() === 'PUT' && req.url().includes('/translations/')) {
          sent.push(req.postData() ?? '');
        }
      });

      // Save via Cmd+Enter (advances to next segment, deactivating row 0).
      await editor.click();
      await page.keyboard.press("ControlOrMeta+Enter");
      await page.waitForTimeout(800);

      // The saved human_text must contain a single '\n' between A and B.
      // Asserting the wire format is the
      // deterministic proof; the reload round-trip itself is downstream
      // mechanics that don't add coverage beyond this.
      expect(sent.length).toBeGreaterThan(0);
      expect(sent[0], "saved human_text should contain single '\\n', not '\\n\\n'").toContain('"human_text":"A\\nB"');
      expect(sent[0], "saved human_text must NOT contain doubled newline").not.toContain('A\\n\\nB');
    } finally {
      await job.cleanup();
    }
  });

  // The hidden-character toggle shows ¶ for line breaks
  // from imported HTML <br> too. richTextToTipTapContent emits a hardBreak
  // for <x ctype="lb"/>, so WhitespaceMarkers decorates it like Shift+Enter.
  test("imported <br> renders as line break + ¶ marker (after copy-source)", async ({ page }) => {
    test.setTimeout(30_000);

    const job = await createIsolatedCATJob(page, {
      fileName: `br-marker-${Date.now()}.txt`,
      content: 'Line one<br>Line two<br/>Line three\n',
    });
    try {
      await job.navigateToEditor(/Click/);

      const toggle = page.getByRole('button', { name: '¶' });
      const isOn = await toggle.evaluate(el => el.classList.contains('bg-blue-600') || el.classList.contains('bg-primary'));
      if (!isOn) await toggle.click();

      // Activate the first segment + copy source to target so the
      // editor receives the line-break rich text.
      await page.locator("[data-translation-cell]").first().click();
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.click();
      await page.keyboard.press("ControlOrMeta+Shift+s");
      await page.waitForTimeout(400);

      // Two <br>s → two hardBreak nodes → two ¶ widgets.
      const pilcrows = await editor.locator('.ws-pilcrow').count();
      expect(pilcrows, "expected ≥2 ¶ markers (one per imported <br>)").toBeGreaterThanOrEqual(2);
    } finally {
      await job.cleanup();
    }
  });

  // Anti-tautology: this test was proven non-tautological by reverting the
  // `contentHeight === lastHeight` half of the settle loop's stability check
  // (web/src/pages/CATEditor.tsx, the `settle` function in the activeSegmentIdx
  // effect) on 2026-09-22. With the revert applied it fails with: "clicked row
  // drifted 257px from click position (anchor failed)". It only fails that way
  // when frames are slow, which is why the CI runner saw it and a laptop does
  // not.
  // To re-prove: add at the top of the test body —
  //   const cdp = await page.context().newCDPSession(page);
  //   await cdp.send("Emulation.setCPUThrottlingRate", { rate: 4 });
  // then `npx playwright test tests/cat-editor.spec.ts -g "keeps it visually anchored"
  // --repeat-each=4`: 4/4 red before the fix, 4/4 green after. The throttling
  // stays out of the committed test — it costs ~8s per run.
  //
  // Clicking a segment keeps the clicked row in its visual position
  // while the previously-active row collapses. The scroll-restore anchors on
  // the row's DOM boundingClientRect (TanStack's virtual `start` lags the
  // layout) and captures on mousedown, before blur on the previously-active
  // editor triggers saveDraft and shifts layout.
  test("clicking a segment keeps it visually anchored", async ({ page }) => {
    test.setTimeout(40_000);
    // Many segments so the user has room to scroll + click in the middle.
    const content = Array.from({ length: 80 }, (_, i) => `Segment ${i + 1}`).join("\n");
    const job = await createIsolatedCATJob(page, {
      fileName: `inc102-${Date.now()}.txt`,
      content,
    });
    try {
      await job.navigateToEditor(/Click/);
      const cells = page.locator("[data-translation-cell]");

      // Scroll down so we have room to compensate up or down.
      const scrollContainer = page.locator('.overflow-y-auto.overflow-x-hidden').first();
      await scrollContainer.evaluate(el => { el.scrollTop = 600 });
      await page.waitForTimeout(300);

      // Activate a visible segment, type a tall translation. This is the
      // crucial part — without a tall previously-active row, the click
      // wouldn't trigger noticeable layout collapse on deactivation.
      const startIdx = await cells.evaluateAll(els => els.findIndex(el => {
        const r = el.getBoundingClientRect();
        return r.y > 100 && r.y < 400;
      }));
      await cells.nth(startIdx).click();
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.click();
      await page.keyboard.type("línea ".repeat(80), { delay: 0 });
      await page.waitForTimeout(400);

      // Find another visible cell further down and click it natively (no
      // Playwright auto-scroll-into-view, which would change the click
      // target's position before our handler runs).
      const targetIdx = await cells.evaluateAll(els => els.findIndex(el => {
        const r = el.getBoundingClientRect();
        return r.y > 500 && r.y < 800;
      }));
      const before = await cells.nth(targetIdx).boundingBox();
      expect(before).not.toBeNull();
      await cells.nth(targetIdx).evaluate(el => {
        const r = el.getBoundingClientRect();
        const x = r.x + r.width / 2;
        const y = r.y + r.height / 2;
        const opts = { bubbles: true, cancelable: true, clientX: x, clientY: y, button: 0 };
        el.dispatchEvent(new MouseEvent('mousedown', opts));
        el.dispatchEvent(new MouseEvent('mouseup', opts));
        el.dispatchEvent(new MouseEvent('click', opts));
      });
      await page.waitForTimeout(800); // let the settle loop finish

      const after = await cells.nth(targetIdx).boundingBox();
      expect(after, "clicked row should still be visible").not.toBeNull();
      if (before && after) {
        const delta = Math.abs(after.y - before.y);
        // A failed anchor shifts rows by 200-500px; the settle loop should
        // stabilize within a few pixels of the click anchor.
        expect(delta, `clicked row drifted ${delta}px from click position (anchor failed)`).toBeLessThan(40);
      }
    } finally {
      await job.cleanup();
    }
  });

  // HTML inline tags inside segment text bodies should
  // render as atomic chips wherever the source format-aware parser detects
  // them and emits XLIFF <g>/<x> rich text (internal/parsers/common/inline_tag.go).
  // For TXT imports, this means a line containing `<i>...</i>` lands in the
  // DB with source_text="Hello world here." and source_rich_text containing
  // <g id="N" ctype="italic">world</g>. Same primitive (SegmentText →
  // RichSourceText → richTextToTipTapContent) feeds the CAT editor source +
  // target, TM Browser, and Concordance — chips render in all three.
  test("HTML inline tags from non-HTML imports render as atomic chips", async ({ page }) => {
    test.setTimeout(30_000);
    // TXT file with literal HTML markup. TXT.Parse calls DetectInlineTags and
    // populates source_rich_text — that's what the display layer keys off.
    const job = await createIsolatedCATJob(page, {
      fileName: `html-tags-${Date.now()}.txt`,
      content: 'Hello <i>world</i> here.\n',
    });
    try {
      await job.navigateToEditor(/Click/);

      // Source cell renders via SegmentText → RichSourceText → chips because
      // the TXT parser populated source_rich_text with <g ctype="italic">.
      // The chip tagType reflects the XLIFF <g> origin (not the original <i>
      // HTML name) — that's the data shape inside the system after import.
      const chipCount = await page.locator('[data-tag-type="g"]').count();
      expect(chipCount, "expected at least 2 chips for <g> open + close after italic detection").toBeGreaterThanOrEqual(2);

      // Activate the segment — the active editor (TipTap) renders the same
      // rich text via textToContent → richTextToTipTapContent → chips.
      await page.locator("[data-translation-cell]").first().click();
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await editor.click();
      await page.keyboard.press("ControlOrMeta+Shift+s");
      await page.waitForTimeout(300);

      const editorChips = editor.locator('.xliff-tag');
      expect(await editorChips.count(), "expected chips inside the target editor after copy-source").toBeGreaterThanOrEqual(2);

      // Atomic guarantee — caret can't enter, chip moves/deletes as one unit.
      const editableChips = await editor.locator('.xliff-tag[contenteditable="false"]').count();
      expect(editableChips, "chips must be contenteditable=false (atomic)").toBeGreaterThanOrEqual(2);
    } finally {
      await job.cleanup();
    }
  });

  test("active row's source cell has a visible outlined box", async ({ page }) => {
    test.setTimeout(15_000);
    await page.getByRole("link", { name: /demo project/i }).click();
    await openJob(page, { fileName: "demo.txt" });
    await expect(page.getByText(/patience/i).first()).toBeVisible({ timeout: 10000 });

    // Before activation: no active-source box anywhere.
    expect(await page.getByTestId("active-source-box").count()).toBe(0);

    await page.locator("[data-translation-cell]").first().click();
    await expect(page.locator('[data-testid="segment-editor"]').first()).toBeVisible({ timeout: 5000 });

    // After activation: exactly one active-source box, with a visible
    // (non-zero) border.
    const box = page.getByTestId("active-source-box");
    await expect(box).toBeVisible({ timeout: 3000 });
    const borderTopWidth = await box.evaluate(el => parseFloat(getComputedStyle(el).borderTopWidth));
    expect(borderTopWidth, "active source box should have a non-zero border").toBeGreaterThan(0);
  });

  test("Resources sidebar shows 99% near-exact for punctuation-different TM hit and surfaces short-segment fuzzy", async ({ page }) => {
    test.setTimeout(45_000);

    const job = await createIsolatedCATJob(page, {
      fileName: `tm-rescore-${Date.now()}.txt`,
      content: "The text appears in bold.\nThe text appears in italics\n",
      targetLanguages: ["es"],
    });
    let tmId: number | undefined;

    try {
      const headers = { Authorization: `Bearer ${job.token}` };

      const tmName = `Rescore E2E ${Date.now()}`;
      const tmRes = await page.request.post(`/api/tms`, {
        headers, data: { name: tmName, source_language: "en", target_languages: ["es"] },
      });
      expect(tmRes.ok(), `create TM: ${tmRes.status()}`).toBe(true);
      tmId = (await tmRes.json()).data.id as number;

      const tmx = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en" creationtool="e2e" creationtoolversion="1"/>
  <body>
    <tu><tuv xml:lang="en"><seg>The text appears in bold</seg></tuv><tuv xml:lang="es"><seg>El texto aparece en negrita</seg></tuv></tu>
    <tu><tuv xml:lang="en"><seg>The text appears in italics</seg></tuv><tuv xml:lang="es"><seg>El texto aparece en cursiva</seg></tuv></tu>
  </body>
</tmx>`;
      await importTMXViaAPI(page, tmId, tmx, "rescore.tmx");

      const assignRes = await page.request.put(`/api/projects/${job.projectId}/tms`, {
        headers,
        data: { assignments: [{ tm_id: tmId, source_language: "en", target_language: "es", mode: "read_write", priority: 0 }] },
      });
      expect(assignRes.ok(), `assign TM: ${assignRes.status()}`).toBe(true);

      await job.navigateToEditor(/The text appears in bold\./);

      await page.getByText("The text appears in bold.", { exact: true }).first().click();
      await expect(segmentEditor(page).first()).toBeVisible({ timeout: 5000 });
      await expect(page.getByText("TM matches").first()).toBeVisible({ timeout: 5000 });
      const sidebar = page.getByTestId('editor-sidebar');
      await expect(sidebar).toBeVisible({ timeout: 5000 });
      await expect(sidebar.getByText(/^99%$/).first(), "punctuation-only diff is a near-exact match (99%), not 100% (pg_trgm) nor ~96% (raw Levenshtein cliff)").toBeVisible({ timeout: 5000 });
      await expect(sidebar.getByText(/^100%$/), "punctuation-different hit must not show as an exact match").not.toBeVisible();

      // Await the italics segment's OWN match fetch before asserting. Both TM
      // entries are in the bold segment's result too, so every text assertion
      // below is satisfiable by the previous segment's list — `keepPreviousData`
      // keeps it on screen across the switch, and only the 100% badge tells the
      // two lists apart. Without this the text assertions pass against stale
      // data and the percentage assertion is the one that fails, under load.
      await Promise.all([
        page.waitForResponse(
          r => r.url().includes("/tm-matches") &&
            decodeURIComponent(r.url()).includes("appears in italics"),
          { timeout: 10000 },
        ),
        page.getByText("The text appears in italics", { exact: true }).first().click(),
      ]);
      await expect(segmentEditor(page).first()).toBeVisible({ timeout: 5000 });
      await expect(sidebar.getByText("The text appears in italics").first()).toBeVisible({ timeout: 5000 });
      await expect(sidebar.getByText("The text appears in bold").first(), "bold entry must surface as a fuzzy for the italics segment (pg_trgm alone would filter it out)").toBeVisible({ timeout: 5000 });
      await expect(sidebar.getByText(/^100%$/).first(), "italics segment's own exact TM hit").toBeVisible();
      await expect(sidebar.getByText(/^[67][0-9]%$/).first(), "bold-as-fuzzy should render 60-79%").toBeVisible({ timeout: 5000 });
    } finally {
      try {
        // Deleting the project drops its TM assignment, which a TM delete needs.
        await job.cleanup();
      } finally {
        if (tmId !== undefined) await deleteTMViaAPI(page, tmId);
      }
    }
  });

  // Ignore a QA finding.
  //
  // Pins the whole loop a translator uses on a false positive:
  //   1. A number_consistency ERROR blocks Cmd+Enter (422) — proves the gate
  //      is live before we ignore anything (anti-tautology for step 4).
  //   2. Ignore it from the sidebar's QA findings section. The row's dot
  //      becomes a hollow ring (`data-ignored`) and the toolbar count drops
  //      (ignored findings are not counted there).
  //   3. Cmd+Enter on the same text now succeeds (200) — the ignored error no
  //      longer blocks.
  //   4. Un-ignore (sidebar) restores the red dot and the count.
  //
  // Fixture: 2-line file, pre-translate OFF. Segment 1's source carries "42";
  // typing a target without it fires number_consistency (Standard QA seed,
  // severity=error). Segment 2 stays empty → empty_target (error).
  test("QA ignore: ignoring a finding greys the dot, drops the count, unblocks confirm; un-ignore reverts", async ({ page }) => {
    test.setTimeout(60_000);

    const job = await createIsolatedCATJob(page, {
      onCreated: (id) => projectIds.push(id),
      fileName: `qa-ignore-${Date.now()}.txt`,
      content: "alpha 42 source line\nbeta source line\n",
    });

    await job.navigateToEditor(/alpha 42 source line/);
    const isMac = process.platform === "darwin";
    const confirmKey = isMac ? "ControlOrMeta+Enter" : "Control+Enter";
    const isConfirmPut = (r: import("@playwright/test").Response) =>
      /\/api\/files\/\d+\/translations\/\d+$/.test(r.url()) && r.request().method() === "PUT";

    const cells = page.locator("[data-translation-cell]");
    await cells.nth(0).click();
    const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
    await expect(editor).toBeVisible({ timeout: 5000 });
    await editor.fill("alpha translated without the number");

    // STEP 1 — the error blocks confirm.
    const [blocked] = await Promise.all([
      page.waitForResponse(isConfirmPut, { timeout: 10000 }),
      page.keyboard.press(confirmKey),
    ]);
    expect(blocked.status(), "precondition: number_consistency error must 422 the confirm").toBe(422);

    // Run QA so the dots render from the persisted report.
    const { runResp, reportResp } = await runQAAndReadReport(page, page.getByTestId("run-qa-btn"));
    expect(runResp.ok(), `/run-qa: ${runResp.status()}`).toBe(true);
    expect(reportResp.ok(), `/qa-report: ${reportResp.status()}`).toBe(true);
    await expect(page.getByRole("button", { name: /^QA \(2\)$/ }), "two errors: missing number + empty target").toBeVisible({ timeout: 5000 });
    // One dot per row, coloured by the row's worst OPEN finding; a row whose
    // findings are all ignored is a ring (data-ignored, no data-severity).
    const errorBadges = page.locator('[data-testid="qa-issue-badge"][data-severity="error"]:not([data-ignored])');
    await expect(errorBadges).toHaveCount(2, { timeout: 5000 });

    // STEP 2 — ignore segment 1's finding from the sidebar's QA findings
    // section (the row dot is read-only). Row 1 is still the active segment.
    const qaSection = page.getByTestId("qa-findings-section");
    const ignoreBtn = qaSection.getByTestId("qa-finding-ignore-btn");
    await expect(ignoreBtn, "the sidebar lists segment 1's one open finding").toHaveCount(1, { timeout: 5000 });
    const [ignoreResp] = await Promise.all([
      page.waitForResponse(r => r.url().includes("/ignore") && r.request().method() === "POST", { timeout: 10000 }),
      ignoreBtn.click(),
    ]);
    expect(ignoreResp.ok(), `POST …/ignore: ${ignoreResp.status()}`).toBe(true);
    const ignoredBadges = page.locator('[data-testid="qa-issue-badge"][data-ignored="true"]');
    await expect(ignoredBadges, "the ignored finding renders as a greyed dot").toHaveCount(1, { timeout: 5000 });
    await expect(errorBadges, "the other error is untouched").toHaveCount(1);
    await expect(page.getByRole("button", { name: /^QA \(1\)$/ }), "count excludes the ignored finding").toBeVisible({ timeout: 5000 });

    // STEP 3 — the same text now confirms.
    await cells.nth(0).click();
    await expect(editor).toBeVisible({ timeout: 5000 });
    const [allowed] = await Promise.all([
      page.waitForResponse(isConfirmPut, { timeout: 10000 }),
      page.keyboard.press(confirmKey),
    ]);
    expect(allowed.status(), "ignored error must not block confirm").toBe(200);
    // The per-segment refresh after confirm keeps the ignore (same finding).
    await expect(ignoredBadges).toHaveCount(1, { timeout: 5000 });

    // STEP 4 — un-ignore from the sidebar, which says who ignored it. The
    // confirm moved on to segment 2, so open segment 1 again first: the
    // sidebar lists the ACTIVE segment's findings.
    await cells.nth(0).click();
    await expect(editor).toBeVisible({ timeout: 5000 });
    await expect(

      qaSection.getByTestId("qa-finding-ignored-meta"),
      "the sidebar shows who ignored it",
    ).toContainText(/Ignored by .+ on /, { timeout: 5000 });
    const [unignoreResp] = await Promise.all([
      page.waitForResponse(r => r.url().includes("/ignore") && r.request().method() === "DELETE", { timeout: 10000 }),
      qaSection.getByTestId("qa-finding-unignore-btn").click(),
    ]);
    expect(unignoreResp.ok(), `DELETE …/ignore: ${unignoreResp.status()}`).toBe(true);
    await expect(ignoredBadges).toHaveCount(0, { timeout: 5000 });
    await expect(errorBadges).toHaveCount(2, { timeout: 5000 });
    await expect(page.getByRole("button", { name: /^QA \(2\)$/ })).toBeVisible({ timeout: 5000 });
  });

  // Stored QA findings load with the editor (no click needed).
  //
  // The report lives in the database; opening the editor must show the dots
  // and the count from the last run. A fresh job with no findings keeps the
  // plain "Run QA" label (covered by the precondition in the Run QA test above).
  test("QA findings persist: after Run QA, reopening the editor shows the dots and count without clicking", async ({ page }) => {
    test.setTimeout(60_000);

    const job = await createIsolatedCATJob(page, {
      onCreated: (id) => projectIds.push(id),
      fileName: `qa-persist-${Date.now()}.txt`,
      content: "first source line\nsecond source line\n",
    });

    await job.navigateToEditor(/first source line/);
    const { runResp, reportResp } = await runQAAndReadReport(page, page.getByTestId("run-qa-btn"));
    expect(runResp.ok(), `/run-qa: ${runResp.status()}`).toBe(true);
    expect(reportResp.ok(), `/qa-report: ${reportResp.status()}`).toBe(true);
    await expect(page.getByRole("button", { name: /^QA \(2\)$/ }), "two empty targets → two errors").toBeVisible({ timeout: 5000 });

    // Reload: no Run QA click this time. The page must fetch the stored
    // report on its own and render the same state.
    const [reloadReport] = await Promise.all([
      page.waitForResponse(r => r.url().includes("/qa-report") && r.request().method() === "GET", { timeout: 15000 }),
      page.reload(),
    ]);
    expect(reloadReport.ok(), `/qa-report after reload: ${reloadReport.status()}`).toBe(true);
    await expect(page.getByRole("button", { name: /^QA \(2\)$/ }), "count restored from the stored report").toBeVisible({ timeout: 10000 });
    await expect(
      page.locator('[data-testid="qa-issue-badge"][data-severity="error"]'),
      "dots restored from the stored report",
    ).toHaveCount(2, { timeout: 5000 });
  });

  // Segment history section: the sidebar read surface over translation_history.
  // Open on arrival like the other sections, and a confirm must show up in it
  // as a `human` event carrying the confirmed text.
  test("segment history lists the confirmed edit", async ({ page }) => {
    test.setTimeout(60_000);
    const uniqueSrc = `history-source-${Date.now()}`;
    const job = await createIsolatedCATJob(page, {
      fileName: `history-${Date.now()}.txt`,
      content: `${uniqueSrc}\n`,
      qaProfile: null,
    });

    try {
      await job.navigateToEditor(new RegExp(uniqueSrc));
      // Activating the segment opens the sidebar on it, and History is open, so
      // the fetch goes out with no interaction at all.
      const [historyResp] = await Promise.all([
        page.waitForResponse(
          r => r.url().includes("/history") && r.request().method() === "GET",
          { timeout: 10000 },
        ),
        page.locator("[data-translation-cell]").first().click(),
      ]);
      expect(historyResp.ok(), `/history: ${historyResp.status()}`).toBe(true);
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      await expect(editor).toBeVisible({ timeout: 5000 });

      // Nothing has touched this segment yet, and the section says so rather
      // than sitting closed.
      await expect(page.getByTestId("segment-history-empty")).toBeVisible();

      await editor.click();
      await editor.fill("historia confirmada");
      const isMac = process.platform === "darwin";
      await Promise.all([
        page.waitForResponse(
          r => /\/translations\/\d+$/.test(r.url()) && r.request().method() === "PUT",
          { timeout: 10000 },
        ),
        page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
      ]);

      // Confirm advances to the next segment, so go back to the first one to
      // read its history.
      await page.locator("[data-translation-cell]").first().click();

      const humanRow = page.locator('[data-testid="segment-history-row"][data-kind="human"]');
      await expect(humanRow).toHaveCount(1, { timeout: 5000 });
      await expect(humanRow).toContainText("historia confirmada");
    } finally {
      await job.cleanup();
    }
  });
  // A confirm that changes nothing is recorded as an approval, not an edit: the
  // event carries no text, which is also what keeps it out of the edit-distance
  // baseline. In the panel, one person's confirms in one step are one row — the
  // text they confirmed, marked Approved — so the recording is read from the API.
  test("segment history records a confirm that changed nothing as an approval", async ({ page }) => {
    test.setTimeout(60_000);
    const uniqueSrc = `approve-source-${Date.now()}`;
    const job = await createIsolatedCATJob(page, {
      fileName: `approve-${Date.now()}.txt`,
      content: `${uniqueSrc}\n`,
      qaProfile: null,
    });

    try {
      await job.navigateToEditor(new RegExp(uniqueSrc));
      await page.locator("[data-translation-cell]").first().click();
      const editor = page.locator('[data-testid="segment-editor"] .ProseMirror').first();
      await expect(editor).toBeVisible({ timeout: 5000 });

      // Type and confirm — that is the `human` row.
      await editor.click();
      await editor.fill("primera version");
      const isMac = process.platform === "darwin";
      const confirm = () => Promise.all([
        page.waitForResponse(
          r => /\/translations\/\d+$/.test(r.url()) && r.request().method() === "PUT",
          { timeout: 10000 },
        ),
        page.keyboard.press(isMac ? "ControlOrMeta+Enter" : "Control+Enter"),
      ]);
      const [saved] = await confirm();
      const translationId = Number(/\/translations\/(\d+)$/.exec(saved.url())?.[1]);
      expect(translationId, `translation id from ${saved.url()}`).toBeGreaterThan(0);

      // Confirm the very same text again — nothing changed, so this one is an
      // approval.
      await page.locator("[data-translation-cell]").first().click();
      await expect(editor).toBeVisible({ timeout: 5000 });
      await confirm();

      // Recorded: the approval, with no text, above the edit that changed it.
      const res = await page.request.get(`/api/translations/${translationId}/history`, {
        headers: { Authorization: `Bearer ${job.token}` },
      });
      expect(res.ok(), `/history: ${res.status()}`).toBe(true);
      const events = (await res.json()).data as Array<{ kind: string; text: string | null }>;
      expect(events.map(e => e.kind)).toEqual(["approved", "human"]);
      expect(events[0].text, "an approval has no text").toBeNull();

      // Shown: the real panel draws the person's work in the step as one
      // approved row with their text. These checks hold after the first
      // confirm too, so the fold of the second one into it is pinned by the
      // SegmentHistorySection component test, not here.
      await page.locator("[data-translation-cell]").first().click();
      const rows = page.getByTestId("segment-history-row");
      await expect(rows).toHaveCount(1, { timeout: 5000 });
      await expect(rows).toHaveAttribute("data-kind", "human");
      await expect(rows).toContainText("primera version");
      await expect(rows).toContainText("Approved");
    } finally {
      await job.cleanup();
    }
  });
});
