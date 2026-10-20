import { test, expect } from "@playwright/test";
import { login } from "./helpers";

/**
 * OAuth 2.1 authorization server — browser-truth for the
 * consent flow the Go integration suite can't render: the real consent page,
 * through the Vite proxy, with the real session cookie. The code→token→/mcp
 * chain then runs over the API to prove the browser-issued grant works.
 */

// A fixed PKCE pair (verifier + its S256 challenge), precomputed.
const VERIFIER = "e2e-verifier-e2e-verifier-e2e-verifier-e2e-verifier";
const CHALLENGE_B64 = async () => {
  const data = new TextEncoder().encode(VERIFIER);
  const digest = await crypto.subtle.digest("SHA-256", data);
  return Buffer.from(digest).toString("base64url");
};

test.describe("OAuth connector flow", () => {
  test("register → consent → code → token → MCP tools/list", async ({ page, request }) => {
    await login(page);

    // 1. Register a client (DCR) with a redirect we can intercept.
    const redirectURI = "https://client.example/callback";
    const reg = await request.post("/oauth/register", {
      data: { client_name: "E2E Client", redirect_uris: [redirectURI] },
    });
    expect(reg.status()).toBe(201);
    const { client_id } = await reg.json();

    // 2. Consent page renders server-side with the client name + org select.
    const q = new URLSearchParams({
      client_id,
      redirect_uri: redirectURI,
      response_type: "code",
      scope: "read",
      state: "e2e-state",
      code_challenge: await CHALLENGE_B64(),
      code_challenge_method: "S256",
    });
    await page.goto(`/oauth/authorize?${q}`);
    await expect(page.getByText("E2E Client wants access to OpenLoc")).toBeVisible();
    await expect(page.getByText("Read-only access")).toBeVisible();

    // 3. Approve → 302 to the client carrying code + echoed state. Playwright
    // can't intercept a redirect HOP (only the first request of a chain), so
    // the callback URL is read from the outgoing request; the navigation
    // itself dead-ends at a fake host, which is fine — the code is what matters.
    const cbRequest = page.waitForRequest(r => r.url().startsWith(redirectURI));
    await page.getByRole("button", { name: "Approve" }).click();
    const cb = new URL((await cbRequest).url());
    const code = cb.searchParams.get("code");
    expect(code).toBeTruthy();
    expect(cb.searchParams.get("state")).toBe("e2e-state");

    // 4. Exchange the code (PKCE) and call MCP tools/list with the token.
    const tok = await request.post("/oauth/token", {
      form: {
        grant_type: "authorization_code",
        code: code!,
        code_verifier: VERIFIER,
        client_id,
        redirect_uri: redirectURI,
      },
    });
    expect(tok.status()).toBe(200);
    const { access_token } = await tok.json();
    expect(access_token).toMatch(/^eltm_oat_/);

    const mcp = await request.post("/mcp", {
      headers: {
        Authorization: `Bearer ${access_token}`,
        "Content-Type": "application/json",
        Accept: "application/json, text/event-stream",
      },
      data: { jsonrpc: "2.0", id: 1, method: "tools/list", params: {} },
    });
    expect(mcp.status()).toBe(200);
    // The token is accepted and the server answers as that user: the result carries a tool list.
    expect(await mcp.text()).toContain('"tools"');
  });

  test("login ?next open-redirect guard: the /\\ authority bypass is refused", async ({ page }) => {
    // `/\evil.example` is the real bypass — a browser normalizes `/\` to an
    // authority (`//evil.example`), which the old `!startsWith('//')` check let
    // through. If the guard regresses, dev auto-login's finishLogin would
    // window.location.assign to evil.example; this interceptor catches it.
    let hitEvil = false;
    await page.route("**/evil.example/**", (route) => {
      hitEvil = true;
      return route.abort();
    });

    await page.goto("/login?next=" + encodeURIComponent("/\\evil.example/phish"));

    // Dev auto-login settles on a localhost app route (org picker or projects,
    // depending on org count) — which only happens if the hostile next was
    // ignored. A followed redirect would leave /login for evil.example instead.
    await page.waitForURL(/localhost:\d+\/(select-org|projects|tasks)\b/);
    expect(hitEvil).toBe(false);
    expect(new URL(page.url()).hostname).toBe("localhost");
  });
});
