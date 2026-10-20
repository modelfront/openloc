import { useLocation, useNavigate, useParams } from 'react-router-dom'
import { useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ApiError,
  getTemplate,
  updateTemplate,
  listTMCollections,
  listMTRouters,
  listTermBases,
  listQAProfiles,
  listLanguages,
  setTemplateTMAssignments,
  setTemplateTermAssignments,
  listTMProfiles,
  detachTemplateTMProfile,
  saveTemplateTMProfile,
  applyTemplateWorkflow,
  listLabels,
  listTemplateLabels,
  setTemplateLabels,
} from '../api'
import { toast } from 'sonner'
import { queryKeys } from '../lib/query-keys'
import { workflowStepsFromApi } from '../lib/workflow-steps'
import { WorkflowTab } from '../components/workflow/WorkflowTab'
import { focusMTRouter } from '../components/workflow/RawMTPane'
import { EditableTitle } from '@/components/list/EditableTitle'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { PageContainer } from '@/components/layout/PageContainer'
import { EmptyState } from '@/components/layout/EmptyState'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { SubNav } from '@/components/layout/SubNav'
import { TemplateMetadataForm, type TemplateMetadata } from '../components/template/TemplateMetadataForm'
import { type LabelPick } from '../components/project/ProjectLabelFields'
import { SettingsEditor } from '../components/SettingsEditor'
import { DEFAULT_AUTO_COMPLETE, DEFAULT_INLINE_HTML, DEFAULT_PENALTY_PCT, toTMLookupOrder } from '@/lib/settings-values'
import { usePageTitle } from '../hooks/usePageTitle'
import { errorText } from '@/lib/error-text'

