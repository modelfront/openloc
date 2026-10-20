import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { QueryBoundary } from '@/components/layout/QueryBoundary'
import { SettingsCard } from '@/components/layout/SettingsCard'
import { useMutationWithToast } from '@/hooks/use-mutation-with-toast'
import { queryKeys } from '@/lib/query-keys'
import {
  getAnalyticsWeights,
  updateAnalyticsWeights,
  type WeightEntry,
} from '../../api'

const WEIGHT_BANDS = [
  { band: 'tm_exact', label: '100% (exact)', description: 'Exact TM matches — no effort needed' },
  { band: 'tm_fuzzy_95', label: '95–99%', description: 'Near-perfect fuzzy matches' },
  { band: 'tm_fuzzy_85', label: '85–94%', description: 'Good fuzzy matches' },
  { band: 'tm_fuzzy_75', label: '75–84%', description: 'Moderate fuzzy matches' },
  { band: 'tm_fuzzy_50', label: '50–74%', description: 'Low fuzzy matches' },
  { band: 'new', label: 'New (no match)', description: 'No TM match — full translation effort' },
  { band: 'repetitions', label: 'Repetitions', description: 'Repeated segments (2nd+ occurrence)' },
  { band: 'qe', label: 'QE approved', description: 'Auto-approved by quality estimation' },
]

function AnalysisWeightsSection() {
  const queryClient = useQueryClient()
  const weightsQuery = useQuery({
    queryKey: queryKeys.orgSettings.analysisWeights,
    queryFn: getAnalyticsWeights,
  })
  // Unsaved edits; null shows the server's weights.
  const [draft, setDraft] = useState<WeightEntry[] | null>(null)

  const save = useMutationWithToast({
    mutationFn: updateAnalyticsWeights,
    successMessage: 'Analysis weights saved',
    errorMessage: 'Failed to save analysis weights',
    invalidateKeys: [queryKeys.orgSettings.analysisWeights, queryKeys.projectAnalysis.all],
    onSuccess: (saved) => {
      queryClient.setQueryData(queryKeys.orgSettings.analysisWeights, saved)
      setDraft(null)
    },
  })

  const updateWeight = (weights: WeightEntry[], band: string, pct: number) =>
    setDraft(weights.map(w => w.band === band ? { ...w, weight_pct: pct } : w))

  return (
    <SettingsCard
      title="Analysis weights"
      className="mt-6"
      description={<>
        Configure how match bands are weighted into weighted words. Weight 0% means no effort (free), 100% means full effort.
        Industry defaults: exact matches = 0%, new words = 100%.
      </>}
    >
      <QueryBoundary query={weightsQuery} what="analysis weights">{saved => {
        const weights = draft ?? saved
        return (<>
          <div className="space-y-3">
            {WEIGHT_BANDS.map(({ band, label, description }) => {
              const entry = weights.find(w => w.band === band)
              const pct = entry?.weight_pct ?? 100
              return (
                <div key={band} className="flex items-center gap-4">
                  <div className="flex-1 min-w-0">
                    <span className="text-sm font-medium text-gray-900 dark:text-gray-100">{label}</span>
                    <p className="text-xs text-gray-500 dark:text-gray-400 truncate">{description}</p>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    <Input
                      type="number"
                      min={0}
                      max={100}
                      value={pct}
                      onChange={e => updateWeight(weights, band, Math.max(0, Math.min(100, Number(e.target.value))))}
                      className="w-20 text-right tabular-nums"
                    />
                    <span className="text-sm text-gray-500 dark:text-gray-400 w-4">%</span>
                  </div>
                </div>
              )
            })}
          </div>
          <div className="mt-4 flex justify-end">
            <Button onClick={() => save.mutate(weights)} disabled={save.isPending} size="sm">
              {save.isPending ? 'Saving…' : 'Save weights'}
            </Button>
          </div>
        </>)
      }}</QueryBoundary>
    </SettingsCard>
  )
}

export default AnalysisWeightsSection
