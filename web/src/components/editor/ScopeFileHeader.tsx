import type { ScopeFile } from '@/api'
import { EditableTitle } from '@/components/list/EditableTitle'
import { tableHeaderRowClass } from '@/components/list/list-row'
import { Chip } from '@/components/ui/chip'
import type { UnapprovedCounts } from '@/lib/step-completion'
import { cn } from '@/lib/utils'

interface Props {
  file: ScopeFile
  unapproved: UnapprovedCounts
}

/**
 * The band above a file's first row in the multi-file editor: the file's
 * name, its step, how much of it is approved, and why it is read-only when it
 * is. Segment numbers restart under it, as in the file's own editor.
 */
export function ScopeFileHeader({ file, unapproved }: Props) {
  const left = unapproved.drafts + unapproved.untranslated
  const approved = file.row_count - left
  return (
    <div
      data-testid={`scope-file-header-${file.job_id}`}
      className={cn(tableHeaderRowClass, 'flex min-w-0 items-center gap-2 px-3 py-1.5 normal-case tracking-normal')}
    >
      <EditableTitle name={file.file_name} canEdit={false} className="font-semibold text-gray-700 dark:text-gray-200" />
      {file.step_name && <Chip size="small">{file.step_name}</Chip>}
      {file.edit_block && (
        <Chip size="small" color="yellow" title={file.edit_block.message} data-testid={`scope-file-readonly-${file.job_id}`}>
          Read only
        </Chip>
      )}
      <span className="ml-auto shrink-0 tabular-nums">
        {approved}/{file.row_count} approved
      </span>
    </div>
  )
}
