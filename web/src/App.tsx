import { useState, useEffect, Suspense } from 'react'
import { Routes, Route, Navigate, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { lazyWithReload } from '@/lib/lazy-reload'
import ProjectList from './pages/ProjectList'
import ProjectCreate from './pages/ProjectCreate'
import ProjectDetail from './pages/ProjectDetail'
// Lazy-load CATEditor — heaviest page (TipTap + ProseMirror ~70KB gzipped).
// lazyWithReload recovers from stale-chunk failures during a rolling deploy:
// a browser holding the pre-deploy index.html requests an old chunk hash that
// no longer exists → the dynamic import rejects → without recovery the user
// sees "CATEditor crashed". lazyWithReload forces a one-time reload to pull the
// fresh bundle instead (see INC entry / lazy-reload.ts).
const CATEditor = lazyWithReload(() => import('./pages/CATEditor'), 'CATEditor')
// Lazy-load the /debug component gallery — hidden dev tool, kept out of the
// main bundle so it costs production users nothing.
const DebugPage = lazyWithReload(() => import('./pages/DebugPage'), 'DebugPage')
// /debug/llm — live LLM/extension request-response viewer. Also hidden; the
// backing endpoint only exists when the server runs with DEV_AUTO_LOGIN=true.
const LLMDebugPage = lazyWithReload(() => import('./pages/LLMDebugPage'), 'LLMDebugPage')
// /docs — in-app user documentation. Lazy so the Markdown
// renderer and docs UI stay out of the main bundle for users who never open it.
const DocsPage = lazyWithReload(() => import('./pages/docs/DocsPage'), 'DocsPage')
const PublicDocsPage = lazyWithReload(() => import('./pages/docs/PublicDocsPage'), 'PublicDocsPage')
const PublicApiDocsPage = lazyWithReload(() => import('./pages/api-docs/PublicApiDocsPage'), 'PublicApiDocsPage')
const ApiDocsPage = lazyWithReload(() => import('./pages/api-docs/ApiDocsPage'), 'ApiDocsPage')
import TMList from './pages/TMList'
import TMBrowser from './pages/TMBrowser'
import TMProfileDetail from './pages/TMProfileDetail'
import TerminologyList from './pages/TerminologyList'
import NotificationBell from './components/NotificationBell'
import { OrgSwitcher } from './components/OrgSwitcher'
import { ThemeToggle } from './components/layout/ThemeToggle'
import TerminologyBrowser from './pages/TerminologyBrowser'
import MTRoutersPage from './pages/mt/MTRoutersPage'
import MTEnginesPage from './pages/mt/MTEnginesPage'
import MTPromptsPage from './pages/mt/MTPromptsPage'
import AIRoutersPage from './pages/ai/AIRoutersPage'
import AIEnginesPage from './pages/ai/AIEnginesPage'
import AIPromptsPage from './pages/ai/AIPromptsPage'
import PromptDetailPage from './pages/ai/PromptDetailPage'
import TemplateList from './pages/TemplateList'
import QAProfiles from './pages/QAProfiles'
import TemplateDetail from './pages/TemplateDetail'
import WorkflowList from './pages/workflows/WorkflowList'
import StepLibrary from './pages/workflows/StepLibrary'
import LoginPage from './pages/LoginPage'
import OrgPicker from './pages/OrgPicker'
import AwaitingApproval from './pages/AwaitingApproval'
import PlatformOrgs from './pages/PlatformOrgs'
import IssuesPage from './pages/issues/IssuesPage'
import IssueDetailPage from './pages/issues/IssueDetailPage'
import { UserMenu } from './components/layout/UserMenu'
import AcceptInvite from './pages/AcceptInvite'
import ForgotPassword from './pages/ForgotPassword'
import ResetPassword from './pages/ResetPassword'
import { NavRail } from './components/layout/NavRail'
import { ErrorBoundary } from './components/ErrorBoundary'
import { LoadingPlaceholder } from './components/layout/LoadingPlaceholder'
import { setIdentity as setErrtrackIdentity } from './lib/errtrack'
import { PaletteProvider } from './components/PaletteProvider'
import { DocsLayout } from './pages/docs/DocsLayout'
import { useAppConfig } from './pages/docs/DocsHost'
import SettingsPage from './pages/SettingsPage'
import OrgMembersPage from './pages/org/OrgMembersPage'
import OrgSettingsPage from './pages/org/OrgSettingsPage'
import AISettingsPage from './pages/AISettingsPage'
import LabelsPage from './pages/LabelsPage'
import { DashboardsPage, DashboardsLanding } from './pages/DashboardsPage'
import { PinnedDashboardPage } from './pages/PinnedDashboardPage'
import { IntegrationsPage } from './pages/IntegrationsPage'
import { DashboardPage } from './pages/DashboardPage'
import { ChartPage } from './pages/ChartPage'
import { SharedChartPage } from './pages/SharedChartPage'
import TasksPage from './pages/TasksPage'
import LearnPage from './pages/LearnPage'
import VendorsPage from './pages/org/VendorsPage'
import { ORG_VENDORS_PATH } from './pages/org/org-paths'
import { ChatPanel } from './components/chat/ChatPanel'
import { ChatDemoPanel } from './components/chat/ChatDemoPanel'
import { DemoBanner } from './components/demo/DemoBanner'
import { TourFromURL } from './components/guide/TourFromURL'
import TourEntryPage from './components/guide/TourEntryPage'
import { tourFromHash, tourPath } from './lib/demo'
import { SessionBanner, SessionBannerAction } from './components/layout/SessionBanner'
import { isLoggedIn, checkAuth, getMe } from './api'
import { queryKeys } from './lib/query-keys'
import { isAdminRole as isAdmin, isLinguistRole } from './lib/roles'

// anonymous: what a visitor without a session sees instead of the sign-in
// redirect.
function AuthGuard({ children, anonymous }: {
  children: (ctx: { role: string; isPlatformAdmin: boolean; impersonating: boolean; demo: boolean }) => React.ReactNode
  anonymous?: React.ReactNode
}) {
  const [status, setStatus] = useState<'checking' | 'ok' | 'no'>(
    isLoggedIn() ? 'ok' : 'checking'
  )
  const [role, setRole] = useState<string>('member')
  // Authenticated but no org selected → bounce to the org picker.
  const [needsOrg, setNeedsOrg] = useState(false)
  // Selected org is awaiting operator approval → awaiting-approval screen.
  const [orgPending, setOrgPending] = useState(false)
  // Platform operator → unlock the org console. Suppressed while
  // impersonating (operator "logged in as" an org).
  const [isPlatformAdmin, setIsPlatformAdmin] = useState(false)
  const [impersonating, setImpersonating] = useState(false)
  // The shared read-only demo organization (the Playground).
  const [demo, setDemo] = useState(false)
  // Gate rendering on the initial role fetch so routes/nav don't flash the
  // wrong view for a tick (e.g. a vendor briefly seeing the full nav + a
  // redirect, or an admin's hard-loaded /org/vendors bouncing to "/"). Set true on
  // success OR after retries fail (fall back to the default 'member' role
  // rather than hang on the loading screen forever).
  const [roleLoaded, setRoleLoaded] = useState(false)

  useEffect(() => {
    if (status === 'checking') {
      checkAuth().then(ok => setStatus(ok ? 'ok' : 'no'))
    }
    if (status === 'ok') {
      // GetMe can fail on a network blip or a 401 race during
      // refresh. Log it and retry once after 1s; if both attempts fail, render
      // with the default role. checkAuth (above) decides whether the session
      // is valid — if it fails, status flips to 'no' and the user is redirected.
      //
      // Cancellation: useEffect cleanup flips `cancelled` so an in-flight
      // retry that resolves after unmount (logout, status change) won't
      // call setRole on a dead component or fire a wasted network request.
      let cancelled = false
      let retryHandle: ReturnType<typeof setTimeout> | undefined
      const fetchRole = (attempt: number) =>
        getMe()
          .then(u => {
            if (cancelled) return
            if (u.needs_org_selection) { setNeedsOrg(true); setRoleLoaded(true); return }
            if (u.org_pending) { setOrgPending(true); setRoleLoaded(true); return }
            setRole(u.role ?? 'member')
            // Operator tooling is hidden while impersonating an org.
            setIsPlatformAdmin(!!u.is_platform_admin && !u.impersonating)
            setImpersonating(!!u.impersonating)
            setDemo(!!u.demo)
            setRoleLoaded(true)
            // Advisory: the notice proxy rewrites identity from the session
            // cookie before anything reaches Honeybadger.
            // Cleared on logout.
            setErrtrackIdentity({
              id: u.id,
              tenant_id: u.tenant_id,
              role: u.role,
              impersonating: u.impersonating,
            })
          })
          .catch(err => {
            if (cancelled) return
            console.error('getMe failed (attempt', attempt, '):', err)
            if (attempt === 1) {
              retryHandle = setTimeout(() => {
                if (!cancelled) fetchRole(2)
              }, 1000)
            } else if (!cancelled) {
              // Both attempts failed — render with the default role rather than
              // stay stuck on the loading screen.
              setRoleLoaded(true)
            }
          })
      fetchRole(1)
      return () => {
        cancelled = true
        if (retryHandle) clearTimeout(retryHandle)
      }
    }
  }, [status])

  if (status === 'checking' || (status === 'ok' && !roleLoaded)) {
    return <div className="min-h-screen bg-background flex items-center justify-center">
      <LoadingPlaceholder continues />
    </div>
  }
  if (status === 'no') {
    return anonymous ?? <Navigate to="/login" replace />
  }
  if (needsOrg) {
    return <Navigate to="/select-org" replace />
  }
  if (orgPending) {
    return <Navigate to="/awaiting-approval" replace />
  }
  return <>{children({ role, isPlatformAdmin, impersonating, demo })}</>
}

// Amber banner shown while a platform operator has "Open"-ed an org.
// Names the org so it's unmistakable which workspace is being viewed.
function ImpersonationBanner() {
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  return (
    <SessionBanner action={<SessionBannerAction href="/select-org">Exit</SessionBannerAction>}>
      You&rsquo;re viewing <strong>{me?.tenant_name ?? 'this organization'}</strong> as a platform operator.
    </SessionBanner>
  )
}

function SignedInDocs({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <DocsLayout signedIn>
      <ErrorBoundary title={title}><Suspense fallback={<LoadingPlaceholder continues />}>{children}</Suspense></ErrorBoundary>
    </DocsLayout>
  )
}

// Without a session, the help and the API docs are public; everything else
// signs in first. The docs host and the developer host (docs_host,
// developer_host in /api/config) are each one site, at the root; there is no
// session on them, since the session cookie belongs to the app's address.
function AnonymousRoutes() {
  const { data, isError } = useAppConfig()
  if (!data && !isError) return <LoadingPlaceholder continues />
  const docs = (atRoot: boolean) => (
    <ErrorBoundary title="Docs crashed"><Suspense fallback={<LoadingPlaceholder continues />}><PublicDocsPage atRoot={atRoot} /></Suspense></ErrorBoundary>
  )
  const apiDocs = (atRoot: boolean) => (
    <ErrorBoundary title="API docs crashed"><Suspense fallback={<LoadingPlaceholder continues />}><PublicApiDocsPage atRoot={atRoot} /></Suspense></ErrorBoundary>
  )
  if (data?.docs_host) return <Routes><Route path="*" element={docs(true)} /></Routes>
  if (data?.developer_host) return <Routes><Route path="*" element={apiDocs(true)} /></Routes>
  return (
    <Routes>
      <Route path="/docs/*" element={docs(false)} />
      <Route path="/api-docs/*" element={apiDocs(false)} />
      <Route path="*" element={<Navigate to="/login" replace />} />
    </Routes>
  )
}

export default function App() {
  return (
    // Root boundary: a render throw on ANY route shows the error panel
    // instead of a blank page, and reports to Honeybadger (lib/errtrack).
    <ErrorBoundary>
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/invite/:token" element={<AcceptInvite />} />
      <Route path="/forgot-password" element={<ForgotPassword />} />
      <Route path="/reset-password/:token" element={<ResetPassword />} />
      {/* Org picker: authenticated but pre-org-selection. Requires a
          valid token — listOrgs 401s to /login if not — but is NOT behind the
          org-scoped AuthGuard. */}
      <Route path="/select-org" element={<OrgPicker />} />
      {/* Awaiting-approval: a pending org's owner holds a valid token
          but the app is gated. Top-level like /select-org — not behind the
          org-scoped shell. */}
      <Route path="/awaiting-approval" element={<AwaitingApproval />} />
      <Route
        path="/*"
        element={
          <AuthGuard anonymous={<AnonymousRoutes />}>
            {({ role, isPlatformAdmin, impersonating, demo }) => {
            const linguist = isLinguistRole(role)
            // Linguists land on and are confined to /tasks; the block redirects
            // any route they may not see. Since linguist is the only non-manager
            // role, this also gates the manager-only routes below.
            const linguistHome = '/tasks'
            const linguistBlock = (el: React.ReactNode) => (linguist ? <Navigate to={linguistHome} replace /> : el)
            // An address carrying a saved tour after "#" belongs to the demo:
            // outside it, go to the tour's entry address before any page renders.
            const hashTour = demo ? null : tourFromHash(window.location.hash)
            return (
            <Routes>
            {/* The docs and the API docs have their own frame, without the nav
                rail. Every role (no linguistBlock): the server filters pages
                per role, and any member may hold an access key. */}
            <Route path="/docs/*" element={<SignedInDocs title="Docs crashed"><DocsPage /></SignedInDocs>} />
            <Route path="/api-docs/*" element={<SignedInDocs title="API docs crashed"><ApiDocsPage /></SignedInDocs>} />
            <Route path="*" element={
            <div className="h-screen flex bg-background overflow-hidden">
              {/* Applies the user's saved palette. Mounted here (inside
                  the authed shell) so it never fetches appearance while logged
                  out — see PaletteProvider. */}
              <PaletteProvider />
              <NavRail role={role} isPlatformAdmin={isPlatformAdmin} />
              <div className="flex-1 flex flex-col min-w-0">
                {/* Impersonation banner: a platform operator is viewing
                    this org via "Open". Kept prominent so they don't mistake it
                    for their own org. Exit returns to the org picker. */}
                {impersonating && <ImpersonationBanner />}
                {demo && <DemoBanner />}
                {/* Top header: the org switcher on the left, page-level controls
                    (notifications, theme, account) on the right. */}
                <header className="h-12 shrink-0 border-b border-gray-200 dark:border-gray-800 bg-card flex items-center justify-end gap-1 pl-2 pr-4">
                  <div className="mr-auto min-w-0">
                    <OrgSwitcher />
                  </div>
                  <NotificationBell />
                  <ThemeToggle />
                  <UserMenu role={role} isPlatformAdmin={isPlatformAdmin} />
                </header>
                <main className="flex-1 overflow-y-auto pt-6">
                {hashTour ? <Navigate to={tourPath(hashTour.tour, hashTour.start)} replace /> : (
                <Routes>
                  {/* A saved tour's entry address; it shows no organization's data. */}
                  <Route path="/tour/:name/:step?" element={<TourEntryPage />} />
                  {/* Tasks: every role. The linguist's home + a "my work" view. */}
                  <Route path="/tasks" element={<TasksPage />} />
                  {/* Learn: every role; the server lists the tours per role. */}
                  <Route path="/learn" element={<LearnPage />} />
                  {/* Manager pages — linguists are redirected to their Tasks page.
                      (linguistBlock == manager-gate: linguist is the only
                      non-manager role.) */}
                  <Route path="/" element={<Navigate to={linguist ? linguistHome : '/projects'} replace />} />
                  <Route path="/projects" element={linguistBlock(<ProjectList />)} />
                  <Route path="/projects/new" element={linguistBlock(<ProjectCreate />)} />
                  {/* One route per project tab (SubNav): the URL is the state,
                      so a deep link and back/forward work without extra wiring.
                      All five render the same page, which picks the panel from
                      the path — the shell (header, metadata, SubNav) is shared. */}
                  <Route path="/projects/:projectId" element={linguistBlock(<ProjectDetail />)} />
                  <Route path="/projects/:projectId/files" element={linguistBlock(<ProjectDetail />)} />
                  <Route path="/projects/:projectId/analyses" element={linguistBlock(<ProjectDetail />)} />
                  <Route path="/projects/:projectId/workflow" element={linguistBlock(<ProjectDetail />)} />
                  <Route path="/projects/:projectId/settings" element={linguistBlock(<ProjectDetail />)} />
                  <Route path="/projects/:projectId/history" element={linguistBlock(<ProjectDetail />)} />
                  {/* CAT editor: allowed for linguists (backend enforces they may
                      only open jobs assigned to them). */}
                  <Route path="/projects/:projectId/jobs/:jobId/edit" element={<ErrorBoundary title="CAT editor crashed"><Suspense fallback={<LoadingPlaceholder continues message="Loading editor…" />}><CATEditor /></Suspense></ErrorBoundary>} />
                  <Route path="/memories" element={linguistBlock(<TMList />)} />
                  <Route path="/memories/profiles" element={linguistBlock(<TMList />)} />
                  {/* Static segment before /memories/:tmId so a profile id is never read as a TM id. */}
                  <Route path="/memories/profiles/:profileId" element={linguistBlock(<TMProfileDetail />)} />
                  <Route path="/memories/:tmId" element={linguistBlock(<TMBrowser />)} />
                  <Route path="/terminology" element={linguistBlock(<TerminologyList />)} />
                  <Route path="/terminology/:terminologyId" element={linguistBlock(<TerminologyBrowser />)} />
                  {/* MT section: Routers | Engines | Prompts, one route per tab, like /ai.
                      /engines redirects to /mt/engines so existing links still land. */}
                  <Route path="/mt" element={linguistBlock(<MTRoutersPage />)} />
                  <Route path="/mt/engines" element={linguistBlock(<MTEnginesPage />)} />
                  <Route path="/mt/prompts" element={linguistBlock(<MTPromptsPage />)} />
                  <Route path="/mt/prompts/:promptId" element={linguistBlock(<PromptDetailPage purpose="mt" />)} />
                  <Route path="/engines" element={<Navigate to="/mt/engines" replace />} />
                  {/* AI section: Routers | Engines | Prompts,
                      one route per tab. */}
                  <Route path="/ai" element={linguistBlock(<AIRoutersPage />)} />
                  <Route path="/ai/engines" element={linguistBlock(<AIEnginesPage />)} />
                  <Route path="/ai/engines/new" element={linguistBlock(<AIEnginesPage />)} />
                  <Route path="/ai/new" element={linguistBlock(<AIRoutersPage />)} />
                  <Route path="/ai/prompts" element={linguistBlock(<AIPromptsPage />)} />
                  <Route path="/ai/prompts/:promptId" element={linguistBlock(<PromptDetailPage purpose="qe_ape" />)} />
                  <Route path="/templates" element={linguistBlock(<TemplateList />)} />
                  <Route path="/quality" element={linguistBlock(<QAProfiles />)} />
                  <Route path="/labels" element={linguistBlock(<LabelsPage />)} />
                  {/* /customers redirects to /labels so an
                      existing bookmark or link still lands. */}
                  <Route path="/customers" element={<Navigate to="/labels" replace />} />
                  <Route path="/integrations" element={linguistBlock(<IntegrationsPage />)} />
                  {/* /dashboards lands on Pinned when the person has pinned
                      something, else on Organization dashboards. */}
                  <Route path="/dashboards" element={linguistBlock(<DashboardsLanding />)} />
                  <Route path="/dashboards/pinned" element={linguistBlock(<DashboardsPage />)} />
                  <Route path="/dashboards/pinned/:pinnedId" element={linguistBlock(<PinnedDashboardPage />)} />
                  <Route path="/dashboards/organization" element={linguistBlock(<DashboardsPage />)} />
                  <Route path="/dashboards/mine" element={linguistBlock(<DashboardsPage />)} />
                  <Route path="/dashboards/:dashboardId" element={linguistBlock(<DashboardPage />)} />
                  {/* One chart, full page — the unit people share by link. Same
                      gate as the dashboard it belongs to. */}
                  <Route path="/dashboards/:dashboardId/charts/:chartId" element={linguistBlock(<ChartPage />)} />
                  {/* NOT linguistBlock'd: a share token is the authorization,
                      so someone who cannot browse dashboards can still open a
                      chart that was sent to them. */}
                  <Route path="/shared/charts/:token" element={<SharedChartPage />} />
                  <Route path="/vendors" element={<Navigate to={ORG_VENDORS_PATH} replace />} />
                  <Route path="/templates/:templateId" element={linguistBlock(<TemplateDetail />)} />
                  <Route path="/templates/:templateId/workflow" element={linguistBlock(<TemplateDetail />)} />
                  {/* Step + workflow libraries. A workflow is
                      edited in a dialog on the list; the old detail URL redirects. */}
                  <Route path="/workflows" element={linguistBlock(<WorkflowList />)} />
                  <Route path="/workflows/steps" element={linguistBlock(<StepLibrary />)} />
                  <Route path="/workflows/steps/new" element={linguistBlock(<StepLibrary />)} />
                  <Route path="/workflows/:workflowId" element={<WorkflowEditRedirect />} />
                  {/* Organization: Members and Settings are owner/admin; Vendors is
                      every manager, so a project manager lands there. */}
                  <Route path="/org" element={isAdmin(role) ? <OrgMembersPage /> : <Navigate to={linguist ? linguistHome : ORG_VENDORS_PATH} replace />} />
                  <Route path="/org/vendors" element={linguistBlock(<VendorsPage />)} />
                  <Route path="/org/settings" element={isAdmin(role) ? <OrgSettingsPage /> : <Navigate to={linguist ? linguistHome : ORG_VENDORS_PATH} replace />} />
                  {/* AI settings: org vendor keys (admin-only, gated inside the
                      page) + the user's own MCP tokens, which every role has
. Hence no route gate. */}
                  <Route path="/ai-settings" element={<AISettingsPage />} />
                  <Route path="/settings" element={<SettingsPage />} />
                  {/* Platform operator org console. Client gate mirrors
                      the backend PLATFORM_ADMIN_EMAILS check. */}
                  <Route path="/platform/orgs" element={isPlatformAdmin ? <PlatformOrgs /> : <Navigate to="/projects" replace />} />
                  <Route path="/platform/issues" element={isPlatformAdmin ? <IssuesPage scope="platform" role={role} /> : <Navigate to="/projects" replace />} />
                  <Route path="/platform/issues/:issueId" element={isPlatformAdmin ? <IssueDetailPage scope="platform" /> : <Navigate to="/projects" replace />} />
                  {/* Issues: every role files and follows their own. */}
                  <Route path="/issues" element={<IssuesPage scope="mine" role={role} />} />
                  <Route path="/issues/:issueId" element={<IssueDetailPage scope="mine" />} />
                  {/* Component gallery — hidden (not in nav), reachable at /debug
                      in every build for tuning shared components against real
                      theme tokens. */}
                  {/* `/debug/*`: the gallery's SubNav demo links to a sibling path
                      that renders the gallery too, so the demo tabs are live. */}
                  <Route path="/debug/*" element={<Suspense fallback={<LoadingPlaceholder />}><DebugPage /></Suspense>} />
                  {/* Live LLM/extension request-response viewer — hidden, dev-only
                      (the endpoint 404s unless DEV_AUTO_LOGIN=true and the page
                      shows a hint instead). */}
                  <Route path="/debug/llm" element={<Suspense fallback={<LoadingPlaceholder />}><LLMDebugPage /></Suspense>} />
                </Routes>
                )}
                </main>
                {/* Built-in AI assistant. Inside the authed shell so
                    it exists on every page; fixed-position so it never affects
                    layout. */}
                {/* The demo organization is read-only and shared, so the
                    assistant (which writes conversations) does not run
                    there; it shows an example conversation instead. */}
                {demo ? <ChatDemoPanel /> : <ChatPanel />}
                <TourFromURL />
              </div>
            </div>
            } />
            </Routes>
            )
            }}
          </AuthGuard>
        }
      />
    </Routes>
    </ErrorBoundary>
  )
}

// /workflows/:id → the list with that workflow's edit dialog open.
function WorkflowEditRedirect() {
  const { workflowId } = useParams<{ workflowId: string }>()
  return <Navigate to={`/workflows?edit=${workflowId ?? ''}`} replace />
}
