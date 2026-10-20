// Shared Markdown renderer. react-markdown emits React elements — no raw HTML
// pass-through, so the no-dangerouslySetInnerHTML rule holds by construction.
// The repo has no typography plugin (bare `prose` classes are inert), so every
// element is styled explicitly, in one of two maps:
//
//   chat — sized for the 400px assistant panel; every heading collapses to h3;
//          in-app links through react-router; images are dropped.
//   docs — full-page article: real h2/h3 with anchor ids (for the "On this
//          page" list), in-app links through react-router, images resolved
//          against `assetBase`.
import { useMemo } from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { Link } from 'react-router-dom'
import { cn } from '@/lib/utils'
import { headingIdsByLine, slugifyHeading, textOfChildren } from '@/lib/markdown-headings'
import { textLinkClass } from '@/components/ui/text-link'

export type MarkdownVariant = 'chat' | 'docs'

type Props = {
  children: string
  variant: MarkdownVariant
  /** URL prefix for relative image sources (docs variant), e.g. `/api/docs/assets`. */
  assetBase?: string
  /** Maps a link's href before it is rendered (docs variant), e.g. /docs paths on the docs host. */
  linkHref?: (href: string) => string
  className?: string
}

export function Markdown({ children, variant, assetBase = '', linkHref, className }: Props) {
  // Heading ids come from the raw Markdown (source line → id), the same table
  // the "On this page" list is built from — a pure function of the text, so
  // ids never drift between renders and anchors always resolve.
  const components = useMemo(
    () => (variant === 'chat' ? chatComponents : docsComponents(assetBase, headingIdsByLine(children), linkHref)),
    [variant, assetBase, children, linkHref],
  )
  return (
    <div className={cn(variant === 'chat' ? 'chat-markdown min-w-0' : 'docs-markdown min-w-0', className)}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {children}
      </ReactMarkdown>
    </div>
  )
}

const chatComponents: Components = {
  p: props => <p className="my-1 leading-relaxed" {...props} />,
  ul: props => <ul className="my-1 list-disc pl-5 space-y-0.5" {...props} />,
  ol: props => <ol className="my-1 list-decimal pl-5 space-y-0.5" {...props} />,
  h1: props => <h3 className="mb-1 mt-2 text-sm font-semibold" {...props} />,
  h2: props => <h3 className="mb-1 mt-2 text-sm font-semibold" {...props} />,
  h3: props => <h3 className="mb-1 mt-2 text-sm font-semibold" {...props} />,
  a: markdownLink(textLinkClass()),
  // Model output never loads an image: a prompt-injected URL would leak data
  // with zero clicks. The alt text stays as plain text.
  img: ({ alt }) => (alt ? <span>{alt}</span> : null),
  code: props => (
    <code className="rounded bg-gray-100 px-1 py-0.5 text-[12px] dark:bg-gray-800" {...props} />
  ),
  pre: props => (
    <pre className="my-1.5 overflow-x-auto rounded-md bg-gray-100 p-2 text-[12px] dark:bg-gray-800" {...props} />
  ),
  blockquote: props => (
    <blockquote className="my-1.5 border-l-2 border-gray-300 pl-2 text-gray-600 dark:border-gray-600 dark:text-gray-400" {...props} />
  ),
  // Tables: horizontal scroll instead of squeezing — model tables routinely
  // have 4-5 columns and the panel is 400px wide.
  table: props => (
    <div className="my-1.5 overflow-x-auto">
      <table className="w-full border-collapse text-xs" {...props} />
    </div>
  ),
  thead: props => <thead className="border-b border-gray-300 dark:border-gray-600" {...props} />,
  th: props => <th className="px-2 py-1 text-left font-semibold text-gray-700 dark:text-gray-300" {...props} />,
  tr: props => <tr className="border-b border-gray-100 last:border-0 dark:border-gray-800" {...props} />,
  td: props => <td className="px-2 py-1 align-top" {...props} />,
}

const isExternal = (href: string) => /^[a-z]+:/i.test(href) || href.startsWith('//')
const isAbsolutePath = (href: string) => href.startsWith('/') && !href.startsWith('//')

// Links for both variants: external ones open a new tab, API paths (export
// links from the assistant) download with the user's session, in-app paths go
// through react-router, in-page anchors stay plain.
function markdownLink(className: string, linkHref?: (href: string) => string): Components['a'] {
  return function MarkdownLink({ href: raw = '', children, node, ...props }) {
    const href = linkHref ? linkHref(raw) : raw
    if (href.startsWith('/api/')) {
      return <a href={href} className={className} download {...props}>{children}</a>
    }
    if (isExternal(href)) {
      return <a href={href} className={className} target="_blank" rel="noreferrer" {...props}>{children}</a>
    }
    if (isAbsolutePath(href)) {
      return <Link to={href} className={className} {...props}>{children}</Link>
    }
    return <a href={href} className={className} {...props}>{children}</a>
  }
}

const RASTER = /\.(png|jpe?g|webp)(\?|$)/i

