import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { createSegmentationProfile, listLanguages, type SRXImport } from '@/api'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { queryKeys } from '@/lib/query-keys'
import { modeLabel } from '@/lib/segmentation'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { FormField } from '@/components/ui/form-field'
import { Input } from '@/components/ui/input'
import { SourceLanguagesPicker } from './SourceLanguagesPicker'

interface DraftRow {
  keep: boolean
  name: string
  /** Empty: the profile suits any language. */
  languages: string[]
  saved: boolean
}

/**
 * The drafts an SRX file holds, with every rule the import had to leave out.
 * The user names each draft, picks its source language and saves the ones
 * they keep; nothing is stored before Save. Render it with a key per file.
 */
export function SRXImportDialog({ result, fileName, onClose }: {
  result: SRXImport
  fileName: string
  onClose: () => void
}) {
  const queryClient = useQueryClient()
  const { data: languages = [] } = useQuery({ queryKey: queryKeys.languages.all, queryFn: listLanguages })
  const [rows, setRows] = useState<DraftRow[]>(() =>
    result.drafts.map(d => ({ keep: true, name: d.name, languages: d.source_languages, saved: false })))
  const update = (i: number, patch: Partial<DraftRow>) =>
    setRows(prev => prev.map((r, j) => (j === i ? { ...r, ...patch } : r)))

  const pending = rows.filter(r => r.keep && !r.saved)
  const missing = pending.find(r => !r.name.trim())

  // Saves the kept drafts one by one and stops at the first refusal; the
  // drafts saved before it show as saved, so Save retries only the rest.
  const saveMut = useMutationWithToast({
    mutationFn: async () => {
      let saved = 0
      for (const [i, row] of rows.entries()) {
        if (!row.keep || row.saved) continue
        await createSegmentationProfile({ name: row.name.trim(), source_languages: row.languages, definition: result.drafts[i].rule_set })
        update(i, { saved: true })
        saved++
      }
      return saved
    },
    successMessage: saved => (saved === 1 ? 'Segmentation profile imported' : `${saved} segmentation profiles imported`),
    errorMessage: 'Could not save a segmentation profile',
    invalidateKeys: [queryKeys.segmentationProfiles.lists],
    onSuccess: onClose,
    onError: () => { void queryClient.invalidateQueries({ queryKey: queryKeys.segmentationProfiles.lists }) },
  })
  const saving = saveMut.isPending

  return (
    <Dialog open onOpenChange={open => !open && onClose()} keepOpen={saving}>
      <DialogContent className="sm:max-w-2xl" data-testid="srx-import-dialog">
        <DialogHeader>
          <DialogTitle>Import SRX</DialogTitle>
          <DialogDescription>
            {fileName} holds {result.drafts.length === 1 ? 'one segmentation profile' : `${result.drafts.length} segmentation profiles`}.
            Name each one you keep and pick its source languages.
          </DialogDescription>
        </DialogHeader>
        <div className="flex max-h-[60vh] flex-col gap-4 overflow-y-auto">
          {result.warnings.length > 0 && (
            <Alert tone="warning" title="Some settings of the file are not used">
              <ul className="list-disc pl-4">{result.warnings.map(w => <li key={w}>{w}</li>)}</ul>
            </Alert>
          )}
          {result.drafts.map((d, i) => {
            const row = rows[i]
            if (!row) return null
            return (
              <section key={i} className="rounded-lg border border-border bg-muted/40 p-3" data-testid={`srx-draft-${i}`}>
                <CheckboxField checked={row.keep} disabled={row.saved || saving} onChange={keep => update(i, { keep })}
                  label={row.saved ? `${d.name} (saved)` : `Import "${d.name}"`}
                  description={`${d.rule_set.rules.length} rules, ${d.rule_set.abbreviations.length} abbreviations, ${modeLabel(d.rule_set.mode)} mode`}
                  data-testid={`srx-draft-keep-${i}`} />
                {row.keep && !row.saved && (
                  <div className="mt-3 grid gap-3 sm:grid-cols-2">
                    <FormField label="Name" required error={row.name.trim() ? undefined : 'Enter a name.'}>
                      <Input value={row.name} onChange={e => update(i, { name: e.target.value })} disabled={saving}
                        data-testid={`srx-draft-name-${i}`} />
                    </FormField>
                    <FormField label="Source languages">
                      <SourceLanguagesPicker value={row.languages} onChange={languages => update(i, { languages })}
                        languages={languages} disabled={saving} data-testid={`srx-draft-languages-${i}`} />
                    </FormField>
                  </div>
                )}
                {d.warnings.length > 0 && (
                  <Alert tone="warning" className="mt-3" title={d.warnings.length === 1 ? 'One rule was left out' : 'Some rules were left out'}>
                    <ul className="list-disc pl-4" data-testid={`srx-draft-warnings-${i}`}>{d.warnings.map(w => <li key={w}>{w}</li>)}</ul>
                  </Alert>
                )}
              </section>
            )
          })}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={saving}>Cancel</Button>
          <Button onClick={() => saveMut.mutate()} disabled={saving || pending.length === 0 || !!missing}
            disabledReason={pending.length === 0 ? 'Select a profile to import.' : missing ? 'Every profile needs a name.' : undefined}
            data-testid="srx-import-save">
            {saving ? 'Saving…' : pending.length === 1 ? 'Save profile' : `Save ${pending.length} profiles`}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
