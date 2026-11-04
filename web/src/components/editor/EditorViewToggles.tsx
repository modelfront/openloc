import { useState } from 'react'
import { FileDiff } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { readStorage, writeStorage } from '@/lib/safe-storage'
import { KeyboardShortcutsButton } from './KeyboardShortcutsButton'

/** Flips a stored on/off view choice. */
function toggleStored(key: string) {
  return (v: boolean) => {
    const next = !v
    writeStorage(key, String(next))
    return next
  }
}

/** The editor's per-user view choices: hidden characters and track changes. */
export function useEditorViewOptions() {
  const [showWhitespace, setShowWhitespace] = useState(() => readStorage('cat-show-whitespace') === 'true')
  const [trackChanges, setTrackChanges] = useState(() => readStorage('cat-track-changes') === 'true')
  return {
    showWhitespace,
    toggleWhitespace: () => setShowWhitespace(toggleStored('cat-show-whitespace')),
    trackChanges,
    toggleTrackChanges: () => setTrackChanges(toggleStored('cat-track-changes')),
  }
}

export type EditorViewOptions = ReturnType<typeof useEditorViewOptions>

interface EditorViewTogglesProps {
  view: EditorViewOptions
  /** Track changes has a finished step to compare with. */
  hasPreviousStep: boolean
  /** Track changes is on and has a step to compare with. */
  showTrackChanges: boolean
  previousStepName: string | undefined
}

/** The filter bar's view buttons: track changes, hidden characters, shortcuts. */
export function EditorViewToggles({ view, hasPreviousStep, showTrackChanges, previousStepName }: EditorViewTogglesProps) {
  return (
    <>
      {hasPreviousStep ? (
        <Tooltip>
          <TooltipTrigger render={
            <Button
              variant="outline"
              size="sm"
              className="h-7 text-xs"
              aria-label="Track changes"
              aria-pressed={showTrackChanges}
              onClick={view.toggleTrackChanges}
            />
          }>
            <FileDiff className="size-3.5" />
          </TooltipTrigger>
          <TooltipContent>{`Track changes: mark what changed since ${previousStepName ?? 'the previous step'}`}</TooltipContent>
        </Tooltip>
      ) : (
        <Button
          variant="outline"
          size="sm"
          className="h-7 text-xs"
          aria-label="Track changes"
          disabled
          disabledReason="Track changes: available once a workflow step is finished"
        >
          <FileDiff className="size-3.5" />
        </Button>
      )}
      <Tooltip>
        <TooltipTrigger render={
          <Button
            variant="outline"
            aria-pressed={view.showWhitespace}
            size="sm"
            className="h-7 text-xs font-mono"
            onClick={view.toggleWhitespace}
          />
        }>
          ¶
        </TooltipTrigger>
        <TooltipContent>Toggle hidden characters and full tag names (spaces, tabs, newlines, &lt;sub&gt;)</TooltipContent>
      </Tooltip>
      <KeyboardShortcutsButton />
    </>
  )
}
