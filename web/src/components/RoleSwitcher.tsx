// RoleSwitcher — DEV-ONLY header control to switch the current user's role on
// the fly, for testing role-gated UI (admin vs vendor vs member …). Rendered
// only in dev builds (guarded at the call site in App.tsx); the backend
// endpoint it calls (POST /api/auth/dev-role) 404s in production too.
//
// Switching mints a fresh token with the new role (role lives in the JWT), then
// a full reload re-runs AuthGuard so nav + routes + every query reflect it.

import { useQuery } from '@tanstack/react-query'
import { Wrench } from 'lucide-react'
import { getMe, devSetRole } from '../api'
import { queryKeys } from '../lib/query-keys'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from './ui/dropdown-menu'

const ROLES = ['owner', 'admin', 'project_manager', 'linguist']

export function RoleSwitcher() {
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const current = me?.role

  const switchTo = async (role: string) => {
    if (role === current) return
    await devSetRole(role)
    // Hard reload so AuthGuard re-fetches the role and the whole app re-renders
    // against the new one (nav visibility, route guards, cached queries).
    window.location.assign('/')
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        title="Dev: switch role"
        data-testid="role-switcher"
        className="flex items-center gap-1 rounded-md border border-dashed border-amber-400/70 px-2 py-0.5 text-xs font-medium text-amber-600 dark:text-amber-400 transition-colors hover:bg-amber-50 dark:hover:bg-amber-950/40"
      >
        <Wrench className="w-3 h-3" />
        {current ?? '…'}
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        <div className="px-2 py-1.5 text-xs font-medium text-gray-500 dark:text-gray-400">Switch role (dev)</div>
        <DropdownMenuSeparator />
        {ROLES.map(r => (
          <DropdownMenuItem
            key={r}
            onClick={() => switchTo(r)}
            disabled={r === current}
            data-testid={`role-switcher-${r}`}
          >
            {r}
            {r === current ? ' (current)' : ''}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
