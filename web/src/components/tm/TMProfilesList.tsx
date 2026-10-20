import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { listTMProfiles, createTMProfile, deleteTMProfile } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import ConfirmDialog from '../ConfirmDialog'
import { TMProfileNameDialog } from './TMProfileNameDialog'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { DeleteButton } from '@/components/ui/delete-button'
import { DotSeparator } from '@/components/ui/dot-separator'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { listRowClass } from '@/components/list/list-row'
import { cn } from '@/lib/utils'

/**
 * The "TM profiles" tab of the Translation memories page: the reusable bundles
 * of TMs + match scoring that projects and templates follow. Rows link to the
 * profile's page. The page header owns the "New TM profile" button and passes the
 * dialog's open state down; create prompts for a name and opens the new
 * profile so its TMs and scoring can be filled in there.
 */
export function TMProfilesList({ canDelete, createOpen, onCreateClose }: {
  canDelete: boolean
  createOpen: boolean
  onCreateClose: () => void
}) {
  const navigate = useNavigate()
  const [deleteTarget, setDeleteTarget] = useState<{ id: number; name: string } | null>(null)

  const { data: profiles, isLoading, isError, error, refetch } = useQuery({ queryKey: queryKeys.tmProfiles.all, queryFn: listTMProfiles })

  const deleteMut = useMutationWithToast({
    mutationFn: deleteTMProfile,
    successMessage: 'TM profile deleted',
    // The 409 message names how many projects/templates still follow it.
    errorMessage: 'Could not delete the TM profile',
    invalidateKeys: [queryKeys.tmProfiles.all],
  })
  const createMut = useMutationWithToast({
    mutationFn: (vars: { name: string; description: string }) => createTMProfile(vars),
    successMessage: 'TM profile created',
    errorMessage: 'Could not create the TM profile',
    invalidateKeys: [queryKeys.tmProfiles.all],
  })

  return (
    <div data-testid="tm-profiles-section" data-guide="tm_profiles.list">
      <p className="mb-4 text-sm text-muted-foreground">
        A profile bundles translation memories — with their read/write mode, priority and penalty —
        together with the fuzzy threshold and match penalties. Projects and templates follow a
        profile live: edit it once and every project using it changes.
      </p>
      {isLoading ? (
        <LoadingPlaceholder />
      ) : isError && !profiles ? (
        <LoadFailed what="TM profiles" error={error} onRetry={() => refetch()} />
      ) : (
        <div className="space-y-2">
          {profiles?.map(p => (
            <Link
              key={p.id}
              to={`/memories/profiles/${p.id}`}
              className={cn(listRowClass, 'hover:border-primary transition-colors')}
              data-testid={`tm-profile-row-${p.id}`}
            >
              <div className="flex max-md:flex-wrap items-center gap-2 gap-y-1 min-w-0 flex-1">
                <span className="font-medium text-gray-900 dark:text-gray-100 truncate">{p.name}</span>
                <span className="flex items-center text-xs text-gray-500 dark:text-gray-400 shrink-0">
                  <span>{p.tm_count} {p.tm_count === 1 ? 'TM' : 'TMs'}</span>
                  <DotSeparator />
                  <span>{Math.round(p.fuzzy_threshold * 100)}% threshold</span>
                  <DotSeparator />
                  <span data-testid={`tm-profile-usage-${p.id}`}>
                    {p.usage_count === 0 ? 'not in use' : `used by ${p.usage_count}`}
                  </span>
                </span>
                {p.description && (
                  <span className="text-xs text-gray-400 dark:text-gray-500 truncate">{p.description}</span>
                )}
              </div>
              {canDelete && (
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <DeleteButton
                        aria-label="Delete"
                        onClick={e => { e.preventDefault(); e.stopPropagation(); setDeleteTarget({ id: p.id, name: p.name }) }}
                        data-testid={`tm-profile-delete-${p.id}`}
                      />
                    }
                  />
                  <TooltipContent>Delete profile</TooltipContent>
                </Tooltip>
              )}
            </Link>
          ))}
          {profiles?.length === 0 && (
            <EmptyState message="No TM profiles yet. Create one to reuse a set of translation memories across projects." />
          )}
        </div>
      )}

      <TMProfileNameDialog
        open={createOpen}
        onClose={onCreateClose}
        title="New TM profile"
        submitLabel="Create"
        hint="Add translation memories and match scoring on the profile's page next."
        onSubmit={async (name, description) => {
          const created = await createMut.mutateAsync({ name, description })
          navigate(`/memories/profiles/${created.id}`)
        }}
      />
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={open => !open && setDeleteTarget(null)}
        title={`Delete TM profile "${deleteTarget?.name}"?`}
        description="Projects and templates that follow it must be detached first; the delete is refused while any still do."
        onConfirm={() => { if (deleteTarget) deleteMut.mutate(deleteTarget.id); setDeleteTarget(null) }}
      />
    </div>
  )
}
