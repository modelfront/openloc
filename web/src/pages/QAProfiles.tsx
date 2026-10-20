import { useState } from 'react'
import { SEVERITIES, SEVERITY_CHIP_COLORS, SEVERITY_LABELS } from '../lib/qa-findings'
import { useQuery } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import {
  listQAProfiles,
  createQAProfile,
  getQAProfile,
  updateQAProfile,
  deleteQAProfile,
  upsertQARule,
} from '../api'
import type { QaProfile } from '../types/generated'
import { queryKeys } from '../lib/query-keys'
import { parseQARuleConfig } from '../lib/qa-rule-config'
import { formatDate } from '../utils'
import { useCRUDList } from '../hooks/use-crud-list'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import ConfirmDialog from '../components/ConfirmDialog'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { PageHeader } from '@/components/layout/PageHeader'
import { PageContainer } from '@/components/layout/PageContainer'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { DeleteButton } from '@/components/ui/delete-button'
import { InlineRenameField } from '@/components/list/InlineRenameField'
import { Pencil, ChevronDown } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { useScrollToError } from '../hooks/useScrollToError'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { ChipButton } from '@/components/ui/chip'
import { cn } from '@/lib/utils'
import { VerticalDivider } from '@/components/ui/vertical-divider'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { listCardHeaderClass, listCardTitleClass } from '@/components/list/list-row'

const qaProfileSchema = z.object({
  name: z.string().min(1, 'Profile name is required'),
  description: z.string().optional(),
})
type QAProfileFormValues = z.infer<typeof qaProfileSchema>

const ALL_CHECK_TYPES = [
  { type: 'empty_target', label: 'Empty target', description: 'Target segment is empty' },
  { type: 'tag_checks', label: 'Tag checks', description: 'Missing, extra, unclosed, or unopened tags', hasSubChecks: true },
  { type: 'number_consistency', label: 'Number consistency', description: 'Numbers differ between source and target' },
  { type: 'terminology', label: 'Terminology', description: 'Approved terms from a glossary are missing in the target' },
  { type: 'forbidden_terms', label: 'Forbidden terms', description: 'Forbidden terms (glossary entries with status=forbidden) appear in the target' },
  { type: 'consistency', label: 'Consistency', description: 'Same source translated differently' },
  { type: 'punctuation', label: 'Punctuation', description: 'Trailing punctuation mismatch' },
  { type: 'target_identical_to_source', label: 'Target identical to source', description: 'Target text is the same as source (potentially untranslated)' },
] as const

const TAG_SUB_CHECKS = [
  { key: 'missing', label: 'Missing tags', description: 'Source tag missing from target' },
  { key: 'extra', label: 'Extra tags', description: 'Target has tag not in source' },
  { key: 'unclosed', label: 'Unclosed tags', description: 'Opening tag without closing tag' },
  { key: 'unopened', label: 'Unopened tags', description: 'Closing tag without opening tag' },
] as const

// Only the option in force carries its colour; the rest sit back as faded grey,
// so the row reads as one setting rather than four competing labels.
const UNSET_OPTION = 'opacity-30 hover:opacity-60'

