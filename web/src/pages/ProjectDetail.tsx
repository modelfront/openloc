import { useRef, useState, useEffect, useCallback, useMemo } from 'react'
import { useParams, useNavigate, useLocation } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { listProjects, listTemplates, listJobs, getProjectSettings, getMe, updateProjectMetadata, listLabels, listProjectLabels, setProjectLabels, type ProjectStatus } from '../api'
import { toast } from 'sonner'
import { queryKeys } from '../lib/query-keys'
import { projectCacheKeys } from '../lib/query-invalidation'
import { useUploadQueue, type LayoutAnswer } from '../hooks/useUploadQueue'
import { usePageTitle } from '../hooks/usePageTitle'
import { isAdminRole, isManagerRole } from '../lib/roles'
import { isPipelineRunning } from '../lib/project-logic'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import { PageContainer } from '@/components/layout/PageContainer'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { EditableTitle } from '@/components/list/EditableTitle'
import ProjectSettingsTab from '../components/ProjectSettingsTab'
import { ProjectWorkflowTab } from '../components/project/ProjectWorkflowTab'
import ProjectAnalysesTab from '../components/ProjectAnalysesTab'
import { ProjectHistoryTab } from '@/components/project/ProjectHistoryTab'
import { ProjectTasksTab } from '@/components/project/task-matrix/ProjectTasksTab'
import { ProjectFilesTab } from '@/components/project/files/ProjectFilesTab'
import { ProjectLabelFields, type LabelPick } from '../components/project/ProjectLabelFields'
import { ProjectStatusSelect, STATUS_LABELS, STATUS_COLORS } from '../components/project/ProjectStatusSelect'
import { deadlineChipProps } from '../lib/deadline'
import { CreatorLabel } from '@/components/user/CreatorLabel'
import { DeadlinePicker } from '../components/ui/deadline-picker'
import { UploadConfirmDialog } from '../components/project/UploadConfirmDialog'
import ProcessingTimeDialog from '../components/project/ProcessingTimeDialog'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import { Button } from '@/components/ui/button'
import { Chip } from '@/components/ui/chip'
import { SubNav } from '@/components/layout/SubNav'
import { useOpenFilesWhenEmpty } from '@/components/project/use-open-files-when-empty'
import { LanguagePair } from '@/components/ui/language-code'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { FormField } from '@/components/ui/form-field'
import { Gauge, LayoutTemplate } from 'lucide-react'

// The project's panels, in SubNav order. `tasks` is the section's index route
// (/projects/:id); the rest hang off it as /projects/:id/<value>.
const PROJECT_TABS = [
  { value: 'files', label: 'Files' },
  { value: 'tasks', label: 'Tasks' },
  { value: 'analyses', label: 'Analyses' },
  { value: 'workflow', label: 'Workflow' },
  { value: 'settings', label: 'Settings' },
  { value: 'history', label: 'History' },
] as const
const TAB_VALUES = PROJECT_TABS.map(t => t.value)

