// Same-origin redirect guard. A `?next=` value is
// attacker-controllable, so it may only ever drive a navigation to a path on
// OUR origin. Prefix checks alone are not enough: browsers normalize `\` to
// `/`, so `/\evil.example` becomes the protocol-relative `//evil.example` and
// a naive `!startsWith('//')` lets it through. Two layers close it: reject any
// authority-introducing prefix, then resolve against our origin and require it
// to stay same-origin.
export function isSameOriginPath(next: string): boolean {
  if (typeof next !== 'string' || !next.startsWith('/')) return false
  // `//host` and `/\host` (and `\/`) both introduce an authority in a browser.
  if (/^\/[/\\]/.test(next)) return false
  try {
    return new URL(next, window.location.origin).origin === window.location.origin
  } catch {
    return false
  }
}
