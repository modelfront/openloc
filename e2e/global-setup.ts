import { request } from "@playwright/test";
import { mkdirSync, writeFileSync } from "fs";
import path from "path";
import { SEED_ORG_SLUG } from "./seed-identity";

// Auth reuse. Log in once as the seed owner and persist a Playwright
// storageState (the tenant-scoped JWT in localStorage, plus the auth cookies).
// Every test starts authenticated in the seed tenant; `login()` is a single
// `goto('/')`.
//
// The token is a 24h tenant JWT (auth/handler.go tokenExpiry) — far longer than
// a full gate run — and is regenerated every invocation (globalSetup runs after
// each suite's DB reset), so it always matches the freshly-seeded DB.
//
// Tests that need a different identity (vendors, j9/j23/j26) overlay their own
// token via addInitScript/setItem, which wins over this one. auth.spec opts out
// entirely (it tests the logged-out login page).

const BASE = process.env.APP_URL ?? "http://localhost:9000";
// One file per shard. Sharded local runs (scripts/journeys-parallel.sh) point
// each shard at its OWN backend + database, so each logs in separately and gets
// a token valid only there. A single shared path means the shards clobber each
// other's token and the loser 401s on every request.
export const STORAGE_STATE = path.join(
  __dirname,
  ".auth",
  `owner${process.env.PW_SHARD ? `-${process.env.PW_SHARD.replace("/", "of")}` : ""}.json`,
);

export default async function globalSetup() {
  const ctx = await request.newContext({ baseURL: BASE });

  const res = await ctx.post("/api/auth/dev-login");
  if (!res.ok()) throw new Error(`global-setup dev-login failed: ${res.status()}`);
  const body = await res.json();

  // Seed identity belongs to 2 orgs → resolve a tenant-scoped token for the
  // seed tenant (where the seed data lives), matching helpers.ts/login().
  let token: string = body.token;
  if (body.needs_org_selection) {
    const orgs: Array<{ tenant_id: number; tenant_slug: string }> = body.orgs || [];
    const org = orgs.find((o) => o.tenant_slug === SEED_ORG_SLUG) || orgs[0];
    if (!org) throw new Error("global-setup: dev-login returned no orgs");
    const sel = await ctx.post(`/api/auth/orgs/${org.tenant_id}/select`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!sel.ok()) throw new Error(`global-setup org select failed: ${sel.status()}`);
    token = (await sel.json()).token;
  }
  // Capture the auth COOKIES the server set during dev-login + org-select.
  // Some flows —
  // e.g. the TMX export download — authenticate by cookie, not the localStorage
  // bearer token, so an empty cookie jar silently breaks them. Then layer the
  // localStorage token on top for the bearer-based API calls.
  const state = await ctx.storageState();
  await ctx.dispose();

  const origin = new URL(BASE).origin;
  state.origins = [{ origin, localStorage: [{ name: "token", value: token }] }];
  mkdirSync(path.dirname(STORAGE_STATE), { recursive: true });
  writeFileSync(STORAGE_STATE, JSON.stringify(state));
}
