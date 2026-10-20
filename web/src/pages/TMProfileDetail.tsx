import { useParams } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { errorText } from '@/lib/error-text'
import { ApiError, getTMProfile, updateTMProfile, setTMProfileTMs, listTMCollections } from '../api'
import { queryKeys } from '../lib/query-keys'
import { EditableTitle } from '@/components/list/EditableTitle'
import { BlurCommitInput } from '@/components/ui/blur-commit-input'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { PageContainer } from '@/components/layout/PageContainer'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { TooltipProvider } from '@/components/ui/tooltip'
import { TMScoringFields, describeScoringSave, SCORING_SAVED_TOAST_ID } from '../components/settings/TMScoringFields'
import { TMLookupOrderField } from '../components/settings/TMLookupOrderField'
import { toTMLookupOrder } from '../lib/settings-values'
import { TMAssignmentSection } from '../components/project/TMAssignmentSection'
import { useInlineCreateResources } from '../components/settings/useInlineCreateResources'
import { usePageTitle } from '../hooks/usePageTitle'

/**
 * One TM profile: name, description, match scoring and the TM rows — the same
 * shape as the Translation memory block in project settings, minus the profile
 * picker. Every save here reaches every project that follows the profile, so
 * the settings and editor caches are invalidated along with it.
 */
export default function TMProfileDetail() {
  const { profileId } = useParams<{ profileId: string }>()
  const id = Number(profileId)
  const queryClient = useQueryClient()

  const { data: profile, isLoading, error, refetch } = useQuery({
    queryKey: queryKeys.tmProfile.detail(id),
    queryFn: () => getTMProfile(id),
  })
  const { data: tms } = useQuery({ queryKey: queryKeys.tms.all, queryFn: listTMCollections })
  usePageTitle(profile?.name)
  // "+ Create new TM…" in the add picker, same dialog flow as project settings.
  const { handleCreateNewTM, dialogs: inlineCreateDialogs } = useInlineCreateResources({})

  // A profile edit is live for every linked project: their settings pages
  // and the editor's Matches panel must refetch.
  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: queryKeys.tmProfile.detail(id) })
    queryClient.invalidateQueries({ queryKey: queryKeys.tmProfiles.all })
    queryClient.invalidateQueries({ queryKey: queryKeys.project.allSettings })
    queryClient.invalidateQueries({ queryKey: queryKeys.template.all })
    queryClient.invalidateQueries({ queryKey: queryKeys.tmMatches.all })
  }

  // Resolves to whether the save went through, for fields that undo a failed one.
  const patch = async (data: Parameters<typeof updateTMProfile>[1], okMessage: string, toastId?: string) => {
    try {
      await updateTMProfile(id, data)
      refresh()
      toast.success(okMessage, toastId ? { id: toastId } : undefined)
      return true
    } catch (err) {
      toast.error(`Failed: ${errorText(err, String(err))}`)
      return false
    }
  }

  if (isLoading) return <PageContainer><LoadingPlaceholder /></PageContainer>
  if (!profile) {
    return (
      <PageContainer>
        {error && !(error instanceof ApiError && error.status === 404)
          ? <LoadFailed what="TM profile" error={error} onRetry={() => refetch()} />
          : <EmptyState message="TM profile not found." />}
      </PageContainer>
    )
  }

  return (
    <TooltipProvider delay={100}>
      <PageContainer>
        <Breadcrumbs items={[
          { label: 'TM profiles', to: '/memories/profiles' },
          { label: <EditableTitle name={profile.name} onRename={name => patch({ name }, 'TM profile renamed')} data-testid="rename-tm-profile" /> },
        ]} />

        <div className="mb-4 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-gray-500 dark:text-gray-400">
          <BlurCommitInput
            value={profile.description}
            onCommit={description => patch({ description }, 'Description saved')}
            variant="seamless"
            placeholder="Add a description"
            aria-label="Description"
            className="min-w-[16rem] flex-1 border-b border-transparent hover:border-gray-300 focus-within:border-primary text-sm text-gray-700 dark:text-gray-300"
            data-testid="tm-profile-description-field"
          />
          <span data-testid="tm-profile-usage" data-guide="tm_profile.usage">
            {profile.usage_count === 0
              ? 'Not used by any project or template yet.'
              : `Followed by ${profile.usage_count} project${profile.usage_count === 1 ? '' : 's'} or template${profile.usage_count === 1 ? '' : 's'} — edits apply to all of them.`}
          </span>
        </div>

        <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-4 space-y-4" data-guide="tm_profile.settings">
          <TMScoringFields
            values={{
              fuzzyThreshold: profile.fuzzy_threshold,
              sublanguagePenaltyPct: profile.sublanguage_penalty_pct,
              nearExactPenaltyPct: profile.near_exact_penalty_pct,
            }}
            isCreate={false}
            onCommit={u => patch(u, describeScoringSave(u) ?? 'Saved', SCORING_SAVED_TOAST_ID)}
          />
          <TMAssignmentSection
            variant="embedded"
            items={profile.tm_assignments.map(a => ({
              id: a.id, tmId: a.tm_id, mode: a.mode, priority: a.priority, penaltyPct: a.penalty_pct ?? 0,
            }))}
            tmCollections={tms?.map(t => ({ id: t.id, name: t.name, source_language: t.source_language, target_languages: t.target_languages })) ?? []}
            onSave={async items => {
              await setTMProfileTMs(id, items.map(i => ({
                tm_id: i.tmId, mode: i.mode, priority: i.priority, penalty_pct: i.penaltyPct ?? 0,
              })))
              refresh()
            }}
            onCreateNewResource={handleCreateNewTM}
            createNewLabel="Create new TM..."
            footer={
              <TMLookupOrderField
                value={toTMLookupOrder(profile.tm_lookup_order)}
                tmCount={profile.tm_assignments.length}
                onCommit={u => patch(u, describeScoringSave(u) ?? 'Saved', SCORING_SAVED_TOAST_ID)}
              />
            }
          />
        </div>
        {inlineCreateDialogs}
      </PageContainer>
    </TooltipProvider>
  )
}
