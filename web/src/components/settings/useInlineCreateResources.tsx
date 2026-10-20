import { useCallback, useState, type ReactNode } from 'react'
import { getTermBase } from '../../api'
import { useDialogPromise } from '@/hooks/useDialogPromise'
import { CreateTMDialog } from '../CreateTMDialog'
import { CreateTermBaseDialog } from '../CreateTermBaseDialog'
import type { CreateResourceContext, CreateResourceResult } from '../AssignmentSection'

/**
 * "+ Create new…" for the TM and term-base pickers inside the settings editor.
 *
 * The pickers call `onCreateNewResource` and await a promise; this hook opens
 * the matching dialog and resolves that promise when the dialog fires
 * onCreated (or onClose → null). A second call while a dialog is already open
 * (double-click, two pickers) resolves null at once; the first flow keeps
 * running.
 */
export function useInlineCreateResources({ sourceLanguage, targetLanguages }: {
  sourceLanguage?: string
  targetLanguages?: string[]
}): {
  handleCreateNewTM: (ctx: CreateResourceContext) => Promise<CreateResourceResult | null>
  handleCreateNewTermBase: (ctx: CreateResourceContext) => Promise<CreateResourceResult | null>
  dialogs: ReactNode
} {
  const tmDialog = useDialogPromise<CreateResourceResult>()
  const tbDialog = useDialogPromise<CreateResourceResult>()
  const [tbDialogCtx, setTbDialogCtx] = useState<CreateResourceContext>({ sourceLang: '', targetLang: '' })
  const { request: requestTM } = tmDialog
  const { request: requestTB } = tbDialog

  const handleCreateNewTM = useCallback(() => requestTM(), [requestTM])

  const handleCreateNewTermBase = useCallback((ctx: CreateResourceContext) => requestTB(() => {
    // Preset from the project (source + first target) so the dialog opens
    // ready to fill, mirroring the TM create flow.
    setTbDialogCtx({
      sourceLang: ctx.sourceLang || sourceLanguage || '',
      targetLang: ctx.targetLang || targetLanguages?.[0] || '',
    })
  }), [requestTB, sourceLanguage, targetLanguages])

  const dialogs = (
    <>
      <CreateTMDialog
        open={tmDialog.open}
        initialSourceLanguage={sourceLanguage}
        initialTargetLanguages={targetLanguages}
        onClose={() => tmDialog.settle(null)}
        onCreated={(id) => tmDialog.settle({ id })}
      />
      <CreateTermBaseDialog
        open={tbDialog.open}
        initialSrcLang={tbDialogCtx.sourceLang}
        initialTgtLang={tbDialogCtx.targetLang}
        onClose={() => tbDialog.settle(null)}
        onCreated={(id) => tbDialog.settle(createdTermBase(id))}
      />
    </>
  )

  return { handleCreateNewTM, handleCreateNewTermBase, dialogs }
}

/**
 * The new term base with its baked-in languages, so the picker can pre-fill
 * the Add-row language fields; just the id when that fetch fails.
 */
async function createdTermBase(id: number): Promise<CreateResourceResult> {
  try {
    const tb = await getTermBase(id)
    return {
      id,
      sourceLang: tb.term_base.source_language,
      targetLang: tb.term_base.target_languages?.[0] ?? '',
    }
  } catch {
    return { id }
  }
}
