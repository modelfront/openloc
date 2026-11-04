import { Routes, Route, Navigate } from 'react-router-dom'
import ProjectList from '@/pages/ProjectList'
import TasksPage from '@/pages/TasksPage'
import SettingsPage from '@/pages/SettingsPage'
import OrgMembersPage from '@/pages/org/OrgMembersPage'
import IssuesPage from '@/pages/issues/IssuesPage'
import IssueDetailPage from '@/pages/issues/IssueDetailPage'
import { isAdminRole, isLinguistRole } from '@/lib/roles'

/**
 * The pages of a vendor org. Admins manage members, project managers see
 * Projects and Tasks, linguists see Tasks; everyone keeps their personal
 * settings and their reported issues. Every other address goes home. The
 * server refuses the rest of the API to vendor orgs; this only keeps the UI
 * off pages that would fail.
 */
export function VendorOrgRoutes({ role }: { role: string }) {
  const linguist = isLinguistRole(role)
  const home = linguist ? '/tasks' : '/projects'
  const goHome = <Navigate to={home} replace />
  return (
    <Routes>
      <Route path="/tasks" element={<TasksPage />} />
      <Route path="/projects" element={linguist ? goHome : <ProjectList />} />
      <Route path="/org" element={isAdminRole(role) ? <OrgMembersPage /> : goHome} />
      <Route path="/settings" element={<SettingsPage />} />
      <Route path="/issues" element={<IssuesPage scope="mine" role={role} />} />
      <Route path="/issues/:issueId" element={<IssueDetailPage scope="mine" />} />
      <Route path="*" element={goHome} />
    </Routes>
  )
}
