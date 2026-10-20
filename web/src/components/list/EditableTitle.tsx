import { useEffect, useRef, useState } from 'react'
import { Pencil } from 'lucide-react'
import { InlineRenameField } from './InlineRenameField'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

interface Props {
  /** Current name shown as the title. */
  name: string
  /**
   * Called with the trimmed new name — only when it actually changed. Needed
   * only where the title can be edited; a read-only title (`canEdit={false}`)
   * leaves it out.
   */
  onRename?: (name: string) => void
  /** Hide the pencil (e.g. non-admins on the project page). Defaults to true. */
  canEdit?: boolean
  /**
   * Drive the edit state from outside, for callers whose rename lives in a
   * MENU rather than on the pencil. Both must be passed together; omitting them
   * keeps the component self-contained, which is how every earlier caller uses
   * it.
   */
  editing?: boolean
  onEditingChange?: (editing: boolean) => void
  /** Classes for the display title text (size / weight / color). */
  className?: string
  /** Classes for the edit field; a page title's size by default, so a card
   *  or row passes its own. */
  fieldClassName?: string
  /** The edit field's accessible name. */
  fieldLabel?: string
  /**
   * Whether the name itself takes keyboard focus, so the full name is
   * reachable without a mouse. Turn it off when the name sits inside a control
   * that already takes focus (a button): a focusable element inside a button
   * is invalid, and the button announces the full name anyway.
   */
  focusable?: boolean
  'data-testid'?: string
}

/**
 * The one way an entity name is displayed: it truncates to the width it has and
 * offers the full text in a tooltip once the ellipsis hides something. With the
 * pencil (the default) it also renames in place — clicking swaps in a compact
 * edit field, and `onRename` fires only when the value actually changed. With
 * `canEdit={false}` it is the read-only name a list row or chip shows.
 */
export function EditableTitle({
  name, onRename, canEdit = true, className, focusable = true, 'data-testid': testId,
  editing: editingProp, onEditingChange,
  fieldClassName = 'text-xl font-semibold w-80', fieldLabel = 'Rename',
}: Props) {
  const [ownEditing, setOwnEditing] = useState(false)
  const controlled = editingProp !== undefined
  const editing = controlled ? editingProp : ownEditing
  const setEditing = (next: boolean) => {
    if (!controlled) setOwnEditing(next)
    onEditingChange?.(next)
  }
  const [value, setValue] = useState('')
  const [clipped, setClipped] = useState(false)
  const nameRef = useRef<HTMLSpanElement>(null)

  // The tooltip only exists while the ellipsis is hiding something. Measured
  // when the trigger is reached, not on every render — layout width depends on
  // the container, which nothing here observes.
  const measureClipping = () => {
    const el = nameRef.current
    setClipped(!!el && el.scrollWidth > el.clientWidth)
  }

  // A controlled caller opens the field without going through the pencil, so
  // the draft is seeded here rather than in the click handler.
  useEffect(() => {
    if (editing) setValue(name)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only on open
  }, [editing])

  if (editing) {
    const submit = () => {
      const next = value.trim()
      setEditing(false)
      if (next && next !== name) onRename?.(next)
    }
    return (
      <InlineRenameField
        value={value}
        onChange={setValue}
        onSubmit={submit}
        onCancel={() => setEditing(false)}
        aria-label={fieldLabel}
        className={fieldClassName}
      />
    )
  }

  return (
    <span className={cn('inline-flex min-w-0 items-center gap-2', className)}>
      {/* A runaway name truncates instead of pushing the pencil (or the row's
          actions) off the viewport; the tooltip is the way back to the full
          name. */}
      <TooltipProvider delay={200}>
        <Tooltip>
          <TooltipTrigger
            render={
              <span
                ref={nameRef}
                // Focusable so the full name is reachable by keyboard, not just
                // by hover (same span-trigger shape as SectionInfo).
                tabIndex={focusable ? 0 : undefined}
                className="truncate outline-none focus-visible:underline"
                onMouseEnter={measureClipping}
                onFocus={measureClipping}
              />
            }
          >
            {name}
          </TooltipTrigger>
          {clipped && <TooltipContent className="max-w-md break-all">{name}</TooltipContent>}
        </Tooltip>
      </TooltipProvider>
      {canEdit && !controlled && (
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          onClick={() => { setValue(name); setEditing(true) }}
          data-testid={testId}
          title="Rename"
          className="text-gray-400 hover:text-gray-600 dark:text-gray-500 dark:hover:text-gray-300"
        >
          <Pencil className="w-4 h-4" />
        </Button>
      )}
    </span>
  )
}
