/**
 * J25 — TM Entry Editing Gauntlet (Adversarial)
 *
 * Validates TM entry inline editing (PATCH + history table). Designed to BE
 * AS ADVERSARIAL AS POSSIBLE — each phase tries to break a separate
 * invariant:
 *
 *   - B: payload validation (empty/garbage)
 *   - C: cross-tenant + cross-TM URL-tampering attacks
 *   - D: optimistic locking + parallel-PATCH race (mirrors j22)
 *   - E: history audit trail (BEFORE-snapshot, ordering, atomicity)
 *   - F: tag-bearing rich text round-trip
 *   - G: permission gate (member 403 on PATCH/DELETE, 200 on history GET)
 *
 * Locale: cs-CZ (Czech, Czech Republic). Mock supports `cs` natively, so
 * cs-CZ exercises the base-code fallback the same way j17's sv-SE
 * does. Diacritics give a visual sanity check on round-trip.
 *
 * Honest scope notes:
 *   - This is API-level. The frontend ConfirmDialog UX is exercised in
 *     `web/src/pages/TMBrowser.test.tsx`.
 *   - dev-login provisions a SECOND tenant so the cross-tenant tests can
 *     prove tenant-A's owner can't reach into tenant-B's TM.
 *   - The 5-parallel-PATCH race uses `Promise.all` — server-side ordering
 *     is up to Postgres. We assert outcomes that are deterministic
 *     regardless of order (exactly-1-winner, history-grows-by-1).
 */
import { test, expect, type APIRequestContext } from "@playwright/test";
import {
  inviteMember,
  login,
  createTMViaAPI,
  importTMXViaAPI,
  deleteTMViaAPI,
} from "./helpers";

const API = "";
const RUN_ID = Date.now();

const MEMBER_EMAIL = `j25-member-${RUN_ID}@test.com`;
const MEMBER_PASSWORD = "MemberPass!23j25";
const TENANT2_EMAIL = `j25-tenant2-${RUN_ID}@test.com`;
const TENANT2_PASSWORD = "Tenant2Pass!23j25";

let ownerToken: string;
let memberToken: string;
let tenant2Token: string;

let tmId: number; // Owner-tenant TM A (the one we edit)
let tmIdSibling: number; // Owner-tenant TM B (sibling — for cross-TM URL-tampering tests)
let tenant2TmId: number; // Tenant-2 TM (for cross-tenant tests)
let tenant2EntryId: number;

const entryBySource: Record<string, number> = {};

// ─── Inline TMX fixtures (kept tiny so we don't share with other journeys) ──

const baseTMX = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en"
          creationtool="J25" creationtoolversion="1"/>
  <body>
    <tu><tuv xml:lang="en"><seg>Welcome to the application</seg></tuv><tuv xml:lang="cs"><seg>Vítejte v aplikaci</seg></tuv></tu>
    <tu><tuv xml:lang="en"><seg>Your session has expired</seg></tuv><tuv xml:lang="cs"><seg>Vaše relace vypršela</seg></tuv></tu>
    <tu><tuv xml:lang="en"><seg>Click here to continue</seg></tuv><tuv xml:lang="cs"><seg>Pokračujte kliknutím sem</seg></tuv></tu>
    <tu><tuv xml:lang="en"><seg>Reset password</seg></tuv><tuv xml:lang="cs"><seg>Obnovit heslo</seg></tuv></tu>
    <tu><tuv xml:lang="en"><seg>Press <ph id="1" x="ENTER"/> to submit</seg></tuv><tuv xml:lang="cs"><seg>Stiskněte <ph id="1" x="ENTER"/> pro odeslání</seg></tuv></tu>
    <tu><tuv xml:lang="en"><seg>Open file</seg></tuv><tuv xml:lang="cs"><seg>Otevřít soubor</seg></tuv></tu>
  </body>
</tmx>`;

const siblingTMX = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en"
          creationtool="J25" creationtoolversion="1"/>
  <body>
    <tu><tuv xml:lang="en"><seg>Sibling marker entry</seg></tuv><tuv xml:lang="cs"><seg>Zarážkový záznam</seg></tuv></tu>
  </body>
</tmx>`;

const tenant2TMX = `<?xml version="1.0" encoding="UTF-8"?>
<tmx version="1.4">
  <header srclang="en" datatype="plaintext" segtype="sentence" adminlang="en"
          creationtool="J25" creationtoolversion="1"/>
  <body>
    <tu><tuv xml:lang="en"><seg>Tenant 2 secret entry</seg></tuv><tuv xml:lang="cs"><seg>Tajný záznam</seg></tuv></tu>
  </body>
</tmx>`;

