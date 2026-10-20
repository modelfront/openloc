// NewAnalysisModal — job picker + name input for creating a saved
// analysis. POSTs to /projects/{id}/analyses; the worker computes
// breakdowns asynchronously and the parent's polling picks up the
// transition.
//
// Gate errors (400 from the pretranslate-completion rule) are surfaced
// inline with the offending job names so the PM knows which file to Apply
// TM/MT on first.

import { errorText } from '@/lib/error-text'
import { useMemo, useState } from 'react'
import { useQuery, useMutation } from '@tanstack/react-query'
import { listJobs, type Job } from '../api/analytics'
import { createAnalysis } from '../api/analyses'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from './ui/dialog'
import { Button } from './ui/button'
import { Input } from './ui/input'
import { TextLink } from '@/components/ui/text-link'
import { LanguageCode } from '@/components/ui/language-code'
import { CheckboxField } from '@/components/ui/checkbox-field'

interface Props {
  open: boolean
  projectId: number
  onOpenChange: (open: boolean) => void
  onCreated: () => void
}

export default function NewAnalysisModal({ open, projectId, onOpenChange, onCreated }: Props) {
  const [name, setName] = useState('')
  // `null` = "default": all jobs selected. We derive the effective selection
  // from the jobs list rather than syncing it in an effect — a
  // set-state-in-effect loops while the modal is closed. An explicit
  // Set means the user has toggled.
  const [selectedOverride, setSelectedOverride] = useState<Set<number> | null>(null)
  const [error, setError] = useState<string | null>(null)

  const { data: jobs = [], isLoading } = useQuery({
    queryKey: ['project-jobs-list', projectId],
    queryFn: () => listJobs(projectId),
    enabled: open,
  })

  const allIds = useMemo(() => new Set(jobs.map(j => j.id)), [jobs])
  const selected = selectedOverride ?? allIds

  // Reset to the default selection whenever the modal opens or closes.
  // Adjusting state during render on a prop change is React's recommended
  // alternative to an effect here (https://react.dev/learn/you-might-not-need-an-effect#adjusting-some-state-when-a-prop-changes):
  // no cascading-render lint, no loop.
  const [prevOpen, setPrevOpen] = useState(open)
  if (open !== prevOpen) {
    setPrevOpen(open)
    setSelectedOverride(null)
  }

  // Smart default name derived from the current selection. Shown as the
  // placeholder so the user sees what will be saved if they don't type
  // anything; sent as the actual name on submit when the field is blank.
  // (User-typed names are never overwritten — see the input's onChange.)
  const defaultName = useMemo(() => computeDefaultName(selected, jobs), [selected, jobs])

  const mut = useMutation({
    mutationFn: () =>
      createAnalysis(projectId, {
        name: name.trim() || defaultName || undefined,
        job_ids: Array.from(selected),
      }),
    onSuccess: () => {
      setName('')
      setSelectedOverride(null)
      setError(null)
      onCreated()
    },
    onError: (err: unknown) => {
      const msg = errorText(err, 'Failed to create analysis')
      setError(msg)
    },
  })

  const toggle = (id: number) => {
    setSelectedOverride(prev => {
      const next = new Set(prev ?? allIds)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const selectAll = () => setSelectedOverride(new Set(allIds))
  const clearAll = () => setSelectedOverride(new Set())

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>New analysis</DialogTitle>
          <DialogDescription>
            Computes a snapshot of TM/MT leverage across the selected jobs. Numbers reflect what the
            CAT editor currently shows for each segment — jobs must have completed their enabled
            pretranslate phases first.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3">
          <div>
            <label className="block text-xs font-medium mb-1">Name (optional)</label>
            <Input
              value={name}
              onChange={e => setName(e.target.value)}
              placeholder={defaultName || 'e.g. Q3 quote for ACME'}
            />
            {defaultName && !name.trim() && (
              <p className="text-[11px] text-gray-500 mt-1 break-all">
                Saved as: <span className="font-mono">{defaultName}</span>
              </p>
            )}
          </div>

          <div>
            <div className="flex items-center justify-between mb-1">
              <label className="block text-xs font-medium">Jobs ({selected.size} selected)</label>
              <div className="text-xs space-x-2">
                <TextLink onClick={selectAll}>Select all</TextLink>
                <TextLink onClick={clearAll}>Clear</TextLink>
              </div>
            </div>
            {isLoading ? (
              <p className="text-xs text-gray-500">Loading…</p>
            ) : jobs.length === 0 ? (
              <p className="text-xs text-gray-500">No jobs in this project yet.</p>
            ) : (
              <div className="max-h-64 overflow-y-auto border border-gray-200 dark:border-gray-800 rounded-md">
                {jobs.map(j => (
                  <CheckboxField
                    key={j.id}
                    className="mx-0 rounded-none"
                    checked={selected.has(j.id)}
                    onChange={() => toggle(j.id)}
                    label={<span className="block truncate">{j.file_name ?? `Job ${j.id}`}</span>}
                    description={<>→ <LanguageCode code={j.target_language} /></>}
                  />
                ))}
              </div>
            )}
          </div>

          {error && (
            <p className="text-xs text-red-600 dark:text-red-400">{error}</p>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={mut.isPending}>
            Cancel
          </Button>
          <Button
            onClick={() => mut.mutate()}
            disabled={selected.size === 0 || mut.isPending}
          >
            {mut.isPending ? 'Creating…' : `Run analysis (${selected.size})`}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// computeDefaultName picks a sensible label from the current selection:
//   • zero jobs       → '' (user must type or pick)
//   • one job         → 'Analysis: filename.docx > de'
//   • N jobs, 1 file  → 'Analysis: filename.docx > de, fr, es' (all targets)
//   • N jobs, M files → 'Analysis: 12 jobs - 3 targets'
// Plain-ASCII separators (`>` for file → lang, `-` between counts) so the
// names are easy to grep / search / paste anywhere without smart-quote or
// unicode-arrow weirdness.
function computeDefaultName(selectedIds: Set<number>, jobs: Job[]): string {
  if (selectedIds.size === 0) return ''
  const picked = jobs.filter(j => selectedIds.has(j.id))
  if (picked.length === 0) return '' // jobs still loading

  if (picked.length === 1) {
    return `Analysis: ${picked[0].file_name} > ${picked[0].target_language}`
  }

  const firstFile = picked[0].file_name
  if (picked.every(j => j.file_name === firstFile)) {
    const langs = picked.map(j => j.target_language).join(', ')
    return `Analysis: ${firstFile} > ${langs}`
  }

  const uniqueTargets = new Set(picked.map(j => j.target_language)).size
  return `Analysis: ${picked.length} jobs - ${uniqueTargets} target${uniqueTargets > 1 ? 's' : ''}`
}
