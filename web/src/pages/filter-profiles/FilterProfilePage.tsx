// FilterProfilePage — one filter profile: its name, its format and the
// format's import options. A built-in is read-only (Copy makes an editable
// org profile); /file-configurations/filter-profiles/new creates one, from
// scratch or as a copy (?from=<id>), and the format is picked only there.
// Owners, admins and project managers edit; linguists never reach the page.

import { useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Controller, useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { Copy } from 'lucide-react'
import { ApiError, createFilterProfile, getFilterProfile, getMe, updateFilterProfile, type FilterProfile } from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { isManagerRole } from '../../lib/roles'
import { FILTER_FORMAT_EXTENSIONS, FILTER_FORMAT_LABELS, FILTER_FORMATS, filterFormatHint, isFilterFormat } from '../../lib/filter-formats'
import { filterProfileSchema, optionsOf, valuesOf, type FilterProfileForm } from '../../lib/filter-options'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { useScrollToError } from '../../hooks/useScrollToError'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { PageContainer } from '@/components/layout/PageContainer'
import { SettingsCard } from '@/components/layout/SettingsCard'
import { EditableTitle } from '@/components/list/EditableTitle'
import { Alert } from '@/components/ui/alert'
import { Button, NOTHING_CHANGED_REASON } from '@/components/ui/button'
import { Chip } from '@/components/ui/chip'
import { Combobox } from '@/components/ui/combobox'
import { FormField } from '@/components/ui/form-field'
import { Input } from '@/components/ui/input'
import { FilterOptionsEditor } from '@/components/filter-profiles/FilterOptionsEditor'
import { FILTER_PROFILES_PATH } from './filter-profile-paths'

const FORMAT_OPTIONS = FILTER_FORMATS.map(f => ({ value: f, label: FILTER_FORMAT_LABELS[f], keywords: FILTER_FORMAT_EXTENSIONS[f] }))

export default function FilterProfilePage() {
  const { profileId } = useParams()
  const [params] = useSearchParams()
  const creating = profileId === undefined
  const fromId = Number(params.get('from')) || undefined
  const id = creating ? fromId : Number(profileId)

  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const profileQuery = useQuery({
    queryKey: queryKeys.filterProfiles.detail(id ?? 0),
    queryFn: () => getFilterProfile(id as number),
    enabled: !!id,
  })
  const profile = profileQuery.data

  if (id && profileQuery.isLoading) return <PageContainer><LoadingPlaceholder /></PageContainer>
  if (id && (profileQuery.error || !profile)) {
    return (
      <PageContainer>
        <Breadcrumbs items={[{ label: 'Filter profiles', to: FILTER_PROFILES_PATH }, { label: 'Not found' }]} />
        <LoadFailed what="filter profile" error={profileQuery.error} onRetry={() => profileQuery.refetch()} />
      </PageContainer>
    )
  }
  const canWrite = isManagerRole(me?.role)
  return (
    <ProfileEditor
      // A new version of the profile (a save, a reload) restarts the editor from it.
      key={`${creating ? 'new' : id}:${fromId ?? ''}:${profile?.updated_at ?? ''}`}
      profile={profile} creating={creating} fromId={fromId}
      readOnly={!canWrite || (!creating && !!profile?.builtin)} canCopy={canWrite}
      onReload={() => profileQuery.refetch()}
    />
  )
}

function ProfileEditor({ profile, creating, fromId, readOnly, canCopy, onReload }: {
  profile: FilterProfile | undefined
  creating: boolean
  fromId: number | undefined
  readOnly: boolean
  canCopy: boolean
  onReload: () => void
}) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const initial = useMemo((): FilterProfileForm => ({
    name: profile ? (creating ? `${profile.name} (copy)` : profile.name) : '',
    format: profile?.format ?? '',
    options: valuesOf(profile?.options),
  }), [profile, creating])
  const { control, register, handleSubmit, setValue, formState: { errors } } = useForm<FilterProfileForm>({
    resolver: zodResolver(filterProfileSchema),
    defaultValues: initial,
  })
  useScrollToError(errors)
  const [name, format, options] = useWatch({ control, name: ['name', 'format', 'options'] })
  // The server's refusal of a save: a stale version or a name taken meanwhile.
  const [conflict, setConflict] = useState<string | null>(null)

  const knownFormat = isFilterFormat(format) ? format : undefined
  const dirty = creating || name.trim() !== initial.name ||
    (!!knownFormat && JSON.stringify(optionsOf(knownFormat, options)) !== JSON.stringify(optionsOf(knownFormat, initial.options)))

  const saveMut = useMutationWithToast({
    mutationFn: (v: FilterProfileForm) => {
      if (!isFilterFormat(v.format)) throw new Error('Choose a format.')
      const opts = optionsOf(v.format, v.options)
      return creating || !profile
        ? createFilterProfile({ name: v.name, format: v.format, options: opts, from: fromId })
        : updateFilterProfile(profile.id, { name: v.name, options: opts, updated_at: profile.updated_at })
    },
    successMessage: creating ? 'Filter profile created' : 'Filter profile saved',
    errorMessage: 'Failed to save the filter profile',
    errorToast: err => !(err instanceof ApiError && err.status === 409 && !creating),
    // Project and template settings name the profiles they use.
    invalidateKeys: [queryKeys.filterProfiles.lists, queryKeys.project.allSettings, queryKeys.template.all],
    // Create and Save both go back to the list.
    onSuccess: saved => {
      queryClient.setQueryData(queryKeys.filterProfiles.detail(saved.id), saved)
      navigate(FILTER_PROFILES_PATH)
    },
    onError: err => {
      if (err instanceof ApiError && err.status === 409 && !creating) setConflict(err.message)
    },
  })
  const save = handleSubmit(v => saveMut.mutate(v))
  // A copy keeps the format of the profile it copies.
  const pickFormat = creating && !fromId

  return (
    <PageContainer>
      <Breadcrumbs
        items={[
          { label: 'Filter profiles', to: FILTER_PROFILES_PATH },
          {
            label: creating ? 'New filter profile' : (
              <span className="flex min-w-0 items-center gap-2">
                <EditableTitle name={name} canEdit={!readOnly} onRename={n => setValue('name', n, { shouldDirty: true })}
                  data-testid="filter-profile-rename" />
              </span>
            ),
          },
        ]}
        trailing={!creating && profile?.builtin && <Chip>Built-in</Chip>}
        actions={(
          <div className="flex gap-2">
            {!creating && profile && canCopy && (
              <Button variant="outline" onClick={() => navigate(`${FILTER_PROFILES_PATH}/new?from=${profile.id}`)}
                startIcon={<Copy />} data-testid="filter-profile-copy">
                Copy
              </Button>
            )}
            {!readOnly && (
              <Button onClick={save} disabled={saveMut.isPending || !dirty}
                disabledReason={saveMut.isPending || dirty ? undefined : NOTHING_CHANGED_REASON} data-testid="filter-profile-save">
                {saveMut.isPending ? 'Saving…' : creating ? 'Create' : 'Save'}
              </Button>
            )}
          </div>
        )}
      />
      {!creating && profile?.builtin && (
        <p className="mb-4 text-sm text-muted-foreground" data-testid="filter-profile-builtin-note">
          Built-in — copy it to make changes.{profile.description && ` ${profile.description}`}
        </p>
      )}
      {!creating && errors.name && <Alert tone="error" className="mb-4">{errors.name.message}</Alert>}
      {conflict && (
        <Alert tone="error" className="mb-4" title="Your changes are not saved">
          <span className="flex items-center justify-between gap-3" data-testid="filter-profile-conflict">
            {conflict}
            <Button variant="outline" size="sm" onClick={onReload}>Reload</Button>
          </span>
        </Alert>
      )}
      <div className="grid gap-6">
        <SettingsCard title="Settings">
          <div className="grid gap-4">
            {creating && (
              <FormField label="Name" required error={errors.name?.message}>
                <Input {...register('name')} data-testid="filter-profile-name" />
              </FormField>
            )}
            <FormField label="Format" required={pickFormat} error={errors.format?.message}>
              {pickFormat ? (
                <div data-field="format">
                  <Controller control={control} name="format" render={({ field }) => (
                    <Combobox value={field.value} onChange={field.onChange} options={FORMAT_OPTIONS} searchable={false}
                      placeholder="Choose a format" aria-label="Format" aria-invalid={!!errors.format}
                      data-testid="filter-profile-format" />
                  )} />
                </div>
              ) : (
                <p className="py-1.5 text-sm" data-testid="filter-profile-format-label">
                  {knownFormat ? FILTER_FORMAT_LABELS[knownFormat] : ''}
                </p>
              )}
              {knownFormat && (
                <p className="mt-1 text-xs text-muted-foreground">Files: {filterFormatHint(knownFormat)}</p>
              )}
            </FormField>
          </div>
        </SettingsCard>
        <SettingsCard title="Import options">
          <div data-field="options" data-guide="filter_profiles.options">
            {knownFormat ? (
              <Controller control={control} name="options" render={({ field }) => (
                <FilterOptionsEditor format={knownFormat} value={field.value} readOnly={readOnly}
                  onChange={patch => field.onChange({ ...field.value, ...patch })}
                  errors={{
                    sheets: errors.options?.sheets?.message, columns: errors.options?.columns?.message,
                    targetColumns: errors.options?.targetColumns?.message,
                    htmlAttributes: errors.options?.htmlAttributes?.message,
                    xmlInline: errors.options?.xmlInline?.message, xmlNoTranslate: errors.options?.xmlNoTranslate?.message,
                    xmlRefElements: errors.options?.xmlRefElements?.message, xmlRefAttributes: errors.options?.xmlRefAttributes?.message,
                    xmlSkip: errors.options?.xmlSkip?.message,
                  }} />
              )} />
            ) : (
              <p className="text-sm text-muted-foreground">Choose a format to see its options.</p>
            )}
          </div>
        </SettingsCard>
      </div>
    </PageContainer>
  )
}
