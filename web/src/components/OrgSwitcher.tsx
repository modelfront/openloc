import { useEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { toast } from 'sonner'
import { ArrowLeft, Building2, Check, FlaskConical } from 'lucide-react'
import { getMe, listOrgs, selectOrg } from '../api'
import { demoLeaveLabel, goToDemo, leavePlayground } from '../lib/demo'
import { queryKeys } from '../lib/query-keys'
import { useAppConfig } from '../pages/docs/DocsHost'
import { headerControlHover } from './layout/header-styles'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from './ui/dropdown-menu'

// Workspace switcher: the current org's name, at the left end of the
// top header. Hovering it reveals the identity's orgs; the current one is
// checkmarked. The shared demo organization (the Playground) is the last
// entry while the server has it on. Inside the Playground the menu holds
// only the way back, as the banner does. Base UI's Menu has no built-in open-on-hover, so
// we drive its controlled open state from mouse move/leave with a short close
// delay that bridges the gap between the trigger and the popup. The trigger
// opens on `mousemove`, not `mouseenter`: when the nav rail collapses, the
// header slides under a still cursor and the browser fires `mouseenter` alone.
export function OrgSwitcher() {
  const { data: me } = useQuery({ queryKey: queryKeys.auth.me, queryFn: getMe })
  const inDemo = !!me?.demo
  // A Playground visitor may not list orgs; the menu offers the way back instead.
  const { data: orgs } = useQuery({ queryKey: queryKeys.auth.orgs, queryFn: listOrgs, enabled: !!me && !inDemo })
  const playground = !!useAppConfig().data?.playground
  const currentId = me?.tenant_id

  const [open, setOpen] = useState(false)
  const closeTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const cancelClose = () => {
    if (closeTimer.current) clearTimeout(closeTimer.current)
    closeTimer.current = null
  }
  const openNow = () => { cancelClose(); setOpen(true) }
  const closeSoon = () => { cancelClose(); closeTimer.current = setTimeout(() => setOpen(false), 120) }
  useEffect(() => cancelClose, [])

  const switchOrg = async (tenantId: number) => {
    if (tenantId === currentId) return
    await selectOrg(tenantId)
    // Full reload so all cached queries refetch under the new org's token.
    window.location.assign('/')
  }

  const name = me?.tenant_name ?? ''
  const label = 'flex max-md:min-w-0 max-md:max-w-full items-center gap-2 px-2 py-1.5 rounded-md text-sm text-gray-600 dark:text-gray-400 select-none'
  // Same icon as the menu's org rows.
  const content = (
    <>
      <Building2 className="w-4 h-4 shrink-0" />
      <span className="max-md:min-w-0 max-w-[12rem] truncate">{name}</span>
    </>
  )

  // No orgs to offer (loading, or somehow none) → plain name, no menu.
  if (!inDemo && (!orgs || orgs.length === 0)) {
    return <span className={label}>{content}</span>
  }

  return (
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger
        onMouseMove={openNow}
        onMouseLeave={closeSoon}
        aria-label={`${name} — switch organization`}
        className={`${label} outline-hidden cursor-pointer ${headerControlHover}`}
      >
        {content}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" onMouseEnter={openNow} onMouseLeave={closeSoon}>
        {inDemo && (
          <DropdownMenuItem onClick={leavePlayground} className="flex items-center gap-2" data-testid="org-switcher-leave-demo">
            <ArrowLeft className="w-4 h-4" />
            <span className="flex-1">{demoLeaveLabel(me)}</span>
          </DropdownMenuItem>
        )}
        {orgs?.map(org => (
          <DropdownMenuItem
            key={org.tenant_id}
            onClick={() => switchOrg(org.tenant_id)}
            className="flex items-center gap-2"
          >
            <Building2 className="w-4 h-4" />
            <span className="flex-1">{org.tenant_name}</span>
            {org.tenant_id === currentId && <Check className="w-4 h-4 text-gray-500" />}
          </DropdownMenuItem>
        ))}
        {playground && !inDemo && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              onClick={() => { goToDemo().catch((err: Error) => toast.error(`Could not open the demo: ${err.message}`)) }}
              className="flex items-center gap-2"
              data-testid="org-switcher-demo"
            >
              <FlaskConical className="w-4 h-4" />
              <span className="flex-1">Playground</span>
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
