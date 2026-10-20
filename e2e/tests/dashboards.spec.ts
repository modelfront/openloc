import { readFileSync } from "node:fs";
import { test, expect, type Locator, type Page } from "@playwright/test";
import { login } from "./helpers";

/**
 * Dashboards — the seeded "Operations" dashboard renders
 * LIVE charts: each card's stored definition re-runs through the metrics
 * semantic layer on load. Unlike the jsdom tests (which stop at the card
 * mount), a real browser sizes ResponsiveContainer, so real SVG geometry is
 * asserted here. Read-only over seed data — nothing to clean up.
 */

/**
 * A dashboard card by its EXACT name. `filter({ hasText })` is a substring
 * match, so "Operations" also finds "Copy of Operations" the moment the copy
 * test runs beside this one.
 */
const cardNamed = (page: Page, name: string, testId = "dashboard-card") =>
  page.getByTestId(testId).filter({ has: page.getByText(name, { exact: true }) });

/**
 * The box of a chart shape once Recharts has finished drawing it: it grows in
 * from nothing, so the box is null or still changing for a moment after the
 * shape appears. Polls until two reads in a row agree.
 */
async function settledBox(shape: Locator) {
  let last = "";
  let box: { x: number; y: number; width: number; height: number } | null = null;
  await expect.poll(async () => {
    box = await shape.boundingBox();
    const now = box && box.width > 0 && box.height > 0 ? JSON.stringify(box) : "";
    const settled = now !== "" && now === last;
    last = now;
    return settled;
  }, { message: "the shape's box stops changing" }).toBe(true);
  return box!;
}

/**
 * Moves the mouse onto a pie slice. Not `hover()`: that aims at the middle of
 * the slice's bounding box, which for a slice over half the donut is the hole.
 * Walks the slice's outline, steps a little to either side, and takes the
 * first point whose topmost element is the slice itself. Right after a legend
 * click the pie is still animating, so it waits for the slice to stop moving,
 * and the point it moves to is the one it just found (a second look can find
 * none while the slice redraws).
 */
async function hoverSlice(page: Page, slice: Locator) {
  const findPoint = () => slice.evaluate((g: Element) => {
    const path = g.querySelector("path")!;
    const length = path.getTotalLength();
    const toScreen = path.getScreenCTM()!;
    for (let i = 1; i < 60; i++) {
      const p = path.getPointAtLength((length * i) / 60);
      for (const [dx, dy] of [[4, 0], [-4, 0], [0, 4], [0, -4]]) {
        const at = new DOMPoint(p.x + dx, p.y + dy).matrixTransform(toScreen);
        if (g.contains(document.elementFromPoint(at.x, at.y))) return { x: at.x, y: at.y };
      }
    }
    return null;
  });
  await settledBox(slice);
  await expect(async () => {
    const point = await findPoint();
    expect(point, "a point inside the slice").not.toBeNull();
    await page.mouse.move(point!.x, point!.y);
  }).toPass();
}

const TAB_LABELS = { shared: "Organization dashboards", mine: "My dashboards", pinned: "Pinned" } as const;
const dashboardsTab = (page: Page, tab: keyof typeof TAB_LABELS) =>
  page.getByRole("navigation", { name: "Section" }).getByRole("link", { name: TAB_LABELS[tab], exact: true });

/** The id of the first dashboard the caller can see — used to seed a second card. */
async function firstDashboardId(page: Page): Promise<number> {
  const res = await page.request.get("/api/dashboards");
  return (await res.json()).data[0].id as number;
}

