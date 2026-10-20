import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { ChevronDown, Plus, X } from 'lucide-react'
import { useOnChange } from '@/hooks/useOnChange'

import { Badge } from '@/components/ui/badge'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList, CommandSeparator } from '@/components/ui/command'
import { cn } from '@/lib/utils'
import { controlBoxClasses, controlDisabledSelfClasses, controlFocusVisibleClasses, controlHeightClass, controlInvalidSelfClasses } from '@/components/ui/control-box'

// Combobox — the one searchable select in the app, plus
// MultiCombobox, its multi-select counterpart (chips in the field, toggle
// list). Both render ComboboxBase below. Callers map their own data onto
// `options` (`components/lang-options.tsx` does it for languages) and add
// pinned `actions` (`createAction` for "+ Create new…").
//
// Anatomy, open:
//   search            always present, never hidden
//   actions + divider optional pinned rows (e.g. "New MT engine")
//   selected          the current selection(s), pinned above a divider
//   rest              everything else
// Anatomy of a row: label on the left wall, optional chip right-aligned, check
// at the far right when selected.
//
// E2E: the trigger carries the passed `data-testid`; rows get
// `${testid}-option-${value}` and actions `${testid}-action-${key}`, so
// Playwright opens the trigger then clicks the row (cmdk popovers can't be
// driven by selectOption).

export interface ComboboxOption {
  value: string
  label: string
  disabled?: boolean
  /** Optional leading element (e.g. a provider icon) shown before the label. */
  icon?: ReactNode
  /**
   * Right-aligned chip — a short read-only qualifier (a type, a provider, a
   * count). A string renders in a secondary `Badge`; pass a node for anything
   * else. The selected option's chip also shows in the trigger, so keep it
   * short: it shares the field with the label.
   */
  chip?: ReactNode
  /** Extra text the search matches, on top of `label` (codes, aliases). */
  keywords?: string
  /**
   * Text for this option's chip in the multi-select trigger; defaults to
   * `label`. A chip row is tight, so a language passes its code here and keeps
   * the full name for the list.
   */
  shortLabel?: string
}

/**
 * A pinned row under the search field (e.g. "New MT engine"). Not a selectable
 * value: picking one closes the popover and runs `onSelect` without touching
 * `value`, and it stays visible no matter what the user types.
 */
export interface ComboboxAction {
  key: string
  label: string
  icon?: ReactNode
  onSelect: () => void
}

// Stands in for the empty string as a cmdk row id. See renderOption.
const EMPTY_VALUE = '__combobox_empty__'

// cmdk keys items by `value`; actions share that namespace, so they get a prefix
// that can't collide with a real value (and that the filter keys off).
const ACTION_PREFIX = '__action__:'

interface BaseProps {
  options: ComboboxOption[]
  /** Pinned, non-selectable rows under the search field. */
  actions?: ComboboxAction[]
  /**
   * Makes the search box double as a way to add an option that does not exist
   * yet: text matching no option pins a `Add "…"` row, which hands the raw
   * string back. For a field whose options ARE free text — a label's allowed
   * values. A picker over real entities keeps `actions` + `createAction`
   * instead, because those need a form, not a string.
   */
  onCreateOption?: (text: string) => void
  /** Row label for `onCreateOption`; `{}` is replaced by the typed text. */
  createOptionLabel?: string
  placeholder?: string
  searchPlaceholder?: string
  /**
   * Show the search field (default). Turn it off for a short, fixed list — a
   * two-way setting — where typing buys nothing. Keyboard navigation still
   * works: the list takes focus instead.
   */
  searchable?: boolean
  /**
   * Hands the search text to the caller, who fetches the matching `options`
   * (a server search over a list too long to load whole). The list then shows
   * `options` as given, unfiltered; keep the current pick among them so the
   * field can still name it.
   */
  onSearchChange?: (text: string) => void
  emptyText?: string
  disabled?: boolean
  className?: string
  /** Width/side overrides for the popover panel. */
  contentClassName?: string
  /**
   * What the single-select trigger shows for the current pick: its `label`
   * (default) or only its `chip`, for a field whose chip already says
   * everything and whose label would just repeat it. The list rows are
   * unchanged either way, and an option with no chip still shows its label —
   * a blank field is never the answer. Ignored by MultiCombobox, whose field
   * is chips already.
   */
  triggerDisplay?: 'label' | 'chip'
  id?: string
  /** Optional controlled open state (uncontrolled if omitted). */
  open?: boolean
  onOpenChange?: (open: boolean) => void
  'data-testid'?: string
  'aria-label'?: string
  /**
   * Marks the field as failing validation: the trigger gets `aria-invalid` and
   * the same red frame an `Input` shows. Callers pass their form error here,
   * never a border colour through `className`.
   */
  'aria-invalid'?: boolean
}

