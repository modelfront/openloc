import { useState, type ReactNode } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
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
  exportQAProfile,
  importQAProfile,
  replaceQAProfileRules,
  type QAProfileWithRules,
} from '../api'
import type { QaProfile, QaRule } from '../types/generated'
import { queryKeys } from '../lib/query-keys'
import { qaProfileKey } from '../lib/asset-links'
import { useAttentionTarget } from '@/hooks/useAttention'
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
import { Pencil, ChevronDown, Download, Upload } from 'lucide-react'
import { Button, MISSING_REQUIRED_REASON } from '@/components/ui/button'
import { CreateButton } from '@/components/ui/create-button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { useScrollToError } from '../hooks/useScrollToError'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { ChoiceToggle, type ChoiceToggleOption } from '@/components/ui/choice-toggle'
import { listCardHeaderClass, listCardTitleClass } from '@/components/list/list-row'
import { QARuleSeverityToggle } from '../components/QARuleSeverityToggle'
import { QARegexRules } from '../components/QARegexRules'
import { QANumberFormatLanguages } from '../components/QANumberFormatLanguages'
import { parseNumberFormatConfig, type NumberFormatMode } from '../lib/qa-number-format'

const qaProfileSchema = z.object({
  name: z.string().trim().min(1, 'Profile name is required'),
  description: z.string().optional(),
})
type QAProfileFormValues = z.infer<typeof qaProfileSchema>

