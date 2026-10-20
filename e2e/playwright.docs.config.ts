import { defineConfig } from "@playwright/test";
import { STORAGE_STATE } from "./global-setup";

/**
 * Documentation screenshots — `make docs-screenshots`.
 *
 * Not a test suite: the specs in ./docs-shots drive the seeded app and write
 * PNGs into internal/productdocs/content/assets/, where go:embed picks them up.
 * Re-shooting from the real UI is what keeps the pictures in the help pages
 * from going stale.
 *
 * Run against a fresh database (`make kill && make reset && make fresh`, then
 * `make docs-screenshots`, which seeds the docs dataset itself) so every
 * shot shows the same data and a re-run produces identical bytes — identical
 * bytes mean no git churn.
 *
 * Fixed 1440×900 light viewport at 2×. The pages are read on high-DPI screens,
 * where a 1× PNG is visibly soft — text in a screenshot has to survive being
 * displayed at the size it was captured. The cost is file size, and these are
 * `go:embed`-ed into the binary, so keep an eye on the assets total.
 */
export default defineConfig({
  testDir: "./docs-shots",
  globalSetup: "./global-setup.ts",
  timeout: 60_000,
  retries: 0,
  workers: 1,
  fullyParallel: false,
  reporter: [["line"]],
  use: {
    baseURL: process.env.APP_URL ?? "http://localhost:9000",
    storageState: STORAGE_STATE,
    headless: true,
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    colorScheme: "light",
    screenshot: "off",
    trace: "off",
    actionTimeout: 15_000,
  },
  projects: [{ name: "chromium", use: { browserName: "chromium" } }],
});