test.describe("Dashboards", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  // A chart-data request can still be inside a route handler when the test
  // ends (a card the test never looked at). Its result is unused, and a
  // route.fetch cut off by the closing page would fail the test.
  test.afterEach(async ({ page }) => {
    await page.unrouteAll({ behavior: "ignoreErrors" });
  });

  test("seeded Operations dashboard renders live charts from stored definitions", async ({ page }) => {
    // Each chart re-runs its stored definition through the metrics layer, so
    // no SVG can exist until those responses land. The list card's preview
    // asks first, under the same default selection the dashboard page uses —
    // so arm the waits BEFORE the list loads, and the dashboard opened from it
    // draws from the same answers. Every card previews at once, so wait on
    // Operations' own charts, not on whichever response lands first. Await the
    // round trip, never out-wait it.
    const listed = (await (await page.request.get("/api/dashboards")).json()).data as {
      name: string
      preview_charts: { id: number; title: string }[]
    }[];
    const ops = listed.find(d => d.name === "Operations");
    expect(ops, "the seeded Operations dashboard is missing — run make seed").toBeDefined();
    const opsChart = (title: string) => {
      const c = ops!.preview_charts.find(p => p.title === title);
      expect(c, `Operations previews no "${title}" chart`).toBeDefined();
      return page.waitForResponse(
        r => r.url().includes(`/api/dashboards/charts/${c!.id}/data`) && r.status() === 200,
        { timeout: 30_000 },
      );
    };
    const chartData = Promise.all([opsChart("Words processed per week"), opsChart("MT volume by engine")]);
    await page.goto("/dashboards/organization");
    // Seed-state facts only — a dev DB may carry extra user-created charts on
    // top of the healed seed ones, so assert presence, never exact counts.
    const opsCard = cardNamed(page, "Operations");
    await expect(opsCard).toBeVisible();
    await expect(opsCard.getByText(/\d+ charts?/)).toBeVisible();
    const [firstChart] = await chartData;

    // The card previews its first charts, drawn small.
    await expect(opsCard.getByText("Words processed per week", { exact: true })).toBeVisible();
    await expect(
      opsCard.locator('[data-testid="dashboard-preview-chart"] svg.recharts-surface').first(),
    ).toBeVisible({ timeout: 15_000 });

    await opsCard.click();
    await expect(page).toHaveURL(/\/dashboards\/\d+$/);

    // The two seed charts render with their definition subtitles (make seed
    // heals them by title, so they exist regardless of manual QA churn).
    await expect(page.getByText("Words processed per week", { exact: true })).toBeVisible();
    await expect(page.getByText("MT volume by engine", { exact: true })).toBeVisible();
    // The window is the filter's to state; cards never carry a note of it.
    await expect(page.getByTestId("dashboard-filter-date-range")).toBeVisible();
    await expect(page.getByTestId("dashboard-chart-card").first()).not.toContainText(/Last \d+ days/);

    // Guard the precondition explicitly. Without this, a seed that produced no
    // attributable history fails below as an opaque "element not found" —
    // healDashboard swallows a missing MT engine (`if err == nil`) and leaves
    // mt_engine_usage with zero rows, which looks identical to a render bug.
    const payload = await firstChart.json();
    expect(
      (payload?.data?.rows ?? []).length,
      "seeded metrics returned no rows — the seed did not backfill attributable history, so there is nothing for the chart to draw",
    ).toBeGreaterThan(0);

    // Live data actually drew: the bar chart has ≥1 rectangle, the area chart
    // a path — seeded history spreads over 8 weeks, well inside both windows.
    // Recharts animates bars up from zero height, so allow for the tween on a
    // slow box; `toBeVisible` needs a non-empty box, not just a DOM node.
    const bars = page.locator('[data-testid="dashboard-chart"] svg .recharts-bar-rectangle');
    await expect(bars.first()).toBeVisible({ timeout: 15_000 });
    const area = page.locator('[data-testid="dashboard-chart"] svg .recharts-area-area');
    await expect(area.first()).toBeVisible({ timeout: 15_000 });
  });

  // The share flow end to end: narrow the dashboard, open one chart, and check
  // the permalink the page hands you reproduces exactly that view. jsdom can
  // assert the href; only a browser proves the chart actually draws at the
  // other end of it.
  test("opening a chart keeps the dashboard's filter selection, and its link reproduces it", async ({ page }) => {
    await page.goto("/dashboards/organization");
    await cardNamed(page, "Operations").click();
    await expect(page).toHaveURL(/\/dashboards\/\d+$/);

    // Narrow the window, then open a chart from that narrowed view. Its card
    // fetches the narrowed data as soon as the option is picked, and the chart
    // page reads that same cached entry without asking again, so the listener
    // goes in before the click.
    const openLink = page.getByTestId("chart-open-link").first();
    const chartId = (await openLink.getAttribute("href"))?.match(/\/charts\/(\d+)/)?.[1];
    expect(chartId).toBeTruthy();
    const chartData = `/api/dashboards/charts/${chartId}/data`;
    const narrowed = page.waitForResponse(
      r => r.url().includes(chartData) && r.url().includes("last_days=30") && r.status() === 200,
    );
    await page.getByTestId("dashboard-filter-date-range").click();
    await page.getByRole("option", { name: "Last 30 days" }).click();
    await expect(page).toHaveURL(/last_days=30/);
    await narrowed;

    // The URL changes before React re-renders the cards, so the link can still
    // carry the old selection for a moment. Wait for the link itself.
    await expect(openLink).toHaveAttribute("href", /last_days=30/);
    // From here on, any data request for this chart must be for the narrowed
    // window.
    const otherWindows: string[] = [];
    page.on("request", r => {
      if (r.url().includes(chartData) && !r.url().includes("last_days=30")) otherWindows.push(r.url());
    });
    await openLink.click();
    await expect(page).toHaveURL(/\/dashboards\/\d+\/charts\/\d+\?.*last_days=30/);
    await expect(page.getByTestId("dashboard-filter-date-range")).toContainText("Last 30 days");

    // The chart drew on its own page. `.recharts-surface` IS the <svg>, not a
    // child of one — a descendant selector here matches nothing.
    await expect(page.getByTestId("chart-detail")).toBeVisible();
    await expect(
      page.locator('[data-testid="chart-detail-chart"] svg.recharts-surface').first(),
    ).toBeVisible({ timeout: 8_000 });
    expect(otherWindows).toEqual([]);

    // The share link is self-describing: it pins the selection rather than
    // relying on whatever the defaults happen to be when it is opened.
    await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
    await page.getByTestId("chart-share").click();
    await expect(page.getByTestId("share-link")).toBeVisible();
    await page.getByTestId("share-copy").click();
    const copied = await page.evaluate(() => navigator.clipboard.readText());
    expect(copied).toContain("last_days=30");
    expect(copied).toMatch(/\/shared\/charts\/[\w-]{40,}\?/);

    // Following it lands on the same view — this is what a colleague receives,
    // through the token route rather than the dashboard one.
    await page.goto(copied);
    await expect(page.getByTestId("shared-chart")).toBeVisible();
    await expect(page.getByTestId("dashboard-filter-date-range")).toContainText("Last 30 days");
  });

  // Axis labels are the one thing jsdom cannot check — recharts measures 0x0
  // there and renders no SVG — so the date formatting is asserted in a real
  // browser. The seeded "Words processed per week" chart is weekly.
  test("time axes read as dates, and follow the granularity control", async ({ page }) => {
    await page.goto("/dashboards/organization");
    await cardNamed(page, "Operations").click();
    await expect(page).toHaveURL(/\/dashboards\/\d+$/);

    // The weekly chart by its title, not by position: charts heal onto the end
    // of the dashboard, so which card comes first depends on the database's
    // history. The Y axis shares the tick class; only X ticks are `bottom`.
    const weekly = page.getByTestId("dashboard-chart-card")
      .filter({ has: page.getByTestId("chart-open-link").getByText("Words processed per week", { exact: true }) });
    const ticks = weekly.locator(
      '[data-testid="dashboard-chart"] text.recharts-cartesian-axis-tick-value[orientation="bottom"]',
    );
    // Weekly: a span, "14 Sep - 20 Sep".
    await expect(ticks.first()).toHaveText(/^\d{1,2} \w{3} - \d{1,2} \w{3}$/, { timeout: 15_000 });
    // …and THINNED. A 90-day window is ~13 weeks; printing a span per week is
    // unreadable, so recharts drops the ones that would not fit (minTickGap).
    // A loose bound: this fails if labelling ever goes back to one-per-point,
    // without pinning a count that depends on the exact card width.
    expect(await ticks.count()).toBeLessThanOrEqual(8);

    // Monthly: the month name alone.
    await page.getByTestId("dashboard-filter-granularity").click();
    await page.getByRole("option", { name: "Monthly" }).click();
    await expect(page).toHaveURL(/grain=month/);
    await expect(ticks.first()).toHaveText(/^\w{3}$/, { timeout: 15_000 });

    // Daily: day and month, no year.
    await page.getByTestId("dashboard-filter-granularity").click();
    await page.getByRole("option", { name: "Daily" }).click();
    await expect(page).toHaveURL(/grain=day/);
    await expect(ticks.first()).toHaveText(/^\d{1,2} \w{3}$/, { timeout: 15_000 });
  });

  // Large values make long Y labels: the chart data is the real response with
  // every number replaced by a big one, so the test never depends on how much
  // the seed happens to hold.
  test("Y axis labels are compact and never cut off", async ({ page }) => {
    await page.route(/\/api\/dashboards\/charts\/\d+\/data/, async route => {
      const response = await route.fetch();
      const body = await response.json();
      const rows = body.data.rows as Record<string, unknown>[];
      body.data.rows = rows.map((row, i) =>
        Object.fromEntries(Object.entries(row).map(([k, v]) => [k, typeof v === "number" ? 12_345_678 + i * 1_000_000 : v])));
      await route.fulfill({ response, json: body });
    });
    await page.goto("/dashboards/organization");
    await cardNamed(page, "Operations").click();
    await expect(page).toHaveURL(/\/dashboards\/\d+$/);

    const weekly = page.getByTestId("dashboard-chart-card")
      .filter({ has: page.getByTestId("chart-open-link").getByText("Words processed per week", { exact: true }) });
    const chart = weekly.getByTestId("dashboard-chart");
    const yTicks = chart.locator('text.recharts-cartesian-axis-tick-value[orientation="left"]');
    await expect(yTicks.last()).toHaveText(/^\d+(\.\d)?M$/, { timeout: 15_000 });

    const surface = await chart.locator(".recharts-wrapper > svg.recharts-surface").boundingBox();
    expect(surface).not.toBeNull();
    for (const tick of await yTicks.all()) {
      const box = await tick.boundingBox();
      expect(box, `tick "${await tick.textContent()}"`).not.toBeNull();
      expect(box!.x, `tick "${await tick.textContent()}" starts left of the chart`).toBeGreaterThanOrEqual(surface!.x);
    }

    // The tooltip uses the same short form as the axis.
    const plot = chart.locator(".recharts-wrapper > svg.recharts-surface");
    await plot.scrollIntoViewIfNeeded();
    await plot.hover({ position: { x: surface!.width * 0.6, y: surface!.height * 0.5 } });
    await expect(chart.locator(".recharts-tooltip-wrapper")).toContainText(/\d+(\.\d)?M\b/);
  });

  // Extra series make both the legend and the tooltip tall, so hovering low in
  // the chart puts the tooltip over the legend; whatever is on top there is
  // what the reader sees.
  test("the data tooltip is drawn over the legend, not under it", async ({ page }) => {
    // Tall enough that the legend under the chart, and the stretch of it the
    // tooltip reaches over, are on screen: hit testing sees only the viewport.
    await page.setViewportSize({ width: 1280, height: 1600 });
    await page.route(/\/api\/dashboards\/charts\/\d+\/data/, async route => {
      const response = await route.fetch();
      const body = await response.json();
      const rows = body.data.rows as Record<string, unknown>[];
      if (body.data.columns.includes("kind")) {
        const weeks = [...new Set(rows.map(r => r.week))];
        body.data.rows = weeks.flatMap(week =>
          Array.from({ length: 10 }, (_, i) => ({ week, kind: `origin ${i + 1}`, words: 100 + i, segments: 1, characters: 1 })));
      }
      await route.fulfill({ response, json: body });
    });
    await page.goto("/dashboards/organization");
    await cardNamed(page, "Operations").click();
    await expect(page).toHaveURL(/\/dashboards\/\d+$/);

    const card = page.getByTestId("dashboard-chart-card")
      .filter({ has: page.getByTestId("chart-open-link").getByText("Words by origin per week", { exact: true }) });
    const chart = card.getByTestId("dashboard-chart");
    const legend = chart.getByTestId("chart-legend");
    await expect(legend).toContainText("origin 10", { timeout: 15_000 });
    // A tooltip that fits the plot stays inside it; only one taller than the
    // plot reaches the legend, so make this one tall.
    await page.addStyleTag({ content: '[data-testid="chart-tooltip"] { font-size: 24px; line-height: 1.6; }' });

    const surface = chart.locator(".recharts-wrapper > svg.recharts-surface");
    await surface.scrollIntoViewIfNeeded();
    const plot = (await surface.boundingBox())!;
    await surface.hover({ position: { x: plot.width * 0.5, y: plot.height * 0.6 } });
    const tooltip = chart.locator(".recharts-tooltip-wrapper");
    await expect(tooltip).toContainText("origin 10");
    // Let the running transitions end before measuring. allSettled, not all:
    // a transition Recharts restarts on a re-draw is cancelled, and a
    // cancelled animation rejects `finished` with an AbortError.
    await tooltip.evaluate(el => Promise.allSettled(el.getAnimations({ subtree: true }).map(a => a.finished)));

    // Recharts slides the tooltip into place (a 400 ms transform transition),
    // so a box read when it appears is stale a moment later. Wait in the page
    // until it stops moving, then measure both boxes and probe in that frame.
    const { overlapX, overlapY, onTop } = await chart.evaluate(root => new Promise<{
      overlapX: number; overlapY: number; onTop: boolean;
    }>(resolve => {
      const wrapper = root.querySelector<HTMLElement>(".recharts-tooltip-wrapper")!;
      const legendBox = root.querySelector<HTMLElement>('[data-testid="chart-legend"]')!.getBoundingClientRect();
      let last = "";
      const onFrame = () => {
        const t = wrapper.getBoundingClientRect();
        if (`${t.x},${t.y}` !== last) {
          last = `${t.x},${t.y}`;
          requestAnimationFrame(onFrame);
          return;
        }
        const left = Math.max(t.left, legendBox.left), right = Math.min(t.right, legendBox.right);
        const top = Math.max(t.top, legendBox.top), bottom = Math.min(t.bottom, legendBox.bottom);
        // The tooltip ignores the mouse (pointer-events: none), and so does hit
        // testing; turned back on for one probe, hit testing follows paint order.
        wrapper.style.pointerEvents = "auto";
        const hit = document.elementFromPoint((left + right) / 2, (top + bottom) / 2);
        wrapper.style.pointerEvents = "none";
        resolve({ overlapX: right - left, overlapY: bottom - top, onTop: wrapper.contains(hit) });
      };
      requestAnimationFrame(onFrame);
    }));
    expect(overlapX, "the tooltip overlaps the legend horizontally").toBeGreaterThan(0);
    expect(overlapY, "the tooltip overlaps the legend vertically").toBeGreaterThan(0);
    expect(onTop, "the tooltip is on top where it overlaps the legend").toBe(true);
  });

  // Clicking a legend entry keeps it alone and strikes the others; clicking it
  // again shows everything. A pie recomputes the shares of what is left.
  test("a legend entry keeps its slice alone on click, and a second click shows all", async ({ page }) => {
    await page.route(/\/api\/dashboards\/charts\/\d+\/data/, async route => {
      const response = await route.fetch();
      const body = await response.json();
      if (body.data.columns.includes("severity")) {
        body.data.rows = [
          { severity: "error", issues: 60 },
          { severity: "warning", issues: 30 },
          { severity: "info", issues: 10 },
        ];
      }
      await route.fulfill({ response, json: body });
    });
    await page.goto("/dashboards/organization");
    await cardNamed(page, "Operations").click();
    await expect(page).toHaveURL(/\/dashboards\/\d+$/);

    const card = page.getByTestId("dashboard-chart-card")
      .filter({ has: page.getByTestId("chart-open-link").getByText("QA issues for Nordwind Travel, by severity", { exact: true }) });
    const chart = card.getByTestId("dashboard-chart");
    const sectors = chart.locator(".recharts-pie-sector");
    const item = (name: string) => chart.getByTestId("chart-legend-item").filter({ hasText: name });
    await expect(sectors).toHaveCount(3, { timeout: 15_000 });
    // Biggest first; the legend names the slices, their shares are in the tooltip.
    await expect(chart.getByTestId("chart-legend-item")).toHaveText(["error", "warning", "info"]);
    await chart.scrollIntoViewIfNeeded();
    const rows = chart.getByTestId("chart-tooltip-row");

    await item("error").click();
    await expect(item("error")).toHaveAttribute("aria-pressed", "true");
    await expect(item("warning")).toHaveAttribute("aria-pressed", "false");
    await expect(item("info")).toHaveAttribute("aria-pressed", "false");
    await expect(sectors).toHaveCount(1);
    await hoverSlice(page, sectors.first());
    await expect(rows).toHaveText(["error60 issues100%"]);

    // Adding one back draws both.
    await item("info").click();
    await expect(sectors).toHaveCount(2);

    // Taking out both picks shows everything again.
    await item("error").click();
    await item("info").click();
    await expect(item("warning")).toHaveAttribute("aria-pressed", "true");
    await expect(sectors).toHaveCount(3);

    // Every slice with its value and share, the one under the pointer in bold,
    // and their total.
    await hoverSlice(page, sectors.first());
    await expect(rows).toHaveText(["error60 issues60%", "warning30 issues30%", "info10 issues10%"]);
    await expect(rows.first()).toHaveAttribute("data-highlighted", "true");
    await expect(chart.locator('[data-testid="chart-tooltip-row"][data-highlighted="true"]')).toHaveCount(1);
    await expect(chart.getByTestId("chart-tooltip-total")).toHaveText("Total100 issues");
  });

  // The pointer over a band highlights its series as the legend does: the other
  // bands fade, and its row in the tooltip is bold and carries the unit.
  test("hovering a band highlights its series and bolds its tooltip row", async ({ page }) => {
    await page.route(/\/api\/dashboards\/charts\/\d+\/data/, async route => {
      const response = await route.fetch();
      const body = await response.json();
      if (body.data.columns.includes("kind")) {
        // Four weeks of its own: an area needs two points to have a band.
        const weeks = ["2026-09-21", "2026-09-28", "2026-10-05", "2026-10-12"];
        body.data.rows = weeks.flatMap(week => [
          { week, kind: "tm", words: 300, segments: 3, characters: 3 },
          { week, kind: "mt", words: 600, segments: 6, characters: 6 },
          { week, kind: "human", words: 100, segments: 1, characters: 1 },
        ]);
      }
      await route.fulfill({ response, json: body });
    });
    await page.goto("/dashboards/organization");
    await cardNamed(page, "Operations").click();
    await expect(page).toHaveURL(/\/dashboards\/\d+$/);

    const card = page.getByTestId("dashboard-chart-card")
      .filter({ has: page.getByTestId("chart-open-link").getByText("Words by origin per week", { exact: true }) });
    const chart = card.getByTestId("dashboard-chart");
    // Biggest first, so mt (600) is the first band, at the bottom of the stack.
    const bands = chart.locator("path.recharts-area-area");
    await expect(bands).toHaveCount(3, { timeout: 15_000 });
    await expect(chart.getByTestId("chart-legend-item")).toHaveText(["mt", "tm", "human"]);

    // The middle of the bottom band, which runs the width of the plot at a
    // constant height. The mouse moves in viewport coordinates, so the chart
    // has to be on screen first.
    await chart.scrollIntoViewIfNeeded();
    const box = await settledBox(bands.first());
    await page.mouse.move(box.x + box.width / 2, box.y + box.height * 0.75);
    const bold = chart.locator('[data-testid="chart-tooltip-row"][data-highlighted="true"]');
    await expect(bold).toHaveCount(1);
    await expect(bold).toContainText("mt");
    await expect(bold).toContainText("600 words");
    await expect(chart.getByTestId("chart-tooltip-total")).toHaveText("Total1,000 words");
    await expect(bands.nth(1)).toHaveAttribute("fill-opacity", String(0.15 * 0.25));
    await expect(bands.first()).toHaveAttribute("fill-opacity", "0.15");

    // Hovering a legend entry highlights its band the same way, straight from
    // the chart, and leaving it shows every band again.
    const faded = String(0.15 * 0.25);
    await chart.getByTestId("chart-legend-item").filter({ hasText: "tm" }).hover();
    await expect(bands.nth(1)).toHaveAttribute("fill-opacity", "0.15");
    await expect(bands.nth(0)).toHaveAttribute("fill-opacity", faded);
    await expect(bands.nth(2)).toHaveAttribute("fill-opacity", faded);
    await page.mouse.move(0, 0);
    for (const i of [0, 1, 2]) await expect(bands.nth(i)).toHaveAttribute("fill-opacity", "0.15");
  });

  // The two tests that REARRANGE the Organization dashboards tab run one after
  // the other. The drag asserts the tab's whole order and saves it as one list,
  // so a card the move test is passing through the tab (or deletes a moment
  // later) would break it — that is a property of the gesture, not a race worth losing.
  test.describe("rearranging the Organization dashboards tab", () => {
    test.describe.configure({ mode: "serial" });

    // The GESTURE, in a real browser. jsdom cannot drive dnd-kit, so the unit
    // tests pin only the DECISION (resolveDrop); this pins the gesture. Drag
    // only reorders.
    //
    // Puts the order back, so the suite leaves the seed as it found it.
    test("dashboards reorder by dragging one card onto another", async ({ page }) => {
      await page.goto("/dashboards/organization");
      const cards = page.getByTestId("dashboard-card-draggable");
      await expect(cards.first()).toBeVisible();
      // The name alone: the card's full text also carries its preview tiles,
      // which change as their data loads.
      const namesNow = async () =>
        (await page.getByTestId("dashboard-card-name").allTextContents()).map(t => t.trim());
      // Guarantee a second card rather than skipping: a test that quietly stops
      // running is indistinguishable from one that passes, and this suite moves
      // dashboards between tabs, so the Organization dashboards tab's contents
      // are not a given.
      let cleanup: number | null = null;
      if ((await namesNow()).length < 2) {
        const res = await page.request.post(
          `/api/dashboards/${await firstDashboardId(page)}/duplicate`,
          { data: { personal: false } },
        );
        cleanup = (await res.json()).data.id;
        await page.reload();
        // allTextContents does not wait: read before the reload has rendered
        // and the tab looks empty.
        await expect.poll(async () => (await namesNow()).length).toBeGreaterThanOrEqual(2);
      }
      try {
        const before = await namesNow();
        expect(before.length).toBeGreaterThanOrEqual(2);

        const swapFirstTwo = async () => {
          const first = cards.nth(0);
          const grip = first.getByTestId("dashboard-drag-handle");
          const gb = (await grip.boundingBox())!;
          const target = (await cards.nth(1).boundingBox())!;
          await page.mouse.move(gb.x + gb.width / 2, gb.y + gb.height / 2);
          await page.mouse.down();
          // Cross the 4px activation constraint, then WAIT for dnd-kit to report the
          // drag before travelling — moves dispatched before activation are lost
          // ("drag test dispatches every pointer move…").
          await page.mouse.move(gb.x + gb.width / 2, gb.y + gb.height / 2 + 8);
          await expect(first).toHaveAttribute("data-dragging", "true");
          // Centre-onto-centre: closestCenter decides which side of the neighbour
          // the card lands on, so a marginal overlap makes the swap ambiguous.
          for (let i = 1; i <= 5; i++) {
            await page.mouse.move(
              gb.x + ((target.x + target.width / 2 - gb.x) * i) / 5,
              gb.y + ((target.y + target.height / 2 - gb.y) * i) / 5,
            );
          }
          const saved = page.waitForResponse(r => /\/dashboards\/order$/.test(r.url()) && r.request().method() === "PUT");
          await page.mouse.up();
          await saved;
        };

        await swapFirstTwo();
        await expect.poll(namesNow).toEqual([before[1], before[0], ...before.slice(2)]);

        // Swap back, and confirm the new order actually persisted rather than only
        // living in local state.
        await swapFirstTwo();
        await page.reload();
        await expect.poll(namesNow).toEqual(before);
      } finally {
        // Also on failure: a leaked copy on the Organization dashboards tab changes the next run.
        if (cleanup) await page.request.delete(`/api/dashboards/${cleanup}`);
      }
    });

    // Moving between tabs is an explicit action on the card, not a second meaning
    // for the drag. Uses a dashboard of its own: the other tests in this file
    // read the seeded "Operations" on the Organization dashboards tab.
    test("a dashboard moves between tabs from its card action", async ({ page }) => {
      const name = `Tabs ${Date.now()}`;
      const created = await page.request.post("/api/dashboards", { data: { name, personal: true } });
      const id = (await created.json()).data.id as number;
      try {
        const move = async (from: "shared" | "mine") => {
          await dashboardsTab(page, from).click();
          const target = cardNamed(page, name, "dashboard-card-draggable");
          await target.hover();
          await target.getByTestId("dashboard-folder-menu").click();
          const saved = page.waitForResponse(r => /\/visibility$/.test(r.url()) && r.request().method() === "PUT");
          await page.getByTestId("dashboard-move").click();
          await saved;
        };

        await page.goto("/dashboards/organization");
        await move("mine");
        await dashboardsTab(page, "shared").click();
        await expect(cardNamed(page, name)).toBeVisible();

        await move("shared");
        await dashboardsTab(page, "mine").click();
        await expect(cardNamed(page, name)).toBeVisible();
      } finally {
        await page.request.delete(`/api/dashboards/${id}`);
      }
    });

  });

  // Copying leaves the original alone, which is what makes it safe to do to the
  // org's dashboard. Cleans up the copy it makes.
  test("a dashboard can be copied to the other tab", async ({ page }) => {
    await page.goto("/dashboards/organization");
    const card = cardNamed(page, "Operations", "dashboard-card-draggable");
    await expect(card).toBeVisible();
    await card.hover();
    await card.getByTestId("dashboard-folder-menu").click();
    // The card's actions stay in view while their menu is open, even with the
    // pointer on the menu, outside the card.
    await page.getByTestId("dashboard-copy").hover();
    await expect(card.getByTestId("dashboard-card-actions")).toHaveCSS("opacity", "1");

    const created = page.waitForResponse(r => /\/duplicate$/.test(r.url()) && r.request().method() === "POST");
    await page.getByTestId("dashboard-copy").click();
    const copied = (await (await created).json()).data as { id: number; name: string };

    try {
      // The original is untouched…
      await expect(cardNamed(page, "Operations")).toBeVisible();
      // …and the copy carries its charts, under a name that says what it is.
      expect(copied.name).toMatch(/^Copy of Operations/);
      await dashboardsTab(page, "mine").click();
      const copy = cardNamed(page, copied.name);
      await expect(copy).toBeVisible();
      await expect(copy).toContainText(/\d+ charts?/);
    } finally {
      // Also on failure: a leaked copy changes the next run's copy name.
      await page.request.delete(`/api/dashboards/${copied.id}`);
    }
  });

  test("navigation: Dashboards appears in the left nav for managers", async ({ page }) => {
    await page.getByRole("link", { name: "Dashboards" }).click();
    // /dashboards lands on Pinned or on Organization dashboards.
    await expect(page).toHaveURL(/\/dashboards\/(pinned|organization)$/);
  });

  // The builder: "New dashboard" makes an empty folder and
  // "New chart" fills it from a form, with no trip through the chat. Both
  // create real rows, so both clean up after themselves.
  test("New dashboard creates an empty one and New chart fills it", async ({ page }) => {
    const name = `Builder ${Date.now()}`;
    await page.goto("/dashboards/organization");

    // On the My dashboards tab, so the new one is personal: workers run this
    // file in parallel, and a shared dashboard appearing and vanishing mid-run
    // is visible to every other test's dashboard list.
    await dashboardsTab(page, "mine").click();
    // The tab switch is a router transition: wait until it has rendered, the
    // way a user presses New on the tab they can see.
    await expect(page).toHaveURL(/\/dashboards\/mine$/);
    await expect(dashboardsTab(page, "mine")).toHaveAttribute("aria-current", "page");
    await page.getByTestId("new-dashboard").click();
    await page.getByTestId("dashboard-name").fill(name);
    const created = page.waitForResponse(
      r => r.url().endsWith("/api/dashboards") && r.request().method() === "POST" && r.status() === 201,
    );
    await page.getByTestId("create-dashboard-submit").click();
    const body = (await (await created).json()).data;
    const dashboardId = body.id as number;
    // The tab was switched after the page loaded; the dialog must follow it.
    expect(body.personal, "created from My dashboards, so it must be personal").toBe(true);

    try {
      // Straight into the new dashboard, which is empty — that is the folder
      // half of the feature.
      await expect(page).toHaveURL(new RegExp(`/dashboards/${dashboardId}$`));
      await expect(page.getByText("No charts yet.")).toBeVisible();

      await page.getByTestId("new-chart").click();
      // Nothing can be built before a measure is chosen: everything else is a
      // property of it, and the form says so.
      await page.getByTestId("create-chart-submit").click();
      await expect(page.getByText("Pick a metric", { exact: true })).toBeVisible();
      await page.getByTestId("measure-qa_issues").click();
      // Choosing the measure seeds a usable chart — its definition, a value to
      // plot, and a title — so Create is reachable without more input.
      await expect(page.getByTestId("measure-definition")).not.toBeEmpty();
      await expect(page.getByTestId("chart-value-issues")).toBeChecked();

      await page.getByTestId("chart-title").fill("QA issues per week");
      // Both waits armed before the click: the data request follows the save's
      // invalidation, and can land before a wait set up afterwards exists.
      const chartCreated = page.waitForResponse(
        r => r.url().includes(`/api/dashboards/${dashboardId}/charts`) && r.status() === 201,
      );
      // Under the CI test timeout (20s), so a miss fails here, not in cleanup.
      const chartData = page.waitForResponse(
        r => /\/api\/dashboards\/charts\/\d+\/data/.test(r.url()) && r.status() === 200,
        { timeout: 15_000 },
      );
      await page.getByTestId("create-chart-submit").click();
      await chartCreated;
      // The real proof: the saved definition renders through the metrics layer.
      await chartData;
      await expect(page.getByTestId("dashboard-chart-card").filter({ hasText: "QA issues per week" })).toBeVisible();
    } finally {
      await page.request.delete(`/api/dashboards/${dashboardId}`);
    }
  });

  // Editing is the same dialog, seeded from the chart. Round trip in a real
  // browser because the proof is that the EDITED definition still renders.
  test("the chart edit action re-opens the builder and saves the whole chart", async ({ page }) => {
    const name = `Edit ${Date.now()}`;
    const created = await page.request.post("/api/dashboards", { data: { name, personal: true } });
    const dashboardId = (await created.json()).data.id as number;
    await page.request.post(`/api/dashboards/${dashboardId}/charts`, {
      data: {
        title: "Before", measure: "qa_issues", dimensions: ["week"],
        chart_type: "line", x_key: "week", series: ["issues"], controls: ["customer"],
      },
    });

    try {
      await page.goto(`/dashboards/${dashboardId}`);
      await page.getByTestId("dashboard-chart-card").hover();
      await page.getByTestId("chart-edit").click();

      // Seeded from the chart, not from the defaults.
      await expect(page.getByTestId("chart-title")).toHaveValue("Before");
      await expect(page.getByTestId("measure-qa_issues")).toHaveAttribute("aria-checked", "true");
      await expect(page.getByTestId("builder-filter-customer")).toBeChecked();

      await page.getByTestId("chart-title").fill("After");
      await page.getByTestId("chart-type-bar").click();
      // Both waits armed before the click (see the create test above).
      const saved = page.waitForResponse(
        r => /\/api\/dashboards\/charts\/\d+$/.test(r.url())
          && r.request().method() === "PUT" && r.status() === 204,
      );
      // Under the CI test timeout (20s), so a miss fails here, not in cleanup.
      const chartData = page.waitForResponse(
        r => /\/api\/dashboards\/charts\/\d+\/data/.test(r.url()) && r.status() === 200,
        { timeout: 15_000 },
      );
      await page.getByTestId("create-chart-submit").click();
      await saved;
      // The edited definition renders — the whole point of validating it.
      await chartData;
      await expect(page.getByTestId("dashboard-chart-card").filter({ hasText: "After" })).toBeVisible();
      await expect(page.getByText("Before")).toHaveCount(0);
    } finally {
      await page.request.delete(`/api/dashboards/${dashboardId}`);
    }
  });

  // Saving an edit reloads every chart on the dashboard, not just the edited one.
  test("editing one chart leaves the dashboard's other charts loading", async ({ page }) => {
    const name = `Edit two ${Date.now()}`;
    const created = await page.request.post("/api/dashboards", { data: { name, personal: true } });
    const dashboardId = (await created.json()).data.id as number;
    const chartIds: number[] = [];
    for (const [title, measure, dims, series] of [
      ["First", "words_processed", ["week"], ["words"]],
      ["Second", "segments_by_origin", ["week", "kind"], ["words"]],
    ] as const) {
      const chart = await page.request.post(`/api/dashboards/${dashboardId}/charts`, {
        data: { title, measure, dimensions: dims, chart_type: "bar", x_key: "week", series, controls: [] },
      });
      chartIds.push((await chart.json()).data.id as number);
    }
    const failed: string[] = [];
    page.on("response", async r => {
      if (/\/api\/dashboards\/charts\/\d+\/data/.test(r.url()) && r.status() >= 400) {
        failed.push(`${r.status()} ${r.url()} ${await r.text().catch(() => "")}`);
      }
    });

    try {
      await page.goto(`/dashboards/${dashboardId}`);
      const cards = page.getByTestId("dashboard-chart-card");
      await expect(cards).toHaveCount(2);
      const first = cards.filter({ hasText: "First" });
      await first.hover();
      await first.getByTestId("chart-edit").click();
      await page.getByTestId("chart-title").fill("First, edited");
      const saved = page.waitForResponse(
        r => /\/api\/dashboards\/charts\/\d+$/.test(r.url()) && r.request().method() === "PUT" && r.status() === 204,
      );
      // The second chart was drawn before the save; only its reload proves
      // the save didn't break it.
      let putSeen = false;
      const secondReloaded = page.waitForResponse(r => {
        if (r.request().method() === "PUT" && /\/api\/dashboards\/charts\/\d+$/.test(r.url())) putSeen = true;
        return putSeen && r.url().includes(`/api/dashboards/charts/${chartIds[1]}/data`);
      });
      await page.getByTestId("create-chart-submit").click();
      await saved;
      expect((await secondReloaded).status(), "the second chart reloads").toBe(200);

      await expect(cards.filter({ hasText: "First, edited" })).toBeVisible();
      const second = cards.filter({ hasText: "Second" });
      await expect(second.getByTestId("dashboard-chart").locator(".recharts-wrapper")).toBeVisible({ timeout: 15_000 });
      await expect(page.getByText(/Couldn't load the chart/)).toHaveCount(0);
      expect(failed, "no chart data request failed").toEqual([]);
    } finally {
      await page.request.delete(`/api/dashboards/${dashboardId}`);
    }
  });

  // A personal dashboard with one chart, created over the API so each test owns
  // its data. Personal, because workers run this file in parallel and a shared
  // dashboard appearing mid-run shows up in every other test's list.
  async function dashboardWithChart(page: Page, label: string) {
    const created = await page.request.post("/api/dashboards", { data: { name: `${label} ${Date.now()}`, personal: true } });
    const dashboardId = (await created.json()).data.id as number;
    const chart = await page.request.post(`/api/dashboards/${dashboardId}/charts`, {
      data: { title: `${label} chart`, measure: "qa_issues", dimensions: ["week"], chart_type: "line", x_key: "week", series: ["issues"] },
    });
    const chartId = (await chart.json()).data.id as number;
    return { dashboardId, chartId, title: `${label} chart` };
  }

  test("a chart copies beside itself and moves to another dashboard", async ({ page }) => {
    const src = await dashboardWithChart(page, "MoveSrc");
    const dst = await dashboardWithChart(page, "MoveDst");
    try {
      await page.goto(`/dashboards/${src.dashboardId}`);
      // By EXACT title: once the copy exists, "Copy of <title>" contains it too.
      const cardTitled = (title: string) => page.getByTestId("dashboard-chart-card")
        .filter({ has: page.getByTestId("chart-open-link").getByText(title, { exact: true }) });
      const card = cardTitled(src.title);
      await card.hover();

      // Copy onto its own dashboard: the copy needs a different name, or the
      // dashboard would show two identical cards.
      await card.getByTestId("chart-folder-menu").click();
      await page.getByTestId("chart-copy").click();
      const copied = page.waitForResponse(r => r.url().endsWith(`/charts/${src.chartId}/copy`) && r.status() === 201);
      await page.getByTestId(`chart-copy-to-${src.dashboardId}`).click();
      await copied;
      await expect(cardTitled(`Copy of ${src.title}`)).toBeVisible();

      // Move the original away: it leaves this dashboard and lands on the other.
      await card.hover();
      await card.getByTestId("chart-folder-menu").click();
      await page.getByTestId("chart-move").click();
      const moved = page.waitForResponse(r => r.url().endsWith(`/charts/${src.chartId}/dashboard`) && r.status() === 204);
      await page.getByTestId(`chart-move-to-${dst.dashboardId}`).click();
      await moved;
      // Gone from here; the copy made a moment ago stays.
      await expect(cardTitled(src.title)).toHaveCount(0);
      await expect(cardTitled(`Copy of ${src.title}`)).toBeVisible();

      await page.goto(`/dashboards/${dst.dashboardId}`);
      await expect(cardTitled(src.title)).toBeVisible();
    } finally {
      await page.request.delete(`/api/dashboards/${src.dashboardId}`);
      await page.request.delete(`/api/dashboards/${dst.dashboardId}`);
    }
  });

  // A regular download: the file is opened, not just its name checked, so a
  // blank or broken workbook fails here.
  test("export downloads the chart as an xlsx", async ({ page }) => {
    const src = await dashboardWithChart(page, "Export");
    try {
      await page.goto(`/dashboards/${src.dashboardId}`);
      const card = page.getByTestId("dashboard-chart-card").filter({ hasText: src.title });
      await expect(card.getByTestId("dashboard-chart")).toBeVisible({ timeout: 30_000 });
      await card.hover();
      const download = page.waitForEvent("download");
      await card.getByTestId("chart-export").click();
      const file = await download;

      expect(file.suggestedFilename()).toMatch(new RegExp(`^${src.title} \\d{4}-\\d{2}-\\d{2}\\.xlsx$`));
      const bytes = readFileSync((await file.path())!);
      // An xlsx is a zip: it starts "PK\x03\x04".
      expect([...bytes.subarray(0, 4)]).toEqual([0x50, 0x4b, 0x03, 0x04]);
      expect(bytes.length).toBeGreaterThan(1000);
    } finally {
      await page.request.delete(`/api/dashboards/${src.dashboardId}`);
    }
  });

  test("the builder only offers a measure's own dimensions", async ({ page }) => {
    await page.goto("/dashboards/organization");
    await cardNamed(page, "Operations").click();
    await expect(page).toHaveURL(/\/dashboards\/\d+$/);
    await page.getByTestId("new-chart").click();

    // qa_issues lives on the QA graph, which has no engine columns at all.
    await page.getByTestId("measure-qa_issues").click();
    await page.getByTestId("chart-series-key").click();
    await expect(page.getByRole("option", { name: "Check type" })).toBeVisible();
    await expect(page.getByRole("option", { name: "MT engine" })).toHaveCount(0);
    await page.keyboard.press("Escape");

    // Switching measures re-reads the vocabulary rather than keeping the old one.
    await page.getByTestId("measure-segments_by_origin").click();
    await page.getByTestId("chart-series-key").click();
    await expect(page.getByRole("option", { name: "MT engine" })).toBeVisible();
    await expect(page.getByRole("option", { name: "Check type" })).toHaveCount(0);
  });

});
