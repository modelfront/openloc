// DownloadMenu — the one download affordance in the app.
//
// Mounted in three places, all with the same three choices:
//   - the bulk-action bar (acts on the checkbox selection)
//   - each file row on the Files tab (acts on that file's jobs — discoverable
//     without selecting anything first), as an icon like the row's other actions
//   - the CAT editor toolbar (acts on the open job)
//
// Eligibility: the deliverable
// needs at least one approved/auto-approved segment, the bilingual XLIFF only
// needs segments to exist (its whole point is handing untranslated rows to an
// external translator). The source file is always available.
//
// Transport is decided in api/download.ts: one item → the single-item GET
// (linguist-reachable), several → one ZIP from the manager-gated project
// endpoint. That's why the editor's menu works for vendors.

import type { ComponentProps } from 'react'
import { FileCheck2, FileText, Languages, type LucideIcon } from 'lucide-react'

import { downloadFiles, type DownloadKind, type Job } from '../../api'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import type { Button } from '../ui/button'
import { DownloadOptionsMenu } from '../ui/download-options-menu'

interface Props {
  projectId: number
  /** Jobs in scope — the selection, a file group's jobs, or the open job. */
  jobs: Job[]
  /** Files in scope for the source-file option (deduped internally). */
  fileIds: number[]
  size?: 'xs' | 'sm'
  variant?: 'outline' | 'default' | 'ghost'
  /** Text colour at rest and on hover; `muted` matches the icon actions
   *  beside it in a table row. Set both — a `color` alone loses its hover
   *  colour in dark mode. */
  color?: ComponentProps<typeof Button>['color']
  hoverColor?: ComponentProps<typeof Button>['hoverColor']
  /** Prefix for data-testids, so the three mount points stay addressable. */
  testidPrefix?: string
  /** An icon button with a tooltip instead of icon + label, for a table row's
   *  actions beside other icon buttons. */
  iconOnly?: boolean
}

/** A job whose deliverable can be rebuilt — at least one segment has a
 *  translation. Approval is no gate: it starts over at each workflow step,
 *  while the export writes every translated segment. */
const hasDeliverable = (j: Job) => j.counts.total - j.counts.untranslated > 0
/** A job with segments at all — enough for a bilingual hand-off. */
const hasSegments = (j: Job) => j.counts.total > 0

export function DownloadMenu({
  projectId,
  jobs,
  fileIds,
  size = 'xs',
  variant = 'outline',
  color,
  hoverColor,
  testidPrefix = 'download',
  iconOnly = false,
}: Props) {
  const deliverableJobs = jobs.filter(hasDeliverable)
  const bilingualJobs = jobs.filter(hasSegments)
  const uniqueFileIds = [...new Set(fileIds)]

  const download = useMutationWithToast<DownloadKind, DownloadKind>({
    mutationFn: async (kind) => {
      await downloadFiles(projectId, kind, {
        jobIds: (kind === 'xliff' ? bilingualJobs : deliverableJobs).map(j => j.id),
        fileIds: uniqueFileIds,
      })
      return kind
    },
    successMessage: (kind) => `${OPTIONS.find(o => o.kind === kind)!.label} downloaded`,
    errorMessage: 'Download failed',
  })

  const counts: Record<DownloadKind, number> = {
    source: deliverableJobs.length,
    xliff: bilingualJobs.length,
    original: uniqueFileIds.length,
  }

  return (
    <DownloadOptionsMenu
      options={OPTIONS.map(opt => {
        const n = counts[opt.kind]
        return {
          id: opt.kind,
          label: `${n > 1 ? opt.pluralLabel : opt.label}${n > 1 ? ` (${n})` : ''}`,
          hint: n === 0 ? opt.emptyHint : n > 1 ? `${opt.hint} · one .zip` : opt.hint,
          icon: opt.icon,
          disabled: n === 0,
          testId: `${testidPrefix}-${opt.kind}`,
        }
      })}
      onSelect={kind => download.mutate(kind)}
      busy={download.isPending}
      disabled={jobs.length + uniqueFileIds.length === 0}
      size={size}
      variant={variant}
      color={color}
      hoverColor={hoverColor}
      iconOnly={iconOnly}
      testId={`${testidPrefix}-btn`}
    />
  )
}

interface DownloadKindOption {
  kind: DownloadKind
  label: string
  pluralLabel: string
  hint: string
  /** Shown instead of `hint` when nothing in scope qualifies. */
  emptyHint: string
  // `hint` is deliberately number-neutral ("Exactly as uploaded", not "The
  // file exactly as uploaded") — it renders unchanged next to a count of 1
  // or 12, so no plural variant is needed.
  icon: LucideIcon
}

const OPTIONS: DownloadKindOption[] = [
  {
    kind: 'source',
    label: 'Translated file',
    pluralLabel: 'Translated files',
    hint: 'Deliverable, in the original format',
    emptyHint: 'No approved segments yet',
    icon: FileCheck2,
  },
  {
    kind: 'xliff',
    label: 'Bilingual XLIFF',
    pluralLabel: 'Bilingual XLIFFs',
    hint: 'For other CAT tools',
    emptyHint: 'No segments to export',
    icon: Languages,
  },
  {
    kind: 'original',
    label: 'Source file',
    pluralLabel: 'Source files',
    hint: 'Exactly as uploaded',
    emptyHint: 'No file selected',
    icon: FileText,
  },
]

export default DownloadMenu
