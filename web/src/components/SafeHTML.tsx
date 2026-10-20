import DOMPurify from 'dompurify'

/**
 * Allowlist of tags used in TMS translation content.
 * Only formatting tags from TipTap editor + XLIFF inline elements.
 * Everything else (script, img, iframe, etc.) is stripped.
 */
const ALLOWED_TAGS = ['strong', 'em', 'sup', 'sub', 'span', 'p', 'br']

const purifyConfig = {
  ALLOWED_TAGS,
  ALLOWED_ATTR: ['class', 'data-type', 'data-id'], // XLIFF tag chip attributes
}

/**
 * Renders sanitized HTML. Use this instead of `dangerouslySetInnerHTML`.
 * Strips all tags except TMS formatting (strong, em, sup, sub, span).
 * Strips all attributes except class and data-* for XLIFF tags.
 */
export function SafeHTML({ html, className }: { html: string; className?: string }) {
  const clean = DOMPurify.sanitize(html, purifyConfig)
  return <span className={className} dangerouslySetInnerHTML={{ __html: clean }} />
}