type SingleProps = BaseProps & {
  multiple?: false
  value: string
  onChange: (value: string) => void
  /** Show an X inside the field while a value is picked; it fires `onChange('')`. */
  clearable?: boolean
}

type MultiProps = BaseProps & {
  multiple: true
  value: string[]
  onChange: (values: string[]) => void
  /**
   * Let the chips wrap onto further lines and the field grow with them,
   * instead of collapsing the ones that do not fit into "+N". For a field
   * whose whole selection must stay readable (a router row's languages);
   * the default keeps a fixed-height row for dense forms and tables.
   */
  wrap?: boolean
}

export type ComboboxProps = Omit<SingleProps, 'multiple'>
export type MultiComboboxProps = Omit<MultiProps, 'multiple'>

function ComboboxBase(props: SingleProps | MultiProps) {
  const {
    options,
    actions,
    onCreateOption,
    createOptionLabel = 'Add "{}"',
    placeholder = 'Select…',
    searchPlaceholder = 'Search…',
    searchable = true,
    onSearchChange,
    emptyText = 'No results',
    disabled = false,
    className,
    contentClassName,
    triggerDisplay = 'label',
    id,
    open: openProp,
    onOpenChange,
    'data-testid': testId,
    'aria-label': ariaLabel,
    'aria-invalid': ariaInvalid,
  } = props

  // Single keeps '' as a candidate on purpose: "None" rows carry value ''. An
  // unmatched value (including '' with no such row) is filtered out below.
  const selectedValues = props.multiple ? props.value : [props.value]

  const [openState, setOpenState] = useState(false)
  const open = openProp ?? openState
  const setOpen = (o: boolean) => {
    setOpenState(o)
    onOpenChange?.(o)
    // Here, not in the close reset below: that runs during render, where only
    // this component's own state may change.
    if (!o) onSearchChange?.('')
  }

  // Controlled only so `onCreateOption` can read what was typed. Cleared on
  // close, so reopening never starts on a stale filter.
  const [search, setSearchState] = useState('')
  const setSearch = (text: string) => {
    setSearchState(text)
    onSearchChange?.(text)
  }
  useOnChange([open], () => { if (!open) setSearchState('') })
  const typed = search.trim()
  const canCreateTyped =
    onCreateOption != null &&
    typed !== '' &&
    !options.some((o) => o.label.toLowerCase() === typed.toLowerCase())
  // Whether anything would render under the create row. Mirrors the `filter`
  // below rather than counting `options`: typing a value nothing matches is the
  // usual way to reach the create row, and then the list under it is empty even
  // though options exist. Actions always survive the filter.
  const anythingBelowCreateRow =
    (actions?.length ?? 0) > 0 ||
    options.some((o) => `${o.label} ${o.keywords ?? ''}`.toLowerCase().includes(search.toLowerCase()))

  // Row order is snapshotted on open: in multi-select, toggling a value must not
  // re-sort the list under the pointer.
  const [pinned, setPinned] = useState<string[]>(selectedValues)
  const listRef = useRef<HTMLDivElement>(null)
  const latestSelected = useRef(selectedValues)
  useEffect(() => {
    latestSelected.current = selectedValues
  })
  useEffect(() => {
    if (open) setPinned(latestSelected.current)
  }, [open])

  const select = (value: string) => {
    if (props.multiple) {
      props.onChange(
        props.value.includes(value) ? props.value.filter((v) => v !== value) : [...props.value, value],
      )
      return
    }
    props.onChange(value)
    setOpen(false)
  }

  const pinnedSet = new Set(pinned)
  const selectedOptions = options.filter((o) => pinnedSet.has(o.value))
  const restOptions = options.filter((o) => !pinnedSet.has(o.value))

  // Multi keeps a value that has no option as a chip labelled with the raw value
  // — it stays removable. Single falls back to the placeholder instead of
  // showing a bare id.
  const found = selectedValues.map((v) => options.find((o) => o.value === v))
  const selectedOpts: ComboboxOption[] = props.multiple
    ? selectedValues.map((v, i) => found[i] ?? { value: v, label: v })
    : found.filter((o): o is ComboboxOption => !!o)
  const first = selectedOpts[0]
  // `triggerDisplay="chip"` only applies once there is a pick AND it has a chip:
  // with neither there is nothing to draw, and an empty field says less than the
  // label or the placeholder it replaced.
  const chipOnly = triggerDisplay === 'chip' && !!first?.chip

  const multi = props.multiple === true
  const wrap = props.multiple === true && props.wrap === true
  const clearable = !props.multiple && props.clearable === true && !!first && !disabled
  const clearSingle = () => {
    if (!props.multiple) props.onChange('')
  }
  const remove = (value: string) => {
    if (props.multiple) props.onChange(props.value.filter((v) => v !== value))
  }

  // Multi-select keeps its chips INSIDE the control. By default that is one
  // row that never changes height: as many chips as fit are shown and the rest
  // collapse into "+N". A hidden row mirrors the chips so their widths can be
  // measured for the current field width. With `wrap` every chip shows and
  // the field grows — no measuring.
  const rowRef = useRef<HTMLDivElement>(null)
  const measureRef = useRef<HTMLDivElement>(null)
  const [visibleCount, setVisibleCount] = useState(selectedOpts.length)
  const chipCount = selectedOpts.length
  // Re-measure when the labels change, not just the count: swapping one
  // selection for a longer one changes how many fit.
  const chipLabels = selectedOpts.map((o) => o.shortLabel ?? o.label).join(' ')
  useLayoutEffect(() => {
    // Wrapping shows every chip regardless of visibleCount, so there is
    // nothing to measure.
    if (wrap) return
    const row = rowRef.current
    const measure = measureRef.current
    if (!row || !measure) return

    const compute = () => {
      const avail = row.clientWidth
      // Unmeasurable (not laid out yet, or the field is hidden) — collapsing
      // every chip there would render an empty-looking control.
      if (!avail) {
        setVisibleCount(chipCount)
        return
      }
      const GAP = 4
      const BADGE_W = 40 // reserved width for the "+N" badge (+ gap)
      let used = 0
      let count = 0
      const chips = Array.from(measure.children) as HTMLElement[]
      for (let i = 0; i < chips.length; i++) {
        const w = chips[i].offsetWidth + GAP
        const reserve = i < chips.length - 1 ? BADGE_W : 0 // room for "+N" if more remain
        if (used + w + reserve > avail) break
        used += w
        count++
      }
      setVisibleCount(count)
    }

    compute()
    const ro = new ResizeObserver(compute)
    ro.observe(row)
    return () => ro.disconnect()
  }, [chipCount, chipLabels, wrap])

  const visibleChips = wrap ? selectedOpts : selectedOpts.slice(0, visibleCount)
  const hiddenCount = chipCount - visibleChips.length

  const renderChipLabel = (o: ComboboxOption) => (
    <Badge
      key={o.value}
      variant="secondary"
      // The chip itself never reacts to hover — only its × does. A chip is a
      // label for a value, not a button.
      className={cn('max-w-[12rem]', o.disabled ? undefined : 'pr-0.5')}
      data-slot="combobox-value-chip"
    >
      <span className="truncate">{o.shortLabel ?? o.label}</span>
      {/* A disabled option is a pick that cannot be changed, so it carries no
          ×. Without this the row is unclickable in the list while the chip
          quietly removes it anyway. */}
      {!o.disabled && (
        /* role=button span, not a <button>: this lives inside the <button>
           trigger, where nested buttons are invalid HTML. */
        <span
          role="button"
          tabIndex={0}
          aria-label={`Remove ${o.label}`}
          data-testid={testId ? `${testId}-remove-${o.value}` : undefined}
          onPointerDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation()
            remove(o.value)
          }}
          onKeyDown={(e) => {
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault()
              e.stopPropagation()
              remove(o.value)
            }
          }}
          className="inline-flex shrink-0 cursor-pointer rounded-full p-0.5 hover:bg-foreground/35"
        >
          <X className="size-3" />
        </span>
      )}
    </Badge>
  )

  const renderOption = (o: ComboboxOption) => (
    <CommandItem
      key={o.value}
      // cmdk identifies the highlighted row by its value string and treats an
      // empty one as "nothing highlighted", so a "None" row would never light
      // up under the pointer. It still SELECTS — onSelect uses the closure —
      // which is what makes the symptom look like a styling bug.
      value={o.value === '' ? EMPTY_VALUE : o.value}
      disabled={o.disabled}
      // Use the closure value, not cmdk's lowercased arg.
      onSelect={() => select(o.value)}
      data-checked={selectedValues.includes(o.value)}
      data-testid={testId ? `${testId}-option-${o.value}` : undefined}
      // Row under the pointer (or the keyboard cursor — cmdk marks both with
      // `data-selected`) lifts toward the foreground: lighter than the panel in
      // dark mode, darker in light. `bg-muted`, CommandItem's default, is too
      // close to the panel to read.
      className="data-selected:bg-foreground/15"
    >
      {o.icon}
      {/* The label takes the slack so the chip lands hard right. It can't just be
          `ml-auto` on the chip: CommandItem always renders its check with
          `ml-auto` too, and two auto margins SPLIT the free space — a short label
          would park the chip mid-row. */}
      <span className="min-w-0 flex-1 truncate">{o.label}</span>
      {o.chip && (
        <span className={cn('shrink-0 pl-2', CHIP_CAP)} data-slot="combobox-chip">
          {renderChip(o.chip)}
        </span>
      )}
    </CommandItem>
  )

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        id={id}
        data-slot="combobox-trigger"
        data-testid={testId}
        aria-label={ariaLabel}
        aria-invalid={ariaInvalid || undefined}
        disabled={disabled}
        // Field tokens, not button tokens: a combobox sits in forms next to
        // Input, so it borrows Input's radius, border and height.
        className={cn(
          'relative flex items-center justify-between gap-2 text-left disabled:pointer-events-none',
          // A wrapping multi grows with its chips; every other field is one
          // control tall.
          wrap ? 'min-h-input py-1' : controlHeightClass,
          controlBoxClasses,
          controlFocusVisibleClasses,
          controlInvalidSelfClasses,
          controlDisabledSelfClasses,
          // Chips carry their own inset, so the multi field starts tighter.
          multi && 'pl-1.5',
          first ? 'text-foreground' : 'text-muted-foreground',
          className,
        )}
      >
        {multi ? (
          <div ref={rowRef} className={cn('flex min-w-0 flex-1 items-center gap-1', wrap ? 'flex-wrap' : 'overflow-hidden')} data-wrap={wrap || undefined}>
            {chipCount === 0 ? (
              <span className="min-w-0 truncate px-1">{placeholder}</span>
            ) : (
              <>
                {visibleChips.map(renderChipLabel)}
                {hiddenCount > 0 && (
                  <Badge variant="secondary" className="shrink-0" data-slot="combobox-overflow" title={`${hiddenCount} more`}>
                    +{hiddenCount}
                  </Badge>
                )}
              </>
            )}
          </div>
        ) : (
          <span className="flex min-w-0 flex-1 items-center gap-1.5">
            {first?.icon}
            {chipOnly ? (
              // The chip stands in for the label, so it sits where the label
              // would: left wall, not right-aligned. No CHIP_CAP here — capping
              // the stand-in label would leave half the field empty.
              <span className="min-w-0 truncate" data-slot="combobox-selected-chip">
                {renderChip(first!.chip)}
              </span>
            ) : (
              <>
                <span className="min-w-0 flex-1 truncate">{first ? first.label : placeholder}</span>
                {/* Right-aligned like the rows, so the chip sits in the same
                    place open and closed. */}
                {first?.chip && (
                  <span className={cn('shrink-0', CHIP_CAP)} data-slot="combobox-selected-chip">
                    {renderChip(first.chip)}
                  </span>
                )}
              </>
            )}
          </span>
        )}
        {clearable && (
          // role=button span, not a <button>: nested buttons are invalid HTML.
          // Out of the tab order, like Input's clear X.
          <span
            role="button"
            tabIndex={-1}
            aria-label="Clear"
            data-slot="combobox-clear"
            data-testid={testId ? `${testId}-clear` : undefined}
            onPointerDown={(e) => e.stopPropagation()}
            onClick={(e) => {
              e.stopPropagation()
              clearSingle()
            }}
            className="inline-flex shrink-0 cursor-pointer rounded-full p-0.5 text-muted-foreground hover:text-foreground"
          >
            <X className="size-3.5" />
          </span>
        )}
        <ChevronDown className="h-4 w-4 shrink-0 opacity-50" aria-hidden="true" />
        {multi && !wrap && (
          // Hidden mirror of the chips — measured to decide how many fit.
          <div ref={measureRef} aria-hidden className="pointer-events-none invisible absolute top-0 left-0 flex items-center gap-1">
            {selectedOpts.map(renderChipLabel)}
          </div>
        )}
      </PopoverTrigger>
      {/* The panel is as wide as the field (`--anchor-width` comes from the
          popover positioner), with a floor so a narrow field still shows
          readable rows. */}
      <PopoverContent
        className={cn('w-(--anchor-width) min-w-56 p-0', contentClassName)}
        align="start"
        initialFocus={searchable ? undefined : listRef}
      >
        <Command
          ref={listRef}
          tabIndex={searchable ? undefined : -1}
          className={searchable ? undefined : 'outline-none'}
          shouldFilter={!onSearchChange}
          filter={(val, search) => {
            // Actions are pinned: cmdk filters every item regardless of where
            // it's rendered, so they opt out explicitly or they'd vanish the
            // moment the user types — exactly when an empty list makes them
            // most useful.
            if (val.startsWith(ACTION_PREFIX)) return 1
            const opt = options.find((o) => o.value === (val === EMPTY_VALUE ? '' : val))
            if (!opt) return 0
            const haystack = `${opt.label} ${opt.keywords ?? ''}`.toLowerCase()
            return haystack.includes(search.toLowerCase()) ? 1 : 0
          }}
        >
          {searchable && <CommandInput placeholder={searchPlaceholder} autoFocus value={search} onValueChange={setSearch} />}
          {canCreateTyped && (
            <>
              <CommandGroup>
                <CommandItem
                  value={`${ACTION_PREFIX}create-option`}
                  className="font-medium text-primary"
                  onSelect={() => {
                    onCreateOption!(typed)
                    // The panel stays open and the box empties: adding values
                    // comes in runs, and the new one has to be visible to be
                    // ticked. Closing would cost a click per value.
                    setSearch('')
                  }}
                  data-testid={testId ? `${testId}-action-create-option` : undefined}
                >
                  <Plus className="h-3.5 w-3.5" />
                  {createOptionLabel.replace('{}', typed)}
                </CommandItem>
              </CommandGroup>
              {/* Only with something below it. Typing a value nothing matches
                  is exactly how the create row is reached, and then the list
                  under it is empty — an unguarded separator hangs alone at the
                  bottom of the panel. `alwaysRender` because the row above
                  survives the filter and the divider has to survive with it. */}
              {anythingBelowCreateRow && <CommandSeparator alwaysRender />}
            </>
          )}
          {actions && actions.length > 0 && (
            <>
              <CommandGroup>
                {actions.map((a) => (
                  <CommandItem
                    key={a.key}
                    value={`${ACTION_PREFIX}${a.key}`}
                    className="font-medium text-primary"
                    onSelect={() => {
                      setOpen(false)
                      a.onSelect()
                    }}
                    data-testid={testId ? `${testId}-action-${a.key}` : undefined}
                  >
                    {a.icon ?? <Plus className="h-3.5 w-3.5" />}
                    {a.label}
                  </CommandItem>
                ))}
              </CommandGroup>
              {/* Only with rows below it — an empty list would leave it hanging.
                  `alwaysRender` because the actions above it survive the filter;
                  the divider has to survive with them. */}
              {options.length > 0 && <CommandSeparator alwaysRender />}
            </>
          )}
          <CommandList className="max-h-[240px]">
            <CommandEmpty>{emptyText}</CommandEmpty>
            {selectedOptions.length > 0 && (
              <>
                <CommandGroup>{selectedOptions.map(renderOption)}</CommandGroup>
                {/* Only with rows below it, or it hangs at the bottom of the
                    panel once everything is selected. Plain, no `alwaysRender`:
                    while searching, either group can come back empty. */}
                {restOptions.length > 0 && <CommandSeparator />}
              </>
            )}
            {restOptions.length > 0 && <CommandGroup>{restOptions.map(renderOption)}</CommandGroup>}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}