/**
 * Lays a help-page screenshot out at half its pixel width.
 *
 * The screenshots are captured at `deviceScaleFactor: 2`
 * (e2e/playwright.docs.config.ts), so their pixel dimensions are twice the size
 * they are meant to occupy. Without this, a small crop — the QA finding popover
 * is 578px of pixels for a 289px panel — renders at double size and fills the
 * column. Wide shots are unaffected either way: `max-w-full` already caps them.
 *
 * `srcset="… 2x"` looks like the declarative fix and is not: with no 1x
 * candidate the browser appends `src` as one (HTML spec, "update the source
 * set"), so the same file is offered at both densities and a non-retina display
 * picks 1x — rendering double-size again. Explicit width is the only
 * DPR-independent answer, and it needs the natural size, which only the loaded
 * image knows.
 *
 * Runs from both `ref` and `onLoad`: a cached image can finish before React
 * attaches the handler, in which case `onLoad` never fires and only the ref
 * pass catches it. SVGs are skipped — they are vector and their intrinsic size
 * is already in CSS pixels.
 */
export function halveRasterAsset(el: HTMLImageElement | null) {
  if (!el || el.dataset.halved) return
  if (!el.complete || !el.naturalWidth) return
  if (!RASTER.test(el.currentSrc || el.src)) return
  el.style.width = `${el.naturalWidth / 2}px`
  el.dataset.halved = '1'
}

function docsComponents(assetBase: string, idByLine: Map<number, string>, linkHref?: (href: string) => string): Components {
  const heading = (Tag: 'h2' | 'h3', className: string): Components['h2'] =>
    ({ children, node, ...props }) => {
      // Fallback covers a heading the line scan could not see (e.g. Setext
      // style), which then simply gets no TOC entry.
      const line = node?.position?.start.line
      const id = (line !== undefined && idByLine.get(line)) || slugifyHeading(textOfChildren(children))
      return (
        <Tag id={id} className={cn('group scroll-mt-4', className)} {...props}>
          {children}
          <a href={`#${id}`} aria-label="Link to this section" className="ml-2 text-gray-300 opacity-0 transition-opacity group-hover:opacity-100 dark:text-gray-600">#</a>
        </Tag>
      )
    }
  return {
    p: props => <p className="my-3 leading-7 text-gray-800 dark:text-gray-200" {...props} />,
    ul: props => <ul className="my-3 list-disc pl-6 space-y-1.5 leading-7" {...props} />,
    ol: props => <ol className="my-3 list-decimal pl-6 space-y-1.5 leading-7" {...props} />,
    li: props => <li className="text-gray-800 dark:text-gray-200" {...props} />,
    // Page bodies start at ## (the title comes from frontmatter); a stray # is
    // rendered at the same weight so the outline stays flat.
    h1: heading('h2', 'mt-10 mb-3 text-xl font-semibold text-gray-900 dark:text-gray-100'),
    h2: heading('h2', 'mt-10 mb-3 text-xl font-semibold text-gray-900 dark:text-gray-100'),
    h3: heading('h3', 'mt-6 mb-2 text-base font-semibold text-gray-900 dark:text-gray-100'),
    a: markdownLink(textLinkClass({ underlined: true }), linkHref),
    img: ({ src = '', alt = '', node, ...props }) => {
      const resolved = typeof src === 'string' && !isExternal(src) && !isAbsolutePath(src)
        ? `${assetBase}/${src.replace(/^(\.\/)?(assets\/)?/, '')}`
        : src
      return (
        <img
          src={resolved}
          alt={alt}
          loading="lazy"
          ref={halveRasterAsset}
          onLoad={e => halveRasterAsset(e.currentTarget)}
          className="my-5 max-w-full rounded-md border border-gray-200 shadow-sm dark:border-gray-800"
          {...props}
        />
      )
    },
    code: ({ className, node, ...props }) => (
      // Inline code only — fenced blocks get their styling from `pre`.
      <code className={cn('rounded bg-gray-100 px-1.5 py-0.5 text-[0.9em] dark:bg-gray-800', className)} {...props} />
    ),
    pre: props => (
      <pre className="my-4 overflow-x-auto rounded-md bg-gray-100 p-3 text-sm leading-6 dark:bg-gray-800 [&_code]:bg-transparent [&_code]:p-0" {...props} />
    ),
    blockquote: props => (
      <blockquote className="my-4 border-l-2 border-gray-300 pl-4 text-gray-600 dark:border-gray-600 dark:text-gray-400" {...props} />
    ),
    hr: props => <hr className="my-8 border-gray-200 dark:border-gray-800" {...props} />,
    strong: props => <strong className="font-semibold text-gray-900 dark:text-gray-100" {...props} />,
    table: props => (
      <div className="my-4 overflow-x-auto">
        <table className="w-full border-collapse text-sm" {...props} />
      </div>
    ),
    thead: props => <thead className="border-b border-gray-300 dark:border-gray-600" {...props} />,
    th: props => <th className="px-3 py-2 text-left font-semibold text-gray-700 dark:text-gray-300" {...props} />,
    tr: props => <tr className="border-b border-gray-100 last:border-0 dark:border-gray-800" {...props} />,
    td: props => <td className="px-3 py-2 align-top" {...props} />,
  }
}