const ALL_CHECK_TYPES = [
  { type: 'empty_target', label: 'Empty target', description: 'Target segment is empty' },
  { type: 'tag_checks', label: 'Tag checks', description: 'Missing, extra, unclosed, or unopened tags', hasSubChecks: true },
  { type: 'number_consistency', label: 'Number consistency', description: 'A number is missing or has a different value in the target' },
  { type: 'number_format', label: 'Number format', description: "Decimal or thousands separator not written the target language's way" },
  { type: 'date_consistency', label: 'Dates', description: 'A date has a different day, month or year, in any date format' },
  { type: 'time_consistency', label: 'Times', description: 'A time differs: 3:30 PM and 15:30 match, 13:30 does not' },
  { type: 'unit_conversion', label: 'Units', description: 'A value converted between imperial and metric units does not match' },
  { type: 'placeables', label: 'Placeables', description: 'A web or email address, product code or variable such as {0} or %s is missing or changed', hasSubChecks: true },
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

const PLACEABLE_SUB_CHECKS = [
  { key: 'urls_emails', label: 'URLs and emails', description: 'Web and email addresses' },
  { key: 'codes', label: 'Alphanumerics', description: 'Product and part codes such as XJ-500B' },
  { key: 'variables', label: 'Variables', description: 'Placeholders such as {0}, %s or {{name}}' },
] as const

// Rules whose config is a set of sub-checks, each on unless saved off.
const SUB_CHECKS: Record<string, readonly { key: string; label: string; description: string }[]> = {
  tag_checks: TAG_SUB_CHECKS,
  placeables: PLACEABLE_SUB_CHECKS,
}

const NUMBER_FORMAT_MODES: ChoiceToggleOption<NumberFormatMode>[] = [
  { value: 'allow', label: 'Source or target format' },
  { value: 'require', label: 'Target format only' },
]

/** The rules with `saved` in place of the built-in rule of its check type. */
function withRule(rules: QaRule[], saved: QaRule): QaRule[] {
  return rules.some(r => r.check_type === saved.check_type)
    ? rules.map(r => (r.check_type === saved.check_type ? saved : r))
    : [...rules, saved]
}

/** A hidden file input for an exported QA profile; `pick` opens it. */
function useProfileFilePicker(testId: string, onPick: (text: string) => void) {
  const [input, setInput] = useState<HTMLInputElement | null>(null)
  const element = (
    <input
      ref={setInput}
      type="file"
      accept=".json,application/json"
      className="hidden"
      data-testid={testId}
      onChange={e => {
        const file = e.target.files?.[0]
        e.target.value = ''
        if (file) file.text().then(onPick, () => toast.error('Could not read the file.'))
      }}
    />
  )
  return { pick: () => input?.click(), element }
}

export default function QAProfiles() {
  const {
    register: registerCreate,
    handleSubmit: handleCreateSubmit,
    reset: resetCreate,
    formState: { errors: createErrors, isValid: createValid },
  } = useForm<QAProfileFormValues>({
    resolver: zodResolver(qaProfileSchema),
    mode: 'onTouched',
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

  const importMut = useMutationWithToast({
    mutationFn: importQAProfile,
    successMessage: p => `Imported as "${p.name}"`,
    errorMessage: 'Import failed',
    invalidateKeys: [queryKeys.qaProfiles.all],
    onSuccess: p => setExpandedId(p.id),
  })
  const importPicker = useProfileFilePicker("import-qa-profile-input", text => importMut.mutate(text))

  if (isLoading) return <LoadingPlaceholder />

  return (
    <TooltipProvider delay={200}>
      <PageContainer>
        <PageHeader
          title="QA profiles"
          actions={
            <>
              <Button variant="outline" onClick={importPicker.pick} disabled={importMut.isPending} startIcon={<Upload />} data-testid="import-qa-profile">
                {importMut.isPending ? 'Importing...' : 'Import'}
              </Button>
              <CreateButton onClick={() => setShowForm(!showForm)} data-guide="quality.new">
                New QA profile
              </CreateButton>
            </>
          }
        />
        {importPicker.element}

        {showForm && (
          <form onSubmit={handleCreateSubmit(data => createMut.mutate(data))} className="mb-6 p-4 bg-card rounded-lg border border-gray-200 dark:border-gray-800 flex max-md:flex-wrap gap-3 items-end">
            <FormField label="Name" error={createErrors.name?.message} required className="flex-1">
              <Input {...registerCreate('name')} placeholder="e.g. Strict QA" autoFocus />
            </FormField>
            <FormField label="Description" className="flex-1">
              <Input {...registerCreate('description')} placeholder="Optional description" />
            </FormField>
            <Button
              type="submit"
              size="sm"
              disabled={createMut.isPending || !createValid}
              disabledReason={createValid ? undefined : MISSING_REQUIRED_REASON}
            >
              {createMut.isPending ? 'Creating...' : 'Create'}
            </Button>
          </form>
        )}

        {loadFailed && <LoadFailed what="QA profiles" error={error} onRetry={refetch} />}

        <div className="space-y-2" data-guide="quality.list">
          {profiles?.map(profile => (
            <ProfileCard key={profile.id} id={profile.id} onOpen={() => setExpandedId(profile.id)}>
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
                <ProfileRulesEditor profileId={profile.id} profileName={profile.name} />
              )}
            </ProfileCard>
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

// A profile's row and its rules. A link from a project's settings opens the
// page pointing at it (`qaProfilePath`), which also expands it.
function ProfileCard({ id, onOpen, children }: { id: number; onOpen: () => void; children: ReactNode }) {
  return <div ref={useAttentionTarget(qaProfileKey(id), onOpen)} className="rounded-lg" data-testid={qaProfileKey(id)}>{children}</div>
}

function ProfileRulesEditor({ profileId, profileName }: { profileId: number; profileName: string }) {
  const { data, isLoading } = useQuery({
    queryKey: queryKeys.qaProfile.detail(profileId),
    queryFn: () => getQAProfile(profileId),
  })

  const queryClient = useQueryClient()
  const detailKey = queryKeys.qaProfile.detail(profileId)
  const ruleMut = useMutationWithToast({
    mutationFn: ({ checkType, enabled, severity, config }: { checkType: string; enabled: boolean; severity: string; config?: Record<string, unknown> }) =>
      upsertQARule(profileId, checkType, enabled, severity, config),
    successMessage: 'Rule updated',
    errorMessage: 'Failed to update rule',
    invalidateKeys: [detailKey],
    // A save sends the rule's whole config, so the next one must start from
    // this one, not from the profile as last fetched.
    onSuccess: saved => queryClient.setQueryData<QAProfileWithRules>(detailKey, old => old && { ...old, rules: withRule(old.rules, saved) }),
  })

  const [confirmReplace, setConfirmReplace] = useState(false)
  const replaceMut = useMutationWithToast({
    mutationFn: (text: string) => replaceQAProfileRules(profileId, text),
    successMessage: 'Rules replaced from the file',
    errorMessage: 'Import failed',
    invalidateKeys: [queryKeys.qaProfile.detail(profileId)],
  })
  const replacePicker = useProfileFilePicker("replace-qa-rules-input", text => replaceMut.mutate(text))
  const exportMut = useMutationWithToast({
    mutationFn: () => exportQAProfile(profileId),
    successMessage: () => null,
    errorMessage: 'Export failed',
  })

  if (isLoading) return <LoadingPlaceholder size="sm" message="Loading rules..." />

  const ruleMap = new Map((data?.rules ?? []).map(r => [r.check_type, r]))

  return (
    <div className="mt-1 mb-2 bg-background border border-gray-200 dark:border-gray-800 rounded-lg overflow-hidden" data-testid="qa-rules-panel">
      <div className="px-4 py-2 flex justify-end gap-2 border-b border-gray-200 dark:border-gray-800">
        <Button size="sm" variant="ghost" onClick={() => exportMut.mutate()} disabled={exportMut.isPending} startIcon={<Download />} data-testid="export-qa-profile">
          Export
        </Button>
        <Button size="sm" variant="ghost" onClick={() => setConfirmReplace(true)} disabled={replaceMut.isPending} startIcon={<Upload />} data-testid="replace-qa-rules">
          {replaceMut.isPending ? 'Importing...' : 'Import rules'}
        </Button>
        {replacePicker.element}
      </div>
      <div className="divide-y divide-gray-200 dark:divide-gray-800">
        {ALL_CHECK_TYPES.map(check => {
          const rule = ruleMap.get(check.type)
          const enabled = rule?.enabled ?? false
          const severity = rule?.severity ?? 'warning'

          // Config arrives as an object, a JSON string or base64;
          // parseQARuleConfig accepts all three.
          const subChecks = SUB_CHECKS[check.type]
          const configObj = parseQARuleConfig(rule?.config)
          const subConfig = subChecks
            ? Object.fromEntries(subChecks.map(s => [s.key, configObj[s.key] !== false]))
            : null
          // Every save of number_format sends its whole config, languages included.
          const numberFormat = check.type === 'number_format' ? parseNumberFormatConfig(rule?.config) : null
          const config = subConfig ?? numberFormat ?? undefined

          return (
            <div key={check.type} data-testid={`qa-rule-${check.type}`}>
              <div className="px-4 py-3 flex items-center gap-4">
                <div className="min-w-0 flex-1">
                  <div className={`text-base font-medium ${enabled ? 'text-gray-900 dark:text-gray-100' : 'text-gray-400 dark:text-gray-500'}`}>
                    {check.label}
                  </div>
                  <div className="text-sm text-gray-400 dark:text-gray-500">{check.description}</div>
                </div>
                <QARuleSeverityToggle
                  enabled={enabled}
                  severity={severity}
                  disabled={ruleMut.isPending}
                  onChange={next => ruleMut.mutate({ checkType: check.type, ...next, config })}
                />
              </div>
              {subChecks && enabled && subConfig && (
                <div className="px-4 pb-3 pl-12 flex flex-col" data-testid={`qa-sub-checks-${check.type}`}>
                  {subChecks.map(sub => (
                    <CheckboxField
                      key={sub.key}
                      checked={subConfig[sub.key]}
                      onChange={checked => {
                        const newConfig = { ...subConfig, [sub.key]: checked }
                        ruleMut.mutate({ checkType: check.type, enabled, severity, config: newConfig })
                      }}
                      label={sub.label}
                      description={sub.description}
                    />
                  ))}
                </div>
              )}
              {numberFormat && enabled && (
                <div className="px-4 pb-3 pl-12">
                  <ChoiceToggle
                    size="sm"
                    aria-label="Number format mode"
                    data-testid="qa-number-format-mode"
                    options={NUMBER_FORMAT_MODES.map(o => ({ ...o, disabled: ruleMut.isPending }))}
                    value={numberFormat.mode}
                    onChange={mode => ruleMut.mutate({ checkType: 'number_format', enabled, severity, config: { ...numberFormat, mode } })}
                  />
                </div>
              )}
              {numberFormat && enabled && (
                <QANumberFormatLanguages
                  languages={numberFormat.languages}
                  saving={ruleMut.isPending}
                  onChange={languages => ruleMut.mutateAsync({ checkType: 'number_format', enabled, severity, config: { ...numberFormat, languages } })}
                />
              )}
            </div>
          )
        })}
      </div>
      <QARegexRules profileId={profileId} rules={data?.rules ?? []} />
      <ConfirmDialog
        open={confirmReplace}
        onOpenChange={setConfirmReplace}
        title={`Replace every rule of "${profileName}"?`}
        description="Pick an exported QA profile file. Its rules replace all rules of this profile, custom regex rules included. The name and description stay."
        actionLabel="Pick file"
        testId="confirm-replace-qa-rules"
        onConfirm={() => {
          setConfirmReplace(false)
          replacePicker.pick()
        }}
      />
    </div>
  )
}