// A string chip gets the standard Badge; a node is the caller's own.
function renderChip(chip: ReactNode) {
  return typeof chip === 'string' ? <Badge variant="secondary">{chip}</Badge> : chip
}

/**
 * Caps a row's chip at 55% and ellipsises it. Without the cap a long chip
 * claims the whole row: the `flex-1 truncate` label collapses to zero width and
 * the chip overflows the field.
 */
const CHIP_CAP = 'max-w-[55%] truncate'

/** Single-select picker. Picking a row closes the panel. */
export function Combobox(props: ComboboxProps) {
  return <ComboboxBase {...props} />
}

/**
 * Multi-select picker. Rows toggle and the panel stays open; the selection sits
 * as removable chips inside the field, overflow collapsing into "+N".
 */
export function MultiCombobox(props: MultiComboboxProps) {
  return <ComboboxBase {...props} multiple />
}

/**
 * The "+ Create new…" action for resource pickers: `run` opens the create flow
 * and resolves to the new id (or null when cancelled); `onCreated` then
 * receives the id — callers select it. Pinned under the search like any action.
 */
export function createAction(label: string, run: () => Promise<number | null>, onCreated: (id: number) => void): ComboboxAction {
  return {
    key: 'create',
    label,
    onSelect: async () => {
      const id = await run()
      if (id != null) onCreated(id)
    },
  }
}
