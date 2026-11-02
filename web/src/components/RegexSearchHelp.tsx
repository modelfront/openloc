import { Fragment, type ReactNode } from 'react'
import { CircleHelp } from 'lucide-react'
import { Popover, PopoverContent, PopoverDescription, PopoverTitle, PopoverTrigger } from '@/components/ui/popover'
import { InlineCode } from '@/components/ui/inline-code'
import { QA_REGEX_HELP_PATTERNS, SEGMENT_REGEX_HELP_PATTERNS, TM_REGEX_HELP_PATTERNS, type RegexHelpPattern } from '@/lib/regex-help-patterns'

/** Which regex the card describes: the editor's segment search (Go RE2 on the
 *  server), the TM entry search (Postgres), or a QA profile's regex rule (Go
 *  RE2, checked on save). */
export type RegexHelpVariant = 'segments' | 'tm' | 'qa'

interface Copy {
  title: string
  label: string
  patterns: RegexHelpPattern[]
  intro: ReactNode
  bullets: ReactNode[]
}

const containsBullet = (exact: string) => (
  <>
    It&rsquo;s &ldquo;contains&rdquo;, not &ldquo;equals&rdquo; — <InlineCode>error</InlineCode> also finds
    &ldquo;errors&rdquo;. Use <InlineCode>^…$</InlineCode>{exact} for exact.
  </>
)

const searchCopy = (patterns: RegexHelpPattern[], target: string, item: string, exact: string, invalid: string, syntax?: ReactNode): Copy => ({
  title: 'Regex search',
  label: 'How regex search works',
  patterns,
  intro: (
    <>
      The pattern matches anywhere in the text (&ldquo;contains&rdquo;), in the fields
      {' '}<strong>Search in</strong> picks: Both, Source or {target}. Case sensitive
      controls case — no slashes or flags needed.
    </>
  ),
  bullets: [
    containsBullet(exact),
    <>With Search in set to Both, {item} matches if either side matches.</>,
    ...(syntax ? [syntax] : []),
    <>Invalid pattern (e.g. a lone <InlineCode>(</InlineCode>) → {invalid}</>,
  ],
})

const COPY: Record<RegexHelpVariant, Copy> = {
  segments: searchCopy(SEGMENT_REGEX_HELP_PATTERNS, 'Translation', 'a segment', '', 'inline error + honest 0 / N badge.',
    <>RE2 syntax: no lookahead and no backreferences.</>),
  tm: searchCopy(TM_REGEX_HELP_PATTERNS, 'Target', 'an entry', ', or tick Exact', 'inline error, no results.'),
  qa: {
    title: 'Regex patterns',
    label: 'How regex patterns work',
    patterns: QA_REGEX_HELP_PATTERNS,
    intro: (
      <>
        The pattern matches anywhere in the text (&ldquo;contains&rdquo;), as the editor shows it: a tag is its
        label, such as <InlineCode>&lt;1&gt;</InlineCode>. <strong>Ignore case</strong> controls case — no
        slashes or flags needed.
      </>
    ),
    bullets: [
      containsBullet(''),
      <>RE2 syntax: no lookahead and no backreferences.</>,
      <>A pattern that matches empty text, such as <InlineCode>a*</InlineCode>, is refused.</>,
      <>Invalid pattern (e.g. a lone <InlineCode>(</InlineCode>) → the reason shows under the field when you save.</>,
    ],
  },
}

/**
 * The (?) help icon next to a regex field: the Regex checkbox of a search
 * options row, or a QA regex rule's pattern.
 * A popover, not a tooltip: it opens on click AND hover (help icons get
 * clicked), and it stays open while the pointer travels into the card — a
 * hover tooltip dismisses the moment the mouse leaves the tiny icon, which
 * makes a reference card this size effectively unreadable.
 * Sibling of the checkbox label, NOT inside it — a button nested in the label
 * would toggle the checkbox on click.
 */
export function RegexSearchHelp({ variant = 'segments' }: { variant?: RegexHelpVariant }) {
  const copy = COPY[variant]
  return (
    <Popover>
      <PopoverTrigger
        openOnHover
        delay={100}
        render={
          // eslint-disable-next-line no-restricted-syntax -- help popover trigger, not an action button
          <button
            type="button"
            aria-label={copy.label}
            data-testid="regex-help-trigger"
            className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 transition-colors"
          />
        }
      >
        <CircleHelp className="h-3.5 w-3.5" />
      </PopoverTrigger>
      <PopoverContent side="bottom" align="start" size="compact" className="w-auto max-w-md">
        <PopoverTitle>{copy.title}</PopoverTitle>
        <PopoverDescription>{copy.intro}</PopoverDescription>
        <div className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
          {copy.patterns.map(p => (
            <Fragment key={p.pattern}>
              <InlineCode className="justify-self-start whitespace-nowrap">{p.pattern}</InlineCode>
              <span className="text-muted-foreground">{p.desc}</span>
            </Fragment>
          ))}
        </div>
        <ul className="list-disc space-y-0.5 pl-4 text-muted-foreground">
          {copy.bullets.map((b, i) => <li key={i}>{b}</li>)}
        </ul>
      </PopoverContent>
    </Popover>
  )
}