export default function QAProfiles() {
  const {
    register: registerCreate,
    handleSubmit: handleCreateSubmit,
    reset: resetCreate,
    formState: { errors: createErrors },
  } = useForm<QAProfileFormValues>({
    resolver: zodResolver(qaProfileSchema),
    defaultValues: { name: '', description: '' },
  })
  useScrollToError(createErrors)

  const [expandedId, setExpandedId] = useState<number | null>(null)

  const {
    items: profiles, isLoading, loadFailed, error, refetch,
    showForm, setShowForm, createMut,
    renamingId, renameValue, setRenameValue, startRename, cancelRename,
    deleteTarget, setDeleteTarget, confirmDelete,
  } = useCRUDList({
    queryKey: queryKeys.qaProfiles.all,
    queryFn: listQAProfiles,
    createFn: (data: QAProfileFormValues) => createQAProfile(data.name.trim(), (data.description ?? '').trim()),
    renameFn: ({ id, name }) => updateQAProfile(id, name, ''),
    deleteFn: deleteQAProfile,
    entityName: 'QA profile',
    onCreateSuccess: () => resetCreate(),
    onDeleteSuccess: () => {
      if (deleteTarget && expandedId === deleteTarget.id) setExpandedId(null)
    },
  })

  // Custom rename that preserves the profile's description.
  // Routed through `useMutationWithToast` so failures (5xx, 409 conflict from
  // a cascade-delete check, network error) surface as a toast AND the rename
  // input doesn't get stuck open. Also invalidates the list query so the new
  // name appears immediately.
  const renameWithDescMut = useMutationWithToast({
    mutationFn: ({ id, name, description }: { id: number; name: string; description: string }) =>
      updateQAProfile(id, name, description),
    successMessage: 'QA profile renamed',
    errorMessage: 'Failed to rename QA profile',
    invalidateKeys: [queryKeys.qaProfiles.all],
    onSuccess: () => cancelRename(),
  })

  const submitRenameWithDesc = (profile: QaProfile) => {
    const trimmed = renameValue.trim()
    if (trimmed && trimmed !== profile.name) {
      renameWithDescMut.mutate({
        id: profile.id,
        name: trimmed,
        description: profile.description ?? '',
      })
    } else {
      cancelRename()
    }
  }

  if (isLoading) return <LoadingPlaceholder />

  return (
    <TooltipProvider delay={200}>
      <PageContainer>
        <PageHeader
          title="QA profiles"
          actions={
            <Button onClick={() => setShowForm(!showForm)} data-guide="quality.new">
              New QA profile
            </Button>
          }
        />

        {showForm && (
          <form onSubmit={handleCreateSubmit(data => createMut.mutate(data))} className="mb-6 p-4 bg-card rounded-lg border border-gray-200 dark:border-gray-800 flex max-md:flex-wrap gap-3 items-end">
            <FormField label="Name" error={createErrors.name?.message} required className="flex-1">
              <Input {...registerCreate('name')} placeholder="e.g. Strict QA" autoFocus />
            </FormField>
            <FormField label="Description" className="flex-1">
              <Input {...registerCreate('description')} placeholder="Optional description" />
            </FormField>
            <Button type="submit" size="sm" disabled={createMut.isPending}>
              {createMut.isPending ? 'Creating...' : 'Create'}
            </Button>
          </form>
        )}

        {loadFailed && <LoadFailed what="QA profiles" error={error} onRetry={refetch} />}

        <div className="space-y-2" data-guide="quality.list">
          {profiles?.map(profile => (
            <div key={profile.id}>
              <div
                onClick={() => setExpandedId(expandedId === profile.id ? null : profile.id)}
                className="block p-4 bg-card rounded-lg border border-gray-200 dark:border-gray-800 hover:bg-muted transition-colors cursor-pointer group"
              >
                <div className={listCardHeaderClass}>
                  <div className={`${listCardTitleClass} gap-3`}>
                    {renamingId === profile.id ? (
                      <InlineRenameField
                        value={renameValue}
                        onChange={setRenameValue}
                        onSubmit={() => submitRenameWithDesc(profile)}
                        onCancel={cancelRename}
                      />
                    ) : (
                      <>
                        <div className="font-medium text-gray-900 dark:text-gray-100 truncate">{profile.name}</div>
                        {/* Rename pencil next to the name (QA profiles have no
                            detail page, so it lives here). startRename stops
                            propagation so it doesn't toggle the row's expander. */}
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          type="button"
                          onClick={e => startRename(e, profile.id, profile.name)}
                          data-testid={`rename-qa-profile-${profile.id}`}
                          title="Rename"
                          className="shrink-0 text-gray-400 hover:text-gray-600 dark:text-gray-500 dark:hover:text-gray-300"
                        >
                          <Pencil className="w-4 h-4" />
                        </Button>
                      </>
                    )}
                    {profile.description && (
                      <span className="text-xs text-gray-400 dark:text-gray-500 truncate">{profile.description}</span>
                    )}
                  </div>
                  <div className="flex items-center gap-2 ml-3 shrink-0">
                    <span className="text-xs text-gray-400 dark:text-gray-500">
                      Created {formatDate(profile.created_at)}
                    </span>
                    <ChevronDown
                      className={`w-4 h-4 text-gray-400 transition-transform ${expandedId === profile.id ? 'rotate-180' : ''}`}
                    />
                    <Tooltip>
                      <TooltipTrigger
                        render={
                          <DeleteButton
                            aria-label="Delete"
                            onClick={e => {
                              e.stopPropagation()
                              setDeleteTarget({ id: profile.id, name: profile.name })
                            }}
                          />
                        }
                      />
                      <TooltipContent>Delete profile</TooltipContent>
                    </Tooltip>
                  </div>
                </div>
              </div>
              {expandedId === profile.id && (
                <ProfileRulesEditor profileId={profile.id} />
              )}
            </div>
          ))}
          {profiles?.length === 0 && (
            <EmptyState message="No QA profiles yet. Create one to get started." />
          )}
        </div>

        <ConfirmDialog
          open={!!deleteTarget}
          onOpenChange={open => !open && setDeleteTarget(null)}
          title={`Delete QA profile "${deleteTarget?.name}"?`}
          description="This will also delete all its rules. Projects using this profile will lose their QA configuration."
          onConfirm={confirmDelete}
        />
      </PageContainer>
    </TooltipProvider>
  )
}