// ─── Helpers ────────────────────────────────────────────────────────────────

async function bearerHeader(token: string): Promise<Record<string, string>> {
  return { Authorization: `Bearer ${token}` };
}

/** Deactivates the invited member: the member looks up their own id, the owner deletes it. */
async function deactivateMember(req: APIRequestContext) {
  const meRes = await req.get(`${API}/api/auth/me`, { headers: await bearerHeader(memberToken) });
  if (!meRes.ok()) return;
  const body = await meRes.json();
  const memberID = body.data?.id ?? body.id;
  if (memberID) {
    await req.delete(`${API}/api/members/${memberID}`, { headers: await bearerHeader(ownerToken) });
  }
}

async function waitForLatestImport(req: APIRequestContext, headers: Record<string, string>, tm: number, atLeastN: number) {
  for (let i = 0; i < 20; i++) {
    const r = await req.get(`${API}/api/tms/${tm}/entries?limit=200`, { headers });
    if (r.ok()) {
      const body = await r.json();
      if ((body.data?.length ?? 0) >= atLeastN) return body.data;
    }
    await new Promise(res => setTimeout(res, 250));
  }
  throw new Error(`TMX import did not load >=${atLeastN} entries within 5s for TM ${tm}`);
}

interface TMEntry {
  id: number;
  tm_id: number;
  source_text: string;
  target_text: string;
  source_rich_text?: string | null;
  target_rich_text?: string | null;
  updated_at: string;
}

async function getEntry(req: APIRequestContext, headers: Record<string, string>, tm: number, entryId: number): Promise<TMEntry | null> {
  // No GET-by-ID endpoint; pull the list and filter. List is small (~6 rows).
  const r = await req.get(`${API}/api/tms/${tm}/entries?limit=200`, { headers });
  if (!r.ok()) return null;
  const body = await r.json();
  return (body.data as TMEntry[]).find(e => e.id === entryId) ?? null;
}

async function patchEntry(
  req: APIRequestContext,
  headers: Record<string, string>,
  tm: number,
  entryId: number,
  payload: Partial<{
    source_text: string;
    target_text: string;
    source_rich_text: string;
    target_rich_text: string;
    expected_updated_at: string;
    change_reason: string;
  }>,
) {
  // Default timeout on purpose: D4's 5-PATCH race must finish fast. A slow
  // storm means a pool hold-and-wait deadlock in HandleUpdateEntry's conflict
  // path (shows on CI where pgxpool defaults to 4 conns).
  return req.patch(`${API}/api/tms/${tm}/entries/${entryId}`, { headers, data: payload });
}

async function listHistory(req: APIRequestContext, headers: Record<string, string>, tm: number, entryId: number) {
  return req.get(`${API}/api/tms/${tm}/entries/${entryId}/history`, { headers });
}

