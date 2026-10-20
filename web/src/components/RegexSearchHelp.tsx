import { Fragment } from 'react'
import { CircleHelp } from 'lucide-react'
import { Popover, PopoverContent, PopoverDescription, PopoverTitle, PopoverTrigger } from '@/components/ui/popover'
import { InlineCode } from '@/components/ui/inline-code'
import { REGEX_HELP_PATTERNS, TM_REGEX_HELP_PATTERNS } from '@/lib/regex-help-patterns'

/** Which search the card describes: the editor's segment search (runs in the
 *  browser) or the TM entry search (runs on the server). */
export type RegexHelpVariant = 'segments' | 'tm'

const COPY = {
  segments: { patterns: REGEX_HELP_PATTERNS, target: 'Translation', item: 'a segment', exact: '', invalid: 'inline error + honest 0 / N badge.' },
  tm: { patterns: TM_REGEX_HELP_PATTERNS, target: 'Target', item: 'an entry', exact: ', or tick Exact', invalid: 'inline error, no results.' },
} as const

/**
 * The (?) help icon next to the Regex checkbox of a search options row.
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
            aria-label="How regex search works"
            data-testid="regex-help-trigger"
            className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 transition-colors"
          />
        }
      >
        <CircleHelp className="h-3.5 w-3.5" />
      </PopoverTrigger>
      <PopoverContent side="bottom" align="start" size="compact" className="w-auto max-w-md">
        <PopoverTitle>Regex search</PopoverTitle>
        <PopoverDescription>
          The pattern matches anywhere in the text (&ldquo;contains&rdquo;), in the fields
          {' '}<strong>Search in</strong> picks: Both, Source or {copy.target}. Case sensitive
          controls case — no slashes or flags needed.
        </PopoverDescription>
        <div className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
          {copy.patterns.map(p => (
            <Fragment key={p.pattern}>
              <InlineCode className="justify-self-start whitespace-nowrap">{p.pattern}</InlineCode>
              <span className="text-muted-foreground">{p.desc}</span>
            </Fragment>
          ))}
        </div>
        <ul className="list-disc space-y-0.5 pl-4 text-muted-foreground">
          <li>
            It&rsquo;s &ldquo;contains&rdquo;, not &ldquo;equals&rdquo; — <InlineCode>error</InlineCode> also finds
            &ldquo;errors&rdquo;. Use <InlineCode>^…$</InlineCode>{copy.exact} for exact.
          </li>
          <li>With Search in set to Both, {copy.item} matches if either side matches.</li>
          <li>
            Invalid pattern (e.g. a lone <InlineCode>(</InlineCode>) → {copy.invalid}
          </li>
        </ul>
      </PopoverContent>
    </Popover>
  )
}
