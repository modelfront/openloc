import { SlidersHorizontal } from 'lucide-react'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { Button } from '@/components/ui/button'
import { VerticalDivider } from '@/components/ui/vertical-divider'
import { RegexSearchHelp, type RegexHelpVariant } from './RegexSearchHelp'
import { ChoiceToggle } from '@/components/ui/choice-toggle'
import { searchScopeFlags, searchScopeOf, type TextSearchOptions } from '@/lib/text-search'

/**
 * The sliders button in a search field's `action` slot that opens the
 * options row. A dot marks options that differ from the default while the
 * row is closed.
 */
export function SearchOptionsToggle({ open, active, onToggle, testIdPrefix }: {
  open: boolean
  active: boolean
  onToggle: () => void
  testIdPrefix: string
}) {
  return (
    <Button
      variant="ghost"
      size="icon-xs"
      aria-label="Advanced search options"
      aria-expanded={open}
      data-testid={`${testIdPrefix}-advanced-toggle`}
      className="relative"
      onClick={onToggle}
    >
      <SlidersHorizontal />
      {active && !open && (
        <span className="absolute top-0.5 right-0.5 size-1.5 rounded-full bg-primary" />
      )}
    </Button>
  )
}

interface FieldsProps {
  options: TextSearchOptions
  onChange: (patch: Partial<TextSearchOptions>) => void
  /** Label of the second scope: "Translation" in the editor, "Target" in a TM. */
  targetLabel: string
  /** Shows the Exact checkbox. */
  withExactMatch?: boolean
  regexError?: string | null
  regexHelp: RegexHelpVariant
  testIdPrefix: string
  className?: string
}

/**
 * The search options: "Search in" Both / Source / target, then the match flags
 * as plain checkboxes — no popover, so E2E stays popover-free. Wraps as one
 * group when the row it sits in runs out of room.
 */
export function SearchOptionsFields({ options, onChange, targetLabel, withExactMatch, regexError, regexHelp, testIdPrefix, className = '' }: FieldsProps) {
  const flag = (key: 'exactMatch' | 'caseSensitive' | 'useRegex', label: string) => (
    <CheckboxField size="compact" label={label} checked={!!options[key]} onChange={checked => onChange({ [key]: checked })} />
  )
  return (
    <div className={`flex items-center gap-3 flex-wrap ${className}`} data-testid={`${testIdPrefix}-advanced`}>
      <span className="flex items-center gap-1.5">
        <span className="text-[10px] text-gray-400">Search in:</span>
        <ChoiceToggle
          size="sm"
          aria-label="Search in"
          value={searchScopeOf(options)}
          onChange={scope => onChange(searchScopeFlags(scope))}
          options={[
            { value: 'both', label: 'Both' },
            { value: 'source', label: 'Source' },
            { value: 'target', label: targetLabel },
          ]}
          data-testid={`${testIdPrefix}-scope`}
        />
      </span>
      <VerticalDivider className="h-3.5" />
      {withExactMatch && flag('exactMatch', 'Exact')}
      {flag('caseSensitive', 'Case sensitive')}
      {/* Label + help icon grouped tighter (gap-1) than the row's gap-3,
          so the (?) visually belongs to "Regex". The icon stays a SIBLING
          of the label — nested inside, clicking it would toggle the box. */}
      <span className="flex items-center gap-1">
        {flag('useRegex', 'Regex')}
        <RegexSearchHelp variant={regexHelp} />
      </span>
      {regexError && (
        <span className="text-[10px] text-red-500" data-testid={`${testIdPrefix}-regex-error`}>
          {regexError}
        </span>
      )}
    </div>
  )
}

/**
 * SearchOptionsFields as a collapsible row under a search field, opened by
 * SearchOptionsToggle. Always mounted: the grid-rows 0fr→1fr transition
 * animates open/close (the CSS way to animate height:auto); inert +
 * aria-hidden keep the collapsed row out of the tab order and the
 * accessibility tree.
 */
export function SearchOptionsPanel({ open, ...fields }: FieldsProps & { open: boolean }) {
  return (
    <div
      className={`grid transition-[grid-template-rows,opacity] duration-200 ease-out ${
        open ? 'grid-rows-[1fr] opacity-100' : 'grid-rows-[0fr] opacity-0'
      }`}
      inert={!open}
      aria-hidden={!open}
    >
      <div className="overflow-hidden">
        <SearchOptionsFields {...fields} className="mt-1 ml-1 py-1.5" />
      </div>
    </div>
  )
}
