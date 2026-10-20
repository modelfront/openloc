/**
 * Centralized query key factories for React Query.
 *
 * Using factories instead of inline string arrays ensures:
 * - Consistent keys across all components (no ['term-bases'] vs ['terminology'] bugs)
 * - Type-safe invalidation
 * - Easy to find all usages via "queryKeys.xxx"
 */

import type { IssueScope } from '@/api'
import type { TextSearchOptions } from './text-search'

export const queryKeys = {
  // --- Auth (current user) ---
  auth:              { me: ['auth', 'me'] as const, orgs: ['auth', 'orgs'] as const },

  // --- Platform operator surface (org list + lifecycle) ---
  platform:          {
    orgs: ['platform', 'orgs'] as const,
    issues: ['platform', 'issues'] as const,
    issue: (id: number) => ['platform', 'issues', id] as const,
    issueOrgs: ['platform', 'issues', 'orgs'] as const,
    issueAssignees: ['platform', 'issue-assignees'] as const,
    issueTags: ['platform', 'issue-tags'] as const,
  },
  issues:            {
    all: ['issues'] as const,
    detail: (id: number) => ['issues', id] as const,
  },

  // --- Appearance (per-user palettes) ---
  appearance:        { current: ['appearance'] as const },

  // --- Tasks page. `all` is the prefix: invalidating it refreshes every
  // filtered page and the Step filter's options. ---
  tasks: {
    all:       ['tasks'] as const,
    paginated: ['tasks', 'paginated'] as const,
    steps:     ['tasks', 'steps'] as const,
  },

  // --- Invites (the Organization page: Members and Vendors tabs). `members` key is defined below. ---
  invites:           { all: ['invites'] as const },

  // --- Projects ---
  projects:          {
    all: ['projects'] as const,
    /**
     * String-prefix key for the paginated/filtered project list
     * (useInfiniteQuery). The full key is `[paginated, filtersKey]`
     * but `paginated` alone matches all variants under invalidate (React Query
     * partial-prefix match). EVERY mutation that changes a project's
     * displayable fields (rename, delete, status/customer PATCH) must include
     * this key in invalidateKeys — or the project list will silently go stale.
     */
    paginated: ['projects-paginated'] as const,
    /**
     * Distinct creators of the tenant's projects — the "Created by" chip's
     * option set. Goes stale when a project is created or
     * deleted (the last project by someone drops them from the list), but a
     * momentarily stale option list is harmless, so only the create path
     * bothers invalidating it.
     */
    creators: ['project-creators'] as const,
  },
  project:           {
    /** Prefix key: every project's settings. */
    allSettings: ['project-settings'] as const,
    settings: (id: number) => ['project-settings', id] as const,
  },
  files:             {
    byProject: (pid: number) => ['files', pid] as const,
    /** Under byProject, so a project's file invalidation reaches it. */
    xlsxLayout: (pid: number, fileId: number) => ['files', pid, 'xlsx-layout', fileId] as const,
  },
  /** An .xlsx read before upload; `file` names the local file (name, size, date). */
  xlsxInspect:       { byFile: (pid: number, file: string) => ['xlsx-inspect', pid, file] as const },

  // --- Re-import history ---
  // Per-file audit log of past XLIFF re-imports. ANY mutation that adds a row
  // (i.e., a successful applied re-import) must invalidate the file's key.
  projectEvents:     {
    byProject: (projectId: number) => ['project-events', projectId] as const,
    // Filter variants share the byProject prefix so a prefix invalidation
    // refreshes every filtered view at once.
    byProjectFiltered: (projectId: number, type: string, actorId: number) =>
      ['project-events', projectId, type, actorId] as const,
  },
  jobs: {
    byProject: (pid: number) => ['jobs', pid] as const,
    // Under the byProject prefix, so every invalidation of a project's jobs
    // refreshes its task matrix too.
    tasks: (pid: number) => ['jobs', pid, 'tasks'] as const,
  },

  // --- Single Job ---
  job: {
    detail:       (id: number) => ['job', id] as const,
    translations: (id: number) => ['job-translations', id] as const,
    steps:        (id: number) => ['job-steps', id] as const,
    /** Prefix key for invalidating the step panels of every job at once. */
    allSteps:     ['job-steps'] as const,
    analysis:     (id: number) => ['job-analysis', id] as const,
    // Live 6-band leverage breakdown driving the CAT-editor status bar
    // (planning doc §"Live job-level view"). Invalidated by
    // invalidateJobContext on every confirm/save so the bar reflects the
    // segment status change immediately, without waiting for the 15s
    // refetchInterval.
    leverage:     (id: number) => ['job-leverage', id] as const,
    // Persisted QA findings (GET /jobs/{id}/qa-report). Loaded when the editor
    // opens so dots show without a manual Run QA; per-segment refreshes and
    // ignore/un-ignore patch it in place via setQueryData.
    qaReport:     (id: number) => ['job-qa-report', id] as const,
    // Live segment holds (who has which segment open). The editor refetches
    // them when the hold stream announces a change, and polls as a fallback.
    holds:        (id: number) => ['job-holds', id] as const,
    // Each segment's previous workflow step (GET /jobs/{id}/step-statuses).
    // Changes only when a step starts, ends or is rejected.
    stepStatuses: (id: number) => ['job-step-statuses', id] as const,
  },

  // --- Segments ---
  segments:          { byFile: (fileId: number) => ['segments', fileId] as const },

  // --- Translation memory ---
  tms:               { all: ['tms'] as const },
  tm: {
    langPairs: (id: number) => ['tm-lang-pairs', id] as const,
    entries:   (id: number, src?: string, tgt?: string, search?: string, searchOptions?: TextSearchOptions) =>
      ['tm-entries', id, src, tgt, search, searchOptions] as const,
    /** Prefix key for invalidating all entries of a TM (any language/search). */
    allEntries: (id: number) => ['tm-entries', id] as const,
    /** Per-entry edit history, read by the history viewer. */
    entryHistory: (tmId: number, entryId: number) => ['tm-entry-history', tmId, entryId] as const,
  },
  tmMatches:         {
    all: ['tm-matches'] as const,
    byJobPrefix: (jobId: number) => ['tm-matches', jobId] as const,
    byJob: (jobId: number, source: string, translationId: number | null) => ['tm-matches', jobId, source, translationId] as const,
  },
  // Internal key for the MT preview fetch (endpoint: /jobs/{id}/mt-preview).
  // Keyed by translation id + source: stored MT is per-translation, and two
  // segments with identical source can legitimately carry different MT
  // (segment prompts / context), so sharing a cache entry by source alone
  // would show the wrong segment's stored output.
  // User-facing label in the editor sidebar stays "MT suggestion" — facade.
  mtPreview:         { byJob: (jobId: number, translationId: number | null, source: string) => ['mt-preview', jobId, translationId, source] as const },

  // --- Terminology ---
  terminology:        { all: ['terminology'] as const },
  termEntries: {
    byBase: (gid: number, search?: string) => ['term-entries', gid, search] as const,
    /** Prefix key for invalidating all entries of a term base (any search). */
    allByBase: (gid: number) => ['term-entries', gid] as const,
  },
  termBaseLanguages: (gid: number) => ['term-base-languages', gid] as const,
  concepts: {
    byBase: (gid: number, search?: string, langs?: string[]) => ['concepts', gid, search, langs] as const,
    /** Prefix key for invalidating all concepts of a base (any search/lang). */
    allByBase: (gid: number) => ['concepts', gid] as const,
    /** One concept, as the concept editor loads it. Under the `allByBase`
     * prefix, so a concept mutation also refreshes the open editor. */
    detail: (gid: number, conceptId: number | null) => ['concepts', gid, 'detail', conceptId] as const,
  },

  // --- MT engines ---
  mtEngines:         { all: ['mt-engines'] as const, catalog: ['mt-engines', 'catalog'] as const },
  // Project / template settings show the router's name, so router mutations
  // also invalidate projects + templates.
  mtRouters:         { all: ['mt-routers'] as const },

  // --- TM profiles (reusable TM bundle + match scoring; projects/templates link live) ---
  // Invalidate `tmProfiles.all` after any profile mutation AND after a project/
  // template links, detaches or saves-as (usage_count changes). A profile edit
  // also stales every linked project's settings + the editor's tm-matches.
  tmProfiles:        { all: ['tm-profiles'] as const },
  tmProfile:         { detail: (id: number) => ['tm-profile', id] as const },

  // --- Templates ---
  // `paginated` is the string-prefix key for the searched, cursor-paginated
  // list behind the New project picker (useInfiniteQuery). The full key is
  // `[paginated, search]`; the prefix alone matches every search variant under
  // invalidate, the same way projects.paginated does.
  templates:         { all: ['templates'] as const, paginated: ['templates-paginated'] as const },
  template:          {
    /** Prefix key: every template's detail. */
    all: ['template'] as const,
    detail: (id: number) => ['template', id] as const,
  },

  // --- Step definitions + workflow libraries ---
  // A library edit never touches a project's copied steps, so project-settings
  // keys are NOT invalidated by these — only apply / save-as on a project do
  // that, and they invalidate project.settings themselves.
  stepDefinitions:   { all: ['step-definitions'] as const },
  workflows: {
    all:    ['workflows'] as const,
    detail: (id: number) => ['workflows', id] as const,
    pretranslateOnly: ['workflows', 'pretranslate-only'] as const,
  },

  // --- QA ---
  qaProfiles:        { all: ['qa-profiles'] as const },
  qaProfile:         { detail: (id: number) => ['qa-profile', id] as const },

  // --- Languages ---
  languages:         { all: ['languages'] as const },

  // --- Notifications ---
  notifications:     { all: ['notifications'] as const, count: ['notification-count'] as const },

  // --- Comments ---
  comments: {
    /**
     * Prefix for every translation's comment list. A clear drops the AI
     * comments of the rows it touched, and the caller does not know which —
     * so it invalidates the lot.
     */
    all: ['comments'] as const,
    byTranslation: (id: number) => ['comments', id] as const,
    // Per-file comment counts powering the segment-grid indicator + filter.
    // `allCounts` is the invalidation prefix: CommentPanel adds/deletes a
    // comment knowing only the translation, so it invalidates every file's
    // counts rather than threading fileId/lang down.
    allCounts: ['comment-counts'] as const,
    countsByFile: (fileId: number, lang: string) => ['comment-counts', fileId, lang] as const,
  },

  // --- Segment history ---
  // `all` is the prefix propagation invalidates: it writes a history row on
  // every sibling at once, and one prefix sweep beats N per-id calls.
  segmentHistory: {
    all: ['segment-history'] as const,
    byTranslation: (id: number) => ['segment-history', id] as const,
    /** How each finished workflow step left the segment. Under the `all`
     *  prefix, so every history invalidation refreshes it too. */
    stepStatuses: (id: number) => ['segment-history', id, 'step-statuses'] as const,
  },

  // --- Analysis ---
  projectAnalysis:   {
    /** Prefix key: every project's analysis. Weights feed it. */
    all: ['project-analysis-v2'] as const,
    v2: (pid: number) => ['project-analysis-v2', pid] as const,
  },
  projectProductivity: {
    byProject: (pid: number) => ['project-productivity', pid] as const,
    byStep: (pid: number) => ['project-productivity-by-step', pid] as const,
  },

  // Saved analyses. Separate from the
  // live V2 view above — saved analyses are immutable persisted snapshots;
  // the V2 keys point to the live recompute.
  analyses: {
    list:   (pid: number) => ['analyses', 'list', pid] as const,
    detail: (id: number)  => ['analyses', 'detail', id] as const,
  },

  // Processing time. Read-only derived data — no mutation
  // invalidates it, because nothing in the UI writes timings. It goes stale on
  // its own as the worker flushes, so the panel refetches on open rather than
  // relying on invalidation.
  projectTimings:    { byProject: (pid: number) => ['project-timings', pid] as const },

  // --- AI providers ---
  aiVendors:         {
    all: ['ai-vendors'] as const,
    keylessEngines: (provider: string, tokenId: number) => ['ai-vendors', 'keyless-engines', provider, tokenId] as const,
    enginesUsing: (provider: string, tokenId: number) => ['ai-vendors', 'engines-using', provider, tokenId] as const,
  },

  // --- AI prompts + engines ---
  aiPrompts:         {
    all: ['ai-prompts'] as const,
    // The assembled preview is keyed by the draft it was assembled from, so
    // editing the text or the output mode refetches and nothing else does.
    preview: (draft: unknown) => ['ai-prompts', 'preview', draft] as const,
  },
  aiEngines:         { all: ['ai-engines'] as const },
  // Step lists carry the router's name, so router mutations also invalidate
  // stepDefinitions + workflows.
  aiRouters:         { all: ['ai-routers'] as const },

  // --- Current user (getMe) ---
  me:                { all: ['me'] as const },

  // --- Personal access tokens ---
  // Two distinct scopes: the caller's own tokens, and the org-wide manager view.
  // A create/revoke mutation must invalidate BOTH — a manager looking at the
  // org-wide list after revoking would otherwise see the token still listed.
  apiTokens: {
    mine: ['api-tokens', 'mine'] as const,
    all:  ['api-tokens', 'all'] as const,
  },

  // --- Customers ---
  customers:         { all: ['customers'] as const },
  customer: {
    detail:       (id: number) => ['customer', id] as const,
    projectCount: (id: number) => ['customer-project-count', id] as const,
  },

  // --- Project labels (tenant-defined classifiers + their values) ---
  labels:            { all: ['labels'] as const },
  label: {
    valueProjectCount: (valueId: number) => ['label-value-project-count', valueId] as const,
  },
  projectLabels: {
    all: ['project-labels'] as const,
    byProject: (projectId: number) => ['project-labels', projectId] as const,
  },
  templateLabels: {
    all: ['template-labels'] as const,
    byTemplate: (templateId: number) => ['template-labels', templateId] as const,
  },

  // --- Members (tenant users — admin/member listing) ---
  // The Members tab (OrgMembersPage) reads members with a direct fetch +
  // setState, so it does not share this cache; the Vendors tab does.
  members:           { all: ['members'] as const },

  // --- Organization settings tab: one key per section ---
  orgSettings: {
    registration:      ['org-settings'] as const,
    notificationRules: ['org-notification-rules'] as const,
    analysisWeights:   ['analytics-weights'] as const,
  },

  // --- Built-in chat ---
  chat: { models: ['chat-models'] as const, conversations: ['chat-conversations'] as const, playbooks: ['chat-playbooks'] as const },

  // --- Connected apps ---
  oauthConnections: { list: ['oauth-connections'] as const },

  // --- Dashboards ---
  dashboards: {
    all:   ['dashboards'] as const,
    list:  ['dashboards', 'list'] as const,
    sharedCount: ['dashboards', 'shared-count'] as const,
    // The Pinned view. Under 'dashboards', so every dashboard write refreshes
    // it: a deleted or hidden chart drops out of Pinned too.
    pins: ['dashboards', 'pins'] as const,
    pinned: ['dashboards', 'pinned'] as const,
    pinnedDetail: (id: number) => ['dashboards', 'pinned', id] as const,
    pinnedFilterOptions: (id: number, lastDays: number) => ['dashboards', 'pinned-filter-options', id, lastDays] as const,
    detail: (id: number) => ['dashboards', 'detail', id] as const,
    // `query` is the serialized dashboard filter selection — the SAME string
    // the request carries, so a dropdown change can never leave a card showing
    // a cached answer to a different question.
    chartData: (chartId: number, query = '') => ['dashboards', 'chart-data', chartId, query] as const,
    // Options are derived from the data in the window, so the range is part of
    // the key — a narrower range must not serve the wider range's list.
    filterOptions: (id: number, lastDays: number) => ['dashboards', 'filter-options', id, lastDays] as const,
    suggestion: (dashboardId?: number, shared = false) => ['dashboards', 'suggestion', dashboardId ?? 0, shared] as const,
    // The measure vocabulary behind the chart builder. Generated from the Go
    // registry, so it only changes on deploy.
    catalog: ['dashboards', 'metrics-catalog'] as const,
    // A chart reached by share token. Keyed by the token, not the chart id —
    // the reader may have no way to name the chart any other way.
    sharedChart: (token: string) => ['dashboards', 'shared-chart', token] as const,
    sharedChartData: (token: string, query = '') => ['dashboards', 'shared-chart-data', token, query] as const,
  },

  // --- In-app user documentation. Keyed by API base so a
  // second doc set never shares a cache entry with this one. Read-only content;
  // nothing invalidates these — a deploy changes the bundle, not the cache. ---
  docs: {
    tree:   (base: string) => ['docs', base, 'tree'] as const,
    page:   (base: string, slug: string) => ['docs', base, 'page', slug] as const,
    search: (base: string, q: string) => ['docs', base, 'search', q] as const,
    apiSpec: () => ['docs', 'api-spec'] as const,
    appConfig: () => ['docs', 'app-config'] as const,
  },

  // --- User language pairs ---
  userLanguagePairs: { byUser: (userId: number) => ['user-language-pairs', userId] as const },
  usersByPair: {
    /** Prefix key for invalidating ALL by-pair lookups (any source/target). Use after a user's pair set changes. */
    all:    ['users-by-pair'] as const,
    lookup: (source: string, target: string) => ['users-by-pair', source, target] as const,
  },
} as const

/** The prefix of every issue query of a scope: its lists and its issues' pages. */
export const issuesKey = (scope: IssueScope) => (scope === 'mine' ? queryKeys.issues.all : queryKeys.platform.issues)
