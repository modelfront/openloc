import { defineConfig } from "@playwright/test";
import { STORAGE_STATE } from "./global-setup";

// Optional cross-machine sharding (mirrors playwright.journeys.config.ts). CI
// (two nodes for the `e2e` job) exports
// PW_SHARD="<current>/<total>" per node; Playwright splits the spec FILES across
// shards. Unset locally → whole suite runs unsharded. Safe to shard because the
// page-level tests already run fullyParallel on a shared DB (so they're
// order-independent), and each CI shard boots its own freshly-seeded DB.
const shardParts = process.env.PW_SHARD?.split("/");
const shard =
  shardParts?.length === 2
    ? { current: Number(shardParts[0]), total: Number(shardParts[1]) }
    : undefined;

export default defineConfig({
  testDir: "./tests",
  shard,
  // Log in once, reuse the auth state across all tests (see global-setup.ts).
  globalSetup: "./global-setup.ts",
  // Most tests run in <10s; pipeline-dependent tests (uploadInc057File and
  // friends) self-extend via `test.setTimeout(60_000)`.
  //
  // CI gets 2x: a CI machine runs backend + Vite + Postgres + Chromium on
  // a few shared vCPUs, so every wall-clock-sensitive step
  // (page.reload, keyboard.type of long strings) is ~2x slower than a dev
  // Mac. This is environmental headroom, not flake masking — retries stay
  // at 0 so genuinely flaky tests still fail loudly.
  timeout: process.env.CI ? 20_000 : 10_000,
  // Zero retries and no max-failures cap: a test that passes only on retry is
  // still flaky, and a test left "did not run" hides whether it would pass.
  // If CI ever needs retries for infrastructure reasons, opt in there
  // explicitly, not in the default config.
  retries: 0,
  // 4 workers locally; on CI, one per two CPU cores. Every CAT lifecycle test
  // uploads a file and enqueues pre-translate on the shared asynq pipeline;
  // more workers starve it and jobs wait >30s for pipeline_status to settle.
  // A CI machine also shares its cores with the backend, Vite and Postgres:
  // 4 vCPUs get 2 workers, a 2-vCPU runner 1.
  workers: process.env.CI ? "50%" : 4,
  fullyParallel: true,
  use: {
    // Dev-instance aware: APP_URL is exported by the Makefile from instance.conf.
    // Specs use relative URLs that resolve against this baseURL, so no
    // per-instance edits to the specs are needed.
    baseURL: process.env.APP_URL ?? "http://localhost:9000",
    // Every test starts authenticated as the seed owner (global-setup.ts). Specs
    // that need a different identity overlay their own token; auth.spec opts out.
    storageState: STORAGE_STATE,
    headless: true,
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
    // page.request.* GETs to /api/jobs/* can take 5-10s while the pipeline
    // is busy with other tests' uploads; sequential runs stay well under 5s.
    actionTimeout: 15_000,
  },
  projects: [
    {
      name: "chromium",
      use: { browserName: "chromium" },
    },
  ],
  // Expect the backend + frontend for THIS dev instance to be running before
  // tests (ports come from instance.conf via the Makefile). `make watch` starts
  // both, then `make e2e` runs tests against them.
});