export default function ProjectDetail() {
  const { projectId } = useParams<{ projectId: string }>()
  const pid = Number(projectId)
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const [dragging, setDragging] = useState(false)
  const dragCounter = useRef(0)
  const [pollUntil, setPollUntil] = useState(0) // timestamp — poll until this time
  // Files chosen but not yet uploaded — the confirm dialog is open for them.
  const [pendingFiles, setPendingFiles] = useState<File[] | null>(null)
  // Processing-time panel. Read-only; the query is gated on
  // `open` so a project page visit doesn't pay for timings nobody asked for.
  const [timingsOpen, setTimingsOpen] = useState(false)

  const { data: projects } = useQuery({ queryKey: queryKeys.projects.all, queryFn: listProjects })
  const project = projects?.find(p => p.id === pid)
  usePageTitle(project?.name)

  // A draft is edited in the wizard, which is where the project list links it.
  // This page cannot show one: its Status dropdown drops
  // the Draft option, so the field would render as an empty "Select…". Send a
  // pasted or bookmarked /projects/{id} to the same form.
  const isDraft = project?.status === 'draft'
  useEffect(() => {
    if (isDraft) navigate(`/projects/new?draft=${pid}`, { replace: true })
  }, [isDraft, pid, navigate])


  // Metadata inline edit. Managers get editable controls; everyone
  // else sees a read-only display. The role-load tri-state matches
  // ProjectCreate: undefined → controls disabled.
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const isAdmin: boolean | undefined = me === undefined ? undefined : isAdminRole(me?.role)
  // Managers (owner/admin/PM) do project management (rename/status/upload/assign).
  const isManager: boolean | undefined = me === undefined ? undefined : isManagerRole(me?.role)

  // Labels live on their own endpoint rather than the project payload, so the
  // card fetches them alongside the project itself.
  const { data: labels } = useQuery({ queryKey: queryKeys.labels.all, queryFn: listLabels })
  const { data: projectLabels } = useQuery({
    queryKey: queryKeys.projectLabels.byProject(pid),
    queryFn: () => listProjectLabels(pid),
  })
  const serverPicks = useMemo(
    () => (projectLabels ?? []).map(r => ({ definition_id: r.label_definition_id, value_id: r.label_value_id })),
    [projectLabels],
  )
  // Adding a label and answering it are two steps, and a row between them has
  // no value to store — so the card edits a local copy and the server's answer
  // is only the starting point. `null` means "nothing edited yet", which is
  // what lets a refetch still land before the first edit.
  const [editedPicks, setEditedPicks] = useState<LabelPick[] | null>(null)
  const labelPicks = editedPicks ?? serverPicks

  const toPayload = (picks: LabelPick[]) =>
    picks.filter(p => p.value_id != null).map(p => ({ label_definition_id: p.definition_id, label_value_id: p.value_id! }))

  const setLabelsMut = useMutationWithToast({
    mutationFn: (picks: LabelPick[]) => setProjectLabels(pid, toPayload(picks)),
    successMessage: 'Labels updated',
    errorMessage: 'Failed to update labels',
    invalidateKeys: [queryKeys.projectLabels.byProject(pid)],
  })

  const saveLabels = (next: LabelPick[]) => {
    setEditedPicks(next)
    // An unanswered row changes nothing the server holds, so it costs no
    // request — it just sits on screen waiting for a value.
    const same = JSON.stringify(toPayload(next)) === JSON.stringify(toPayload(labelPicks))
    if (!same) setLabelsMut.mutate(next)
  }

  const updateMetaMut = useMutationWithToast({
    mutationFn: (data: { name?: string; customer_id?: number | null; status?: ProjectStatus; deadline?: string | null; domain?: string }) =>
      updateProjectMetadata(pid, data),
    successMessage: 'Project updated',
    errorMessage: 'Failed to update project',
    // Every cache holding a project shape. Shared with the job-set mutations
    // that can auto-complete the project, so a cache can't be added to one
    // and missed in the other.
    invalidateKeys: projectCacheKeys(pid),
  })

  const { data: templates } = useQuery({ queryKey: queryKeys.templates.all, queryFn: listTemplates })
  const { data: projectSettings, error: settingsError, refetch: refetchSettings } = useQuery({
    queryKey: queryKeys.project.settings(pid),
    queryFn: () => getProjectSettings(pid),
  })
  const { data: jobs, isLoading, dataUpdatedAt: jobsUpdatedAt } = useQuery({
    queryKey: queryKeys.jobs.byProject(pid),
    queryFn: () => listJobs(pid),
    refetchInterval: (query) => {
      const jobList = query.state.data
      if (!jobList) return false
      // Poll while any job's pipeline is running in the background
      const anyProcessing = jobList.some(j => isPipelineRunning(j))
      // Also poll briefly after upload to catch fast-finishing workers
      const recentUpload = Date.now() < pollUntil
      return (anyProcessing || recentUpload) ? 2000 : false
    },
  })

  // Stop forced polling when timer expires
  useEffect(() => {
    if (pollUntil > 0 && Date.now() < pollUntil) {
      const timer = setTimeout(() => setPollUntil(0), pollUntil - Date.now())
      return () => clearTimeout(timer)
    }
  }, [pollUntil])

  const { progress: uploadProgress, uploading, start: startQueue } = useUploadQueue(() => {
    queryClient.invalidateQueries({ queryKey: queryKeys.jobs.byProject(pid) })
    queryClient.invalidateQueries({ queryKey: queryKeys.files.byProject(pid) })
    queryClient.invalidateQueries({ queryKey: queryKeys.projectAnalysis.v2(pid) })
    setPollUntil(Date.now() + 10000)
  })
  const startUpload = useCallback((files: File[], analyze = false, layouts?: ReadonlyMap<File, LayoutAnswer>) => {
    startQueue(pid, files, { analyze, layouts })
  }, [pid, startQueue])

  // Completed projects must not accept new files. The button is
  // disabled at the render layer; the drop-zone needs an explicit guard too,
  // otherwise dragging a file in still triggers an upload (the dimmed button
  // doesn't gate the page-wide drop handler).
  const uploadLocked = project?.status === 'completed'
  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    dragCounter.current = 0
    setDragging(false)
    if (uploadLocked) {
      toast.error('Project is completed — reopen it to upload more files')
      return
    }
    const files = e.dataTransfer.files
    if (files.length > 0) setPendingFiles(Array.from(files))
  }, [uploadLocked])

  // Only react to file drags. Internal element drags (e.g. TM reorder in the
  // Settings tab) carry text, not files — they must NOT trigger the upload
  // drop-zone overlay.
  const isFileDrag = (e: React.DragEvent) => Array.from(e.dataTransfer.types).includes('Files')

  const handleDragEnter = useCallback((e: React.DragEvent) => {
    if (!isFileDrag(e)) return
    e.preventDefault()
    dragCounter.current++
    setDragging(true)
  }, [])

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    if (!isFileDrag(e)) return
    e.preventDefault()
    dragCounter.current--
    if (dragCounter.current === 0) setDragging(false)
  }, [])

  const handleDragOver = useCallback((e: React.DragEvent) => {
    if (!isFileDrag(e)) return
    e.preventDefault()
  }, [])

  // The panel is the last path segment (/projects/:id → tasks). Each SubNav
  // item keeps the current query string, so filters survive a trip to another
  // panel and back.
  const activeTab = TAB_VALUES.find(t => location.pathname.endsWith(`/${t}`)) ?? 'tasks'
  // Tasks come from files, so a project without any has no Tasks tab to show:
  // it is disabled and the page opens on Files instead.
  const noFiles = jobs !== undefined && jobs.length === 0
  const tabItems = useMemo(
    () => PROJECT_TABS.map(({ value, label }) => ({
      to: `/projects/${pid}${value === 'tasks' ? '' : `/${value}`}${location.search}`,
      label,
      end: value === 'tasks',
      disabled: value === 'tasks' && noFiles,
      disabledReason: 'Upload a file to create tasks.',
    })),
    [pid, location.search, noFiles],
  )
  useOpenFilesWhenEmpty({ pid, noFiles, isDraft, activeTab, pathname: location.pathname, search: location.search })
  // Status for the metadata strip. A project with none reads as in progress,
  // for both the read-only chip and the manager's select.
  const metaStatus = project?.status ?? 'in_progress'

  return (
    <PageContainer
      className="relative"
      onDrop={handleDrop}
      onDragEnter={handleDragEnter}
      onDragLeave={handleDragLeave}
      onDragOver={handleDragOver}
    >
      {dragging && (
        <div className="absolute inset-0 z-50 flex items-center justify-center bg-primary/10 border-2 border-dashed border-primary rounded-xl pointer-events-none">
          <p className="text-primary text-lg font-medium">Drop files to upload</p>
        </div>
      )}

      <Breadcrumbs items={[
        { label: 'Projects', to: '/projects' },
        {
          label: (
            <EditableTitle
              name={project?.name ?? `Project ${pid}`}
              onRename={(name) => updateMetaMut.mutate({ name })}
              canEdit={!!isManager}
              data-testid="rename-project"
            />
          ),
        },
      ]} />

      <div className="mb-2 flex items-center justify-between gap-3">
        {/* Processing time is read-only diagnostics, so it stays beside the
            project's language identity. File actions live in the Files tab. */}
        <div className="flex items-center gap-1.5">
          {project ? <LanguagePair source={project.source_language} targets={project.target_languages} /> : <span />}
          {/* Manager-only, matching the endpoint's gate — it exposes
              per-engine/per-model latency. */}
          {project && isManager && (
            <Tooltip>
              <TooltipTrigger render={
                <Button variant="ghost" size="icon"
                  onClick={() => setTimingsOpen(true)}
                  aria-label="Processing time"
                  data-testid="open-processing-time"
                  className="text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-100"
                >
                  {/* Gauge, not Clock — the metadata strip below already uses
                      clock semantics for Deadline, and this is about speed. */}
                  <Gauge />
                </Button>
              } />
              <TooltipContent>Processing time</TooltipContent>
            </Tooltip>
          )}
        </div>
        {/* Project creation is manager-level, so only managers get the
            shortcut. Same deep link as the list's Duplicate action. */}
        {project && isManager && (
          <Tooltip>
            <TooltipTrigger render={
              <Button variant="outline" size="sm"
                onClick={() => navigate(`/projects/new?fromProject=${pid}`)}
                data-testid="use-as-template"
              >
                <LayoutTemplate />
                Use as template
              </Button>
            } />
            <TooltipContent>New project with the same details and settings (no files)</TooltipContent>
          </Tooltip>
        )}
      </div>

      {/* Same pill and creator as a project list row, so the lifecycle state
          reads at a glance without hunting the metadata strip (where managers
          see an edit control, not a pill). Its own row — breadcrumbs never
          share theirs. */}
      {project && (
        <div className="mb-6 flex items-center gap-3 text-xs">
          {project.status && (
            <Chip color={STATUS_COLORS[project.status]} data-testid={`project-status-badge-${project.status}`}>
              {STATUS_LABELS[project.status]}
            </Chip>
          )}
          <div className="flex items-center gap-1.5" data-testid="project-created-by">
            <span className="text-gray-400 dark:text-gray-500">Created by:</span>
            <CreatorLabel name={project.created_by_name} />
          </div>
        </div>
      )}

      <ProcessingTimeDialog projectId={pid} open={timingsOpen} onOpenChange={setTimingsOpen} />

      {/* Metadata strip — visible across all tabs. Managers get the
          edit controls, disabled while `me` loads (so they never flash the
          read-only path) and during in-flight updates; everyone else sees
          read-only chips. */}
      {project && (
        <div className="bg-card rounded-lg border border-gray-200 dark:border-gray-800 p-4 mb-6 grid grid-cols-1 sm:grid-cols-2 gap-4" data-testid="project-metadata-strip">
          {/* Classification first, then where the project stands — the same
              order the New project form uses. */}
          {/* Customer and Domain are built-in labels, so the whole
              classification comes from here. Cells of this grid, so they line
              up with the fields around them. */}
          <ProjectLabelFields
            labels={labels ?? []}
            value={labelPicks}
            onChange={saveLabels}
            disabled={!isManager || setLabelsMut.isPending}
          />
          {/* Splits the card into what the project IS (domain, customer,
              labels) and where it stands (deadline, status). Spanning both
              columns also keeps Deadline + Status a pair whatever the label
              count is — without a full-width row between them, an odd number
              of labels pushes Deadline into the second column and strands
              Status on a row of its own. */}
          <div className="sm:col-span-2 border-t border-gray-200 dark:border-gray-800" />
          {/* Deadline — same manager/readonly split as the labels and status.
              Managers get the picker (saves once, when it closes); everyone
              else sees the deadline chip. */}
          <FormField label="Deadline" className="min-w-0">
          <div data-testid="project-deadline-strip">
            {isManager === false ? (
              <span data-testid="project-deadline-readonly">
                <Chip {...deadlineChipProps(project.deadline, { done: project.status === 'completed' })} />
              </span>
            ) : (
              <DeadlinePicker
                variant="field"
                value={project.deadline}
                onChange={deadline => updateMetaMut.mutate({ deadline })}
                disabled={updateMetaMut.isPending || isManager === undefined}
                aria-label="Project deadline"
                data-testid="project-deadline-edit"
                className="w-full"
              />
            )}
          </div>
          </FormField>
          <FormField label="Status" className="min-w-0">
          <div>
            {isManager === false ? (
              <Chip color={STATUS_COLORS[metaStatus]} data-testid={`project-status-badge-${metaStatus}`}>
                {STATUS_LABELS[metaStatus]}
              </Chip>
            ) : (
              <ProjectStatusSelect
                value={metaStatus}
                onChange={(s) => updateMetaMut.mutate({ status: s })}
                disabled={updateMetaMut.isPending || isManager === undefined}
                className="w-full"
              />
            )}
          </div>
          </FormField>
        </div>
      )}

      <SubNav className="mb-4" items={tabItems} dataGuide="project.tabs" />

      <div data-testid="project-panel">
      {activeTab === 'files' && (
        isLoading ? <LoadingPlaceholder /> : (
          <div data-guide="project.files">
            <ProjectFilesTab
              projectId={pid}
              jobs={jobs ?? []}
              isAdmin={isAdmin === true}
              uploadLocked={uploadLocked}
              uploading={uploading}
              uploadLabel={uploadProgress
                ? `Uploading ${uploadProgress.current}/${uploadProgress.total}${uploadProgress.pct > 0 ? ` (${uploadProgress.pct}%)` : '...'}`
                : 'Upload files'}
              // The deadlines dialog opens first; the upload runs on confirm.
              onPickFiles={setPendingFiles}
            />
          </div>
        )
      )}

      {activeTab === 'tasks' && (
        isLoading ? <LoadingPlaceholder /> : (
          <ProjectTasksTab
            projectId={pid}
            jobs={jobs ?? []}
            jobsUpdatedAt={jobsUpdatedAt}
            isManager={isManager === true}
          />
        )
      )}

      {activeTab === 'analyses' && (
        /* Saved analyses. The live job-level
           view is the CAT editor's status bar; this panel is the only
           project-level surface. */
        <ProjectAnalysesTab projectId={pid} />
      )}

      {activeTab === 'workflow' && (
        <ProjectWorkflowTab
          projectId={pid}
          settings={projectSettings ?? null}
          error={settingsError}
          onRefresh={() => {
            refetchSettings()
            queryClient.invalidateQueries({ queryKey: queryKeys.projects.all })
          }}
        />
      )}

      {activeTab === 'settings' && (
        <ProjectSettingsTab
          projectId={pid}
          settings={projectSettings ?? null}
          error={settingsError}
          templates={templates ?? []}
          onRefresh={() => {
            refetchSettings()
            queryClient.invalidateQueries({ queryKey: queryKeys.projects.all })
          }}
        />
      )}

      {activeTab === 'history' && (
        /* Project audit log. Refetches on mount, so no
           invalidation wiring is needed across the mutation sites. */
        <ProjectHistoryTab projectId={pid} />
      )}
      </div>

      {project && (
        <UploadConfirmDialog
          projectId={pid}
          files={pendingFiles}
          onOpenChange={(next) => { if (!next) setPendingFiles(null) }}
          onConfirm={(analyze, layouts) => {
            const files = pendingFiles
            setPendingFiles(null)
            if (files) startUpload(files, analyze, layouts)
          }}
        />
      )}
    </PageContainer>
  )
}
