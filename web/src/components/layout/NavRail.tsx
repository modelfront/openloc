import { useState } from 'react'
import { Link, NavLink } from 'react-router-dom'
import {
  FolderKanban, Database, BookMarked, Tags, LayoutTemplate, Cpu, Sparkles,
  ShieldCheck, ListChecks, ClipboardCheck, LayoutDashboard, Plug, Workflow,
  PanelLeftClose, PanelLeftOpen,
  type LucideIcon,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { useStoredState } from '@/hooks/useStoredState'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger, TooltipProvider } from '@/components/ui/tooltip'
import { RoleSwitcher } from '../RoleSwitcher'
import { Wordmark } from './Wordmark'
import { isLinguistRole } from '../../lib/roles'

// Vertical nav rows in the left rail: full-width rounded items with muted text
// that sharpens on hover with a subtle background; the active route gets a
// filled background + highlighted, slightly-bolder text. Active styling hangs
// off `aria-current`, which NavLink sets itself — that keeps `className` a
// plain string, which the collapsed rail needs so the row can be handed to a
// Tooltip trigger.
const navLinkBase =
  'flex items-center gap-2.5 rounded-md px-3 py-2 text-gray-600 dark:text-gray-400 transition-colors hover:bg-gray-100 dark:hover:bg-gray-800 hover:text-gray-900 dark:hover:text-gray-100 aria-[current=page]:bg-gray-100 dark:aria-[current=page]:bg-gray-800 aria-[current=page]:text-gray-900 dark:aria-[current=page]:text-gray-100 aria-[current=page]:font-medium'
// Collapsed rows are square icon buttons — the label moves into a tooltip.
const navLinkCollapsed = 'w-10 justify-center px-0'

interface NavItem {
  to: string
  label: string
  icon: LucideIcon
  end?: boolean
  linguistAllowed?: boolean
  guide?: string
}

// Left-side nav. Tasks sits directly below Projects and is the ONLY item a
// linguist sees (`linguistAllowed`); every other tab is manager-only
// (owner/admin/PM).
const NAV_ITEMS: NavItem[] = [
  { to: '/projects', label: 'Projects', icon: FolderKanban, end: true, guide: 'nav.projects' },
  { to: '/tasks', label: 'Tasks', icon: ListChecks, linguistAllowed: true, guide: 'nav.tasks' },
  { to: '/memories', label: 'Translation memories', icon: Database, guide: 'nav.memories' },
  { to: '/terminology', label: 'Terminology', icon: BookMarked, guide: 'nav.terminology' },
  { to: '/labels', label: 'Labels', icon: Tags, guide: 'nav.labels' },
  { to: '/templates', label: 'Templates', icon: LayoutTemplate, guide: 'nav.templates' },
  { to: '/workflows', label: 'Workflows', icon: Workflow, guide: 'nav.workflows' },
  { to: '/mt', label: 'MT', icon: Cpu, guide: 'nav.mt' },
  { to: '/ai', label: 'AI', icon: Sparkles, guide: 'nav.ai' },
  { to: '/integrations', label: 'Integrations', icon: Plug, guide: 'nav.integrations' },
  { to: '/quality', label: 'QA profiles', icon: ShieldCheck, guide: 'nav.quality' },
  { to: '/dashboards', label: 'Dashboards', icon: LayoutDashboard, guide: 'nav.dashboards' },
]

// Platform operator surface — platform operators only.
const PLATFORM_ITEM: NavItem = { to: '/platform/orgs', label: 'Organizations', icon: ClipboardCheck }

// Nav items for a role. Linguists see ONLY their Tasks tab; managers
// (owner/admin/PM) see everything. Settings, Organization, AI settings and Help
// live in the account dropdown, not the nav.
export function navItemsForRole(role: string, isPlatformAdmin: boolean): NavItem[] {
  const items = isLinguistRole(role) ? NAV_ITEMS.filter(i => i.linguistAllowed) : NAV_ITEMS
  return isPlatformAdmin ? [...items, PLATFORM_ITEM] : items
}

