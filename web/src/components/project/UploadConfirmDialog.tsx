// Shown when uploading file(s) to a project: confirm what is about to be
// uploaded and whether to analyze it, and, for each Excel workbook without
// language codes in row 1, what to translate in it. Open while `files` is set.
// Confirm → upload; Cancel → discard the queued files.

import { useState } from 'react'
import { useOnChange } from '@/hooks/useOnChange'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Alert } from '@/components/ui/alert'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { useLastNonNull } from '@/hooks/useLastNonNull'
import type { LayoutAnswer } from '@/hooks/useUploadQueue'
import { useXLSXUploadChoices } from '@/hooks/useXLSXUploadChoices'
import { XLSXColumnsFields } from '@/components/project/files/XLSXColumnsFields'
import { EditableTitle } from '@/components/list/EditableTitle'
import { XLSXColumnsInfo } from '@/components/project/files/XLSXLayoutDialog'

interface Props {
  projectId: number
  /** The queued files; null closes the dialog. */
  files: File[] | null
  onOpenChange: (open: boolean) => void
  /** Called with whether the upload should be auto-analyzed, and the Excel answers. */
  onConfirm: (analyze: boolean, layouts: ReadonlyMap<File, LayoutAnswer>) => void
}

export function UploadConfirmDialog({ projectId, files: queued, onOpenChange, onConfirm }: Props) {
  const open = queued !== null
  // Rendered through the close animation, after the parent clears the queue.
  const files = useLastNonNull(queued) ?? []
  // Auto-analyze this upload. On by default; produces one coalesced
  // project analysis once pretranslate settles.
  const [analyze, setAnalyze] = useState(true)

  useOnChange([open], () => { if (open) setAnalyze(true) })
  const xlsx = useXLSXUploadChoices(projectId, files, open)
  const asking = xlsx.entries.filter(e => e.pending || e.failed || e.choices)

  const confirm = () => {
    onConfirm(analyze, xlsx.answers())
    onOpenChange(false)
  }

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent
        className={asking.length > 0 ? 'data-[size=default]:sm:max-w-lg' : undefined}
        // The Excel block starts with an info icon; focused on open, its
        // tooltip would show by itself.
        initialFocus={asking.length > 0 ? false : undefined}
      >
        <AlertDialogHeader>
          <AlertDialogTitle>
            Upload {files.length} file{files.length === 1 ? '' : 's'}
          </AlertDialogTitle>
          <AlertDialogDescription>
            Each file becomes one job per target language.
          </AlertDialogDescription>
        </AlertDialogHeader>

        {files.length > 0 && (
          <p className="text-xs text-gray-500 dark:text-gray-400 truncate" title={files.map(f => f.name).join(', ')}>
            {files.map(f => f.name).join(', ')}
          </p>
        )}

        {asking.length > 0 && (
          <div className="max-h-[50vh] space-y-3 overflow-y-auto" data-testid="upload-xlsx-columns">
            {asking.map(e => (
              <section key={e.key} className="space-y-1.5">
                <h3 className="flex items-center gap-1.5 text-sm font-medium">
                  <EditableTitle name={e.file.name} canEdit={false} />
                  <XLSXColumnsInfo />
                </h3>
                {e.choices && e.inspection ? (
                  <XLSXColumnsFields
                    inspection={e.inspection}
                    choices={e.choices}
                    onChange={e.setChoices}
                    testId={`upload-xlsx-${e.file.name}`}
                  />
                ) : e.failed ? (
                  <LoadFailed what="workbook" error={e.error} onRetry={e.retry} />
                ) : (
                  <p className="text-xs text-muted-foreground">Reading the workbook…</p>
                )}
              </section>
            ))}
          </div>
        )}
        {xlsx.problem && <Alert tone="error" data-testid="upload-xlsx-problem">{xlsx.problem}</Alert>}

        <CheckboxField
          checked={analyze}
          onChange={setAnalyze}
          label="Auto-analyze project files"
          description="Runs one TM/MT leverage analysis of the whole project."
          className="border-t border-gray-200 pt-3 dark:border-gray-800"
          data-testid="upload-analyze-checkbox"
        />

        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            onClick={confirm}
            disabled={xlsx.hold !== null}
            disabledReason={xlsx.hold ?? undefined}
            data-testid="upload-confirm"
          >
            Upload
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
