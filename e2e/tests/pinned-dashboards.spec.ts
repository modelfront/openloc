import { test, expect, type Page } from "@playwright/test";
import { login } from "./helpers";

/**
 * Pinned — a person's own dashboards of references to charts that live on
 * other dashboards. Every test pins its own personal dashboard, made over the
 * API, and removes both the pinned dashboard and the original afterwards.
 */

const cardNamed = (page: Page, name: string) =>
  page.getByTestId("dashboard-card").filter({ has: page.getByText(name, { exact: true }) });

const chartCardTitled = (page: Page, title: string) =>
  page.getByTestId("dashboard-chart-card")
    .filter({ has: page.getByTestId("chart-open-link").getByText(title, { exact: true }) });

/** A personal dashboard with `titles.length` charts. */
async function dashboardWithCharts(page: Page, label: string, titles: string[]) {
  const name = `${label} ${Date.now()}`;
  const created = await page.request.post("/api/dashboards", { data: { name, personal: true } });
  const dashboardId = (await created.json()).data.id as number;
  const chartIds: number[] = [];
  for (const title of titles) {
    const chart = await page.request.post(`/api/dashboards/${dashboardId}/charts`, {
      data: { title, measure: "qa_issues", dimensions: ["week"], chart_type: "line", x_key: "week", series: ["issues"] },
    });
    chartIds.push((await chart.json()).data.id as number);
  }
  return { name, dashboardId, chartIds };
}

/** Removes the pinned dashboards with any of these names, then the original. */
async function cleanUp(page: Page, dashboardId: number, names: string[]) {
  const pinned = (await (await page.request.get("/api/dashboards/pinned")).json()).data as { id: number; name: string }[];
  for (const p of pinned.filter(p => names.includes(p.name))) {
    await page.request.delete(`/api/dashboards/pinned/${p.id}`);
  }
  await page.request.delete(`/api/dashboards/${dashboardId}`);
}

test.describe("Pinned dashboards", () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test("a chart pinned from its card lands in Pinned and opens the original", async ({ page }) => {
    const src = await dashboardWithCharts(page, "PinChart", ["Pinned issues"]);
    const [chartId] = src.chartIds;
    try {
      await page.goto(`/dashboards/${src.dashboardId}`);
      const card = chartCardTitled(page, "Pinned issues");
      await card.hover();
      const pinned = page.waitForResponse(
        r => r.url().endsWith(`/charts/${chartId}/pin`) && r.request().method() === "POST" && r.status() === 204,
      );
      await card.getByRole("button", { name: "Pin Pinned issues" }).click();
      await pinned;
      await expect(card.getByRole("button", { name: "Unpin Pinned issues" })).toHaveAttribute("aria-pressed", "true");

      // Something is pinned, so the list opens on Pinned.
      await page.goto("/dashboards");
      await expect(page).toHaveURL(/\/dashboards\/pinned$/);
      // Named after the dashboard the chart came from; opens inside Pinned.
      await cardNamed(page, src.name).click();
      await expect(page).toHaveURL(/\/dashboards\/pinned\/\d+$/);

      // The chart itself opens on its original page.
      await chartCardTitled(page, "Pinned issues").getByTestId("chart-open-link").click();
      await expect(page).toHaveURL(new RegExp(`/dashboards/${src.dashboardId}/charts/${chartId}\\?`));
    } finally {
      await cleanUp(page, src.dashboardId, [src.name]);
    }
  });

  test("a pinned dashboard is renamed and thinned in Pinned without touching the original", async ({ page }) => {
    const src = await dashboardWithCharts(page, "PinBoard", ["First issues", "Second issues"]);
    const renamed = `${src.name} (mine)`;
    try {
      await page.goto("/dashboards/mine");
      const card = page.getByTestId("dashboard-card-draggable")
        .filter({ has: page.getByText(src.name, { exact: true }) });
      // The pin is always shown, before the name; the other actions only on
      // hover. Checked as CSS opacity: toBeVisible() also passes for a
      // transparent element.
      const actions = card.getByTestId("dashboard-card-actions");
      await page.mouse.move(0, 0);
      await expect(actions).toHaveCSS("opacity", "0");
      await expect(card.getByRole("button", { name: `Pin ${src.name}` })).toHaveCSS("opacity", "1");
      const pinned = page.waitForResponse(
        r => r.url().endsWith(`/dashboards/${src.dashboardId}/pin`) && r.request().method() === "POST" && r.status() === 204,
      );
      await card.getByRole("button", { name: `Pin ${src.name}` }).click();
      await pinned;
      await page.mouse.move(0, 0);
      await expect(card.getByRole("button", { name: `Unpin ${src.name}` })).toHaveCSS("opacity", "1");
      await expect(actions).toHaveCSS("opacity", "0");

      // Rename in Pinned: the original keeps its name.
      await page.goto("/dashboards/pinned");
      const pinnedCard = page.getByTestId("dashboard-card-draggable")
        .filter({ has: page.getByText(src.name, { exact: true }) });
      await pinnedCard.hover();
      await pinnedCard.getByTestId("dashboard-rename").click();
      const field = page.getByLabel("Rename dashboard");
      await field.fill(renamed);
      const saved = page.waitForResponse(r => /\/api\/dashboards\/pinned\/\d+\/name$/.test(r.url()) && r.status() === 204);
      await field.press("Enter");
      await saved;
      await expect(cardNamed(page, renamed)).toBeVisible();
      const original = (await (await page.request.get(`/api/dashboards/${src.dashboardId}`)).json()).data;
      expect(original.name).toBe(src.name);

      // Unpin one chart: the other stays, and the original keeps both.
      await cardNamed(page, renamed).click();
      await expect(chartCardTitled(page, "Second issues")).toBeVisible();
      const first = chartCardTitled(page, "First issues");
      await first.hover();
      const unpinned = page.waitForResponse(
        r => r.url().endsWith(`/charts/${src.chartIds[0]}/pin`) && r.request().method() === "DELETE" && r.status() === 204,
      );
      await first.getByRole("button", { name: "Unpin First issues" }).click();
      await unpinned;
      await expect(first).toHaveCount(0);
      await expect(chartCardTitled(page, "Second issues")).toBeVisible();

      const after = (await (await page.request.get(`/api/dashboards/${src.dashboardId}`)).json()).data;
      expect(after.charts.map((c: { id: number }) => c.id).sort()).toEqual([...src.chartIds].sort());
      // One chart off means the dashboard as a whole is no longer pinned.
      const pins = (await (await page.request.get("/api/dashboards/pins")).json()).data;
      expect(pins.dashboard_ids).not.toContain(src.dashboardId);
      expect(pins.chart_ids).toContain(src.chartIds[1]);
    } finally {
      await cleanUp(page, src.dashboardId, [src.name, renamed]);
    }
  });
});
