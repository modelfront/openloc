import { useEffect, useMemo, useRef, useState } from 'react'
import { useOnChange } from '@/hooks/useOnChange'
import { cn } from '@/lib/utils'
import { extractHeadings } from '@/lib/markdown-headings'

// A heading counts as "reached" once its top edge is within this many pixels
// below the top of the scroll pane (the renderer gives headings `scroll-mt-4`,
// so a heading jumped to via its anchor lands well inside this band).
const ACTIVATION_PX = 96

// Input on the scroll pane that means the reader is scrolling by hand. A
// pointerdown covers grabbing the scrollbar, which fires plain scroll events.
const RELEASE_EVENTS = ['wheel', 'touchmove', 'pointerdown'] as const

// Within this many pixels of the end, the pane counts as scrolled to the bottom.
const BOTTOM_SLACK_PX = 2

function atBottom(pane: HTMLElement | null): boolean {
  const el = pane ?? document.scrollingElement
  if (!el || el.scrollHeight <= el.clientHeight) return false
  return el.scrollTop + el.clientHeight >= el.scrollHeight - BOTTOM_SLACK_PX
}

// "On this page": the ##/### headings of the article, linked to the ids the
// Markdown renderer assigns. Both sides derive ids from the same text through
// lib/markdown-headings, so this never has to read the DOM for ids.
//
// The current section follows the reader's scroll: the last heading whose top
// has passed the activation band is highlighted (aria-current="location").
// The article scrolls inside the docs content pane, not the window, so the
// listener attaches to that pane.
export function DocsToc({ markdown }: { markdown: string }) {
  const headings = useMemo(() => extractHeadings(markdown), [markdown])
  return <TocNav headings={headings} />
}

/** The heading the URL hash names, when it is one of `ids`. */
function hashHeading(ids: string[]): string | null {
  const fromHash = decodeURIComponent(window.location.hash.slice(1))
  return ids.includes(fromHash) ? fromHash : null
}

/** One entry of "On this page": a heading id on the page and its text. */
export type TocItem = { level: 2 | 3; text: string; id: string }

// The list itself, for any set of headings on the page: the article's, or the
// docs home's sections.
//
// A heading near the end of a short page can never scroll up to the band, so
// two more rules decide: an entry the reader clicked stays active until they
// scroll by hand (wheel, touch, keys or the scrollbar), and at the very bottom
// of the pane the last heading is active.
export function TocNav({ headings }: { headings: TocItem[] }) {
  const ids = headings.map(h => h.id).join('\n')
  const [activeId, setActiveId] = useState<string | null>(null)
  const pinned = useRef<string | null>(null)
  // A new page starts on the heading its URL hash names, else the first one.
  useOnChange([ids], () => {
    const list = ids ? ids.split('\n') : []
    setActiveId(hashHeading(list) ?? list[0] ?? null)
  }, { onMount: true })

  useEffect(() => {
    const list = ids ? ids.split('\n') : []
    pinned.current = hashHeading(list)
    const els = list
      .map(id => document.getElementById(id))
      .filter((el): el is HTMLElement => el !== null)
    if (els.length === 0) return
    const pane = els[0].closest<HTMLElement>('[data-testid="docs-content"]')
    const scroller: HTMLElement | Window = pane ?? window

    let frame = 0
    const update = () => {
      frame = 0
      if (pinned.current) return
      const paneTop = pane ? pane.getBoundingClientRect().top : 0
      let current = els[0].id
      for (const el of els) {
        if (el.getBoundingClientRect().top - paneTop <= ACTIVATION_PX) current = el.id
      }
      if (atBottom(pane)) current = els[els.length - 1].id
      setActiveId(current)
    }
    const onScroll = () => {
      if (frame === 0) frame = requestAnimationFrame(update)
    }
    // Scrolling by hand releases a clicked entry. The jump a click causes
    // fires plain scroll events only, so it keeps the pin.
    const release = () => {
      if (!pinned.current) return
      pinned.current = null
      onScroll()
    }
    scroller.addEventListener('scroll', onScroll, { passive: true })
    for (const type of RELEASE_EVENTS) scroller.addEventListener(type, release, { passive: true })
    // Keys scroll the pane wherever the focus is.
    window.addEventListener('keydown', release)
    return () => {
      scroller.removeEventListener('scroll', onScroll)
      for (const type of RELEASE_EVENTS) scroller.removeEventListener(type, release)
      window.removeEventListener('keydown', release)
      if (frame !== 0) cancelAnimationFrame(frame)
    }
  }, [ids])

  const pick = (id: string) => {
    pinned.current = id
    setActiveId(id)
  }

  if (headings.length === 0) return null
  return (
    <nav aria-label="On this page" className="text-sm" data-testid="docs-toc">
      <p className="mb-2 text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">On this page</p>
      <ul className="space-y-1 border-l border-gray-200 dark:border-gray-800">
        {headings.map(h => {
          const active = h.id === activeId
          return (
            <li key={h.id} className={cn('-ml-px border-l-2', active ? 'border-primary' : 'border-transparent', h.level === 3 ? 'pl-6' : 'pl-3')}>
              <a
                href={`#${h.id}`}
                onClick={() => pick(h.id)}
                aria-current={active ? 'location' : undefined}
                className={cn(
                  'block truncate py-0.5 transition-colors',
                  active
                    ? 'font-medium text-gray-900 dark:text-gray-100'
                    : 'text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-100',
                )}
              >
                {h.text}
              </a>
            </li>
          )
        })}
      </ul>
    </nav>
  )
}

// A docs page's two columns: the content, and "On this page" beside it on
// wide screens. The content column carries its own element and width.
export function WithToc({ toc, children }: { toc?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex gap-10">
      {children}
      {toc && <aside className="hidden w-56 shrink-0 self-start xl:block xl:sticky xl:top-0">{toc}</aside>}
    </div>
  )
}
