// DebugPage — the living component gallery (/debug).
//
// A hidden, always-on route (not linked in the nav) that renders every shared
// UI primitive and app composite in one place. Two jobs:
//   1. Surface drift — when the same concept is implemented three ways, you see
//      the three shapes next to each other here and can converge them.
//   2. Standardization workbench — tune a component once, in real theme tokens
//      (toggle light/dark via the header), and every page that uses it follows.
//
// Convention: when a bespoke bit of styling gets promoted to a named
// component, it lands in components/ui (or a feature folder) AND gets a
// section here. This page is the ratchet that keeps the
// named components in use instead of inline Tailwind creeping back.

import { useState, type ReactNode } from 'react'
import { toast } from 'sonner'
import { Settings, LogOut, Building2, ChevronDown, Search, SlidersHorizontal, Check, LineChart, BarChart3, PieChart, Undo2, Workflow, Languages, Mail, TriangleAlert, FileText, FileSpreadsheet } from 'lucide-react'
import { Alert } from '@/components/ui/alert'
import { Progress } from '@/components/ui/progress'
import { SettingsCard } from '@/components/layout/SettingsCard'
import { QueryBoundary } from '@/components/layout/QueryBoundary'
import { EditorSidebarSection } from '@/components/EditorSidebarSection'
import { Button } from '@/components/ui/button'
import { DeleteButton } from '@/components/ui/delete-button'
import { DownloadOptionsMenu, type DownloadOption } from '@/components/ui/download-options-menu'
import { PinButton } from '@/components/charts/PinButton'
import { CopyLinkButton } from '@/components/ui/copy-link-button'
import { AttachmentsField } from '@/components/issues/AttachmentsField'
import { addAttachments } from '@/components/issues/attachments'
import { IssueTextField } from '@/components/issues/IssueTextField'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Combobox, MultiCombobox, createAction } from '@/components/ui/combobox'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { ChoiceToggle } from '@/components/ui/choice-toggle'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { Chip, ChipButton, chipStripeClass, chipSurfaceClass, type ChipColor } from '@/components/ui/chip'
import { AIStepChip, HumanStepChip, HybridStepChip, PretranslateStepChip } from '@/components/ui/chip-presets'
import { CountMeta } from '@/components/ui/count-meta'
import { DotSeparator } from '@/components/ui/dot-separator'
import { VerticalDivider } from '@/components/ui/vertical-divider'
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { DataTable, type ColumnDef } from '@/components/ui/data-table'
import { AddFilterMenu, FilterChip, FilterOption, InlineRenameField, InlineNameField, AddItemButton, ClearFiltersButton, LoadMoreButton } from '@/components/list'
import { FormField } from '@/components/ui/form-field'
import { CheckboxField } from '@/components/ui/checkbox-field'
import { SearchOptionsFields, SearchOptionsPanel, SearchOptionsToggle } from '@/components/SearchOptions'
import { searchOptionsActive, type SearchScope, type TextSearchOptions } from '@/lib/text-search'
import { EmptyState } from '@/components/layout/EmptyState'
import { PageHeader } from '@/components/layout/PageHeader'
import { Wordmark } from '@/components/layout/Wordmark'
import { PageContainer } from '@/components/layout/PageContainer'
import { LoadingPlaceholder } from '@/components/layout/LoadingPlaceholder'
import ConfirmDialog from '@/components/ConfirmDialog'
import { WorkflowStepsWithDeadlines } from '@/components/workflow/WorkflowDeadlines'
import { DeadlinePicker } from '@/components/ui/deadline-picker'
import type { WorkflowStep as WorkflowDeadlineStep } from '@/components/WorkflowStepsEditor'
import { DateTimePicker } from '@/components/ui/date-picker'
import { DateTimePickerDemo } from './debug/DatePickerDemos'
import { LanguageCode, LanguagePair } from '@/components/ui/language-code'
import { ReorderIcon } from '@/components/ui/reorder-icon'
import { DragHandle } from '@/components/ui/drag-handle'
import { Popover, PopoverContent, PopoverDescription, PopoverTitle, PopoverTrigger } from '@/components/ui/popover'
import { VirtualDataTable } from '@/components/ui/virtual-data-table'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { ColumnResizeHandle } from '@/components/ui/column-resize-handle'
import { EditableTitle } from '@/components/list/EditableTitle'
import { CustomerLabel } from '@/components/customer/CustomerLabel'
import { SafeHTML } from '@/components/SafeHTML'
import ConflictBanner from '@/components/ConflictBanner'
import { TaskActions, TaskDeadlineChip, TaskNotes, TaskStepChip } from '@/components/TaskControls'
import type { JobStepAssignment } from '@/api'
import type { TaskMode, TooltipJob } from '@/lib/current-task'
import { SessionBanner, SessionBannerAction } from '@/components/layout/SessionBanner'
import { SlimCollapsibleSection } from '@/components/layout/SlimCollapsibleSection'
import { DisclosureToggle } from '@/components/ui/disclosure-toggle'
import { SubNav } from '@/components/layout/SubNav'
import { SelectionBar, SelectionBarDivider } from '@/components/list/SelectionBar'
import { SelectAllCheckbox } from '@/components/list/SelectAllCheckbox'
import { listRowClass, listCardClass } from '@/components/list/list-row'
import { EditPanel } from '@/components/ui/edit-panel'
import { SectionInfo } from '@/components/ui/section-info'
import { ProviderModelChip } from '@/components/ai/ProviderModelChip'
import { ProviderTile, ProviderTileRow } from '@/components/ai/ProviderTile'
import { useForm, useWatch } from 'react-hook-form'
import { usePageTitle } from '../hooks/usePageTitle'
import { TextLink } from '@/components/ui/text-link'
import { SaveStatus } from '@/components/ui/save-status'
import { useSavedFlash } from '@/hooks/useSavedFlash'
import { WorkflowStepChips } from '@/components/workflow/WorkflowStepChips'
import { MTRouterLink } from '@/components/workflow/RawMTPane'
import { SpecChart } from '@/components/charts/SpecChart'
import type { ChartSpec } from '@/api'

const GALLERY_PIE: ChartSpec = {
  type: 'pie', x_key: 'target_language', series: ['words'],
  data: [{ target_language: 'de', words: 274_000 }, { target_language: 'fr', words: 131_000 }, { target_language: 'es', words: 52_000 }, { target_language: '', words: 4_000 }],
}

const GALLERY_BARS: ChartSpec = {
  type: 'bar', x_key: 'week', series: ['words'], series_key: 'origin',
  data: ['2026-09-21', '2026-09-28', '2026-10-05', '2026-10-12'].flatMap((week, i) => [
    { week, origin: 'mt', words: 30_000 + i * 8_000 },
    { week, origin: 'tm', words: 12_000 + i * 2_000 },
    { week, origin: 'file_import', words: 4_000 },
  ]),
}

// --- Local gallery scaffolding (only used on this page) ---

const DEBUG_DOWNLOAD_OPTIONS: DownloadOption<'csv' | 'xlsx'>[] = [
  { id: 'xlsx', label: 'Excel (XLSX)', hint: 'One block per language', icon: FileSpreadsheet },
  { id: 'csv', label: 'CSV', hint: 'Plain table, for import', icon: FileText, disabled: true },
]

const DEBUG_DOWNLOAD_OPTIONS_NO_HINTS = DEBUG_DOWNLOAD_OPTIONS.map(({ hint: _hint, ...o }) => o)

