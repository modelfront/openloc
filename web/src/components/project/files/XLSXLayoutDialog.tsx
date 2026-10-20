// Excel columns as a dialog of its own, for the New project wizard: asked at
// upload, where no other upload dialog opens, and from the file's row while
// the project is a draft. The project page asks inside its upload dialog.
//
// Access: managers, like the upload and the project page.

import { useState } from 'react'

import type { XLSXInspection, XLSXLayout } from '@/api'
import type { LayoutAnswer, LayoutQuestion } from '@/hooks/useUploadQueue'
import { choicesToLayout, layoutProblem, sheetChoices, type SheetChoice } from '@/lib/xlsx-columns'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { SectionInfo } from '@/components/ui/section-info'
import { XLSXColumnsFields } from './XLSXColumnsFields'

/** How the Excel columns work, as an info tooltip; shared with the upload dialog. */
export function XLSXColumnsInfo() {
  return (
    <SectionInfo label="About Excel columns" size="sm" data-testid="xlsx-columns-info">
      Choose the columns to translate on each sheet; no column translates the whole sheet. Each translation is
      written back into its own cell.
    </SectionInfo>
  )
}

export interface XLSXLayoutDialogProps {
  open: boolean
  /** Closing without a choice. */
  onCancel: () => void
  fileName: string
  inspection: XLSXInspection
  /** The stored layout, or the suggestion for a new upload. */
  initial: XLSXLayout
  pending?: boolean
  submitLabel: string
  onSubmit: (layout: XLSXLayout) => void
}

export function XLSXLayoutDialog({ open, onCancel, pending = false, ...props }: XLSXLayoutDialogProps) {
  return (
    <Dialog open={open} onOpenChange={o => { if (!o) onCancel() }} keepOpen={pending}>
      {/* The title's info icon would be focused first and show its tooltip by itself. */}
      <DialogContent className="sm:max-w-lg" initialFocus={false} data-testid="xlsx-layout-dialog">
        {/* Mounted per opening, so the choices start from `initial` each time. */}
        {open && <LayoutForm {...props} pending={pending} onCancel={onCancel} />}
      </DialogContent>
    </Dialog>
  )
}

function LayoutForm({
  fileName, inspection, initial, pending, submitLabel, onSubmit, onCancel,
}: Omit<XLSXLayoutDialogProps, 'open'> & { pending: boolean }) {
  const [choices, setChoices] = useState<SheetChoice[]>(() => sheetChoices(inspection, initial))
  const problem = layoutProblem(choices)

  return (
    <>
      <DialogHeader>
        <DialogTitle className="flex items-center gap-1.5">
          Excel columns
          <XLSXColumnsInfo />
        </DialogTitle>
        <DialogDescription>{`${fileName} has no language codes in row 1.`}</DialogDescription>
      </DialogHeader>

      <div className="-mx-1 max-h-[60vh] overflow-y-auto px-1">
        <XLSXColumnsFields inspection={inspection} choices={choices} onChange={setChoices} />
      </div>

      {problem && <Alert tone="error" data-testid="xlsx-layout-problem">{problem}</Alert>}

      <DialogFooter>
        <Button variant="outline" onClick={onCancel} disabled={pending}>Cancel</Button>
        <Button
          onClick={() => onSubmit(choicesToLayout(choices))}
          disabled={pending || problem !== null}
          disabledReason={problem ?? undefined}
          data-testid="xlsx-layout-submit"
        >
          {submitLabel}
        </Button>
      </DialogFooter>
    </>
  )
}

/** The upload queue's question for an .xlsx without language codes in row 1. */
export function UploadLayoutQuestion({ question, onAnswer }: {
  question: LayoutQuestion | null
  onAnswer: (answer: LayoutAnswer) => void
}) {
  if (!question) return null
  return (
    <XLSXLayoutDialog
      open
      onCancel={() => onAnswer('skip')}
      fileName={question.file.name}
      inspection={question.inspection}
      initial={question.suggested}
      submitLabel="Upload"
      onSubmit={onAnswer}
    />
  )
}
