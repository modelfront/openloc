import { defineConfig } from "@playwright/test";
import { STORAGE_STATE } from "./global-setup";

// Optional cross-machine sharding. CI (three nodes for
// the journeys job) exports PW_SHARD="<current>/<total>" per node; Playwright
// splits the spec FILES across shards and keeps each describe.serial file
// atomic on one node. Unset locally → whole suite runs unsharded. Journeys are
// safe to shard because each file is self-contained (creates + tears down its
// own data); within-file step ordering is preserved by workers: 1 below.
const shardParts = process.env.PW_SHARD?.split("/");
const shard =
  shardParts?.length === 2
    ? { current: Number(shardParts[0]), total: Number(shardParts[1]) }
    : undefined;

/**
 * Journey test config — separate from the fast page-level tests. Journeys are
 * ordered multi-step flows, so they run serially. The JSON reporter writes
 * journey-results.json for machine-readable results.
 */
export default defineConfig({
  testDir: "./journeys",
  shard,
  // Log in once, reuse the auth state across all journeys (see global-setup.ts).
  globalSetup: "./global-setup.ts",
  timeout: 120_000, // per-test timeout (a journey describe block)
  retries: 0, // never retry — a flaky journey is a real bug
  // Serial, and NOT raisable to workers>1 on a shared database. Journey files
  // clean up their own data, but several of them snapshot GLOBAL row counts and
  // assert they haven't moved, so a concurrently-running file breaks them.
  //
  // CI's parallelism: 3 is safe only because each node boots its own compose
  // stack + database, so its shard sees nobody else's rows. Reproducing that
  // locally needs isolated DB instances, not more workers.
  workers: 1,
  fullyParallel: false,
  maxFailures: 1, // stop on first failure — later steps depend on earlier ones
  reporter: [
    ["line"], // live terminal output
    ["json", { outputFile: "journey-results.json" }],
  ],
  use: {
    baseURL: process.env.APP_URL ?? "http://localhost:9000",
    storageState: STORAGE_STATE,
    headless: true,
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
    actionTimeout: 10_000, // longer than page tests — journey steps do more
  },
  projects: [
    {
      name: "chromium",
      use: { browserName: "chromium" },
    },
  ],
});
