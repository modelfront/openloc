import type { IssueContext } from '@/api'

/** App paths whose next segment is a secret: a share link, an invite, a password reset. */
const TOKEN_ROUTES = ['/shared/charts/', '/invite/', '/reset-password/']

const ID_PATTERNS: [keyof IssueContext, RegExp][] = [
  ['project_id', /\/projects\/(\d+)/],
  ['file_id', /\/files\/(\d+)/],
  ['job_id', /\/jobs\/(\d+)/],
]

/** The path without its query, fragment or any secret segment. */
export function cleanIssuePath(pathname: string): string {
  const path = pathname.split(/[?#]/)[0]
  const tokenRoute = TOKEN_ROUTES.find(prefix => path.startsWith(prefix))
  return tokenRoute ? `${tokenRoute}:token` : path
}

export function browserName(ua: string): string | undefined {
  const rules: [RegExp, string][] = [
    [/Edg\/(\d+)/, 'Edge'],
    [/Firefox\/(\d+)/, 'Firefox'],
    [/Chrome\/(\d+)/, 'Chrome'],
    [/Version\/(\d+)[\d.]* .*Safari/, 'Safari'],
  ]
  for (const [re, name] of rules) {
    const m = ua.match(re)
    if (m) return `${name} ${m[1]}`
  }
  return undefined
}

export function osName(ua: string): string | undefined {
  if (/iPhone|iPad/.test(ua)) return 'iOS'
  if (/Android/.test(ua)) return 'Android'
  if (/Windows/.test(ua)) return 'Windows'
  if (/Mac OS X|Macintosh/.test(ua)) return 'macOS'
  if (/Linux/.test(ua)) return 'Linux'
  return undefined
}

/**
 * What a report says about where the user was: the page and the ids in it, the
 * browser, the window and the app version. Nothing from the query string goes
 * in, and no secret part of a path.
 */
export function buildIssueContext(
  pathname: string,
  env: { userAgent: string; width: number; height: number; appVersion?: string },
): IssueContext {
  const path = cleanIssuePath(pathname)
  const ctx: IssueContext = { path }
  for (const [key, re] of ID_PATTERNS) {
    const m = path.match(re)
    if (m) (ctx as Record<string, unknown>)[key] = Number(m[1])
  }
  const browser = browserName(env.userAgent)
  if (browser) ctx.browser = browser
  const os = osName(env.userAgent)
  if (os) ctx.os = os
  if (env.width > 0 && env.height > 0) ctx.viewport = `${env.width}x${env.height}`
  if (env.appVersion) ctx.app_version = env.appVersion
  return ctx
}

/** The context of the page the browser is on now. */
export function currentIssueContext(pathname: string, appVersion?: string): IssueContext {
  return buildIssueContext(pathname, {
    userAgent: navigator.userAgent,
    width: window.innerWidth,
    height: window.innerHeight,
    appVersion,
  })
}

/** The context's mark on a report the AI assistant wrote; the server keeps no other value. */
export const ISSUE_VIA_ASSISTANT = 'AI assistant'

/**
 * The support team's pages show every organization's data (the reports under
 * /platform, the LLM calls under /debug/llm), so nothing is captured there: no
 * screenshot, no trail event, no recording. The server keeps the same list.
 */
const SUPPORT_PAGES = ['/platform', '/debug/llm']

/** Routes match without regard to case or escapes, so this check does too. */
export function isSupportPage(pathname: string): boolean {
  let path = pathname
  try {
    path = decodeURIComponent(pathname)
  } catch {
    // A malformed escape is checked as typed.
  }
  path = path.toLowerCase()
  return SUPPORT_PAGES.some(page => path === page || path.startsWith(`${page}/`))
}

/** The browser is on one of the support team's pages now. */
export const onSupportPage = () => isSupportPage(window.location.pathname)
