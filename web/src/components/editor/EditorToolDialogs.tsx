import type { ComponentProps } from 'react'
import ConcordanceModal from '@/components/ConcordanceModal'
import { QuickAddTermDialog } from '@/components/QuickAddTermDialog'
import SpellCheckDialog from '@/components/SpellCheckDialog'
import type { ActiveSegment } from './useActiveSegment'
import type { EditorDialogs } from './useEditorDialogs'

type SpellCheckProps = ComponentProps<typeof SpellCheckDialog>

interface Props {
  dialogs: EditorDialogs
  active: Pick<ActiveSegment, 'activeSegmentIdx' | 'activeInsertRef' | 'switchActiveSegment'>
  projectId: number | null
  sourceLanguage: string
  targetLanguage: string
  isManager: boolean
  /** The open row's job, for its term bases; absent until the editor has loaded. */
  termJobId: number | undefined
  /** The open row's file takes inserts as plain text. */
  plainText: boolean
  /** The open row may not be written: its task, its lock, or someone else's hold. */
  activeRowReadOnly: boolean
  /** The spell check's rows, its server check and whether it may write. */
  spellCheck: Pick<SpellCheckProps, 'check' | 'readOnly' | 'spellFixActions' | 'segmentIds' | 'filteredIndices'
    | 'translationBySegment' | 'filterActive' | 'selectedIds' | 'onApply' | 'onApplyAll'>
  /** Scrolls a row into view behind the spell check without opening it. */
  onNavigate: (translationId: number) => void
}

/**
 * The tool dialogs of both editors, one file or several: Spell check,
 * Concordance and term quick-add.
 */
export function EditorToolDialogs({
  dialogs, active, projectId, sourceLanguage, targetLanguage, isManager, termJobId, plainText,
  activeRowReadOnly, spellCheck, onNavigate,
}: Props) {
  return (
    <>
      <SpellCheckDialog
        open={dialogs.spellCheckOpen}
        onClose={() => dialogs.setSpellCheckOpen(false)}
        {...spellCheck}
        initialScope={dialogs.spellCheckScope}
        // Flushes the open segment's draft and closes it, so the batch writes
        // can't race an editor autosave.
        beforeRun={() => active.switchActiveSegment(null)}
        onNavigate={onNavigate}
      />
      <ConcordanceModal
        open={dialogs.concordanceOpen}
        onClose={() => dialogs.setConcordanceOpen(false)}
        projectId={projectId}
        sourceLanguage={sourceLanguage}
        targetLanguage={targetLanguage}
        readOnly={activeRowReadOnly}
        plainText={plainText}
        onInsert={text => {
          if (active.activeSegmentIdx === null) return
          active.activeInsertRef.current.replaceAll?.(text)
        }}
        initialQuery={dialogs.concordanceQuery}
      />
      {termJobId !== undefined && projectId !== null && (
        <QuickAddTermDialog
          open={dialogs.quickAddTerm !== null}
          onClose={() => {
            dialogs.setQuickAddTerm(null)
            active.activeInsertRef.current.clearSourceSelection?.()
          }}
          jobId={termJobId}
          projectId={projectId}
          isManager={isManager}
          initialSource={dialogs.quickAddTerm?.source ?? ''}
          initialTarget={dialogs.quickAddTerm?.target ?? ''}
        />
      )}
    </>
  )
}
