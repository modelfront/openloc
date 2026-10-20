import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import {
  Settings, LogOut, ChevronDown, Building2, Sparkles, BookOpen, Code2, GraduationCap, Bug, Inbox,
} from 'lucide-react'
import { logout, getMe } from '@/api'
import { queryKeys } from '@/lib/query-keys'
import { isAdminRole as isAdmin, isManagerRole } from '@/lib/roles'
import { useStartSavedTour } from '@/lib/playbooks'
import { siteLink, useAppConfig } from '@/pages/docs/DocsHost'
import { ORG_MEMBERS_PATH, ORG_VENDORS_PATH } from '@/pages/org/org-paths'
import { ReportIssueDialog } from '@/components/issues/ReportIssueDialog'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { headerControlHover } from './header-styles'

// Account dropdown in the top header. The trigger shows the signed-in user's
// name; opening it reveals the current org (workspace) + email, then the menu
// items. Settings, Organization, AI settings and Help live only here, not in the
// left nav. Reuses the cached /me query (same key as OrgSwitcher etc.), so it's
// free. Reported issues opens every org's issues for a platform operator (not
// while impersonating), and the org's for everyone else.
export function UserMenu({ role = 'member', isPlatformAdmin = false }: { role?: string; isPlatformAdmin?: boolean }) {
  const navigate = useNavigate()
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const { data: config } = useAppConfig()
  const { tours } = useStartSavedTour()
  const [reporting, setReporting] = useState(false)
  // Issues go to the deployment's operators; the Playground takes no writes.
  const canReport = !!config?.issues && !me?.demo

  return (
    <>
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label={me?.name ? `${me.name} — account menu` : 'Account menu'}
        className={`flex items-center gap-1 px-2 py-1.5 rounded-md text-sm text-gray-600 dark:text-gray-400 ${headerControlHover}`}
      >
        <span className="max-w-[12rem] truncate">{me?.name ?? 'Account'}</span>
        <ChevronDown className="w-3.5 h-3.5 shrink-0" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <div className="px-2 py-1.5">
          <p className="text-sm font-medium text-gray-900 dark:text-gray-100 truncate">{me?.name ?? '—'}</p>
          {me?.email && (
            <p className="text-xs text-gray-500 dark:text-gray-400 truncate">{me.email}</p>
          )}
          <p className="flex items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400 truncate mt-1.5">
            <Building2 className="w-3.5 h-3.5 text-gray-400 shrink-0" />
            <span className="truncate">{me?.tenant_name ?? '—'}</span>
          </p>
        </div>
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={() => navigate('/settings')}>
          <Settings className="w-4 h-4" />
          Settings
        </DropdownMenuItem>
        {/* AI settings is open to every role: admins manage the org's vendor
            keys there, everyone else manages their own MCP tokens.
            Organization settings is for managers: owners and admins get every
            tab, a project manager only Vendors. */}
        {isManagerRole(role) && (
          <DropdownMenuItem onClick={() => navigate(isAdmin(role) ? ORG_MEMBERS_PATH : ORG_VENDORS_PATH)}>
            <Building2 className="w-4 h-4" />
            Organization settings
          </DropdownMenuItem>
        )}
        <DropdownMenuItem onClick={() => navigate('/ai-settings')}>
          <Sparkles className="w-4 h-4" />
          AI settings
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        {/* Learn is open to every role and lists the tours the role may take;
            it is hidden when there are none (the Playground is off). */}
        {tours.length > 0 && (
          <DropdownMenuItem onClick={() => navigate('/learn')} data-testid="user-menu-learn">
            <GraduationCap className="w-4 h-4" />
            Learn
          </DropdownMenuItem>
        )}
        {/* Docs is open to every role; the server trims the page tree per role.
            Both open in a new tab, on their own address when they have one
            (siteLink), so the app stays where it was. */}
        <DropdownMenuItem onClick={() => window.open(siteLink(config, '/docs'), '_blank', 'noopener')}>
          <BookOpen className="w-4 h-4" />
          Docs
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => window.open(siteLink(config, '/api-docs'), '_blank', 'noopener')}>
          <Code2 className="w-4 h-4" />
          API
        </DropdownMenuItem>
        {canReport && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => setReporting(true)} data-testid="user-menu-report-issue">
              <Bug className="w-4 h-4" />
              Report an issue
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => navigate(isPlatformAdmin ? '/platform/issues' : '/issues')}
              data-testid="user-menu-reported-issues">
              <Inbox className="w-4 h-4" />
              Reported issues
            </DropdownMenuItem>
          </>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={() => logout()} className="text-red-600 dark:text-red-400">
          <LogOut className="w-4 h-4" />
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
    {/* Outside the menu, which unmounts its content on close. */}
    {canReport && <ReportIssueDialog open={reporting} onClose={() => setReporting(false)} />}
    </>
  )
}
