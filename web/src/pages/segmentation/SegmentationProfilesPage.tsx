// SegmentationProfilesPage — the Segmentation profiles tab of File
// configurations: the profiles that split uploaded files into segments, each
// for one source language or for any. Every project and template names one.
// The built-ins are read-only; owners, admins and project managers copy one to
// make their own, edit it, import SRX files and delete their own. Linguists
// never see this page.

import { useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { toast } from 'sonner'
import { Upload } from 'lucide-react'
import {
  deleteSegmentationProfile, exportSegmentationProfileSRX, getMe, listSegmentationProfiles, parseSRX,
  type SegmentationProfile, type SRXImport,
} from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { isManagerRole } from '../../lib/roles'
import { errorText } from '../../lib/error-text'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import ConfirmDialog from '@/components/ConfirmDialog'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { Button } from '@/components/ui/button'
import { CreateButton } from '@/components/ui/create-button'
import { DataTable } from '@/components/ui/data-table'
import { TooltipProvider } from '@/components/ui/tooltip'
import { PROFILE_COLUMNS, type ProfileTableMeta } from '@/components/segmentation/profile-columns'
import { SRXImportDialog } from '@/components/segmentation/SRXImportDialog'
import { FileConfigurationsPageFrame } from '../file-configurations/FileConfigurationsPageFrame'
import { SEGMENTATION_PROFILES_PATH } from './segmentation-paths'

/** Language-neutral profiles first, then by first language; per language the org's profiles, then the built-ins. */
function sortProfiles(profiles: SegmentationProfile[]): SegmentationProfile[] {
  const first = (p: SegmentationProfile) => p.source_languages[0] ?? ''
  return [...profiles].sort((a, b) =>
    first(a).localeCompare(first(b)) || Number(a.builtin) - Number(b.builtin) || a.name.localeCompare(b.name))
}

export default function SegmentationProfilesPage() {
  const navigate = useNavigate()
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const canWrite = isManagerRole(me?.role)
  const listQuery = useQuery({ queryKey: queryKeys.segmentationProfiles.list(), queryFn: () => listSegmentationProfiles() })
  const [deleteTarget, setDeleteTarget] = useState<SegmentationProfile | null>(null)
  const [srx, setSRX] = useState<{ result: SRXImport; fileName: string; key: number } | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const deleteMut = useMutationWithToast({
    mutationFn: (p: SegmentationProfile) => deleteSegmentationProfile(p.id),
    successMessage: 'Segmentation profile deleted',
    errorMessage: 'Failed to delete the segmentation profile',
    // The delete also takes the profile off the projects that use it.
    invalidateKeys: [queryKeys.segmentationProfiles.all, queryKeys.project.allSettings],
  })

  const meta: ProfileTableMeta = {
    canWrite,
    busy: deleteMut.isPending,
    onCopy: p => navigate(`${SEGMENTATION_PROFILES_PATH}/new?from=${p.id}`),
    onExport: p => exportSegmentationProfileSRX(p.id, p.name).catch(err => toast.error(`Export failed: ${errorText(err, 'Unknown error')}`)),
    onDelete: p => setDeleteTarget(p),
  }
  const rows = useMemo(() => sortProfiles(listQuery.data ?? []), [listQuery.data])

  const handleImport = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    e.target.value = ''
    try {
      setSRX({ result: await parseSRX(file), fileName: file.name, key: Date.now() })
    } catch (err) {
      toast.error(`Import failed: ${errorText(err, 'Unknown error')}`)
    }
  }

  return (
    <FileConfigurationsPageFrame
      actions={canWrite && (
        <div className="flex gap-2">
          <input ref={fileInputRef} type="file" accept=".srx,.xml" onChange={handleImport} className="hidden" data-testid="srx-file-input" />
          <Button variant="outline" onClick={() => fileInputRef.current?.click()} startIcon={<Upload />} data-guide="segmentation.import" data-testid="srx-import">
            Import SRX
          </Button>
          <CreateButton onClick={() => navigate(`${SEGMENTATION_PROFILES_PATH}/new`)} data-testid="segmentation-new">
            New profile
          </CreateButton>
        </div>
      )}
    >
      <TooltipProvider delay={200}>
        {listQuery.isLoading ? <LoadingPlaceholder /> : listQuery.error ? (
          <LoadFailed what="segmentation profiles" error={listQuery.error} onRetry={() => listQuery.refetch()} />
        ) : rows.length === 0 ? (
          <EmptyState message="No segmentation profiles yet." />
        ) : (
          <div className="overflow-hidden rounded-lg border border-border bg-card text-sm" data-guide="segmentation.list">
            <DataTable columns={PROFILE_COLUMNS} data={rows} meta={meta as unknown as Record<string, unknown>}
              onRowClick={p => navigate(`${SEGMENTATION_PROFILES_PATH}/${p.id}`)} rowClassName="cursor-pointer"
              rowTestId={p => `segmentation-row-${p.id}`} />
          </div>
        )}
      </TooltipProvider>
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={open => !open && setDeleteTarget(null)}
        title={`Delete segmentation profile "${deleteTarget?.name}"?`}
        description="Projects that use it lose it, and their next upload asks for another profile. Files already uploaded keep their segments."
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
        testId="segmentation-delete-confirm"
      />
      {srx && <SRXImportDialog key={srx.key} result={srx.result} fileName={srx.fileName} onClose={() => setSRX(null)} />}
    </FileConfigurationsPageFrame>
  )
}
