// FilterProfilesPage — the Filter profiles tab of File configurations: named
// import options for one file format (HTML, Excel, JSON, PO, plain text,
// XML). Projects and templates pick one per format; the org default of a
// format is what a new project or template without a template starts with.
// The built-ins are read-only; owners, admins and project managers copy one to
// make their own, edit it, delete their own and set the org defaults.
// Linguists never see this page.

import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { deleteFilterProfile, getMe, setFilterProfileDefault, type FilterProfile } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { isManagerRole } from '../../lib/roles'
import { sortFilterProfiles } from '../../lib/filter-options'
import { FILTER_FORMATS, FILTER_FORMAT_EXTENSIONS, FILTER_FORMAT_LABELS, FILTER_FORMAT_NOTES, type FilterFormat } from '../../lib/filter-formats'
import { SectionInfo } from '@/components/ui/section-info'
import { TextLink } from '@/components/ui/text-link'
import { profileLabel } from '@/lib/segmentation'
import { useFilterProfiles } from '../../hooks/useFilterProfiles'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import ConfirmDialog from '@/components/ConfirmDialog'
import { EmptyState } from '@/components/layout/EmptyState'
import { SlimCollapsibleSection } from '@/components/layout/SlimCollapsibleSection'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { CreateButton } from '@/components/ui/create-button'
import { DataTable } from '@/components/ui/data-table'
import { TooltipProvider } from '@/components/ui/tooltip'
import { FILTER_PROFILE_COLUMNS, type FilterProfileTableMeta } from '@/components/filter-profiles/profile-columns'
import { FileConfigurationsPageFrame } from '../file-configurations/FileConfigurationsPageFrame'
import { FILTER_PROFILES_PATH } from './filter-profile-paths'

export default function FilterProfilesPage() {
  const navigate = useNavigate()
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const canWrite = isManagerRole(me?.role)
  const { profiles, isLoading, error, refetch } = useFilterProfiles()
  const [deleteTarget, setDeleteTarget] = useState<FilterProfile | null>(null)

  const deleteMut = useMutationWithToast({
    mutationFn: (p: FilterProfile) => deleteFilterProfile(p.id),
    successMessage: 'Filter profile deleted',
    errorMessage: 'Failed to delete the filter profile',
    // Projects that used it go back to the format's Default.
    invalidateKeys: [queryKeys.filterProfiles.all, queryKeys.project.allSettings],
  })

  // Only new projects and templates read the default, so no project settings go stale.
  const defaultMut = useMutationWithToast({
    mutationFn: ({ p, on }: { p: FilterProfile; on: boolean }) =>
      // No id: back to the built-in Default.
      setFilterProfileDefault({ format: p.format, filter_profile_id: on ? p.id : undefined }),
    successMessage: 'Organization default saved',
    errorMessage: 'Failed to save the organization default',
    invalidateKeys: [queryKeys.filterProfiles.all],
  })

  const meta: FilterProfileTableMeta = {
    canWrite,
    busy: deleteMut.isPending || defaultMut.isPending,
    onCopy: p => navigate(`${FILTER_PROFILES_PATH}/new?from=${p.id}`),
    onDelete: p => setDeleteTarget(p),
    onOrgDefault: (p, on) => defaultMut.mutate({ p, on }),
  }
  // The formats in their order, each with its built-ins, then the org's own.
  const sections = useMemo(() => {
    const sorted = sortFilterProfiles(profiles)
    return FILTER_FORMATS
      .map(format => ({ format, rows: sorted.filter(p => p.format === format) }))
      .filter(s => s.rows.length > 0)
  }, [profiles])
  // Every format starts closed; a click, or Expand all / Collapse all, opens it.
  const [openOverride, setOpenOverride] = useState<Partial<Record<FilterFormat, boolean>>>({})
  const isOpen = (format: FilterFormat) => openOverride[format] ?? false
  const allOpen = sections.every(s => isOpen(s.format))
  const setAll = (open: boolean) => setOpenOverride(Object.fromEntries(sections.map(s => [s.format, open])))

  return (
    <FileConfigurationsPageFrame
      actions={canWrite && (
        <CreateButton onClick={() => navigate(`${FILTER_PROFILES_PATH}/new`)} data-testid="filter-profile-new">
          New filter profile
        </CreateButton>
      )}
    >
      <TooltipProvider delay={200}>
        {isLoading ? <LoadingPlaceholder /> : error ? (
          <LoadFailed what="filter profiles" error={error} onRetry={() => refetch()} />
        ) : sections.length === 0 ? (
          <EmptyState message="No filter profiles yet." />
        ) : (
          // One collapsible section per format; the closed heading still names
          // the format's files and its org default.
          <div className="text-sm" data-guide="filter_profiles.list">
            <div className="mb-2 flex justify-end">
              <TextLink onClick={() => setAll(!allOpen)} data-testid="filter-profiles-expand-all">
                {allOpen ? 'Collapse all' : 'Expand all'}
              </TextLink>
            </div>
            {/* Grouped sections draw their card edges from first/last child: their own parent. */}
            <div>
              {sections.map(({ format, rows }) => {
                const orgDefault = rows.find(p => p.org_default)
                return (
                  <SlimCollapsibleSection key={format} title={FILTER_FORMAT_LABELS[format]} size="compact" variant="grouped"
                    toggleLabel={rows.length === 1 ? '1 profile' : `${rows.length} profiles`}
                    open={isOpen(format)}
                    onOpenChange={open => setOpenOverride(o => ({ ...o, [format]: open }))}
                    info={
                      <>
                        <span className="truncate text-xs text-muted-foreground">
                          {FILTER_FORMAT_EXTENSIONS[format]}{orgDefault && ` · Org default: ${profileLabel(orgDefault)}`}
                        </span>
                        {FILTER_FORMAT_NOTES[format] && (
                          <SectionInfo label={`About ${FILTER_FORMAT_LABELS[format]} files`} size="sm">{FILTER_FORMAT_NOTES[format]}</SectionInfo>
                        )}
                      </>
                    }
                    data-testid={`filter-profile-section-${format}`}>
                    <DataTable columns={FILTER_PROFILE_COLUMNS} data={rows} meta={meta as unknown as Record<string, unknown>} hideHeader
                      onRowClick={p => navigate(`${FILTER_PROFILES_PATH}/${p.id}`)} rowClassName="cursor-pointer"
                      rowTestId={p => `filter-profile-row-${p.id}`} />
                  </SlimCollapsibleSection>
                )
              })}
            </div>
          </div>
        )}
      </TooltipProvider>
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={open => !open && setDeleteTarget(null)}
        title={`Delete filter profile "${deleteTarget?.name}"?`}
        description="Projects that use it go back to the format's Default profile for their next uploads. Files already uploaded keep how they were read."
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
        testId="filter-profile-delete-confirm"
      />
    </FileConfigurationsPageFrame>
  )
}
