import { readdirSync, readFileSync } from "fs";
import path from "path";
import { test, expect, sessionAs } from "./helpers";
import { SEED_VENDOR_EMAIL } from "../seed-identity";

// A browser session of another user (`sessionAs`) costs no page load, and its
// context closes after the test, on the after-hooks clock: the runner exports
// the context's trace and screenshots its pages before a close, in calls the
// report never names, so a close inside the test body spends the test's own
// budget on them and a slow export ends in a bare "Test timeout exceeded".

test.describe.configure({ mode: "serial" });

let closed = false;
let bodyDone = false;
let exportedAfterBody = false;

test("signing in as another user loads no page, and the page calls the API as that user", async ({ browser }) => {
  const { page, context, token } = await sessionAs(browser, SEED_VENDOR_EMAIL);
  expect(page.url(), "the token is stored without opening the app").toBe("about:blank");
  expect(context.pages()).toHaveLength(1);
  const me = await context.request.get("/api/auth/me");
  expect(me.ok(), `the session's cookie signs in: ${me.status()}`).toBe(true);
  expect((await me.json()).data.email).toBe(SEED_VENDOR_EMAIL);
  // The app's own first call carries the stored token, not only the cookie.
  const loaded = page.waitForResponse(r => r.url().endsWith("/api/auth/me"));
  await page.goto("/issues");
  const res = await loaded;
  expect(await res.request().headerValue("authorization"), "the page sends the session's token").toBe(`Bearer ${token}`);
  expect((await res.json()).data.email, "the page signs in as the user").toBe(SEED_VENDOR_EMAIL);
  await expect(page.getByTestId("issues-report")).toBeVisible();
});

test("the session's context closes after the test, with its trace export there", async ({ browser }) => {
  const { context } = await sessionAs(browser, SEED_VENDOR_EMAIL);
  context.on("close", () => { closed = true; });
  // The runner stops the trace right before a close: after the body if the
  // fixture closes the context, during it if the test did.
  const stopChunk = context.tracing.stopChunk.bind(context.tracing);
  context.tracing.stopChunk = async (options) => {
    exportedAfterBody = bodyDone;
    return stopChunk(options);
  };
  bodyDone = true;
});

test("and the session is gone before the next test, its trace exported after the body", async () => {
  expect(closed, "the previous test's session context closed after it").toBe(true);
  expect(exportedAfterBody, "the trace export ran after the test body, at the fixture's close").toBe(true);
});

// A context closed inside a spec is charged to the test's budget, so every
// context a test opens is closed by the `sessions` fixture instead. The scan
// knows a context by its name (`…context`, `…ctx…`, `page.context()`); one
// under another name is not caught.
const closesAContext = (line: string) => /(?:[cC]ontext|[cC]tx\w*)(\(\))?\.close\(/.test(line);

test("no spec closes a context itself", async () => {
  for (const receiver of ["ctx", "context", "page.context()", "memberContext", "ctx2"]) {
    expect(closesAContext(`await ${receiver}.close({ reason: "done" });`), `the scan catches a close of ${receiver}`).toBe(true);
  }
  expect(closesAContext("await page.close();"), "a page close is not a context close").toBe(false);
  const dir = __dirname;
  const closes = readdirSync(dir)
    .filter((f: string) => f.endsWith(".spec.ts"))
    .flatMap((f: string) => readFileSync(path.join(dir, f), "utf8").split("\n")
      .map((line: string, i: number) => ({ at: `${f}:${i + 1}`, line }))
      .filter(({ line }: { line: string }) => closesAContext(line)));
  expect(closes, "contexts close through the sessions fixture (sessionAs, freshContext), never in a spec").toEqual([]);
});
