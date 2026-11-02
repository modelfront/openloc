// SegmentationProfilePage — one segmentation profile: its source language,
// mode, ordered rules and the two abbreviation lists, with a live preview
// split by the server's engine. A built-in is read-only (Copy makes an
// editable org profile); /file-configurations/segmentation-profiles/new
// creates one, from scratch or as a copy (?from=<id>). Owners, admins and
// project managers edit; linguists never reach the page.

import { useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query'
import { Copy, Download } from 'lucide-react'
import { toast } from 'sonner'
import {
  ApiError, createSegmentationProfile, exportSegmentationProfileSRX, getMe, getSegmentationProfile, listLanguages,
  previewSegmentation, updateSegmentationProfile,
  type SegmentationMode, type SegmentationProfile, type SegmentationRules,
} from '../../api'
import { queryKeys } from '../../lib/query-keys'
import { isManagerRole } from '../../lib/roles'
import { errorText } from '../../lib/error-text'
import { drawAttention } from '../../lib/attention'
import { emptyRules, MODE_OPTIONS, ruleIndexOf } from '../../lib/segmentation'
import { useDebounce } from '../../hooks/useDebounce'
import { useMutationWithToast } from '../../hooks/use-mutation-with-toast'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { PageContainer } from '@/components/layout/PageContainer'
import { SettingsCard } from '@/components/layout/SettingsCard'
import { EditableTitle } from '@/components/list/EditableTitle'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { ChoiceToggle } from '@/components/ui/choice-toggle'
import { Chip } from '@/components/ui/chip'
import { MultiCombobox } from '@/components/ui/combobox'
import { FormField } from '@/components/ui/form-field'
import { Input } from '@/components/ui/input'
import { TooltipProvider } from '@/components/ui/tooltip'
import { RulesTable, withKey, type KeyedRule } from '@/components/segmentation/RulesTable'
import { SAMPLE_TEXT, SegmentationPreview } from '@/components/segmentation/SegmentationPreview'
import { SourceLanguagesPicker } from '@/components/segmentation/SourceLanguagesPicker'
import { SEGMENTATION_PROFILES_PATH } from './segmentation-paths'

interface Draft {
  name: string
  /** Empty: the profile suits any language. */
  sourceLanguages: string[]
  mode: SegmentationMode
  rules: KeyedRule[]
  abbreviations: string[]
  numberAbbreviations: string[]
}

function draftOf(name: string, sourceLanguages: string[], def: SegmentationRules): Draft {
  return {
    name, sourceLanguages: [...sourceLanguages], mode: def.mode === 'paragraph' ? 'paragraph' : 'sentence',
    rules: def.rules.map(withKey), abbreviations: [...def.abbreviations], numberAbbreviations: [...def.number_abbreviations],
  }
}

function definitionOf(d: Draft): SegmentationRules {
  return {
    mode: d.mode,
    rules: d.rules.map(({ before, after, break: brk }) => ({ before, after, break: brk })),
    abbreviations: d.abbreviations,
    number_abbreviations: d.numberAbbreviations,
  }
}

/** A word as a list stores it: trimmed, without its full stop. */
const listWord = (w: string) => w.trim().replace(/\.$/, '')

export default function SegmentationProfilePage() {
  const { profileId } = useParams()
  const [params] = useSearchParams()
  const creating = profileId === undefined
  const fromId = Number(params.get('from')) || undefined
  const id = creating ? fromId : Number(profileId)

  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const profileQuery = useQuery({
    queryKey: queryKeys.segmentationProfiles.detail(id ?? 0),
    queryFn: () => getSegmentationProfile(id as number),
    enabled: !!id,
  })
  // The sample text outlives a save, which restarts the editor.
  const [sample, setSample] = useState(SAMPLE_TEXT)
  const profile = profileQuery.data

  if (id && profileQuery.isLoading) return <PageContainer><LoadingPlaceholder /></PageContainer>
  if (id && (profileQuery.error || !profile)) {
    return (
      <PageContainer>
        <Breadcrumbs items={[{ label: 'Segmentation profiles', to: SEGMENTATION_PROFILES_PATH }, { label: 'Not found' }]} />
        <LoadFailed what="segmentation profile" error={profileQuery.error} onRetry={() => profileQuery.refetch()} />
      </PageContainer>
    )
  }
  return (
    <ProfileEditor
      // A new version of the profile (a save, a reload) restarts the editor from it.
      key={`${creating ? 'new' : id}:${fromId ?? ''}:${profile?.updated_at ?? ''}`}
      profile={profile} creating={creating} fromId={fromId}
      readOnly={!isManagerRole(me?.role) || (!creating && !!profile?.builtin)} canCopy={isManagerRole(me?.role)}
      sample={sample} onSampleChange={setSample} onReload={() => profileQuery.refetch()}
    />
  )
}

function ProfileEditor({ profile, creating, fromId, readOnly, canCopy, sample, onSampleChange, onReload }: {
  profile: SegmentationProfile | undefined
  creating: boolean
  fromId: number | undefined
  readOnly: boolean
  canCopy: boolean
  sample: string
  onSampleChange: (text: string) => void
  onReload: () => void
}) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { data: languages = [] } = useQuery({ queryKey: queryKeys.languages.all, queryFn: listLanguages })
  const initial = useMemo(() => {
    if (!profile) return draftOf('', [], emptyRules())
    return draftOf(creating ? `${profile.name} (copy)` : profile.name, profile.source_languages, profile.definition)
  }, [profile, creating])
  const [draft, setDraft] = useState<Draft>(initial)
  const [conflict, setConflict] = useState(false)
  // Nothing saves until Create or Save, so errors show only from the first
  // click on it; from then on they follow the fields.
  const [showErrors, setShowErrors] = useState(false)

  const definition = useMemo(() => definitionOf(draft), [draft])
  const previewInput = useDebounce(useMemo(() => ({ rule_set: definition, text: sample }), [definition, sample]), 300)
  const previewQuery = useQuery({
    queryKey: queryKeys.segmentationProfiles.preview(previewInput),
    queryFn: () => previewSegmentation(previewInput),
    placeholderData: keepPreviousData,
    retry: false,
  })
  const ruleError = previewQuery.error instanceof ApiError && previewQuery.error.status === 400 ? previewQuery.error.message : undefined
  const shownRuleError = showErrors ? ruleError : undefined
  // Paragraph mode keeps each block whole: the rules and word lists do not
  // apply, so they are hidden (and kept). The rules show again when one of
  // them blocks a save.
  const sentenceMode = draft.mode === 'sentence'
  const showRules = sentenceMode || !!shownRuleError
  const errorIndex = ruleIndexOf(shownRuleError)

  const dirty = creating || draft.name !== initial.name || draft.sourceLanguages.join('|') !== initial.sourceLanguages.join('|') ||
    JSON.stringify(definition) !== JSON.stringify(definitionOf(initial))
  const saveMut = useMutationWithToast({
    mutationFn: (d: Draft) => {
      const input = { name: d.name, source_languages: d.sourceLanguages, definition: definitionOf(d) }
      return creating || !profile
        ? createSegmentationProfile({ ...input, from: fromId })
        : updateSegmentationProfile(profile.id, { ...input, updated_at: profile.updated_at })
    },
    successMessage: creating ? 'Segmentation profile created' : 'Segmentation profile saved',
    errorMessage: 'Failed to save the segmentation profile',
    errorToast: err => !(err instanceof ApiError && err.status === 409 && !creating),
    invalidateKeys: [queryKeys.segmentationProfiles.lists],
    // Create and Save both go back to the list.
    onSuccess: saved => {
      queryClient.setQueryData(queryKeys.segmentationProfiles.detail(saved.id), saved)
      navigate(SEGMENTATION_PROFILES_PATH)
    },
    onError: err => {
      if (err instanceof ApiError && err.status === 409 && !creating) setConflict(true)
      if (err instanceof ApiError && err.status === 400) setShowErrors(true)
    },
  })

  const patch = (p: Partial<Draft>) => setDraft(d => ({ ...d, ...p }))
  const nameError = creating && !draft.name.trim() ? 'Enter a name.' : undefined
  const save = () => {
    const first = nameError ? { target: '[name="name"]', focus: '[name="name"]' }
      : ruleError ? { target: '[data-field="rules"]', focus: '[data-field="rules"] [aria-invalid="true"]' }
        : undefined
    if (first) {
      setShowErrors(true)
      drawAttention(first)
      return
    }
    saveMut.mutate(draft)
  }
  const wordOptions = (words: string[]) => words.map(w => ({ value: w, label: w }))

  return (
    <PageContainer width="wide">
      <TooltipProvider delay={200}>
        <Breadcrumbs
          items={[
            { label: 'Segmentation profiles', to: SEGMENTATION_PROFILES_PATH },
            {
              label: creating ? 'New profile' : (
                <span className="flex min-w-0 items-center gap-2">
                  <EditableTitle name={draft.name} canEdit={!readOnly} onRename={name => patch({ name })} data-testid="segmentation-rename" />
                </span>
              ),
            },
          ]}
          trailing={!creating && profile?.builtin && <Chip>Built-in</Chip>}
          actions={(
            <div className="flex gap-2">
              {!creating && profile && (
                <Button variant="outline" onClick={() => exportSegmentationProfileSRX(profile.id, profile.name).catch(err => toast.error(`Export failed: ${errorText(err, 'Unknown error')}`))}
                  data-testid="segmentation-export">
                  <Download /> Export SRX
                </Button>
              )}
              {!creating && profile && canCopy && (
                <Button variant="outline" onClick={() => navigate(`${SEGMENTATION_PROFILES_PATH}/new?from=${profile.id}`)} data-testid="segmentation-copy">
                  <Copy /> Copy
                </Button>
              )}
              {!readOnly && (
                <Button onClick={save} disabled={saveMut.isPending || !dirty}
                  disabledReason={saveMut.isPending || dirty ? undefined : 'There are no changes to save.'} data-testid="segmentation-save">
                  {saveMut.isPending ? 'Saving…' : creating ? 'Create' : 'Save'}
                </Button>
              )}
            </div>
          )}
        />
        {!creating && profile?.builtin && (
          <p className="mb-4 text-sm text-muted-foreground" data-testid="segmentation-builtin-note">
            Built-in — copy it to make changes.{profile.description && ` ${profile.description}`}
          </p>
        )}
        {conflict && (
          <Alert tone="error" className="mb-4" title="Someone else changed this segmentation profile">
            <span className="flex items-center justify-between gap-3">
              Reload it to see their version; your changes here are not saved.
              <Button variant="outline" size="sm" onClick={onReload}>Reload</Button>
            </span>
          </Alert>
        )}
        {/* One column, with the Preview right under the Rules, until the
            screen leaves the rule patterns room beside a 400px Preview. There
            the last row takes the extra height of a long Preview, so the
            editor cards stay packed. */}
        <div className="grid grid-cols-[minmax(0,1fr)] items-start gap-6 2xl:grid-cols-[minmax(0,1fr)_400px] 2xl:grid-rows-[auto_auto_auto_1fr]">
          <SettingsCard title="Settings">
            <div className="grid gap-4">
              {creating && (
                <FormField label="Name" required error={showErrors ? nameError : undefined}>
                  <Input name="name" value={draft.name} onChange={e => patch({ name: e.target.value })} data-testid="segmentation-name" />
                </FormField>
              )}
              <FormField label="Source languages">
                {readOnly && draft.sourceLanguages.length === 0 ? <p className="py-1.5 text-sm">Any language</p> : (
                  <SourceLanguagesPicker value={draft.sourceLanguages} onChange={sourceLanguages => patch({ sourceLanguages })}
                    languages={languages} disabled={readOnly} data-testid="segmentation-languages" />
                )}
              </FormField>
              <FormField label="Mode">
                <ChoiceToggle aria-label="Mode" options={MODE_OPTIONS.map(o => ({ ...o, disabled: readOnly }))} value={draft.mode}
                  onChange={mode => patch({ mode })} data-testid="segmentation-mode" />
              </FormField>
            </div>
          </SettingsCard>
          {showRules && (
            <SettingsCard title="Rules"
              description={sentenceMode
                ? 'Tried from the top: the first rule whose two patterns match a position breaks there or keeps it whole. Patterns are regular expressions without look-around or back-references. An empty pattern matches any text.'
                : 'Paragraph mode does not use the rules, but a rule that is not valid still stops the save. Fix it or remove it.'}>
              {shownRuleError && errorIndex === null && <Alert tone="error" className="mb-3" data-testid="segmentation-error">{shownRuleError}</Alert>}
              <RulesTable rules={draft.rules} onChange={rules => patch({ rules })} readOnly={readOnly} errorIndex={errorIndex} error={shownRuleError} />
            </SettingsCard>
          )}
          <SettingsCard title="Preview" className="2xl:sticky 2xl:top-0 2xl:col-start-2 2xl:row-span-4 2xl:row-start-1">
            <SegmentationPreview text={sample} onTextChange={onSampleChange} preview={previewQuery.data} rules={definition.rules}
              error={shownRuleError ?? (previewQuery.error && !ruleError ? errorText(previewQuery.error, 'The preview failed.') : undefined)}
              loading={previewQuery.isFetching} showDecisions={sentenceMode} />
          </SettingsCard>
          {sentenceMode && (
            <>
              <SettingsCard title="Abbreviations" description='A full stop after these words does not end the sentence, as in "Dr. Smith". Capital letters must match: "Dr" does not cover "dr".'>
                <MultiCombobox value={draft.abbreviations} options={wordOptions(draft.abbreviations)} wrap disabled={readOnly}
                  placeholder="No abbreviations" searchPlaceholder="Search or add an abbreviation" createOptionLabel={'Add "{}"'}
                  onCreateOption={w => listWord(w) && patch({ abbreviations: [...new Set([...draft.abbreviations, listWord(w)])] })}
                  onChange={abbreviations => patch({ abbreviations })} aria-label="Abbreviations" data-testid="segmentation-abbreviations" />
              </SettingsCard>
              <SettingsCard title="Abbreviations before a number" description='A full stop after one of these words does not end a sentence when a number follows, as in "p. 12" or "No. 5".'>
                <MultiCombobox value={draft.numberAbbreviations} options={wordOptions(draft.numberAbbreviations)} wrap disabled={readOnly}
                  placeholder="No abbreviations" searchPlaceholder="Search or add an abbreviation" createOptionLabel={'Add "{}"'}
                  onCreateOption={w => listWord(w) && patch({ numberAbbreviations: [...new Set([...draft.numberAbbreviations, listWord(w)])] })}
                  onChange={numberAbbreviations => patch({ numberAbbreviations })} aria-label="Abbreviations before a number"
                  data-testid="segmentation-number-abbreviations" />
              </SettingsCard>
            </>
          )}
        </div>
      </TooltipProvider>
    </PageContainer>
  )
}