export default function TemplateDetail() {
  const { templateId } = useParams<{ templateId: string }>()
  const tid = Number(templateId)
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: queryKeys.template.detail(tid),
    queryFn: () => getTemplate(tid),
  })
  usePageTitle(data?.template.name)

  const { data: tms } = useQuery({ queryKey: queryKeys.tms.all, queryFn: listTMCollections })
  const { data: mtRouters } = useQuery({ queryKey: queryKeys.mtRouters.all, queryFn: listMTRouters })
  const { data: termBases } = useQuery({ queryKey: queryKeys.terminology.all, queryFn: listTermBases })
  const { data: qaProfiles } = useQuery({ queryKey: queryKeys.qaProfiles.all, queryFn: listQAProfiles })
  const { data: tmProfiles } = useQuery({ queryKey: queryKeys.tmProfiles.all, queryFn: listTMProfiles })
  const { data: languages } = useQuery({ queryKey: queryKeys.languages.all, queryFn: listLanguages })

  // Settings is the index route (/templates/:id); Workflow is /templates/:id/workflow.
  const { pathname } = useLocation()
  const activeTab = pathname.endsWith('/workflow') ? 'workflow' : 'settings'
  const tabItems = useMemo(() => [
    { to: `/templates/${tid}`, label: 'Settings', end: true },
    { to: `/templates/${tid}/workflow`, label: 'Workflow' },
  ], [tid])

  const handleRename = async (newName: string) => {
    if (!data) return
    try {
      await updateTemplate(tid, {
        name: newName,
        mt_on_upload: data.template.mt_on_upload,
        tm_on_upload: data.template.tm_on_upload,
        qa_profile_id: data.template.qa_profile_id,
      })
      queryClient.invalidateQueries({ queryKey: queryKeys.template.detail(tid) })
      queryClient.invalidateQueries({ queryKey: queryKeys.templates.all })
      toast.success('Template renamed')
    } catch (err) {
      toast.error(`Rename failed: ${errorText(err, String(err))}`)
    }
  }

  // The template's label presets: their own endpoint, like a project's, and
  // the same two-step edit — a row added but not yet answered stays local.
  const { data: labels } = useQuery({ queryKey: queryKeys.labels.all, queryFn: listLabels })
  const { data: templateLabels } = useQuery({
    queryKey: queryKeys.templateLabels.byTemplate(tid),
    queryFn: () => listTemplateLabels(tid),
  })
  const serverPicks = useMemo(
    () => (templateLabels ?? []).map(r => ({ definition_id: r.label_definition_id, value_id: r.label_value_id })),
    [templateLabels],
  )
  const [editedPicks, setEditedPicks] = useState<LabelPick[] | null>(null)
  const labelPicks = editedPicks ?? serverPicks
  const toPayload = (picks: LabelPick[]) =>
    picks.filter(p => p.value_id != null).map(p => ({ label_definition_id: p.definition_id, label_value_id: p.value_id! }))
  const saveLabels = async (next: LabelPick[]) => {
    setEditedPicks(next)
    if (JSON.stringify(toPayload(next)) === JSON.stringify(toPayload(labelPicks))) return
    try {
      await setTemplateLabels(tid, toPayload(next))
      queryClient.invalidateQueries({ queryKey: queryKeys.templateLabels.byTemplate(tid) })
      toast.success('Template updated')
    } catch (err) {
      toast.error(`Update failed: ${errorText(err, String(err))}`)
    }
  }

  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: queryKeys.template.detail(tid) })
    queryClient.invalidateQueries({ queryKey: queryKeys.templates.all })
  }
  const afterTMProfileChange = () => {
    queryClient.invalidateQueries({ queryKey: queryKeys.tmProfiles.all })
    refresh()
  }
  // Project name, languages, customer + domain: a project made from the
  // template starts with them. Each field saves as it changes, like the rest
  // of the page; a refused change (e.g. a target equal to the source) shows
  // the server's reason and the refetch puts the stored value back.
  const saveMetadata = async (patch: Partial<TemplateMetadata>) => {
    if (!data) return
    try {
      await updateTemplate(tid, {
        name: data.template.name,
        mt_on_upload: data.template.mt_on_upload,
        tm_on_upload: data.template.tm_on_upload,
        ...patch,
      })
      toast.success('Template updated')
    } catch (err) {
      toast.error(`Update failed: ${errorText(err, String(err))}`)
    } finally {
      refresh()
    }
  }

  if (isLoading) return <PageContainer><LoadingPlaceholder /></PageContainer>
  if (!data) {
    return (
      <PageContainer>
        {error && !(error instanceof ApiError && error.status === 404)
          ? <LoadFailed what="template" error={error} onRetry={() => refetch()} />
          : <EmptyState message="Template not found." />}
      </PageContainer>
    )
  }

  const t = data.template

  return (
    <PageContainer>
      <Breadcrumbs items={[
        { label: 'Templates', to: '/templates' },
        { label: <EditableTitle name={t.name} onRename={handleRename} data-testid="rename-template" /> },
      ]} />

      <SubNav className="mb-4" items={tabItems} dataGuide="template.tabs" />

      {activeTab === 'workflow' ? (
        <WorkflowTab
          steps={workflowStepsFromApi(data.workflow_steps ?? [])}
          workflowId={t.workflow_id}
          workflowName={t.workflow_name}
          mtRouterName={t.mt_router_name}
          mtOnUpload={t.mt_on_upload}
          onPickRouter={() => { navigate(`/templates/${tid}`); focusMTRouter() }}
          applyWarning="Projects already created from this template are not affected."
          onApplyWorkflow={async (workflowId) => {
            await applyTemplateWorkflow(tid, workflowId)
            refresh()
          }}
        />
      ) : (
        <div data-guide="template.settings">
          <SettingsEditor
            mode="edit"
            projectFields={
              <TemplateMetadataForm
                value={{
                  project_name: t.project_name ?? '', source_language: t.source_language ?? '', target_languages: t.target_languages ?? [],
                }}
                languages={languages ?? []}
                onChange={saveMetadata}
                labels={labels ?? []}
                labelPicks={labelPicks}
                onLabelsChange={saveLabels}
              />
            }
            values={{
              fuzzyThreshold: t.fuzzy_threshold,
              sublanguagePenaltyPct: t.sublanguage_penalty_pct ?? DEFAULT_PENALTY_PCT,
              nearExactPenaltyPct: t.near_exact_penalty_pct ?? DEFAULT_PENALTY_PCT,
              tmLookupOrder: toTMLookupOrder(t.tm_lookup_order),
              mtOnUpload: t.mt_on_upload,
              tmOnUpload: t.tm_on_upload,
              autoComplete: t.auto_complete ?? DEFAULT_AUTO_COMPLETE,
              inlineHTML: t.inline_html ?? DEFAULT_INLINE_HTML,
              qaProfileId: t.qa_profile_id ?? null,
              tmProfileId: data.tm_profile?.id ?? null,
              tmProfileName: data.tm_profile?.name,
              tmProfileUsageCount: data.tm_profile?.usage_count,
              tmAssignments: (data.tm_assignments ?? []).map(a => ({
                id: a.id,
                tmId: a.tm_id,
                mode: a.mode,
                priority: a.priority,
                penaltyPct: a.penalty_pct ?? 0,
              })),
              mtRouterId: t.mt_router_id ?? null,
              mtRouterName: t.mt_router_name ?? undefined,
              termAssignments: (data.term_assignments ?? []).map(a => ({
                id: a.id,
                resourceId: a.term_base_id,
                resourceName: termBases?.find(g => g.id === a.term_base_id)?.name ?? `Term base #${a.term_base_id}`,
                sourceLang: a.source_language,
                targetLang: a.target_language,
                mode: a.mode,
                priority: a.priority,
              })),
              workflowSteps: workflowStepsFromApi(data.workflow_steps ?? []),
            }}
            tmCollections={tms?.map(tm => ({ id: tm.id, name: tm.name, source_language: tm.source_language, target_languages: tm.target_languages })) ?? []}
            mtRouters={mtRouters ?? []}
            routerCoverageLanguages={t.target_languages ?? []}
            termBases={termBases?.map(g => ({ id: g.id, name: g.name, source_language: g.source_language, target_languages: g.target_languages ?? [] })) ?? []}
            qaProfiles={qaProfiles?.map(p => ({ id: p.id, name: p.name })) ?? []}
            languages={languages}
            tmProfiles={tmProfiles?.map(p => ({ id: p.id, name: p.name })) ?? []}
            entity="template"
            onUpdateScalars={async (scalarData) => {
              await updateTemplate(tid, {
                name: t.name,
                mt_on_upload: scalarData.mt_on_upload ?? t.mt_on_upload,
                tm_on_upload: scalarData.tm_on_upload ?? t.tm_on_upload,
                fuzzy_threshold: scalarData.fuzzy_threshold,
                sublanguage_penalty_pct: scalarData.sublanguage_penalty_pct,
                near_exact_penalty_pct: scalarData.near_exact_penalty_pct,
                tm_lookup_order: scalarData.tm_lookup_order,
                auto_complete: scalarData.auto_complete,
                inline_html: scalarData.inline_html,
                qa_profile_id: scalarData.qa_profile_id !== undefined ? scalarData.qa_profile_id : t.qa_profile_id,
                mt_router_id: scalarData.mt_router_id,
              })
              refresh()
            }}
            onSaveTMAssignments={async (items) => {
              await setTemplateTMAssignments(tid, items.map(i => ({
                tm_id: i.tmId, mode: i.mode, priority: i.priority, penalty_pct: i.penaltyPct ?? 0,
              })))
              refresh()
            }}
            onLinkTMProfile={async (id) => {
              await updateTemplate(tid, { name: t.name, mt_on_upload: t.mt_on_upload, tm_on_upload: t.tm_on_upload, tm_profile_id: id })
              afterTMProfileChange()
            }}
            onDetachTMProfile={async () => {
              await detachTemplateTMProfile(tid)
              afterTMProfileChange()
            }}
            onSaveTMProfile={async (name, description, link) => {
              await saveTemplateTMProfile(tid, { name, description, link })
              afterTMProfileChange()
            }}
            onSaveTermAssignments={async (items) => {
              await setTemplateTermAssignments(tid, items.map(i => ({
                term_base_id: i.resourceId, source_language: i.sourceLang, target_language: i.targetLang,
                mode: i.mode ?? 'read', priority: i.priority ?? 0,
              })))
              refresh()
            }}
          />
        </div>
      )}
    </PageContainer>
  )
}
