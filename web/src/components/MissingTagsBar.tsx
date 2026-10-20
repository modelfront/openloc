import type { MissingTag } from '../lib/missing-tags'
import { chipLabel, chipTitle } from '../lib/xliff-tag-label'
import { TextLink } from '@/components/ui/text-link'

interface MissingTagsBarProps {
  missing: MissingTag[]
  onInsert: (tag: MissingTag) => void
  onInsertAll: () => void
}

/**
 * Compact strip shown above the active segment's editor when one or more
 * source tags are missing from the target. Each missing tag is rendered as
 * a clickable chip (same color palette as in-editor chips, via
 * `.xliff-tag[data-tag-type=…]`); clicking inserts that single tag at the
 * editor's current cursor. The trailing "Insert all" button inserts every
 * remaining missing tag in source order.
 *
 * F8 inserts the next missing tag, Alt+F8 inserts all (see SegmentEditor's
 * keymap). The chips here use the same `.xliff-tag` CSS class as in-editor chips so
 * users instantly recognize "this is the same thing I'd type/insert".
 */
export default function MissingTagsBar({ missing, onInsert, onInsertAll }: MissingTagsBarProps) {
  if (missing.length === 0) return null

  const isMac = typeof navigator !== 'undefined' && /mac/i.test(navigator.platform)
  const altLabel = isMac ? '⌥F8' : 'Alt+F8'

  return (
    <div
      data-testid="missing-tags-bar"
      className="flex items-center gap-1 mb-1 px-1 py-1 rounded bg-amber-50 dark:bg-amber-950/30 border border-amber-200 dark:border-amber-800/40 text-[11px]"
    >
      <span className="text-amber-700 dark:text-amber-400 font-medium pr-1">
        Missing tag{missing.length > 1 ? 's' : ''}:
      </span>
      <div className="flex items-center gap-1 flex-wrap">
        {missing.map((tag, i) => (
          // `<button>` for accessibility (keyboard focus + screen readers).
          // `onMouseDown=preventDefault` blocks the browser's default focus
          // shift to the button — important when the bar is rendered
          // OUTSIDE the editor (e.g. at TMBrowser's page-level), so the
          // editor's contenteditable keeps focus and `onBlur=commitEdit`
          // doesn't fire mid-action. The actual insert lives in `onClick`
          // because `onMouseDown` fires too early (selection state may
          // not be settled yet). Same focus-preservation pattern as
          // `EditorSidebar.tsx` for terminology suggestions.
          // eslint-disable-next-line no-restricted-syntax -- missing-tag insert chip, not a standard action button
          <button
            key={`${tag.tagType}-${tag.tagId}-${tag.closing ? 'c' : 'o'}-${i}`}
            type="button"
            data-testid="missing-tag-chip"
            data-tag-type={tag.tagType}
            data-tag-id={tag.tagId}
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => onInsert(tag)}
            className="xliff-tag cursor-pointer hover:ring-2 hover:ring-amber-400 transition-shadow"
            title={`Insert ${chipTitle(tag)} at cursor (F8 inserts the first one)`}
          >
            {chipLabel(tag)}
          </button>
        ))}
      </div>
      {missing.length > 1 && (
        <TextLink
          data-testid="missing-tags-insert-all"
          onMouseDown={(e) => e.preventDefault()}
          onClick={onInsertAll}
          tone="yellow"
          underlined
          className="ml-auto text-xs"
          title={`Insert all missing tags at cursor (${altLabel})`}
        >
          Insert all ({altLabel})
        </TextLink>
      )}
    </div>
  )
}
