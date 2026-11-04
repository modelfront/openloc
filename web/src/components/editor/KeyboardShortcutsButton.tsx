import { Keyboard } from 'lucide-react'
import { modKey } from '@/components/SegmentRow'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'

/** The filter bar's keyboard button and the shortcut list it opens. */
export function KeyboardShortcutsButton() {
  const shortcuts = [
    { keys: `${modKey}+Enter`, desc: 'Confirm segment & go to next' },
    { keys: 'Tab', desc: 'Next segment (without confirming)' },
    { keys: 'Shift+Tab', desc: 'Previous segment' },
    { keys: '↓ / ↑', desc: 'Next / previous segment, from the last / first line' },
    { keys: 'Shift+Enter', desc: 'Line break inside the segment' },
    { keys: 'Escape', desc: 'Exit editing' },
    { keys: `${modKey}+Z`, desc: 'Undo' },
    { keys: `${modKey}+Shift+S`, desc: 'Copy source to target' },
    { keys: `${modKey}+G`, desc: 'Jump to segment #' },
    { keys: `${modKey}+K`, desc: 'Concordance search — looks up the selected phrase' },
    { keys: `${modKey}+F`, desc: 'Search all segments' },
    { keys: `${modKey}+H`, desc: 'Find and replace' },
    { keys: `${modKey}+B`, desc: 'Bold' },
    { keys: `${modKey}+I`, desc: 'Italic' },
    { keys: `${modKey}+1..5`, desc: 'Insert TM match #1–5' },
    { keys: 'F8', desc: 'Insert next missing tag, placeable (number, URL, variable…) or term' },
    { keys: 'Alt+F8', desc: 'Insert all missing tags, placeables and terms' },
    { keys: `${modKey}+Shift+Backspace`, desc: 'Clear translation' },
    { keys: 'Alt+Shift+T', desc: 'Split the segment where you clicked in the source' },
    { keys: 'Alt+Shift+J', desc: 'Join with the next sentence or piece' },
    { keys: 'Alt+Shift+G', desc: 'Add the selected words to the term base' },
    { keys: `${modKey}+Click`, desc: 'Add segment to selection, or remove it' },
    { keys: 'Shift+Click #', desc: 'Select a range of segments' },
    { keys: 'Escape', desc: 'Clear selection (when not editing)' },
  ]

  return (
    <Dialog>
      <Tooltip>
        <TooltipTrigger render={
          <DialogTrigger
            render={
              <Button
                variant="outline"
                size="icon-sm"
                aria-label="Keyboard shortcuts"
              />
            }
          />
        }>
          <Keyboard className="size-4" />
        </TooltipTrigger>
        <TooltipContent>Keyboard shortcuts</TooltipContent>
      </Tooltip>
      <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Keyboard shortcuts</DialogTitle>
        </DialogHeader>
        <div className="-mx-1 mt-2 min-h-0 flex-1 space-y-3 overflow-y-auto px-1" data-testid="keyboard-shortcuts-list">
          {shortcuts.map(s => (
            <div key={s.keys} className="flex items-center justify-between">
              <span className="text-sm text-gray-600 dark:text-gray-400">{s.desc}</span>
              <kbd className="px-2 py-1 text-xs font-mono bg-gray-100 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded">{s.keys}</kbd>
            </div>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  )
}
