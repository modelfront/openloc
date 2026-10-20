import { useQuery, useQueryClient } from '@tanstack/react-query'
import { listTMCollections, listMTRouters, listTermBases, listQAProfiles, listLanguages, updateProjectSettings, setProjectTMAssignments, setProjectTermAssignments, listTMProfiles, detachProjectTMProfile, saveProjectTMProfile, type ProjectSettings } from '../api'
import { queryKeys } from '../lib/query-keys'
import { LoadFailed } from '@/components/layout/LoadFailed'
import { projectSettingsToValues } from '../lib/settings-values'
import { SettingsEditor } from '../components/SettingsEditor'

function ProjectSettingsTab({ projectId, settings, error, templates, onRefresh }: {
  projectId: number
  /** The settings query's error, when it failed (a deleted project, a 500). */
  error?: unknown
  settings: ProjectSettings | null
  templates: import('../api').Template[]
  onRefresh: () => void
}) {
  const queryClient = useQueryClient()

  const { data: tmCollections } = useQuery({ queryKey: queryKeys.tms.all, queryFn: listTMCollections })
  const { data: mtRouters } = useQuery({ queryKey: queryKeys.mtRouters.all, queryFn: listMTRouters })
  const { data: termBases } = useQuery({ queryKey: queryKeys.terminology.all, queryFn: listTermBases })
  const { data: qaProfiles } = useQuery({ queryKey: queryKeys.qaProfiles.all, queryFn: listQAProfiles })
  const { data: tmProfiles } = useQuery({ queryKey: queryKeys.tmProfiles.all, queryFn: listTMProfiles })
  const { data: languages } = useQuery({ queryKey: queryKeys.languages.all, queryFn: listLanguages })

  const project = settings?.project

  // Link / detach / save-as change what the pipeline reads AND the profile's
  // usage count, so both caches go stale together with the settings.
  const afterTMProfileChange = () => {
    queryClient.invalidateQueries({ queryKey: queryKeys.tmProfiles.all })
    queryClient.invalidateQueries({ queryKey: queryKeys.tmMatches.all })
    onRefresh()
  }

  if (!settings) {
    if (error) return <LoadFailed what="project settings" error={error} onRetry={onRefresh} />
    return <p className="text-gray-500 dark:text-gray-400 py-8 text-center">Loading settings...</p>
  }

  return (
    <div className="space-y-6" data-guide="project_settings.editor">
      {/* Templates are create-time-only: the project shows where its setup
          came from, and there is no post-create apply. A template is made
          from the New project form, not from here. */}
      {project?.template_id && (
        <p className="text-xs text-gray-400 dark:text-gray-500" data-testid="based-on-template">
          Based on template “{templates.find(t => t.id === project.template_id)?.name ?? `#${project.template_id}`}”
        </p>
      )}

      {/* Unified Settings Editor */}
      <SettingsEditor
        mode="edit"
        values={projectSettingsToValues(settings, termBases)}
        tmCollections={tmCollections?.map(t => ({ id: t.id, name: t.name, source_language: t.source_language, target_languages: t.target_languages })) ?? []}
        mtRouters={mtRouters ?? []}
        termBases={termBases?.map(t => ({ id: t.id, name: t.name, source_language: t.source_language, target_languages: t.target_languages ?? [] })) ?? []}
        qaProfiles={qaProfiles?.map(p => ({ id: p.id, name: p.name })) ?? []}
        tmProfiles={tmProfiles?.map(p => ({ id: p.id, name: p.name })) ?? []}
        languages={languages}
        sourceLanguage={project?.source_language}
        targetLanguages={project?.target_languages}
        onUpdateScalars={async (data) => {
          await updateProjectSettings(projectId, data)
          onRefresh()
        }}
        onSaveTMAssignments={async (items) => {
          await setProjectTMAssignments(projectId, items.map(i => ({
            tm_id: i.tmId, mode: i.mode, priority: i.priority, penalty_pct: i.penaltyPct ?? 0,
          })))
          onRefresh()
        }}
        onLinkTMProfile={async (id) => {
          await updateProjectSettings(projectId, { tm_profile_id: id })
          afterTMProfileChange()
        }}
        onDetachTMProfile={async () => {
          await detachProjectTMProfile(projectId)
          afterTMProfileChange()
        }}
        onSaveTMProfile={async (name, description, link) => {
          await saveProjectTMProfile(projectId, { name, description, link })
          afterTMProfileChange()
        }}
        onSaveTermAssignments={async (items) => {
          await setProjectTermAssignments(projectId, items.map(i => ({
            term_base_id: i.resourceId, source_language: i.sourceLang, target_language: i.targetLang,
            mode: i.mode ?? 'read', priority: i.priority ?? 0,
          })))
          onRefresh()
        }}
        // Workflow steps live on the project's Workflow tab.
      />
    </div>
  )
}

export default ProjectSettingsTab
