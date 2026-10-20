import { Page, expect } from "@playwright/test";
import { authHeaders } from "./helpers";

/**
 * The newest link starting with `pathPrefix` (e.g. "/invite/") in the emails
 * the dev outbox recorded for `to`, as a path. The API never returns invite
 * tokens, so this is how a test follows an emailed link. The outbox exists
 * only with DEV_AUTO_LOGIN, like dev-login. Emails go out before the API call
 * that triggers them returns, so there is nothing to poll for.
 */
export async function emailedLink(page: Page, to: string, pathPrefix: string, token?: string): Promise<string> {
  const headers = token ? { Authorization: `Bearer ${token}` } : await authHeaders(page);
  const res = await page.request.get(`/api/dev/outbox?to=${encodeURIComponent(to)}`, { headers });
  expect(res.ok(), `dev outbox: ${res.status()} ${await res.text()}`).toBe(true);
  const emails: { body: string }[] = (await res.json()).data;
  const link = new RegExp(`${pathPrefix.replace(/[.*+?^${}()|[\]\\/]/g, "\\$&")}[A-Za-z0-9_-]+`);
  for (const email of emails) {
    const match = email.body.match(link);
    if (match) return match[0];
  }
  throw new Error(`no ${pathPrefix} link emailed to ${to}`);
}