function Section({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
  return (
    <section className="border-b border-gray-200 dark:border-gray-800 py-8 last:border-b-0">
      <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{title}</h2>
      {description && <p className="mt-1 text-sm text-gray-500 dark:text-gray-400 max-w-2xl">{description}</p>}
      <div className="mt-4 space-y-6">{children}</div>
    </section>
  )
}

// A labeled group nested inside a Section — for a customized variant of a
// component (DeleteButton under Button) or a facet of one (the chip
// sub-sections: samples, pre-defined, composed).
function SubSection({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
  return (
    <div className="border-l-2 border-gray-200 pl-4 dark:border-gray-800">
      <h3 className="text-sm font-semibold text-gray-700 dark:text-gray-300">{title}</h3>
      {description && <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400 max-w-2xl">{description}</p>}
      <div className="mt-3 space-y-6">{children}</div>
    </div>
  )
}

/** SaveStatus through a pretend save: one second of Saving..., then Saved ✓. */
function SaveStatusDemo() {
  const [saving, setSaving] = useState(false)
  const [saved, flashSaved] = useSavedFlash()
  const save = () => {
    setSaving(true)
    setTimeout(() => { setSaving(false); flashSaved() }, 1000)
  }
  return (
    <span className="flex items-center gap-3">
      <Button variant="outline" size="sm" onClick={save} disabled={saving}>Change a field</Button>
      <SaveStatus saving={saving} saved={saved} />
    </span>
  )
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
      <div className="w-40 shrink-0 text-xs font-mono uppercase tracking-wide text-gray-400 dark:text-gray-500">
        {label}
      </div>
      <div className="flex flex-wrap items-center gap-3">{children}</div>
    </div>
  )
}

const BUTTON_VARIANTS = ['default', 'outline', 'ghost', 'destructive', 'success', 'final', 'success-outline', 'warning-outline'] as const
const BUTTON_SIZES = ['xs', 'sm', 'default'] as const
const CHIP_COLORS: ChipColor[] = ['grey', 'blue', 'yellow', 'green', 'red']

// InlineRenameField is the always-editing input used for rename-in-place in list
// rows. The gallery keeps its own value so you can type in it.
// WorkflowStepChips with `selection`: the Reject dialog's step picker.
function SelectableStepsDemo() {
  const [value, setValue] = useState<number | null>(1)
  return (
    <WorkflowStepChips
      steps={[
        { name: 'Translation', step_type: 'human', step_definition_id: 2 },
        { name: 'Check', step_type: 'hybrid', step_definition_id: 3, ai_router_id: 1, ai_router_name: 'Default QE+APE router' },
        { name: 'Review', step_type: 'human', step_definition_id: 4 },
      ]}
      selection={{
        value,
        onChange: setValue,
        blockReason: i => (i === 2 ? 'The current step' : undefined),
        'aria-label': 'Step',
      }}
      trailing={i => <Chip color={i === 2 ? 'yellow' : 'green'} className="ml-auto">{i === 2 ? 'In progress' : 'Completed'}</Chip>}
    />
  )
}

function InlineRenameDemo() {
  const [value, setValue] = useState('Rename me')
  return (
    <InlineRenameField value={value} onChange={setValue} onSubmit={() => {}} onCancel={() => {}} />
  )
}

// Engine demos. The list carries chips and one pinned action so the full
// anatomy is visible in one open: search, action + divider, selection pinned
// above a divider, then the rest.
const DEMO_ENGINES = [
  { value: 'deepl', label: 'DeepL', chip: 'MT' },
  { value: 'google', label: 'Google Translate', chip: 'MT' },
  { value: 'gpt', label: 'Default AI MT engine', chip: 'gpt-4o' },
  { value: 'libre', label: 'LibreTranslate', chip: 'test' },
]

function EngineComboboxDemo({ clearable, searchable }: { clearable?: boolean; searchable?: boolean }) {
  const [value, setValue] = useState('google')
  return <Combobox value={value} onChange={setValue} options={DEMO_ENGINES} clearable={clearable} searchable={searchable} placeholder="None" className="w-64" />
}

// triggerDisplay="chip" — the field shows the pick's chip and drops its label,
// for a list whose chip is the answer. The rows keep both.
function EngineChipTriggerDemo() {
  const [value, setValue] = useState('gpt')
  return (
    <Combobox value={value} onChange={setValue} options={DEMO_ENGINES} triggerDisplay="chip" className="w-64" />
  )
}

function EngineActionsDemo() {
  const [value, setValue] = useState('')
  return (
    <Combobox
      value={value}
      onChange={setValue}
      options={DEMO_ENGINES}
      // A plain pinned action, and createAction — the "+ Create new…" flow that
      // resolves to an id and selects it (null here, so nothing moves).
      actions={[
        { key: 'new-engine', label: 'New MT engine', onSelect: () => {} },
        createAction('Create new engine…', async () => null, () => {}),
      ]}
      placeholder="Select engine"
      className="w-64"
    />
  )
}

// Seeded past the field width so the "+N" overflow badge is visible; toggle
// rows in the dropdown to watch chips and the badge trade places.
// `wrap`: chips flow onto further lines and the field grows, instead of the
// default fixed row that collapses the overflow into "+N".
function EngineMultiDemo({ wrap = false }: { wrap?: boolean }) {
  const [value, setValue] = useState<string[]>(['deepl', 'google', 'libre'])
  return (
    <MultiCombobox
      value={value}
      onChange={setValue}
      options={DEMO_ENGINES}
      placeholder="Add engines"
      className="w-72"
      wrap={wrap}
    />
  )
}


// Dialog is used controlled across the app (open + onOpenChange), opened from an
// external button — mirrored here.
function DialogDemo() {
  const [open, setOpen] = useState<'fixed' | 'draggable' | null>(null)
  const draggable = open === 'draggable'
  return (
    <>
      <Button variant="outline" onClick={() => setOpen('fixed')}>Open dialog</Button>
      <Button variant="outline" onClick={() => setOpen('draggable')}>Open draggable dialog</Button>
      <Dialog open={open !== null} onOpenChange={v => !v && setOpen(null)}>
        <DialogContent draggable={draggable} className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Create translation memory</DialogTitle>
            <DialogDescription>
              {draggable ? 'Drag the header to move this dialog.' : 'Give it a name — you can add entries later.'}
            </DialogDescription>
          </DialogHeader>
          <div className="py-2">
            <FormField label="Name" required>
              <Input placeholder="e.g. Marketing TM" />
            </FormField>
          </div>
          <DialogFooter className="mt-4">
            <Button variant="outline" onClick={() => setOpen(null)}>Cancel</Button>
            <Button onClick={() => setOpen(null)}>Create</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

// DataTable demo — a tiny static terminology-shaped dataset.
type DemoTerm = { term: string; translation: string; status: string }
const DEMO_TABLE_COLUMNS: ColumnDef<DemoTerm>[] = [
  { accessorKey: 'term', header: 'Term' },
  { accessorKey: 'translation', header: 'Translation' },
  {
    accessorKey: 'status',
    header: 'Status',
    cell: ({ row }) => (
      <Chip color={row.original.status === 'Approved' ? 'green' : 'yellow'}>
        {row.original.status}
      </Chip>
    ),
  },
]
const DEMO_TABLE_ROWS: DemoTerm[] = [
  { term: 'invoice', translation: 'factura', status: 'Approved' },
  { term: 'shipping', translation: 'envío', status: 'Pending' },
  { term: 'warranty', translation: 'garantía', status: 'Approved' },
]

// VirtualDataTable needs a scroll viewport + enough rows to actually virtualize.
const DEMO_VIRTUAL_ROWS: DemoTerm[] = Array.from({ length: 60 }, (_, i) => ({
  term: `term ${i + 1}`,
  translation: `término ${i + 1}`,
  status: i % 3 === 0 ? 'Approved' : 'Pending',
}))

// DeadlinePicker — holds its own value so the gallery's chip actually changes.
function DeadlinePickerDemo({ initial = null, variant }: { initial?: string | null; variant?: 'chip' | 'field' }) {
  const [value, setValue] = useState<string | null>(initial)
  return <DeadlinePicker value={value} onChange={setValue} variant={variant} aria-label="Demo deadline" />
}

function WorkflowStepsWithDeadlinesDemo() {
  const [steps, setSteps] = useState<WorkflowDeadlineStep[]>([
    {
      step_definition_id: 1, name: 'Translation', step_type: 'human',
      deadline: '2026-07-15T17:00:00Z', language_deadlines: { de: '2026-07-14T09:00:00Z' },
    },
    { step_definition_id: 2, name: 'Review', step_type: 'human', deadline: null },
  ])
  return <WorkflowStepsWithDeadlines steps={steps} targetLanguages={['de', 'fr', 'es']} onChange={setSteps} />
}

// EditableTitle — name + pencil that swaps to an inline rename field. Keeps its
// own value so the rename round-trips in the gallery.
function EditableTitleDemo() {
  const [name, setName] = useState('Acme localization')
  return <EditableTitle name={name} onRename={setName} className="text-xl font-semibold text-gray-900 dark:text-gray-100" />
}

// ConflictBanner — the optimistic-lock (409) side-by-side banner. Static text
// props; the pick-a-side buttons no-op here.
function ConflictBannerDemo() {
  return (
    <ConflictBanner
      editedByUserId={2}
      editedByName="Dana Reviewer"
      currentUserId={1}
      theirText="La factura vence en 30 días."
      myEditText="La factura vence en treinta días."
      onUseTheirs={() => {}}
      onUseMine={() => {}}
    />
  )
}

// TaskControls — one row per mode of the CAT editor's current task: the
// breadcrumb's step and deadline chips, its actions, and the notes row. Buttons no-op.
const DEMO_TASK_STEP: JobStepAssignment = {
  id: 1, job_id: 1, workflow_step_id: 1, assigned_to: null, status: 'in_progress', started_at: null,
  completed_at: null, created_at: '2026-09-01T00:00:00Z', step_name: 'Review', step_order: 1,
  role_required: 'linguist', step_type: 'human', ai_router_id: null, assignee_name: null, assignee_email: null,
  rejection_reason: '', handoff_note: null, deadline: '2030-01-15T12:00:00Z',
}
const DEMO_SENT_BACK_STEP: JobStepAssignment = {
  ...DEMO_TASK_STEP, id: 2, workflow_step_id: 2, step_name: 'Proofread', step_order: 2,
  status: 'rejected', rejection_reason: 'Two dates are still in US format.',
}
const DEMO_TASK_MODES: [TaskMode, string][] = [
  ['editable', 'in_progress'], ['reopenable', 'completed'], ['read_only', 'completed'],
  ['errored', 'in_progress'], ['none', 'pending'],
]
const DEMO_TASK_JOB: TooltipJob = {
  counts: { total: 0, untranslated: 0, pre_translated: 0, auto_approved: 0, needs_review: 0, approved: 0, clearable_non_confirmed: 0, clearable_all: 0 },
  failed_phase: 'ai_step',
}
function TaskControlsDemo() {
  return DEMO_TASK_MODES.map(([mode, status]) => {
    const step = { ...DEMO_TASK_STEP, status, handoff_note: mode === 'editable' ? 'Please check the terminology in section 3.' : null }
    // The editable row carries both notes: the handoff comment and a step sent back.
    const steps = mode === 'editable' ? [step, DEMO_SENT_BACK_STEP] : [step]
    const task = { mode, step: mode === 'none' ? null : step }
    return (
      <Row key={mode} label={mode}>
        <div className="w-[44rem] space-y-2">
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-1.5">
              <TaskStepChip task={task} steps={steps} job={DEMO_TASK_JOB} isFinalStep={false} />
              <TaskDeadlineChip task={task} />
            </div>
            <TaskActions
              task={task}
              isFinalStep={false}
              canRerun
              canReject
              onRerun={() => {}}
              onComplete={() => {}}
              onReject={() => {}}
            />
          </div>
          <TaskNotes
            task={task}
            steps={steps}
            errorMessage="The AI engine returned an error (HTTP 503). Try again later."
            onSkipAIStep={() => {}}
          />
        </div>
      </Row>
    )
  })
}

// InlineNameField is react-hook-form-bound (register + watched value); the
// gallery spins up a tiny local form so it round-trips without a real submit.
function InlineNameFieldDemo() {
  const { register, control } = useForm({ defaultValues: { name: 'Production reviewer' } })
  const name = useWatch({ control, name: 'name' })
  return <InlineNameField value={name} register={register('name')} />
}

// EditPanel — the shared edit/new shell. Also RHF-bound; Save/Delete/Cancel no-op.
function EditPanelDemo() {
  const { register, control, handleSubmit } = useForm({ defaultValues: { name: 'GPT-4o default' } })
  const name = useWatch({ control, name: 'name' })
  return (
    <EditPanel
      testId="debug-edit-panel"
      name={name}
      register={register('name')}
      namePlaceholder="e.g. Production reviewer"
      onSubmit={handleSubmit(() => {})}
      onCancel={() => {}}
      onDelete={() => {}}
      saveLabel="Save"
    >
      <FormField label="Model">
        <Input placeholder="gpt-4o" />
      </FormField>
    </EditPanel>
  )
}

// SearchOptions — the search options row shared by the CAT editor (collapsible,
// opened by the sliders toggle) and the TM browser (inline, with Exact).
function SearchOptionsDemo() {
  const [open, setOpen] = useState(true)
  const [editor, setEditor] = useState<TextSearchOptions>({ searchSource: true, searchTarget: true, caseSensitive: false, useRegex: false })
  const [tm, setTm] = useState<TextSearchOptions>({ searchSource: true, searchTarget: true, exactMatch: false, caseSensitive: false, useRegex: false })
  return (
    <div className="space-y-3">
      <Row label="editor">
        <div>
          <Input
            placeholder="Search segments..."
            className="w-56"
            icon={<Search />}
            action={<SearchOptionsToggle open={open} active={searchOptionsActive(editor)} onToggle={() => setOpen(o => !o)} testIdPrefix="gallery-editor-search" />}
          />
          <SearchOptionsPanel open={open} options={editor} onChange={p => setEditor(o => ({ ...o, ...p }))} targetLabel="Translation" regexHelp="segments" testIdPrefix="gallery-editor-search" />
        </div>
      </Row>
      <Row label="TM browser">
        <SearchOptionsFields options={tm} onChange={p => setTm(o => ({ ...o, ...p }))} targetLabel="Target" withExactMatch regexHelp="tm" testIdPrefix="gallery-tm-search" />
      </Row>
      <Row label="with error">
        <SearchOptionsFields options={{ ...tm, useRegex: true }} onChange={() => {}} targetLabel="Target" withExactMatch regexError="Invalid regular expression" regexHelp="tm" testIdPrefix="gallery-tm-search-error" />
      </Row>
    </div>
  )
}

// CheckboxField — labeled checkbox rows; the gallery holds their checked state.
function CheckboxFieldDemo() {
  const [prefs, setPrefs] = useState({ a: true, b: false, c: true })
  const set = (k: 'a' | 'b' | 'c') => (v: boolean) => setPrefs(p => ({ ...p, [k]: v }))
  return (
    <div className="max-w-md space-y-3">
      <CheckboxField checked={prefs.a} onChange={set('a')} label="Task assignment" description="When you are assigned to a workflow step" />
      <CheckboxField checked={prefs.b} onChange={set('b')} label="Deadline approaching" description="When a job deadline is within 24 hours" />
      <CheckboxField checked={prefs.c} onChange={set('c')} label="Label only (no description)" />
      <CheckboxField checked disabled onChange={() => {}} label="Disabled" description="Not interactive" />
      <div className="flex items-center gap-3 pt-2">
        <CheckboxField size="compact" checked={prefs.a} onChange={set('a')} label="Aa" />
        <CheckboxField size="compact" checked={prefs.b} onChange={set('b')} label="Include locked" />
        <span className="text-xs text-muted-foreground">size="compact" — flags beside a search or find field</span>
      </div>
    </div>
  )
}

// ConfirmDialog is controlled — the gallery opens it from a trigger button.
function ConfirmDialogDemo() {
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button variant="destructive" onClick={() => setOpen(true)}>
        Open confirm dialog
      </Button>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title="Delete this thing?"
        description="This is a demo — nothing happens. Confirms are destructive by default."
        details={<ul className="list-disc pl-5"><li>Optional details</li><li>under the description</li></ul>}
        onConfirm={() => setOpen(false)}
      />
    </>
  )
}

function DisclosureToggleDemo() {
  const [open, setOpen] = useState<Record<string, boolean>>({ row: true })
  const toggle = (k: string) => setOpen(o => ({ ...o, [k]: !o[k] }))
  return (
    <div className="space-y-3">
      {(['default', 'caps', 'field', 'row'] as const).map(v => (
        <Row key={v} label={v}>
          <DisclosureToggle variant={v} open={!!open[v]} onToggle={() => toggle(v)}>
            {v === 'row' ? 'June release analysis' : 'Show details'}
          </DisclosureToggle>
        </Row>
      ))}
    </div>
  )
}

// Self-contained FilterChip demo — the chip is controlled (it closes on select),
// so the gallery wires a tiny local state to show the selected value.
function FilterChipDemo({ size, removable }: { size?: 'sm' | 'md'; removable?: boolean }) {
  const [value, setValue] = useState<string | null>(null)
  const [shown, setShown] = useState(true)
  const options = ['Draft', 'In progress', 'Completed']
  if (!shown) return <Button variant="outline" size="xs" onClick={() => setShown(true)}>Show the chip again</Button>
  return (
    <FilterChip
      size={size}
      onRemove={removable ? () => { setShown(false); setValue(null) } : undefined}
      label="Status"
      value={value}
      testId={`debug-filter-chip-${size ?? 'sm'}`}
      render={(close) => (
        <>
          <FilterOption selected={value == null} onClick={() => { setValue(null); close() }}>
            All
          </FilterOption>
          {options.map((o) => (
            <FilterOption key={o} selected={value === o} onClick={() => { setValue(o); close() }}>
              {o}
            </FilterOption>
          ))}
        </>
      )}
    />
  )
}

// AddFilterMenu + FilterChip as the Tasks tab uses them: a chip shows only
// while its filter is set; the menu offers the others and opens the picked
// one's options right away.
function AddFilterMenuDemo() {
  const [values, setValues] = useState<Record<string, string | null>>({ status: null, priority: null })
  const OPTIONS: Record<string, { label: string; values: string[] }> = {
    status: { label: 'Status', values: ['Draft', 'In progress', 'Completed'] },
    priority: { label: 'Priority', values: ['Low', 'High'] },
  }
  const set = (key: string, value: string | null) => setValues(v => ({ ...v, [key]: value }))
  const options = (key: string) => (close: () => void) => (
    <>
      <FilterOption selected={values[key] == null} onClick={() => { set(key, null); close() }}>All</FilterOption>
      {OPTIONS[key].values.map(o => (
        <FilterOption key={o} selected={values[key] === o} onClick={() => { set(key, o); close() }}>{o}</FilterOption>
      ))}
    </>
  )
  const keys = Object.keys(OPTIONS)
  return (
    <div className="flex items-center gap-2">
      {keys.some(k => values[k] == null) && (
        <AddFilterMenu
          testId="debug-add-filter"
          filters={keys.filter(k => values[k] == null).map(k => ({ key: k, label: OPTIONS[k].label, options: options(k) }))}
        />
      )}
      {keys.filter(k => values[k] != null).map(k => (
        <FilterChip key={k} label={OPTIONS[k].label} value={values[k]} testId={`debug-chip-${k}`} onRemove={() => set(k, null)} render={options(k)} />
      ))}
    </div>
  )
}

export default function DebugPage() {
  // Set after the PageHeader demo's own title (a parent's effect runs after its children's).
  usePageTitle('Component gallery')
  const [text, setText] = useState('')

  return (
    <TooltipProvider delay={200}>
      <PageContainer className="max-w-5xl pb-16">
        <div className="py-6">
          <h1 className="text-2xl font-normal text-gray-900 dark:text-gray-100">Component gallery</h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
            Every shared component in one place. Toggle the theme in the header to check light/dark.
            Edit a component and it updates everywhere it's used.
          </p>
        </div>

        <Section
          title="Button"
        >
          {BUTTON_VARIANTS.map((variant) => (
            <Row key={variant} label={variant}>
              {BUTTON_SIZES.map((size) => (
                <Button key={size} variant={variant} size={size}>
                  {size}
                </Button>
              ))}
              <Button variant={variant} disabled>
                disabled
              </Button>
              <Button variant={variant} disabled disabledReason="Says why on hover and focus.">
                disabled, with reason
              </Button>
              <Button variant={variant} loading>
                loading
              </Button>
              <Button variant={variant} startIcon={<Search />}>
                start icon
              </Button>
              <Button variant={variant} endIcon={<ChevronDown />}>
                end icon
              </Button>
              <Button variant={variant} size="icon" aria-label="Search">
                <Search />
              </Button>
            </Row>
          ))}
          <Row label="icon sizes: icon / icon-sm / icon-xs">
            <Button variant="ghost" size="icon" aria-label="Search"><Search /></Button>
            <Button variant="ghost" size="icon-sm" aria-label="Search"><Search /></Button>
            <Button variant="ghost" size="icon-xs" aria-label="Search"><Search /></Button>
          </Row>
          <Row label="color / hoverColor">
            <Button variant="ghost" color="muted" hoverColor="red">muted → red</Button>
            <Button variant="ghost" hoverColor="red">default → red</Button>
            <Button variant="outline" color="primary">primary (a toggle whose section holds something)</Button>
          </Row>
          <SubSection
            title="DeleteButton"
            description="Use this in any place where you need to delete an entity (components/ui/delete-button.tsx)."
          >
            <Row label="sizes / states">
              <DeleteButton onClick={() => {}} />
              <DeleteButton size="icon-xs" onClick={() => {}} />
              <DeleteButton disabled onClick={() => {}} />
              <DeleteButton loading onClick={() => {}} />
            </Row>
          </SubSection>
          <SubSection
            title="DownloadOptionsMenu"
            description="The Download button and its menu of choices, each an icon, a label and an optional hint (components/ui/download-options-menu.tsx). iconOnly for a table row's icon actions, next to DeleteButton. DownloadMenu (files and jobs) and AnalysisExportMenu (a saved analysis as CSV or XLSX) are built on it."
          >
            <Row label="label / icon-only / busy">
              <DownloadOptionsMenu options={DEBUG_DOWNLOAD_OPTIONS} onSelect={() => {}} />
              <DownloadOptionsMenu options={DEBUG_DOWNLOAD_OPTIONS} onSelect={() => {}} iconOnly variant="ghost" color="muted" hoverColor="muted" />
              <DownloadOptionsMenu options={DEBUG_DOWNLOAD_OPTIONS} onSelect={() => {}} busy />
            </Row>
            <Row label="without hints">
              <DownloadOptionsMenu options={DEBUG_DOWNLOAD_OPTIONS_NO_HINTS} onSelect={() => {}} iconOnly variant="ghost" color="muted" hoverColor="muted" />
            </Row>
          </SubSection>
          <SubSection
            title="PinButton"
            description="Pins a dashboard or chart to the person's Pinned view (components/charts/PinButton.tsx). Sits just before the dashboard's or chart's name and is always shown: an outline when not pinned, filled when pinned. Wire it with usePins(): <PinButton {...pins.chart(id)} subject={title} />."
          >
            <Row label="unpinned / pinned / icon-sm">
              <PinButton pinned={false} subject="Weekly words" onToggle={() => {}} />
              <PinButton pinned subject="Weekly words" onToggle={() => {}} />
              <PinButton pinned subject="Weekly words" size="icon-sm" onToggle={() => {}} />
            </Row>
          </SubSection>
          <SubSection
            title="TextLink"
            description="The inline link (components/ui/text-link.tsx): primary colour, underline on hover, and the size and weight of the text around it — never its own. `to` for a route, `href` for an outside URL or a download, `onClick` for an action that only looks like a link; `tone` for its colour — `muted` for the grey kind that must not compete with the page, `inherit` inside coloured text, a chip colour beside a chip — and `underlined` for running text. The only inline link: no hand-rolled text-primary hover:underline, and Button has no link variant."
          >
            <p className="text-sm">
              Body text with <TextLink to="/projects">a route</TextLink>, <TextLink href="https://openloc.com" target="_blank" rel="noopener noreferrer">an outside page</TextLink> and <TextLink onClick={() => toast('Clicked')}>an action</TextLink>.
            </p>
            <p className="mt-2 text-xs text-gray-500 dark:text-gray-400">
              A caption keeps its size: <TextLink to="/projects">a route</TextLink> · <TextLink tone="muted" to="/projects">a muted one</TextLink>.
            </p>
          </SubSection>
          <SubSection
            title="CopyLinkButton"
            description="Copies a URL and confirms on itself for two seconds (components/ui/copy-link-button.tsx). Use it wherever a view is shareable by link — a chart permalink, an invite. The clipboard call is guarded, so an insecure origin or a denied permission shows an error toast instead of failing silently."
          >
            <Row label="default / custom labels">
              <CopyLinkButton url="https://example.com/dashboards/1/charts/11?last_days=30" />
              <CopyLinkButton
                url="https://example.com/invite/abc"
                label="Copy invite link"
                toastMessage="Invite link copied"
              />
            </Row>
            <Row label="iconOnly">
              <CopyLinkButton iconOnly url="https://example.com/dashboards/1/charts/11" label="Copy link to this chart" />
            </Row>
          </SubSection>
          <SubSection
            title="AttachmentsField"
            description="Files attached to a form (components/issues/AttachmentsField.tsx): pick any number of files of any type; onAdd checks them against the limits with addAttachments. At most 10 files of 25 MB; each shows as a chip with its size and a remove button. Used by IssueTextField."
          >
            <AttachmentsFieldDemo />
          </SubSection>
          <SubSection
            title="IssueTextField"
            description="The text of an issue or a reply with its files (components/issues/IssueTextField.tsx). An image pasted, dropped or picked goes into the text at the cursor as ![name](upload:N); any other file joins the list. The page shows each placed image in place with IssueText. Used by the issue report and its replies."
          >
            <IssueTextFieldDemo />
          </SubSection>
        </Section>

        <Section
          title="Chip"
          description="THE colored label (components/ui/chip.tsx) — every status pill, provenance marker and role tag in the app. Five colors, three sizes, an optional icon. ChipButton is the same chip as a click target (the Tasks legend, QA severity choices): pointer, hover fade, focus ring."
        >
          <Row label="colors">
            {CHIP_COLORS.map((color) => (
              <Chip key={color} color={color}>
                {color}
              </Chip>
            ))}
          </Row>
          <Row label="ChipButton">
            {CHIP_COLORS.map((color) => (
              <ChipButton key={color} color={color} onClick={() => {}}>
                {color}
              </ChipButton>
            ))}
          </Row>
          <Row label="ChipButton pressed">
            {CHIP_COLORS.map((color) => (
              <ChipButton key={color} color={color} pressed onClick={() => {}}>
                {color}
              </ChipButton>
            ))}
          </Row>
          <Row label="sizes">
            <Chip color="blue" size="small">small</Chip>
            <Chip color="blue">default</Chip>
            <Chip color="blue" size="large">large</Chip>
          </Row>
          <Row label="with icon">
            {CHIP_COLORS.map((color) => (
              <Chip key={color} color={color} icon={<Check />}>
                {color}
              </Chip>
            ))}
          </Row>
          <SubSection
            title="Surface tints"
            description="chipSurfaceClass(color): the same five colors as a background for a surface bigger than a chip — a task-matrix cell. A chip stays readable on top through its ring. chipStripeClass(color) adds the state at full strength as a before: stripe on the surface's edge. chipSurfaceHoverClass(color) is the surface one step deeper, for a tinted row's hover (the Tasks tab's step tables)."
          >
            <Row label="surfaces">
              {CHIP_COLORS.map((color) => (
                <div key={color} className={`relative flex h-12 w-28 items-center justify-center overflow-hidden rounded-md border border-gray-200 before:absolute before:inset-y-0 before:left-0 before:w-0.5 dark:border-gray-800 ${chipSurfaceClass(color)} ${chipStripeClass(color)}`}>
                  <Chip color={color} size="small">{color}</Chip>
                </div>
              ))}
            </Row>
          </SubSection>
          <SubSection
            title="Pre-defined chips"
            description="Named chips, defined once in components/ui/chip-presets.tsx. Only presets get the reserved hues."
          >
            <Row label="workflow step type">
              <HumanStepChip />
              <HybridStepChip />
              <AIStepChip />
              <PretranslateStepChip />
            </Row>
          </SubSection>
        </Section>

        <Section
          title="SelectionBar"
          description="The bar above a selectable list once something is selected (components/list/SelectionBar.tsx): what is selected, a clear button, then the actions. Used by the Tasks matrix, the Files table and the CAT editor's segment selection. Separate action groups with SelectionBarDivider. The list's header checkbox is SelectAllCheckbox (components/list/SelectAllCheckbox.tsx)."
        >
          <Row label="with actions">
            <div className="w-full max-w-xl">
              <SelectionBar label="3 files selected" onClear={() => {}} testIdPrefix="debug">
                <Button variant="outline" size="xs">Download</Button>
                <SelectionBarDivider />
                <Button variant="destructive" size="xs">Delete</Button>
              </SelectionBar>
            </div>
          </Row>
          <Row label="SelectAllCheckbox">
            <SelectAllCheckbox checked={false} indeterminate={false} onChange={() => {}} aria-label="None selected" />
            <SelectAllCheckbox checked={false} indeterminate onChange={() => {}} aria-label="Some selected" />
            <SelectAllCheckbox checked indeterminate={false} onChange={() => {}} aria-label="All selected" />
          </Row>
        </Section>

        <Section
          title="SubNav"
          description="Tabs between sibling pages under a PageHeader (components/layout/SubNav.tsx). One route per tab; mark the index tab `end`. A `disabled` tab shows but cannot be followed; `disabledReason` is its tooltip."
        >
          <Row label="two tabs">
            <SubNav
              className="mb-0 w-96"
              items={[
                { to: '/debug', label: 'Gallery', end: true },
                { to: '/debug/second', label: 'Second tab' },
              ]}
            />
          </Row>
          <Row label="disabled tab">
            <SubNav
              className="mb-0 w-96"
              items={[
                { to: '/debug/tasks', label: 'Tasks', disabled: true, disabledReason: 'Upload a file to create tasks.' },
                { to: '/debug', label: 'Files', end: true },
                { to: '/debug/history', label: 'History' },
              ]}
            />
          </Row>
        </Section>

        <Section
          title="Combobox"
          description="Use this for every field where the user picks one entry from a list (components/ui/combobox.tsx). Never a native <select>. searchable={false} drops the search field for a short fixed list (a two-way setting). triggerDisplay set to chip narrows the closed field to the pick's chip; an option with no chip still reads as its label. aria-invalid gives the field the red frame an Input shows for a validation error — never a border colour through className."
        >
          <Row label="default"><EngineComboboxDemo /></Row>
          <Row label="clearable"><EngineComboboxDemo clearable /></Row>
          <Row label="actions"><EngineActionsDemo /></Row>
          <Row label="disabled">
            <Combobox value="deepl" onChange={() => {}} options={DEMO_ENGINES} disabled className="w-64" />
          </Row>
          <Row label="chip only"><EngineChipTriggerDemo /></Row>
          <Row label="no search"><EngineComboboxDemo searchable={false} /></Row>
          <Row label="invalid">
            <Combobox value="" onChange={() => {}} options={DEMO_ENGINES} placeholder="Engine…" aria-invalid className="w-64" />
          </Row>
          <SubSection
            title="MultiCombobox"
            description="Use this when the user picks several entries from a list (same file). The picks sit as chips inside the field."
          >
            <Row label="default"><EngineMultiDemo /></Row>
            <Row label="wrap"><EngineMultiDemo wrap /></Row>
            <Row label="invalid">
              <MultiCombobox value={[]} onChange={() => {}} options={DEMO_ENGINES} placeholder="Engines…" aria-invalid className="w-64" />
            </Row>
          </SubSection>
        </Section>

        <Section
          title="CustomerLabel"
          description="Read-only customer display (components/customer/CustomerLabel.tsx), the display counterpart to CustomerSelect. Three states from the customers cache: None (unset), the customer name, or a rose '(deleted)' when the id no longer resolves."
        >
          <Row label="unset"><CustomerLabel id={null} /></Row>
          <Row label="deleted (unknown id)"><CustomerLabel id={999999} /></Row>
        </Section>

        <Section
          title="DropdownMenu"
          description="Action menu (navigate, sign out, per-row actions) — components/ui/dropdown-menu.tsx, wrapping base-ui Menu."
        >
          <Row label="dropdown">
            <DropdownMenu>
              <DropdownMenuTrigger className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-md text-sm border border-gray-200 dark:border-gray-800 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors">
                Account
                <ChevronDown className="w-3.5 h-3.5" />
              </DropdownMenuTrigger>
              <DropdownMenuContent>
                <DropdownMenuItem><Settings className="w-4 h-4" />Settings</DropdownMenuItem>
                <DropdownMenuItem><Building2 className="w-4 h-4" />Organization settings</DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem className="text-red-600 dark:text-red-400"><LogOut className="w-4 h-4" />Sign out</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </Row>
          <Row label="with description">
            {/* Two-line items: a title row and a grey line explaining it. */}
            <DropdownMenu>
              <DropdownMenuTrigger className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-md text-sm border border-gray-200 dark:border-gray-800 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors">
                Take a tour
                <ChevronDown className="w-3.5 h-3.5" />
              </DropdownMenuTrigger>
              <DropdownMenuContent className="w-80">
                <DropdownMenuItem description="Every page, from projects to the editor.">Product overview</DropdownMenuItem>
                <DropdownMenuItem description="Needs the assistant.">
                  <span className="flex-1 truncate">Set up AI review</span>
                  <Chip size="small" color="grey">Guided</Chip>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </Row>
          <Row label="labelled group">
            {/* A menu that mixes an action with a set of toggles needs a heading
                over the toggles, or they read as options for the action above.
                GroupLabel only works inside a Group — Base UI throws without
                one — so the two always travel together. */}
            <DropdownMenu>
              <DropdownMenuTrigger className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-md text-sm border border-gray-200 dark:border-gray-800 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors">
                Edit
                <ChevronDown className="w-3.5 h-3.5" />
              </DropdownMenuTrigger>
              <DropdownMenuContent>
                <DropdownMenuItem>Rename</DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuGroup>
                  <DropdownMenuLabel>Filters</DropdownMenuLabel>
                  <DropdownMenuCheckboxItem checked>Date range</DropdownMenuCheckboxItem>
                  <DropdownMenuCheckboxItem>Customer</DropdownMenuCheckboxItem>
                </DropdownMenuGroup>
              </DropdownMenuContent>
            </DropdownMenu>
          </Row>
          <Row label="submenu">
            {/* An action that needs a target opens a second list beside it,
                instead of spelling out "Move to X" for every X in one flat list. */}
            <DropdownMenu>
              <DropdownMenuTrigger className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-md text-sm border border-gray-200 dark:border-gray-800 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors">
                Move or copy
                <ChevronDown className="w-3.5 h-3.5" />
              </DropdownMenuTrigger>
              <DropdownMenuContent>
                <DropdownMenuSub>
                  <DropdownMenuSubTrigger>Move to</DropdownMenuSubTrigger>
                  <DropdownMenuSubContent>
                    <DropdownMenuGroup>
                      <DropdownMenuLabel>Shared</DropdownMenuLabel>
                      <DropdownMenuItem>Operations</DropdownMenuItem>
                      <DropdownMenuItem>Quality</DropdownMenuItem>
                    </DropdownMenuGroup>
                  </DropdownMenuSubContent>
                </DropdownMenuSub>
                <DropdownMenuSub>
                  <DropdownMenuSubTrigger>Copy to</DropdownMenuSubTrigger>
                  <DropdownMenuSubContent>
                    <DropdownMenuItem>My scratch</DropdownMenuItem>
                  </DropdownMenuSubContent>
                </DropdownMenuSub>
              </DropdownMenuContent>
            </DropdownMenu>
          </Row>
        </Section>


        <Section
          title="Badge"
          description="The shadcn Badge primitive — the theme-token lozenge, for counts and inline actions. It tints from bg-primary / bg-secondary / bg-destructive, never from a Tailwind palette; anything that picks a palette color is a Chip (above), and a Badge carrying palette classes in its className is an ESLint error."
        >
          <Row label="variants">
            <Badge variant="default">default</Badge>
            <Badge variant="secondary">secondary</Badge>
            <Badge variant="outline">outline</Badge>
            <Badge variant="destructive">destructive</Badge>
            <Badge variant="ghost">ghost</Badge>
          </Row>
          <Row label="count">
            <Badge variant="count">3</Badge>
            <Badge variant="count">99+</Badge>
          </Row>
        </Section>

        <Section title="Input & textarea" description="The text-field primitives (components/ui/input.tsx, textarea.tsx) — the only way to render a text field, never a raw <input>/<textarea>.">
          <Row label="input">
            <Input placeholder="Placeholder" className="w-64" value={text} onChange={(e) => setText(e.target.value)} />
            <Input placeholder="Disabled" className="w-40" disabled />
            <Input defaultValue="Invalid" className="w-36" aria-invalid />
          </Row>
          <Row label="">
            <Input placeholder="With icon and button" className="w-56" icon={<Search />} action={<Button variant="ghost" size="icon-xs" aria-label="Options"><SlidersHorizontal /></Button>} />
            <Input defaultValue="No clear button" className="w-44" clearable={false} />
            <Input defaultValue="Seamless" className="w-40" variant="seamless" />
          </Row>
          <Row label="textarea">
            <Textarea placeholder="Multi-line…" className="w-64" rows={3} />
            <Textarea placeholder="Disabled" className="w-40" rows={3} disabled />
          </Row>
          <Row label="">
            {/* `action` sits on its own row along the bottom edge, never inline —
                it would otherwise crowd the text. */}
            <Textarea
              placeholder="With an action row"
              className="w-64"
              rows={3}
              action={<Button size="xs" variant="outline">Send</Button>}
            />
            <Textarea defaultValue="Seamless" className="w-40" rows={3} variant="seamless" />
            <Textarea placeholder="Drop files here" className="w-40" rows={3} dropTarget />
          </Row>
        </Section>

        <Section title="Search options" description="The search options under a search field (components/SearchOptions.tsx): Search in as a small ChoiceToggle, then the match checkboxes. The CAT editor shows them in a collapsible row behind the sliders toggle; the TM browser shows them inline, with Exact. The (?) opens the regex cheat sheet for each search.">
          <SearchOptionsDemo />
        </Section>

        <Section title="InlineNameField" description="Name + pencil bound to react-hook-form (components/list/InlineNameField.tsx). Edits in place with a seamless field, and commits with the form. (Overlaps EditableTitle — a consolidation candidate.)">
          <Row label="inline name"><InlineNameFieldDemo /></Row>
        </Section>

        <Section title="InlineRenameField" description="Input plus rename-in-place behaviour for list rows (components/list/InlineRenameField.tsx). Enter submits, Esc cancels, blur submits, and clicks never reach the row underneath.">
          <Row label="rename"><InlineRenameDemo /></Row>
        </Section>

        <Section title="EditableTitle" description="A name + pencil that swaps to InlineRenameField (components/list/EditableTitle.tsx), shared by every detail-page breadcrumb. A long name truncates to a tooltip; canEdit={false} drops the pencil, which is how list rows show a name that must not overflow.">
          <Row label="editable"><EditableTitleDemo /></Row>
          <Row label="read-only"><EditableTitle name="No pencil (canEdit=false)" canEdit={false} className="text-xl font-semibold text-gray-900 dark:text-gray-100" /></Row>
        </Section>

        <Section title="DeadlinePicker" description="One deadline (components/ui/deadline-picker.tsx), built on DateTimePicker. It holds a UTC ISO string, shows it in the viewer&apos;s browser zone, and offers a zone selector for entering a time in another zone — that choice is not kept, the next open is back in the viewer&apos;s zone. Edits are held while the popover is open and committed once on close. variant=&quot;chip&quot; (default) is the chip the rest of the app shows for a deadline, reading &quot;Set due date&quot; when unset, with Clear inside the popover — used on each workflow step&apos;s row and each per-language override. variant=&quot;field&quot; is a form field with the clear X — used for the project deadline.">
          <Row label="unset"><DeadlinePickerDemo /></Row>
          <Row label="set"><DeadlinePickerDemo initial="2026-07-15T17:00:00Z" /></Row>
          <Row label="field"><DeadlinePickerDemo variant="field" initial="2026-07-15T17:00:00Z" /></Row>
          <Row label="disabled"><DeadlinePicker value="2026-07-15T17:00:00Z" onChange={() => {}} disabled aria-label="Demo deadline" /></Row>
        </Section>

        <Section title="WorkflowDeadlines" description="When each workflow step is due (components/workflow/WorkflowDeadlines.tsx). A project's jobs are one file per target language, so a step can be due on a different date per language; the step's own deadline covers every language without one, and the project's covers a step without one. WorkflowStepsWithDeadlines puts each step&apos;s deadline at the right end of the step&apos;s own row (the New Project wizard and a project&apos;s attached workflow); the globe beside it opens a row per override — the languages it covers and the date they share. The globe turns primary while any language carries its own date. AI steps get no deadline — they run by themselves.">
          <Row label="with steps"><WorkflowStepsWithDeadlinesDemo /></Row>
        </Section>

        <Section title="DateTimePicker" description="react-day-picker in the app's tokens (components/ui/date-picker.tsx), in a popover. DeadlinePicker (above) and the job list's bulk Set deadline are built on it. It holds an instant (Date), shown in the browser's time zone unless onTimeZoneChange adds a zone selector — switching zones keeps the wall-clock time. presets are any list of { label, resolve(now) }; lib/date-presets.ts has daysFromNow, businessDaysFromNow and hoursFromNow (rounded up to the next hour unless `at` sets a time).">
          <Row label="datetime"><DateTimePickerDemo /></Row>
          <Row label="+ presets"><DateTimePickerDemo presets="deadline" disablePast /></Row>
          <Row label="time zone"><DateTimePickerDemo withTimeZone /></Row>
          <Row label="time zone + presets"><DateTimePickerDemo presets="eod" withTimeZone defaultTime="17:00" disablePast /></Row>
          <Row label="disabled"><DateTimePicker value={new Date('2026-07-15T09:30:00Z')} onChange={() => {}} disabled /></Row>
        </Section>

        <Section title="Tooltip" description="Hover to reveal. Wrap in a TooltipProvider (this page already does).">
          <Row label="tooltip">
            <Tooltip>
              <TooltipTrigger render={<Button variant="outline">Hover me</Button>} />
              <TooltipContent>Tooltip content</TooltipContent>
            </Tooltip>
          </Row>
        </Section>

        <Section
          title="Popover"
          description="Click-to-open card (components/ui/popover.tsx, base-ui). PopoverTitle + PopoverDescription wire the popup's aria labels — never a hand-rolled <p>. size=default is the form-ish card; size=compact is the dense info bubble (tighter padding, 12px text, semibold title) used by the QA finding dot, the regex help and the sidebar customizer."
        >
          <Row label="default">
            <Popover>
              <PopoverTrigger render={<Button variant="outline">Open</Button>} />
              <PopoverContent align="start">
                <PopoverTitle>Popover title</PopoverTitle>
                <PopoverDescription>A short explanation goes here, in the default 14px body.</PopoverDescription>
              </PopoverContent>
            </Popover>
          </Row>
          <Row label="compact">
            <Popover>
              <PopoverTrigger render={<Button variant="outline" size="sm">Open compact</Button>} />
              <PopoverContent align="start" size="compact">
                <PopoverTitle>Compact title</PopoverTitle>
                <PopoverDescription>Dense body for hover-style info cards.</PopoverDescription>
              </PopoverContent>
            </Popover>
          </Row>
        </Section>

        <Section
          title="FilterChip"
          description="Shared popover filter chip (components/list) used by ProjectFilterBar and the project page's Tasks and Files filters. Trigger shows label + active value; picking an option closes the popover. Two sizes: sm (26px, default) and md (30px, matches the h-input form-control height for bars that sit next to a search box). onRemove adds an × that takes the chip off a bar where the user picks which filters show. AddFilterMenu (components/list) is that bar's add button: it lists the filters not set and opens the picked one's options right away, so a chip appears only once a value is picked (the Tasks tab)."
        >
          <Row label="sm (26px, default)">
            <FilterChipDemo size="sm" />
          </Row>
          <Row label="removable (onRemove)">
            <FilterChipDemo size="sm" removable />
          </Row>
          <Row label="AddFilterMenu">
            <AddFilterMenuDemo />
          </Row>
          <Row label="md (30px, h-input)">
            <div className="flex items-center gap-2">
              <FilterChipDemo size="md" />
              <Input placeholder="…next to a 30px input" className="text-sm max-w-[220px]" />
            </div>
          </Row>
        </Section>

        <Section
          title="ClearFiltersButton & LoadMoreButton"
          description="List footers and filter-bar ends (components/list). ClearFiltersButton ends a filter bar with 'Clear filters (N)' and renders nothing while no filter is active. LoadMoreButton is the footer of a cursor-paged list: nothing without a next page, disabled with 'Loading…' while it loads; size sm inside a tab."
        >
          <Row label="ClearFiltersButton (count 2)">
            <ClearFiltersButton count={2} onClear={() => {}} testId="debug-clear-filters" />
          </Row>
          <Row label="LoadMoreButton (idle / loading / sm)">
            <div className="flex items-start gap-4">
              <LoadMoreButton hasNextPage isFetchingNextPage={false} onLoadMore={() => {}} testId="debug-load-more" />
              <LoadMoreButton hasNextPage isFetchingNextPage onLoadMore={() => {}} testId="debug-load-more-busy" />
              <LoadMoreButton size="sm" hasNextPage isFetchingNextPage={false} onLoadMore={() => {}} testId="debug-load-more-sm" />
            </div>
          </Row>
        </Section>

        <Section
          title="SpecChart"
          description="Every chart in the app (components/charts/SpecChart.tsx): dashboards, chart pages, shared links, the assistant. A pie, or a chart split by a dimension, gets a legend under it (ChartLegend): hover or focus an entry to highlight it, click to hide it, click again to show it. A pie's tooltip lists every slice with its value and its share of what is still shown."
        >
          <Row label="pie">
            <div className="w-80"><SpecChart spec={GALLERY_PIE} /></div>
          </Row>
          <Row label="split bar">
            <div className="w-96"><SpecChart spec={GALLERY_BARS} /></div>
          </Row>
        </Section>

        <Section
          title="CountMeta"
          description="Standardized 'N units' metadata (words / segments / chars). Always thousands-separated + tabular-nums, auto-pluralized, middot-joined."
        >
          <Row label="single"><CountMeta items={[{ count: 1145, noun: 'word' }]} /></Row>
          <Row label="multiple">
            <CountMeta items={[{ count: 31, noun: 'segment' }, { count: 1145, noun: 'word' }]} />
          </Row>
          <Row label="singular"><CountMeta items={[{ count: 1, noun: 'segment' }]} /></Row>
          <Row label="chars"><CountMeta items={[{ count: 8420, noun: 'char' }]} /></Row>
        </Section>

        <Section
          title="DotSeparator"
          description="The inline middot (·) that separates adjacent metadata bits in list/meta rows (components/ui/dot-separator.tsx). One glyph + spacing in one place — CountMeta and the TM list row both use it. Color-neutral (inherits the parent's text color)."
        >
          <Row label="separator">
            <span className="text-sm text-gray-500 dark:text-gray-400 inline-flex items-center">
              <span className="font-mono">en → es, fr, de</span>
              <DotSeparator />Acme<DotSeparator />69 entries
            </span>
          </Row>
        </Section>

        <Section
          title="LanguageCode & LanguagePair"
          description="Consistent, background-free treatment for language codes (components/ui/language-code.tsx) — monospace, uppercase, so BCP-47 codes read the same everywhere. LanguagePair renders a source→targets arrow, and caps the list with an overflow chip when given `max`."
        >
          <Row label="code"><LanguageCode code="pt-BR" /></Row>
          <Row label="pair (single)"><LanguagePair source="en" targets="es" /></Row>
          <Row label="pair (multi)"><LanguagePair source="en" targets={['es', 'fr', 'de']} /></Row>
          <Row label="pair (capped)">
            <LanguagePair
              source="en"
              targets={['es', 'fr', 'de', 'it', 'pt-BR', 'nl', 'pl', 'ja']}
              max={5}
            />
          </Row>
        </Section>

        <Section
          title="ReorderIcon"
          description="The glyph inside DragHandle (components/ui/reorder-icon.tsx) — two bars flanked by up/down chevrons. On a sortable row use DragHandle, not the bare icon. Inherits text color; size via className."
        >
          <Row label="grip">
            <span className="text-gray-400 dark:text-gray-500"><ReorderIcon /></span>
            <span className="text-gray-700 dark:text-gray-300"><ReorderIcon className="size-5" /></span>
          </Row>
        </Section>

        <Section
          title="DragHandle"
          description="The grip on a sortable row (components/ui/drag-handle.tsx): a ghost icon-xs Button holding ReorderIcon, cursor-grab, muted until hovered — same focus ring and hover box as every other icon button. dnd-kit rows pass ref + attributes + listeners; HTML5-drag rows pass their mouse handlers. Used by the TM rows table, assignment rows, workflow steps and the sidebar customizer."
        >
          <Row label="default">
            <DragHandle />
          </Row>
          <Row label="custom title">
            <DragHandle title="Drag to reorder (top = checked first)" />
          </Row>
        </Section>

        <Section title="FormField" description="Label + control + error wrapper (components/ui/form-field.tsx). action puts a small control, usually a TextLink, at the right end of the label row: the Excel columns' Select all / Clear.">
          <Row label="default">
            <div className="w-64">
              <FormField label="Project name" required>
                <Input placeholder="Acme localization" />
              </FormField>
            </div>
          </Row>
          <Row label="with error">
            <div className="w-64">
              <FormField label="Email" error="Enter a valid email address">
                <Input placeholder="you@example.com" aria-invalid />
              </FormField>
            </div>
          </Row>
          <Row label="with action">
            <div className="w-64">
              <FormField label="Columns to translate" action={<TextLink onClick={() => {}}>Select all</TextLink>}>
                <Input placeholder="Whole sheet" />
              </FormField>
            </div>
          </Row>
          <Row label='labelVariant="caps"'>
            <div className="w-64">
              <FormField label="Not in dictionary" labelVariant="caps">
                <p className="text-[13px]">Die <span className="text-red-600 dark:text-red-400">Zahlnug</span> ist fällig.</p>
              </FormField>
            </div>
          </Row>
        </Section>

        <Section title="CheckboxField" description="Labeled checkbox row (components/ui/checkbox-field.tsx): a native checkbox + title + optional description, the whole strip clickable. Hover feedback is a subtle full-row background tint (hover:bg-accent) — the accent color is reserved for the checked state, not hover. The checkbox is vertically centered against the text. Used by the Settings + org notification lists.">
          <CheckboxFieldDemo />
        </Section>

        <Section title="PageHeader" description="Page title + optional subtitle + actions (components/layout/PageHeader.tsx).">
          <div className="rounded-lg border border-gray-200 dark:border-gray-800 p-4">
            <PageHeader
              title="Projects"
              subtitle="en → es, fr, de"
              actions={<Button>New project</Button>}
            />
          </div>
        </Section>

        <Section title="Wordmark" description="The OpenLoc wordmark in the display face (components/layout/Wordmark.tsx). Text only: wrap it in the link or heading the spot needs. `default` for the nav rail and the docs bar, `large` for the sign-in card.">
          <div className="flex items-baseline gap-8">
            <Wordmark />
            <Wordmark size="large" />
          </div>
        </Section>

        <Section title="Alert" description="The inline bordered box for a warning or an error inside a page, dialog or form (components/ui/alert.tsx): auth errors, LoadFailed, the save-conflict banner, the one-time token reveal. tone='warning' is yellow with role='status', tone='error' is red with role='alert'; a blocking warning may pass role='alert'. Colours come from the chip palette (chipSurfaceClass / chipTextClass). Optional icon and bold title; className is layout only. Not for a full-width strip (SessionBanner), a one-line field error, or async feedback (toast).">
          <div className="space-y-3 max-w-xl">
            <Alert tone="warning" icon={<TriangleAlert />}>
              <strong>3</strong> segments are not yet confirmed. They will remain in their current state.
            </Alert>
            <Alert tone="warning" title="Copy your access key now — it won't be shown again.">
              <code className="block rounded border border-border bg-background px-3 py-2 text-xs text-foreground">olk_live_0123456789abcdef</code>
            </Alert>
            <Alert tone="error">Invalid email or password.</Alert>
            <Alert tone="error">
              <div className="flex flex-wrap items-center gap-3">
                <span>Couldn't load the projects: server unavailable</span>
                <Button size="sm" variant="outline" className="ml-auto">Retry</Button>
              </div>
            </Alert>
          </div>
        </Section>
        <Section title="Progress" description="Determinate progress bar (components/ui/progress.tsx) — spell check run, TM / termbase upload and import banners, the CAT editor's approved bar. value is a percent (clamped 0–100); null is indeterminate (full pulsing bar, for server-side work with no known end). size sm (h-1.5) sits inside a banner or toolbar, default (h-2) in a dialog. No tone: muted track, primary fill. tone takes a chip colour (track from chipSurfaceClass, fill from chipDotClass) when the bar carries a state: blue for an upload in progress, green for done. Not for a share-of-total chart (the Processing time dialog's stage bars). Always name it with aria-label or aria-labelledby.">
          <Row label="default">
            <div className="w-64"><Progress value={40} aria-label="Spell check progress" /></div>
          </Row>
          <Row label="sm · tone blue">
            <div className="w-64"><Progress value={65} size="sm" tone="blue" aria-label="Upload progress" /></div>
          </Row>
          <Row label="sm · tone green">
            <div className="w-64"><Progress value={82} size="sm" tone="green" aria-label="Segments approved" /></div>
          </Row>
          <Row label="indeterminate">
            <div className="w-64"><Progress value={null} size="sm" tone="blue" aria-label="Import progress" /></div>
          </Row>
        </Section>
        <Section title="SettingsCard" description="One titled card on a settings page (components/layout/SettingsCard.tsx): the card surface, a SectionHeader, an optional muted description line, then the body. Renders a <section>, so tests find a card by its heading. icon is a leading lucide icon sized by the header; info sits right of the title (a SectionInfo, a count); action is right-aligned; className is spacing against the neighbouring cards only. Used by personal Settings and the Organization pages.">
          <SettingsCard title="Notification rules" icon={<Mail />} description="Master switches for email notifications.">
            <p className="text-sm">Card body</p>
          </SettingsCard>
          <SettingsCard title="Members" info={<span className="text-sm text-gray-500">(3)</span>} action={<Button size="sm">Invite</Button>} className="mt-4">
            <p className="text-sm">No description: the header sits closer to the body.</p>
          </SettingsCard>
        </Section>
        <Section title="EditorSidebarSection" description="One collapsible section of the CAT editor's Resources sidebar (components/EditorSidebarSection.tsx): a full-width DisclosureToggle header with a muted count or note on the right (meta; 0 shows nothing), an optional header action outside the toggle, and a body that unmounts while closed. Every sidebar section (TM matches, MT suggestion, Terminology, References, History, QA findings, Comments) uses it.">
          <div className="w-72 rounded-md border border-border bg-card">
            <EditorSidebarSection title="TM matches" open onToggle={() => {}} meta="2" data-testid="debug-sidebar-section-open">
              <p className="text-xs text-muted-foreground">Section body</p>
            </EditorSidebarSection>
            <EditorSidebarSection title="History" open={false} onToggle={() => {}} meta="5" data-testid="debug-sidebar-section-closed">
              <p className="text-xs">Hidden while closed</p>
            </EditorSidebarSection>
          </div>
        </Section>
        <Section title="QueryBoundary" description="The body of a card that owns one query (components/layout/QueryBoundary.tsx): children(data) once the data is there, a small LoadingPlaceholder while the first fetch runs, LoadFailed with Retry when it failed. Loaded data stays when a background refetch fails. Pass the useQuery result and what names the thing in the error. Used by the Organization settings cards.">
          <div className="space-y-3">
            <QueryBoundary query={{ data: ['en → de', 'en → fr'], isPending: false, error: null, refetch: () => {} }} what="language pairs">
              {pairs => <p className="text-sm">{pairs.join(', ')}</p>}
            </QueryBoundary>
            <QueryBoundary query={{ data: undefined, isPending: false, error: new Error('server unavailable'), refetch: () => {} }} what="language pairs">
              {() => null}
            </QueryBoundary>
          </div>
        </Section>
        <Section title="EmptyState" description="Neutral placeholder for empty lists (components/layout/EmptyState.tsx).">
          <div className="rounded-lg border border-gray-200 dark:border-gray-800">
            <EmptyState message="No projects yet. Create one to get started." />
          </div>
        </Section>

        <Section title="VerticalDivider" description="Thin vertical rule between groups of controls in one row (components/ui/vertical-divider.tsx) — the CAT editor filter groups, the advanced search toggles, the QA severity picker. Decorative (aria-hidden); pass className for height or colour.">
          <Row label="between groups">
            <div className="flex items-center gap-2 text-xs">
              <Chip>Draft (0)</Chip><Chip>Approved (3)</Chip>
              <VerticalDivider />
              <Chip>TM</Chip><Chip>MT</Chip>
              <VerticalDivider />
              <Chip>Commented</Chip>
            </div>
          </Row>
        </Section>

        <Section title="WorkflowStepChips" description="A workflow's steps as a stepper (components/workflow/WorkflowStepChips.tsx): numbered dots on a rail, the type icon beside the name, the AI router as a TextLink to the AI page, then who picks the step up or Automated. `pretranslation` heads the rail with the unnumbered Pre-translation row — it happens before any step, so steps count from 1; never a step 0. Under Workflow it shows only while MT on upload has a router; under Raw MT (New project, template) it is the whole rail. `selection` makes each step a radio for a dialog that asks which step (Reject's target); `blockReason` greys out a step and says why on hover; `trailing` here carries each step's state.">
          <SelectableStepsDemo />
          <WorkflowStepChips
            steps={[
              { name: 'Auto APE', step_type: 'ai', step_definition_id: 1, ai_router_id: 1, ai_router_name: 'Default QE+APE router' },
              { name: 'Review', step_type: 'human', step_definition_id: 2 },
            ]}
            pretranslation={<MTRouterLink mtRouterName="Default MT router" onPickRouter={() => toast('Would scroll to the MT router')} />}
          />
        </Section>

        <Section title="Breadcrumbs" description="Navigation trail above page titles (components/layout/Breadcrumbs.tsx). Each item is a label + optional link; the last is the current page (aria-current). Slash-separated. `trailing` puts chips after the current crumb — the CAT editor's target language, current step and step deadline. `actions` puts page controls at the right end of the same line.">
          <div className="space-y-3 rounded-lg border border-gray-200 dark:border-gray-800 p-4">
            <Breadcrumbs items={[
              { label: 'Projects', to: '/projects' },
              { label: 'Acme localization', to: '/projects/1' },
              { label: 'Settings' },
            ]} />
            <Breadcrumbs
              items={[
                { label: 'Projects', to: '/projects' },
                { label: 'Acme localization', to: '/projects/1' },
                { label: 'guide.docx' },
              ]}
              trailing={<><Chip color="blue">Translation</Chip><Chip>DE</Chip></>}
              actions={<Button variant="outline" size="sm">Download</Button>}
            />
          </div>
        </Section>

        <Section title="ColumnResizeHandle" description="The drag strip centred on a column header's right edge, over its border (components/ui/column-resize-handle.tsx). The header cell must be positioned (relative or sticky); the caller wires the drag and the double-click reset. The default variant, hover, shows only on hover, for tables whose column borders already show where to drag (the project Tasks tab, the CAT editor's segment table); variant grip adds a grip on the edge for a table without them (the analysis breakdown's File column).">
          <Row label="header">
            <div className="flex w-96 rounded border border-gray-200 text-xs font-medium uppercase text-gray-500 dark:border-gray-800">
              <div className="relative flex-1 border-r border-gray-200 px-3 py-2 dark:border-gray-800">Source<ColumnResizeHandle /></div>
              <div className="relative flex-1 border-r border-gray-200 px-3 py-2 dark:border-gray-800">Translation<ColumnResizeHandle active /></div>
              <div className="flex-1 px-3 py-2">Match</div>
            </div>
          </Row>
          <Row label="variant grip (no column borders)">
            <div className="flex w-96 rounded border border-gray-200 text-xs font-medium uppercase text-gray-500 dark:border-gray-800">
              <div className="relative w-40 border-r border-gray-200 px-3 py-2 dark:border-gray-800">File<ColumnResizeHandle variant="grip" /></div>
              <div className="flex-1 px-3 py-2 text-right">New</div>
            </div>
          </Row>
        </Section>

        <Section title="ConfirmDialog" description="Destructive/positive confirmation modal (components/ConfirmDialog.tsx, base-ui AlertDialog). Cancels on outside-click.">
          <Row label="confirm"><ConfirmDialogDemo /></Row>
        </Section>

        <Section title="Dialog" description="The base modal (components/ui/dialog.tsx) behind the create/edit dialogs (TM, customer, analysis…). Used controlled — open + onOpenChange. Header / body / footer + auto close button.">
          <Row label="dialog"><DialogDemo /></Row>
        </Section>

        <Section title="DataTable" description="Sortable table (components/ui/data-table.tsx, TanStack Table) behind Terminology / TM / Analyses. Pass columns + data; cells can render any component (status pill below). A column with meta.sort ({ direction, onToggle }) gets a sort button with an arrow in its header and aria-sort; the caller sorts the data (on the server when the list loads in pages, as Reported issues does by Updated). VirtualDataTable is the virtualized variant for large sets.">
          <DataTable columns={DEMO_TABLE_COLUMNS} data={DEMO_TABLE_ROWS} />
        </Section>

        <Section title="VirtualDataTable" description="Virtualized table for large sets (components/ui/virtual-data-table.tsx, TanStack Table + Virtual) — used by TM / Terminology browsers. Sticky header, only visible rows are in the DOM, optional infinite-scroll via onLoadMore. Needs a bounded-height scroll container; 60 rows shown in a fixed viewport.">
          <div className="h-64 rounded-lg border border-gray-200 dark:border-gray-800">
            <VirtualDataTable columns={DEMO_TABLE_COLUMNS} data={DEMO_VIRTUAL_ROWS} />
          </div>
        </Section>

        <Section title="Tabs" description="Segmented tab control (components/ui/tabs.tsx), for panels within one route. A section whose panels each deserve their own URL uses SubNav instead — that is what the project, template and translation memory pages do.">
          <Tabs defaultValue="overview" className="max-w-md">
            <TabsList>
              <TabsTrigger value="overview">Overview</TabsTrigger>
              <TabsTrigger value="settings">Settings</TabsTrigger>
              <TabsTrigger value="activity">Activity</TabsTrigger>
            </TabsList>
            <TabsContent value="overview" className="pt-3 text-sm text-gray-500 dark:text-gray-400">Overview panel content.</TabsContent>
            <TabsContent value="settings" className="pt-3 text-sm text-gray-500 dark:text-gray-400">Settings panel content.</TabsContent>
            <TabsContent value="activity" className="pt-3 text-sm text-gray-500 dark:text-gray-400">Activity panel content.</TabsContent>
          </Tabs>
        </Section>

        <Section title="ChoiceToggle" description="Segmented single choice (components/ui/choice-toggle.tsx): a few options in one pill, one checked. Radio semantics — it answers &quot;which of these?&quot; — unlike Tabs, which switch panels. Used by the chart builder's chart type and the new dashboard's visibility. size=&quot;sm&quot; fits a row of small controls: Search in, in the search options and the TM find &amp; replace popup.">
          <Row label="with icons"><ChoiceToggleDemo /></Row>
          <Row label="small"><ChoiceToggleSmallDemo /></Row>
          <Row label="primary tone + caption"><ChoiceTogglePrimaryDemo /></Row>
        </Section>

        <Section title="Toast" description="Transient notifications via sonner (the toast() helper). Success / error / info — used app-wide after mutations.">
          <Row label="toasts">
            <Button variant="outline" onClick={() => toast.success('Saved successfully')}>Success</Button>
            <Button variant="outline" onClick={() => toast.error('Something went wrong')}>Error</Button>
            <Button variant="outline" onClick={() => toast('Heads up', { description: 'A neutral, informational message.' })}>Info</Button>
          </Row>
        </Section>

        <Section title="SaveStatus" description="The inline state of a change that saves on its own (components/ui/save-status.tsx): Saving... while the request runs, then Saved ✓ for five seconds (useSavedFlash), then nothing. Used by the TM and assignment rows and the support team's issue fields. A form with a Save button uses the button's loading state instead.">
          <Row label="saving"><SaveStatus saving saved={false} /></Row>
          <Row label="saved"><SaveStatus saving={false} saved /></Row>
          <Row label="live"><SaveStatusDemo /></Row>
        </Section>

        <Section title="LoadingPlaceholder" description="Delayed loading indicator (components/layout). Appears after 1s so fast loads don't flash; shown immediately here with delayMs={0}.">
          <div className="rounded-lg border border-gray-200 dark:border-gray-800">
            <LoadingPlaceholder delayMs={0} />
          </div>
        </Section>

        <Section title="SafeHTML" description="Sanitized HTML render (components/SafeHTML.tsx, DOMPurify) — the mandated replacement for dangerouslySetInnerHTML. Allows only TMS formatting tags (strong/em/sup/sub/span/p/br) + class/data-* attrs; everything else is stripped. Below: the same string with a <script> and an onclick — both gone.">
          <Row label="formatting">
            <SafeHTML html="Ship <strong>before</strong> the <em>deadline</em>: H<sub>2</sub>O, x<sup>2</sup>." className="text-sm text-gray-700 dark:text-gray-300" />
          </Row>
          <Row label="stripped">
            <SafeHTML html={'Safe <strong>bold</strong> <script>alert(1)</script><span onclick="evil()">no handlers</span>'} className="text-sm text-gray-700 dark:text-gray-300" />
          </Row>
        </Section>

        <Section title="SessionBanner" description="The strip above the header while the session is not the user's own org (components/layout/SessionBanner.tsx): an operator viewing an org, or a visit to the shared demo organization. The action is a link or a link-styled button.">
          <div className="space-y-2">
            <SessionBanner action={<SessionBannerAction href="#">Exit</SessionBannerAction>}>
              You&rsquo;re viewing <strong>Acme Localization</strong> as a platform operator.
            </SessionBanner>
            <SessionBanner action={<SessionBannerAction onClick={() => {}}>Back to Acme Localization</SessionBannerAction>}>
              You&rsquo;re in the <strong>Playground</strong>: sample data, look around freely. Changes won&rsquo;t be saved.
            </SessionBanner>
          </div>
        </Section>

        <Section title="ConflictBanner" description="The optimistic-lock (409) conflict banner (components/ConflictBanner.tsx) — side-by-side, shown by the CAT segment editor and the TM cell editor. Both versions stay visible; the user picks one explicitly (no dismiss trap). Heading copy branches on who edited (same user / named user / unknown).">
          <ConflictBannerDemo />
        </Section>

        <Section title="TaskControls" description="The CAT editor's current task (components/TaskControls.tsx), by its mode (lib/current-task.ts). TaskStepChip colours the step by the mode, and TaskDeadlineChip follows it while the task is in progress; both sit in the breadcrumb's trailing slot. TaskActions goes in the breadcrumb's actions slot: in progress shows Reject and Complete; a completed task tells a manager that an edit reopens it and reads as read-only for everyone else; an errored task offers Re-run and Complete; nothing started reads Not started. TaskNotes is the row under the toolbar: the job's error while the task is errored, with Skip AI step when a hybrid step's AI run failed, why a later step sent the job back, and, while the task is in progress, the comment the previous step left on completing, behind a show/hide toggle (the in progress row).">
          <TaskControlsDemo />
        </Section>

        <Section title="AddItemButton" description="The standard 'add a row' affordance (components/list/AddItemButton.tsx) — a slim, full-width dashed '+ New …' button sized to match a list row, so it slots in place of the first item or the empty state.">
          <div className="max-w-md space-y-2">
            <AddItemButton label="New provider" onClick={() => {}} />
            <AddItemButton label="New provider" onClick={() => {}} disabled title="Finish the last one first" />
          </div>
        </Section>

        <Section title="EditPanel" description="A shared edit/new form shell (components/ui/edit-panel.tsx): inline editable name (InlineNameField) on the left, Save / Delete / Cancel(✕) on the right, a wall-to-wall divider, then the form's fields below. Esc cancels. Wrap any 'edit or create one entity' form so the UX is identical everywhere.">
          <div className="max-w-xl">
            <EditPanelDemo />
          </div>
        </Section>

        <Section title="DisclosureToggle" description="The inline show/hide control (components/ui/disclosure-toggle.tsx): a chevron that turns when open, aria-expanded and aria-controls. default for small sentence-case text, caps for the 11px section labels inside AI cards, field for a whole form field that starts collapsed, row for the name of a list or table row that opens in place (the Analyses tab's accordion rows). Page-level sections use SlimCollapsibleSection instead.">
          <DisclosureToggleDemo />
        </Section>

        <Section title="SlimCollapsibleSection & SectionInfo" description="The titled collapsible section (components/layout/SlimCollapsibleSection.tsx) — header toggles the body; chip / info / action sit outside the toggle; title leftmost, chevron trailing right. No card: a hairline under the header gives the row a floor without boxing the section in, plus a light hover fill and a focus-visible ring (the header is tabbable). Open state lives in the useCollapsibleOpen hook and is not persisted across mounts. The compact size is the slim form for a list of sections, like the Tasks tab's workflow steps (14px title, 6px padding); toggleLabel puts 'Show 3 tasks' / 'Hide 3 tasks' by the chevron, disabled makes a muted row that does not open, variant='grouped' sections, side by side in one parent, stack as cards — closed rows touch as one card, an open one keeps its body inside and ends the card there, the rows after it starting the next — and tone colours the header with a chip surface (the Tasks tab: green for a finished step, yellow for one in progress). SectionInfo (components/ui/section-info.tsx) is the little info-icon tooltip next to a title; its amber tone is the warning form, its red tone the error form, the icon prop swaps the tone's icon (the Tasks tab's step rows: Sent back, AI step running, Comment), and its small size sits beside small text.">
          <SlimCollapsibleSection
            title="Providers"
            defaultOpen
            info={<SectionInfo label="About providers">Reusable model configs — define once, reference from any AI engine row.</SectionInfo>}
            chip={<Badge variant="outline" className="uppercase tracking-wide">Advanced</Badge>}
            action={<Button size="xs">New provider</Button>}
          >
            <div className={listRowClass}>
              <span className="font-medium">A list row</span>
              <span className="text-xs text-muted-foreground">listRowClass — 40px, shared with AddItemButton</span>
            </div>
            <div className={listCardClass}>
              <div className="font-medium">A list card</div>
              <div className="text-sm text-gray-500 mt-1">listCardClass — one card per item (Projects, Tasks, Translation memories, Terminology, Templates)</div>
            </div>
          </SlimCollapsibleSection>
          <div>
            <SlimCollapsibleSection title="Translation" size="compact" toggleLabel="3 tasks" variant="grouped" tone="yellow" defaultOpen>
              <div className={listRowClass}>
                <span className="font-medium">A compact section: one in a list of many</span>
              </div>
            </SlimCollapsibleSection>
            <SlimCollapsibleSection title="Review" size="compact" toggleLabel="2 tasks" variant="grouped">
              {null}
            </SlimCollapsibleSection>
            <SlimCollapsibleSection title="Sign-off" size="compact" toggleLabel="No tasks" variant="grouped" tone="green" disabled>
              {null}
            </SlimCollapsibleSection>
          </div>
          <Row label="SectionInfo tone / size">
            <SectionInfo label="Info">Muted, the default.</SectionInfo>
            <SectionInfo tone="amber" label="Warning">Amber, for a note that must not be overlooked.</SectionInfo>
            <SectionInfo tone="red" label="Error">Red, for an error: the message goes here.</SectionInfo>
            <SectionInfo icon={Undo2} label="Sent back">The icon prop swaps the tone's icon for a note of another kind.</SectionInfo>
            <SectionInfo size="sm" label="Small info">Beside small text.</SectionInfo>
            <SectionInfo tone="amber" size="sm" label="Small warning">Small and amber.</SectionInfo>
          </Row>
        </Section>

        <Section title="ProviderTile" description="Provider tile (components/ai/ProviderTile.tsx): brand logo left, name right, in a rounded rectangle. ProviderTileRow is a 4-column grid, so exactly FOUR sit side by side at any container width and a fifth starts a second row of the same size. A fixed tile width could not promise that — w-60 fits the 1000px collapsible body but wrapped to three in this 976px gallery, which is why the tile is w-full and the grid owns the sizing. Optional meta line, optional onClick (adds hover + focus ring + aria-pressed). The optional `enabled` prop tints an on tile and fades a dashed off one; leave it undefined for a tile with no switch, so an ordinary tile is never mistaken for a disabled one.">
          <ProviderTileRow>
            <ProviderTile provider="ext:mockqe" name="Mock QE" />
            <ProviderTile provider="openai" name="OpenAI" />
            <ProviderTile provider="claude" name="Claude" />
            <ProviderTile provider="deepl" name="DeepL" />
          </ProviderTileRow>
          <ProviderTileRow>
            <ProviderTile provider="openai" name="OpenAI" meta="api.openai.com" />
            <ProviderTile provider="openai" name="Azure Acme" meta="acme.openai.azure.com" selected />
            <ProviderTile provider="claude" name="Claude" meta="2 keys" onClick={() => toast.success('Tile clicked')} />
          </ProviderTileRow>
          <ProviderTileRow>
            <ProviderTile provider="openai" name="OpenAI" meta="Default endpoint" enabled />
            <ProviderTile provider="claude" name="Claude" meta="Disabled" enabled={false} />
            <ProviderTile provider="ext:mockqe" name="Mock QE" meta="No key yet" enabled />
            <ProviderTile provider="openai" name="Gemini" meta="Not configured" enabled={false} />
          </ProviderTileRow>
        </Section>

        <Section title="ProviderModelChip" description="AI-domain summary chip (components/ai/ProviderModelChip.tsx): «provider icon» model | MODE. Model and mode each drop out with their separator when absent — an MT engine row is provider + icon alone. Shared by the prompt list and the AI-engine list so they stay visually identical. (Stays in ai/ — it's provider-specific.)">
          <Row label="llm"><ProviderModelChip provider="openai" model="gpt-4o" mode="MT" /></Row>
          <Row label="qe+ape"><ProviderModelChip provider="claude" model="claude-sonnet-4" mode="QE+APE" /></Row>
          <Row label="extension"><ProviderModelChip provider="ext:mockqe" name="Mock QE" mode="QE+APE" /></Row>
          <Row label="mt engine"><ProviderModelChip provider="deepl" /></Row>
        </Section>

        <Section title="PageContainer" description="The page wrapper template (components/layout/PageContainer.tsx). Every page picks a width so content never hugs the window edge or over-stretches. This gallery uses it too. Widths visualized (relative):">
          <div className="space-y-2">
            {([
              ['default', 'w-full', 'list / detail / forms — capped + centered'],
              ['full', 'w-full', 'editor / browser — full height, no cap'],
              ['docs', 'w-full', 'docs pages — full height, capped + centered, header lined up (docsBar)'],
            ] as const).map(([name, w, desc]) => (
              <div key={name} className="flex items-center gap-3">
                <span className="w-16 shrink-0 text-xs font-mono text-gray-400 dark:text-gray-500">{name}</span>
                <div className={`${w} h-6 rounded bg-primary/10 border border-primary/40`} />
                <span className="text-xs text-gray-500 dark:text-gray-400 shrink-0">{desc}</span>
              </div>
            ))}
          </div>
        </Section>
      </PageContainer>
    </TooltipProvider>
  )
}

function ChoiceToggleSmallDemo() {
  const [value, setValue] = useState<SearchScope>('both')
  return (
    <ChoiceToggle
      size="sm"
      value={value}
      onChange={setValue}
      aria-label="Search in"
      options={[
        { value: 'both', label: 'Both' },
        { value: 'source', label: 'Source' },
        { value: 'target', label: 'Target' },
      ]}
    />
  )
}

function ChoiceTogglePrimaryDemo() {
  const [value, setValue] = useState<'workflow' | 'raw_mt'>('workflow')
  return (
    <ChoiceToggle
      value={value}
      onChange={setValue}
      aria-label="Workflow mode"
      tone="primary"
      options={[
        { value: 'workflow', label: 'Workflow', caption: 'Human and AI steps', icon: Workflow },
        { value: 'raw_mt', label: 'Raw MT', caption: 'Pre-translation', icon: Languages },
      ]}
    />
  )
}

function ChoiceToggleDemo() {
  const [value, setValue] = useState<'line' | 'bar' | 'pie'>('line')
  return (
    <ChoiceToggle
      value={value}
      onChange={setValue}
      aria-label="Chart type"
      options={[
        { value: 'line', label: 'Line', icon: LineChart },
        { value: 'bar', label: 'Bar', icon: BarChart3 },
        { value: 'pie', label: 'Pie', icon: PieChart },
      ]}
    />
  )
}

function IssueTextFieldDemo() {
  const [text, setText] = useState('')
  const [files, setFiles] = useState<File[]>([])
  return (
    <IssueTextField value={text} onChange={setText} files={files} onFilesChange={setFiles} rows={3}
      placeholder="Paste a screenshot here" filesTestId="debug-issue-text-files" className="max-w-lg" />
  )
}

function AttachmentsFieldDemo() {
  const [files, setFiles] = useState<File[]>([])
  return (
    <AttachmentsField value={files} onAdd={added => setFiles(addAttachments(files, added))}
      onRemove={i => setFiles(files.filter((_, j) => j !== i))} testId="debug-attachments-field" />
  )
}