function ProfileRulesEditor({ profileId }: { profileId: number }) {
  const { data, isLoading } = useQuery({
    queryKey: queryKeys.qaProfile.detail(profileId),
    queryFn: () => getQAProfile(profileId),
  })

  const ruleMut = useMutationWithToast({
    mutationFn: ({ checkType, enabled, severity, config }: { checkType: string; enabled: boolean; severity: string; config?: Record<string, unknown> }) =>
      upsertQARule(profileId, checkType, enabled, severity, config),
    successMessage: 'Rule updated',
    errorMessage: 'Failed to update rule',
    invalidateKeys: [queryKeys.qaProfile.detail(profileId)],
  })

  if (isLoading) return <LoadingPlaceholder size="sm" message="Loading rules..." />

  const ruleMap = new Map((data?.rules ?? []).map(r => [r.check_type, r]))

  return (
    <div className="mt-1 mb-2 bg-background border border-gray-200 dark:border-gray-800 rounded-lg overflow-hidden" data-testid="qa-rules-panel">
      <div className="divide-y divide-gray-200 dark:divide-gray-800">
        {ALL_CHECK_TYPES.map(check => {
          const rule = ruleMap.get(check.type)
          const enabled = rule?.enabled ?? false
          const severity = rule?.severity ?? 'warning'

          // Config arrives as an object, a JSON string or base64;
          // parseQARuleConfig accepts all three.
          const configObj = parseQARuleConfig(rule?.config) as { missing?: boolean; extra?: boolean; unclosed?: boolean; unopened?: boolean }
          const tagConfig = check.type === 'tag_checks'
            ? { missing: configObj.missing ?? true, extra: configObj.extra ?? true, unclosed: configObj.unclosed ?? true, unopened: configObj.unopened ?? true }
            : null

          return (
            <div key={check.type}>
              <div className="px-4 py-3 flex items-center gap-4">
                <div className="min-w-0 flex-1">
                  <div className={`text-base font-medium ${enabled ? 'text-gray-900 dark:text-gray-100' : 'text-gray-400 dark:text-gray-500'}`}>
                    {check.label}
                  </div>
                  <div className="text-sm text-gray-400 dark:text-gray-500">{check.description}</div>
                </div>
                <div className="flex items-center gap-1">
                  {SEVERITIES.map(sev => (
                    <ChipButton
                      key={sev}
                      color={enabled && severity === sev ? SEVERITY_CHIP_COLORS[sev] : 'grey'}
                      // Clicking the active severity again disables the rule;
                      // clicking any severity while disabled re-enables it there.
                      onClick={() => ruleMut.mutate({ checkType: check.type, enabled: !(enabled && severity === sev), severity: sev, config: tagConfig ?? undefined })}
                      className={cn(!(enabled && severity === sev) && UNSET_OPTION)}
                    >
                      {SEVERITY_LABELS[sev]}
                    </ChipButton>
                  ))}
                  <VerticalDivider className="mx-1 bg-gray-300" />
                  <ChipButton
                    color="grey"
                    onClick={() => ruleMut.mutate({ checkType: check.type, enabled: false, severity, config: tagConfig ?? undefined })}
                    className={cn(enabled && UNSET_OPTION)}
                  >
                    Disabled
                  </ChipButton>
                </div>
              </div>
              {check.type === 'tag_checks' && enabled && tagConfig && (
                <div className="px-4 pb-3 pl-12 flex flex-col">
                  {TAG_SUB_CHECKS.map(sub => (
                    <CheckboxField
                      key={sub.key}
                      checked={tagConfig[sub.key as keyof typeof tagConfig]}
                      onChange={checked => {
                        const newConfig = { ...tagConfig, [sub.key]: checked }
                        ruleMut.mutate({ checkType: 'tag_checks', enabled, severity, config: newConfig })
                      }}
                      label={sub.label}
                      description={sub.description}
                    />
                  ))}
                </div>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}