test.describe.serial("J25 — TM Entry Editing Gauntlet", () => {
  // ════════════════════════════════════════════════════════════════════════
  // Phase A — Setup
  // ════════════════════════════════════════════════════════════════════════

  test("A1: owner logs in, creates TM, imports 6-entry TMX (en→cs)", async ({ page }) => {
    await login(page);
    ownerToken = (await page.evaluate(() => localStorage.getItem("token"))) as string;
    expect(ownerToken, "owner login should provide a JWT").toBeTruthy();

    tmId = await createTMViaAPI(page, `J25 TM ${RUN_ID}`);
    await importTMXViaAPI(page, tmId, baseTMX, "j25-base.tmx");

    const headers = await bearerHeader(ownerToken);
    const entries = await waitForLatestImport(page.request, headers, tmId, 6);
    for (const e of entries as TMEntry[]) {
      entryBySource[e.source_text] = e.id;
    }
    expect(Object.keys(entryBySource).length,
      "A1: must have 6 distinct source texts after import (the 5th has tag markup, but each is unique)",
    ).toBe(6);
  });

  test("A2: owner creates a SECOND TM in same tenant (sibling) for cross-TM tests", async ({ page }) => {
    await login(page);
    tmIdSibling = await createTMViaAPI(page, `J25 Sibling TM ${RUN_ID}`);
    await importTMXViaAPI(page, tmIdSibling, siblingTMX, "j25-sibling.tmx");
    await waitForLatestImport(page.request, await bearerHeader(ownerToken), tmIdSibling, 1);
  });

  test("A3: owner invites a member (member role)", async ({ page }) => {
    await login(page);
    memberToken = (await inviteMember(page, MEMBER_EMAIL, "linguist", await bearerHeader(ownerToken))).token;
  });

  test("A4: register a SECOND tenant + create their own TM with one entry", async ({ page }) => {
    // Register API creates a fresh tenant + owner. We use it to prove the
    // tenant boundary holds for TM entry mutations.
    const regRes = await page.request.post(`${API}/api/auth/dev-login`, {
      data: { email: TENANT2_EMAIL, tenant_name: `J25 Tenant2 ${RUN_ID}` },
    });
    expect(regRes.ok(), `A4: provision tenant 2: ${regRes.status()}`).toBe(true);
    tenant2Token = (await regRes.json()).token;
    expect(tenant2Token).toBeTruthy();

    const headers = await bearerHeader(tenant2Token);
    const tmRes = await page.request.post(`${API}/api/tms`, {
      headers,
      // TM creation requires declared languages. The tenant-2 TMX is en→cs.
      data: { name: `J25 Tenant2 TM ${RUN_ID}`, source_language: "en", target_languages: ["cs"] },
    });
    expect(tmRes.ok(), `A4: create tenant-2 TM: ${tmRes.status()}`).toBe(true);
    tenant2TmId = (await tmRes.json()).data.id;

    const importRes = await page.request.post(`${API}/api/tms/${tenant2TmId}/import`, {
      headers,
      multipart: {
        file: { name: "tenant2.tmx", mimeType: "application/xml", buffer: Buffer.from(tenant2TMX) },
      },
    });
    expect(importRes.ok(), `A4: tenant-2 import: ${importRes.status()}`).toBe(true);

    const entries = await waitForLatestImport(page.request, headers, tenant2TmId, 1);
    tenant2EntryId = (entries as TMEntry[])[0].id;
    expect(tenant2EntryId).toBeGreaterThan(0);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase B — Payload validation gauntlet
  // ════════════════════════════════════════════════════════════════════════

  test("B1: PATCH valid payload → 200 + body returns new updated_at", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const entryId = entryBySource["Welcome to the application"];

    const before = await getEntry(page.request, headers, tmId, entryId);
    expect(before, "B1 setup: entry must exist").not.toBeNull();
    const baselineStamp = before!.updated_at;

    const res = await patchEntry(page.request, headers, tmId, entryId, {
      source_text: "Welcome to the application",
      target_text: "Vítejte v naší aplikaci", // changed
      expected_updated_at: baselineStamp,
    });
    expect(res.status(), "B1: valid PATCH must 200").toBe(200);

    const body = await res.json();
    expect(body.data.target_text, "B1: response body must reflect the new target").toBe("Vítejte v naší aplikaci");
    expect(body.data.updated_at,
      "B1: updated_at must advance — proves the row was actually written, not just that the handler returned 200 with no DB effect.",
    ).not.toBe(baselineStamp);
  });

  test("B2: PATCH with empty target_text → 400", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const entryId = entryBySource["Open file"];
    const res = await patchEntry(page.request, headers, tmId, entryId, {
      source_text: "Open file",
      target_text: "",
    });
    expect(res.status(),
      "B2: empty target_text must 400. If 200, the empty entry is now in the DB — silent corruption.",
    ).toBe(400);
  });

  test("B3: PATCH with empty source_text → 400", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const entryId = entryBySource["Open file"];
    const res = await patchEntry(page.request, headers, tmId, entryId, {
      source_text: "",
      target_text: "Otevřít soubor",
    });
    expect(res.status(), "B3: empty source_text must 400").toBe(400);
  });

  test("B4: PATCH with non-numeric entry ID → 400", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const res = await page.request.patch(`${API}/api/tms/${tmId}/entries/notanumber`, {
      headers,
      data: { source_text: "x", target_text: "y" },
    });
    expect(res.status(),
      "B4: non-numeric entry ID must 400. If 404, error code mixed up — bad ID looks like missing row, masks bugs.",
    ).toBe(400);
  });

  test("B5: PATCH non-existent entry ID → 404", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const res = await patchEntry(page.request, headers, tmId, 99_999_999, {
      source_text: "ghost", target_text: "duch",
    });
    expect(res.status(), "B5: missing row must 404").toBe(404);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase C — Cross-tenant + cross-TM URL tampering
  //
  // Two distinct attack surfaces:
  //   (1) Tenant A's owner targets Tenant B's TM/entry — security boundary.
  //   (2) Tenant A's owner targets their OWN tenant's entry but via a
  //       different TM's ID in the URL — hygiene boundary.
  // ════════════════════════════════════════════════════════════════════════

  test("C1: tenant-2 owner CANNOT PATCH tenant-1's entry (tenant-2's URL with tenant-1's IDs → 404)", async ({ page }) => {
    // Use tenant-2's token, target tenant-1's tmId + entryId. RequireTMOwnership
    // should 404 before we ever reach the handler — TM 1 is not theirs.
    const headers = await bearerHeader(tenant2Token);
    const entryId = entryBySource["Reset password"];
    const res = await patchEntry(page.request, headers, tmId, entryId, {
      source_text: "Reset password", target_text: "ATTACK",
    });
    expect(res.status(),
      "C1 (cross-tenant PATCH must 404 via RequireTMOwnership): if 200/204, " +
      "tenant boundary breached on PATCH path.",
    ).toBe(404);
  });

  test("C2: tenant-2 owner CANNOT DELETE tenant-1's entry — SECURITY (cross-tenant DELETE attack)", async ({ page }) => {
    // Target via own TM URL (tenant-2's TM) but a tenant-1 entry ID.
    // RequireTMOwnership sees tenant-2's TM (passes) and RequireRole sees
    // tenant-2 owner (passes), so the delete itself must be scoped to the
    // tenant and the URL's TM. EXPECTED: 404 (entry not in this TM).
    const headers = await bearerHeader(tenant2Token);
    const targetEntryId = entryBySource["Open file"];

    const beforeRes = await page.request.get(`${API}/api/tms/${tmId}/entries?limit=200`, {
      headers: await bearerHeader(ownerToken),
    });
    const beforeIds = new Set(((await beforeRes.json()).data as TMEntry[]).map(e => e.id));
    expect(beforeIds.has(targetEntryId),
      "C2 setup: target entry must exist in tenant-1's TM before the attack.",
    ).toBe(true);

    // The attack: tenant-2 hits their own TM URL with tenant-1's entry ID.
    const attackRes = await page.request.delete(
      `${API}/api/tms/${tenant2TmId}/entries/${targetEntryId}`,
      { headers },
    );
    expect(attackRes.status(),
      "C2 (CORE SECURITY INVARIANT — cross-tenant DELETE must 404): " +
      "the entry delete must be tenant-scoped, or any admin/owner can wipe " +
      "entries from any other tenant by guessing IDs.",
    ).toBe(404);

    // Defensive: verify tenant-1's entry was NOT actually deleted.
    const afterRes = await page.request.get(`${API}/api/tms/${tmId}/entries?limit=200`, {
      headers: await bearerHeader(ownerToken),
    });
    const afterIds = new Set(((await afterRes.json()).data as TMEntry[]).map(e => e.id));
    expect(afterIds.has(targetEntryId),
      "C2 (defensive): tenant-1's entry must still exist after the rejected DELETE. " +
      "If gone, the 404 was returned AFTER the destructive write — worst-case bug.",
    ).toBe(true);
  });

  test("C3: tenant-2 owner CANNOT GET history of tenant-1's entry (404 via RequireTMOwnership)", async ({ page }) => {
    const headers = await bearerHeader(tenant2Token);
    const entryId = entryBySource["Reset password"];
    const res = await listHistory(page.request, headers, tmId, entryId);
    expect(res.status(),
      "C3: tenant-2's token + tenant-1's tmID → RequireTMOwnership must 404.",
    ).toBe(404);
  });

  test("C4: SAME-tenant cross-TM PATCH — `/tms/{TM-A}/entries/{entryFromTM-B}` must 404 (hygiene)", async ({ page }) => {
    // Owner has TM A (tmId) and TM B (tmIdSibling) in same tenant. We
    // fetch an entry from TM B, then PATCH it via TM A's URL. The handler
    // must reject it: entry.tm_id must equal the URL's tmID.
    const headers = await bearerHeader(ownerToken);

    // Find an entry in the sibling TM
    const siblingRes = await page.request.get(`${API}/api/tms/${tmIdSibling}/entries?limit=10`, { headers });
    const siblingEntries = (await siblingRes.json()).data as TMEntry[];
    expect(siblingEntries.length, "C4 setup: sibling TM must have an entry").toBeGreaterThan(0);
    const siblingEntryId = siblingEntries[0].id;

    // Try to PATCH TM B's entry via TM A's URL.
    const res = await patchEntry(page.request, headers, tmId, siblingEntryId, {
      source_text: "Sibling marker entry",
      target_text: "TAMPERED via wrong TM URL",
    });
    expect(res.status(),
      "C4: cross-TM PATCH must 404 — the entry does not live in URL's TM.",
    ).toBe(404);
  });

  test("C5: SAME-tenant cross-TM DELETE — `/tms/{TM-A}/entries/{entryFromTM-B}` must 404 (hygiene + integrity)", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);

    // C4's PATCH was rejected, so the sibling TM's entries are untouched.
    const siblingRes = await page.request.get(`${API}/api/tms/${tmIdSibling}/entries?limit=10`, { headers });
    const siblingEntries = (await siblingRes.json()).data as TMEntry[];
    expect(siblingEntries.length, "C5 setup: sibling TM must still have an entry").toBeGreaterThan(0);
    const siblingEntryId = siblingEntries[0].id;

    const res = await page.request.delete(
      `${API}/api/tms/${tmId}/entries/${siblingEntryId}`,
      { headers },
    );
    expect(res.status(),
      "C5: cross-TM DELETE must 404 — same gap as C4 plus the data-loss surface. " +
      "Without TM scoping, a user who knows entry IDs can delete entries from " +
      "any of their other TMs by addressing them via a different TM's URL — " +
      "confusing UX and hostile to audit logs.",
    ).toBe(404);

    // Defensive: sibling entry still there.
    const afterRes = await page.request.get(`${API}/api/tms/${tmIdSibling}/entries?limit=10`, { headers });
    const afterEntries = (await afterRes.json()).data as TMEntry[];
    expect(afterEntries.find(e => e.id === siblingEntryId),
      "C5 defensive: sibling TM's entry must still be present after the rejected DELETE.",
    ).toBeTruthy();
  });

  test("C6: SAME-tenant cross-TM history GET — `/tms/{TM-A}/entries/{entryFromTM-B}/history` must 404 (hygiene)", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const siblingRes = await page.request.get(`${API}/api/tms/${tmIdSibling}/entries?limit=10`, { headers });
    const siblingEntries = (await siblingRes.json()).data as TMEntry[];
    const siblingEntryId = siblingEntries[0].id;

    const res = await listHistory(page.request, headers, tmId, siblingEntryId);
    expect(res.status(),
      "C6: GET history with wrong-TM-in-URL must 404 — same root cause as C4/C5.",
    ).toBe(404);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase D — Optimistic locking + parallel-PATCH race
  //
  // The translation optimistic-lock invariant, applied to TM editing. The 5-parallel test
  // mirrors j22's C1 but on the PATCH endpoint, AND adds the history-grows-
  // by-EXACTLY-1 check (D5) — proves the tx wrapping the InsertTMHistory +
  // UpdateTMEntry survives the optimistic-lock rollback path.
  // ════════════════════════════════════════════════════════════════════════

  test("D1: PATCH with matching expected_updated_at → 200 + advances updated_at", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const entryId = entryBySource["Click here to continue"];
    const before = await getEntry(page.request, headers, tmId, entryId);
    const baseline = before!.updated_at;

    const res = await patchEntry(page.request, headers, tmId, entryId, {
      source_text: "Click here to continue",
      target_text: "Klikněte pro pokračování",
      expected_updated_at: baseline,
    });
    expect(res.status(), "D1: matching baseline must 200").toBe(200);
    const body = await res.json();
    expect(body.data.updated_at).not.toBe(baseline);
  });

  test("D2: PATCH with stale expected_updated_at → 409 + entry unchanged", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const entryId = entryBySource["Click here to continue"];
    const before = await getEntry(page.request, headers, tmId, entryId);
    const stale = "2020-01-01T00:00:00Z"; // never matches

    const res = await patchEntry(page.request, headers, tmId, entryId, {
      source_text: "Click here to continue",
      target_text: "REJECTED",
      expected_updated_at: stale,
    });
    expect(res.status(),
      "D2 (optimistic-lock invariant for TM): stale baseline must 409. If 200, optimistic " +
      "lock skipped — silent overwrite.",
    ).toBe(409);

    const after = await getEntry(page.request, headers, tmId, entryId);
    expect(after!.target_text,
      "D2 defensive: rejected PATCH must NOT have written. If target == 'REJECTED', " +
      "the 409 was returned AFTER the write.",
    ).toBe(before!.target_text);
  });

  test("D3: PATCH WITHOUT expected_updated_at (legacy mode) → 200 even on stale row", async ({ page }) => {
    // Mirrors j22 B3: clients that don't track updated_at fall back to last-writer-wins.
    const headers = await bearerHeader(ownerToken);
    const entryId = entryBySource["Click here to continue"];
    const res = await patchEntry(page.request, headers, tmId, entryId, {
      source_text: "Click here to continue",
      target_text: "Klikněte (bez kontroly verze)",
    });
    expect(res.status(),
      "D3: omitting expected_updated_at preserves legacy last-writer-wins.",
    ).toBe(200);
  });

  test("D4: 5 parallel PATCHes sharing one stale baseline → EXACTLY 1 wins, 4 conflict", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const entryId = entryBySource["Your session has expired"];
    const before = await getEntry(page.request, headers, tmId, entryId);
    const sharedBaseline = before!.updated_at;

    const payloads = ["varianta_1", "varianta_2", "varianta_3", "varianta_4", "varianta_5"];
    const responses = await Promise.all(
      payloads.map(p => patchEntry(page.request, headers, tmId, entryId, {
        source_text: "Your session has expired",
        target_text: p,
        expected_updated_at: sharedBaseline,
      })),
    );
    const statuses = responses.map(r => r.status());
    const wins = statuses.filter(s => s === 200).length;
    const conflicts = statuses.filter(s => s === 409).length;

    expect(wins,
      `D4 (CORE INVARIANT): EXACTLY 1 of 5 parallel PATCHes with the same stale baseline must win. ` +
      `Got ${wins}. >1 means optimistic locking is broken on the PATCH path.`,
    ).toBe(1);
    expect(conflicts,
      `D4: the other 4 must 409. Got ${conflicts}. Statuses: ${statuses.join(", ")}.`,
    ).toBe(4);

    const after = await getEntry(page.request, headers, tmId, entryId);
    const winnerIdx = statuses.indexOf(200);
    expect(after!.target_text, "D4: row's final target_text must match the winner's payload.")
      .toBe(payloads[winnerIdx]);
  });

  test("D5: after D4's race, history has EXACTLY 1 NEW row (proves tx atomicity)", async ({ page }) => {
    // The handler does: BEGIN tx → InsertTMHistory → UpdateTMEntry. If the
    // UpdateTMEntry returns ErrNoRows (optimistic-lock miss), we emit 409
    // and the deferred Rollback fires — which MUST roll back the history
    // insert too. If history isn't rolled back, the 4 losers leave behind
    // 4 ghost history rows for changes that never happened.
    //
    // `Your session has expired` is changed only in D4, so its history must
    // be exactly 1 row.
    const headers = await bearerHeader(ownerToken);
    const entryId = entryBySource["Your session has expired"];
    const res = await listHistory(page.request, headers, tmId, entryId);
    expect(res.ok(), `D5: history endpoint must work: ${res.status()}`).toBe(true);
    const history = (await res.json()).data as Array<{ id: number; entry_id: number }>;
    expect(history.length,
      `D5 (tx atomicity): history must have EXACTLY 1 row for the D4 entry — ` +
      `1 winner among the 5 parallel PATCHes. Got ${history.length}. ` +
      `>1 means losers' history snapshots were committed even though the ` +
      `entry update was rolled back: ghost audit trail entries.`,
    ).toBe(1);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase E — History audit trail
  // ════════════════════════════════════════════════════════════════════════

  test("E1: after a successful PATCH, history row contains the BEFORE values", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const entryId = entryBySource["Reset password"];

    const before = await getEntry(page.request, headers, tmId, entryId);
    const oldSource = before!.source_text; // "Reset password"
    const oldTarget = before!.target_text; // "Obnovit heslo"
    const oldStamp = before!.updated_at;

    const r = await patchEntry(page.request, headers, tmId, entryId, {
      source_text: oldSource,
      target_text: "Resetovat heslo",
      expected_updated_at: oldStamp,
    });
    expect(r.status()).toBe(200);

    const histRes = await listHistory(page.request, headers, tmId, entryId);
    const history = (await histRes.json()).data as Array<{ source_text: string; target_text: string }>;
    expect(history.length, "E1: must have at least 1 history row").toBeGreaterThanOrEqual(1);
    const latest = history[0];
    expect(latest.source_text,
      "E1 (CORE invariant): history row must snapshot the BEFORE source_text. " +
      "If it has the AFTER value, the snapshot is taken from the wrong side of the update.",
    ).toBe(oldSource);
    expect(latest.target_text,
      "E1: history row must snapshot the BEFORE target_text.",
    ).toBe(oldTarget);
  });

  test("E2: 3 sequential PATCHes → history has 3+ rows newest-first", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const entryId = entryBySource["Reset password"];

    // Already 1 history row from E1. Add 2 more by editing twice in sequence.
    const stages = ["Resetovat heslo (E2.1)", "Resetovat heslo (E2.2)"];
    for (const targetText of stages) {
      const cur = await getEntry(page.request, headers, tmId, entryId);
      const r = await patchEntry(page.request, headers, tmId, entryId, {
        source_text: cur!.source_text,
        target_text: targetText,
        expected_updated_at: cur!.updated_at,
      });
      expect(r.status(), `E2 setup: PATCH '${targetText}' must succeed`).toBe(200);
    }

    const histRes = await listHistory(page.request, headers, tmId, entryId);
    const history = (await histRes.json()).data as Array<{ id: number; target_text: string; created_at: string }>;
    expect(history.length, "E2: must have ≥3 history rows now").toBeGreaterThanOrEqual(3);

    // Verify newest-first ordering by created_at desc (history table uses
    // `created_at` for the timestamp the snapshot was inserted; see migration 022).
    for (let i = 1; i < history.length; i++) {
      expect(history[i - 1].created_at >= history[i].created_at,
        `E2: history must be newest-first (sorted by created_at desc). ` +
        `Out of order at index ${i}: ${history[i - 1].created_at} < ${history[i].created_at}`,
      ).toBe(true);
    }
  });

  test("E3: a 400-validation PATCH does NOT add a history row", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const entryId = entryBySource["Open file"];
    const histBefore = (await (await listHistory(page.request, headers, tmId, entryId)).json()).data.length;

    // Invalid: empty target_text → 400
    const r = await patchEntry(page.request, headers, tmId, entryId, {
      source_text: "Open file", target_text: "",
    });
    expect(r.status()).toBe(400);

    const histAfter = (await (await listHistory(page.request, headers, tmId, entryId)).json()).data.length;
    expect(histAfter,
      "E3: 400-validation rejection must NOT add a history row — validation runs " +
      "before the history insert. If history grew, validation is in the wrong place.",
    ).toBe(histBefore);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase F — Tag-bearing rich text round-trip
  // ════════════════════════════════════════════════════════════════════════

  // The TMX-import path strips paired/standalone XLIFF tags from source_text:
  // `<ph id="1" x="ENTER"/>` becomes empty in the plain text, leaving a
  // double space where the placeholder was. Source text after import is
  // therefore "Press  to submit" (note double space). source_rich_text
  // preserves the original inline tag for chip rendering. The constant
  // matches what the API will return.
  const TAG_SOURCE_PLAIN = "Press  to submit";

  test("F1: imported tag-bearing entry has source_rich_text + target_rich_text populated", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const entryId = entryBySource[TAG_SOURCE_PLAIN];
    expect(entryId, "F1: tag-bearing entry must be findable by stripped source text").toBeTruthy();

    const entry = await getEntry(page.request, headers, tmId, entryId);
    expect(entry!.source_rich_text,
      "F1: tag-bearing entry must round-trip source_rich_text through TMX import. " +
      "If null, the importer dropped the inline-tag form — chips won't render in TM browser/concordance.",
    ).toContain("<ph");
    expect(entry!.target_rich_text).toContain("<ph");
  });

  test("F2: PATCH preserving rich_text → tags round-trip in subsequent GET", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const entryId = entryBySource[TAG_SOURCE_PLAIN];
    const before = await getEntry(page.request, headers, tmId, entryId);

    const newTargetRich = 'Stiskněte <ph id="1" x="ENTER"/> pro odeslání ihned';
    const newTargetPlain = "Stiskněte  pro odeslání ihned";

    const r = await patchEntry(page.request, headers, tmId, entryId, {
      source_text: before!.source_text,
      source_rich_text: before!.source_rich_text!,
      target_text: newTargetPlain,
      target_rich_text: newTargetRich,
      expected_updated_at: before!.updated_at,
    });
    expect(r.status(), `F2: tag-preserving PATCH: ${r.status()}`).toBe(200);

    const after = await getEntry(page.request, headers, tmId, entryId);
    expect(after!.target_rich_text,
      "F2: target_rich_text must reflect the new value verbatim — tags survive PATCH.",
    ).toBe(newTargetRich);
    expect(after!.target_text,
      "F2: target_text must reflect the plain-text companion.",
    ).toBe(newTargetPlain);
  });

  test("F3: PATCH with empty target_rich_text strips the tag form (NULL in DB)", async ({ page }) => {
    const headers = await bearerHeader(ownerToken);
    const entryId = entryBySource[TAG_SOURCE_PLAIN];
    const before = await getEntry(page.request, headers, tmId, entryId);

    // User removed all tags via the editor; rich_text now empty, plain text remains.
    const r = await patchEntry(page.request, headers, tmId, entryId, {
      source_text: before!.source_text,
      source_rich_text: before!.source_rich_text!,
      target_text: "Stiskněte ENTER pro odeslání",
      target_rich_text: "", // strip
      expected_updated_at: before!.updated_at,
    });
    expect(r.status()).toBe(200);

    const after = await getEntry(page.request, headers, tmId, entryId);
    expect(after!.target_rich_text === null || after!.target_rich_text === "",
      "F3: empty target_rich_text must persist as NULL/empty — not as the literal " +
      "string '' which would round-trip but break the 'has tags?' check downstream.",
    ).toBe(true);
    expect(after!.target_text).toBe("Stiskněte ENTER pro odeslání");
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase G — Permission gate
  // ════════════════════════════════════════════════════════════════════════

  test("G1: member CANNOT PATCH (403)", async ({ page }) => {
    const headers = await bearerHeader(memberToken);
    const entryId = entryBySource["Welcome to the application"];
    const r = await patchEntry(page.request, headers, tmId, entryId, {
      source_text: "Welcome to the application",
      target_text: "Member tampering attempt",
    });
    expect(r.status(),
      "G1: member must 403 on PATCH (admin/owner only). If 200, RequireRole was " +
      "skipped — every member can rewrite TM history.",
    ).toBe(403);
  });

  test("G2: member CANNOT DELETE (403)", async ({ page }) => {
    const headers = await bearerHeader(memberToken);
    const entryId = entryBySource["Open file"];
    const r = await page.request.delete(`${API}/api/tms/${tmId}/entries/${entryId}`, { headers });
    expect(r.status(),
      "G2: member must 403 on DELETE (admin/owner only).",
    ).toBe(403);
  });

  test("G3: linguist CANNOT GET TM history (403) — TM is a manager-only surface", async ({ page }) => {
    const headers = await bearerHeader(memberToken);
    const entryId = entryBySource["Reset password"];
    const r = await listHistory(page.request, headers, tmId, entryId);
    expect(r.status(), "G3: linguists are fully blocked from the TM surface").toBe(403);
  });

  test("G4: linguist CANNOT list TM entries (403) — TM is manager-only", async ({ page }) => {
    const headers = await bearerHeader(memberToken);
    const r = await page.request.get(`${API}/api/tms/${tmId}/entries?limit=10`, { headers });
    expect(r.status()).toBe(403);
  });

  // ════════════════════════════════════════════════════════════════════════
  // Phase H — Cleanup
  // ════════════════════════════════════════════════════════════════════════

  test("H1: cleanup all resources (TMs, member, tenant-2)", async ({ page }) => {
    const cleanup = async (fn: () => Promise<void>) => { try { await fn(); } catch {} };
    if (tmId) await cleanup(() => deleteTMViaAPI(page, tmId));
    if (tmIdSibling) await cleanup(() => deleteTMViaAPI(page, tmIdSibling));

    // Deactivate the invited member (soft-delete). Each J25 run
    // creates a unique-email member; without cleanup the dev tenant accumulates
    // soft-deleted users across runs.
    if (memberToken && ownerToken) {
      await cleanup(() => deactivateMember(page.request));
    }

    // Tenant-2 cleanup: hard-delete the entire tenant via /api/tenants/current
    // (owner-only delete with FK CASCADE on TMs, users,
    // imports, etc.). Without this, every J25 run accumulates a zombie tenant
    // in the DB; over time the test environment fills with orphans.
    if (tenant2Token) {
      await cleanup(async () => {
        await page.request.delete(`${API}/api/tenants/current`, {
          headers: await bearerHeader(tenant2Token),
        });
      });
    }
  });

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    try {
      await login(page);
      if (tmId) await deleteTMViaAPI(page, tmId).catch(() => {});
      if (tmIdSibling) await deleteTMViaAPI(page, tmIdSibling).catch(() => {});
      if (memberToken && ownerToken) await deactivateMember(page.request).catch(() => {});
      // Tenant-2 cascade cleanup as a safety net if H1 didn't run
      // (e.g. earlier failure stopped the serial chain).
      if (tenant2Token) {
        await page.request.delete(`${API}/api/tenants/current`, {
          headers: await bearerHeader(tenant2Token),
        }).catch(() => {});
      }
    } catch {} finally {
      await page.close();
    }
  });
});