function RailLink({ item, collapsed }: { item: NavItem; collapsed: boolean }) {
  const link = (
    <NavLink
      to={item.to}
      end={item.end}
      data-guide={item.guide}
      // The tooltip below is for sighted users only — Base UI's tooltip wires
      // no `aria-describedby`/`aria-labelledby`, so without this the collapsed
      // row is a link with no accessible name at all.
      aria-label={collapsed ? item.label : undefined}
      className={cn(navLinkBase, collapsed && navLinkCollapsed)}
    >
      <item.icon className="w-4 h-4 shrink-0" />
      {!collapsed && item.label}
    </NavLink>
  )
  if (!collapsed) return link
  return (
    <Tooltip>
      <TooltipTrigger render={link} />
      <TooltipContent side="right">{item.label}</TooltipContent>
    </Tooltip>
  )
}

/**
 * Left rail: the OpenLoc wordmark with the collapse control at its right end,
 * the vertical nav tabs, and the dev role switcher pinned to the bottom.
 *
 * The rail collapses to an icon strip on every page; the labels move into
 * tooltips and the choice is remembered. Collapsed, the wordmark row holds only
 * the expand control, sized like the nav icons below it.
 *
 * On phone-width screens the rail opens collapsed and the saved choice is left
 * alone. Expanded there, it lies over the page (a spacer keeps the icon strip's
 * width in the layout), and a page pick or a tap on the backdrop collapses it.
 */
export function NavRail({ role, isPlatformAdmin }: { role: string; isPlatformAdmin: boolean }) {
  const [storedCollapsed, setStoredCollapsed] = useStoredState('nav-rail-collapsed', stored => stored === true)
  const isPhone = useMediaQuery('(max-width: 767px)')
  const [phoneExpanded, setPhoneExpanded] = useState(false)
  const collapsed = isPhone ? !phoneExpanded : storedCollapsed
  const toggle = () => (isPhone ? setPhoneExpanded(v => !v) : setStoredCollapsed(v => !v))

  const toggleLabel = collapsed ? 'Expand navigation' : 'Collapse navigation'
  const ToggleIcon = collapsed ? PanelLeftOpen : PanelLeftClose
  const overlay = isPhone && !collapsed

  return (
    <TooltipProvider delay={100}>
      {overlay && (
        <>
          <div className="w-14 shrink-0" />
          <div
            data-testid="nav-rail-backdrop"
            className="fixed inset-0 z-40 bg-black/10 dark:bg-white/10"
            onClick={() => setPhoneExpanded(false)}
          />
        </>
      )}
      <aside
        data-testid="nav-rail"
        data-collapsed={collapsed}
        className={cn(
          'shrink-0 flex flex-col overflow-hidden bg-card border-r border-gray-200 dark:border-gray-800 transition-[width] duration-200 ease-in-out',
          collapsed ? 'w-14' : 'w-60',
          overlay && 'fixed inset-y-0 left-0 z-50 shadow-xl',
        )}
      >
        {/* Brand row. Same height as the top header beside it. */}
        <div
          className={cn(
            'h-12 shrink-0 flex items-center border-b border-gray-200 dark:border-gray-800',
            collapsed ? 'justify-center' : 'justify-between pl-4 pr-2',
          )}
        >
          {!collapsed && (
            // "/" redirects to the role's home page.
            <Link
              to="/"
              data-testid="nav-rail-wordmark"
              className="select-none"
            >
              <Wordmark />
            </Link>
          )}
          <Tooltip>
            <TooltipTrigger
              render={
                <Button
                  variant="ghost"
                  size="icon"
                  color="muted"
                  onClick={toggle}
                  aria-label={toggleLabel}
                  data-testid="nav-rail-toggle"
                />
              }
            >
              <ToggleIcon className="size-4" />
            </TooltipTrigger>
            <TooltipContent side="right">{toggleLabel}</TooltipContent>
          </Tooltip>
        </div>
        <nav
          onClick={isPhone ? () => setPhoneExpanded(false) : undefined}
          className={cn(
            'flex-1 overflow-y-auto p-2 flex flex-col gap-0.5 text-sm',
            collapsed && 'items-center',
          )}
        >
          {navItemsForRole(role, isPlatformAdmin).map(item => (
            <RailLink key={item.to} item={item} collapsed={collapsed} />
          ))}
        </nav>
        {/* Dev-only role switcher — pinned to the bottom of the rail. Too wide
            for the icon strip, so it sits out while the rail is collapsed. */}
        {import.meta.env.DEV && !collapsed && (
          <div className="border-t border-gray-200 dark:border-gray-800 px-2 py-2">
            <RoleSwitcher />
          </div>
        )}
      </aside>
    </TooltipProvider>
  )
}
