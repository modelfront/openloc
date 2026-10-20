import type { ReactNode } from 'react'
import { Menu as MenuPrimitive } from '@base-ui/react/menu'
import { CheckIcon, ChevronRightIcon } from 'lucide-react'

import { cn } from '@/lib/utils'

// DropdownMenu — the standard action menu (navigate, sign out, per-row actions).
//
// Wraps base-ui Menu: open/close, focus trapping, keyboard nav, click-outside
// and select-then-close come from the library. Menu.Item runs its onClick and dismisses the menu
// in one action — the correct primitive for items that navigate.
//
// NOTE: `@base-ui/react/menu` must stay in vite.config optimizeDeps.include so
// it is pre-bundled alongside the other base-ui subpaths against one React
// instance (a lazily-optimized subpath dupes React → "Invalid hook call").
//
// For "pick one value" use Select; for searchable pickers use the cmdk combobox.

function DropdownMenu(props: MenuPrimitive.Root.Props) {
  return <MenuPrimitive.Root {...props} />
}

function DropdownMenuTrigger(props: MenuPrimitive.Trigger.Props) {
  return <MenuPrimitive.Trigger data-slot="dropdown-menu-trigger" {...props} />
}

function DropdownMenuContent({
  className,
  align = 'end',
  sideOffset = 4,
  ...props
}: MenuPrimitive.Popup.Props &
  Pick<MenuPrimitive.Positioner.Props, 'align' | 'sideOffset'>) {
  return (
    <MenuPrimitive.Portal>
      <MenuPrimitive.Positioner align={align} sideOffset={sideOffset} className="isolate z-50">
        <MenuPrimitive.Popup
          data-slot="dropdown-menu-content"
          className={cn(
            'z-50 min-w-52 origin-(--transform-origin) rounded-md border border-gray-200 bg-popover py-1 text-sm shadow-lg outline-hidden dark:border-gray-700 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95',
            className,
          )}
          {...props}
        />
      </MenuPrimitive.Positioner>
    </MenuPrimitive.Portal>
  )
}

// `description` turns the item into two lines: the children as a bold title
// row, and a small grey line under it — for menus whose entries need a word
// of explanation (a tour, a playbook).
function DropdownMenuItem({
  className,
  description,
  children,
  ...props
}: MenuPrimitive.Item.Props & { description?: ReactNode }) {
  const hasDescription = description != null && description !== ''
  return (
    <MenuPrimitive.Item
      data-slot="dropdown-menu-item"
      className={cn(
        'flex w-full cursor-pointer items-center gap-2 px-3 py-2 text-gray-700 outline-hidden select-none data-highlighted:bg-gray-100 dark:text-gray-300 dark:data-highlighted:bg-gray-800',
        hasDescription && 'flex-col items-start gap-0.5',
        className,
      )}
      {...props}
    >
      {hasDescription ? (
        <>
          <span className="flex w-full items-center gap-2 font-medium">{children}</span>
          <span className="text-[11px] text-gray-500 dark:text-gray-400">{description}</span>
        </>
      ) : (
        children
      )}
    </MenuPrimitive.Item>
  )
}

// DropdownMenuCheckboxItem — a toggle row for multi-select menus. Base UI keeps
// the menu open on click for checkbox items (closeOnClick defaults to false), so
// several can be toggled in one open.
function DropdownMenuCheckboxItem({
  className,
  children,
  ...props
}: MenuPrimitive.CheckboxItem.Props) {
  return (
    <MenuPrimitive.CheckboxItem
      data-slot="dropdown-menu-checkbox-item"
      className={cn(
        'relative flex w-full cursor-pointer items-center gap-2 py-2 pr-3 pl-8 text-gray-700 outline-hidden select-none data-highlighted:bg-gray-100 dark:text-gray-300 dark:data-highlighted:bg-gray-800',
        className,
      )}
      {...props}
    >
      <MenuPrimitive.CheckboxItemIndicator className="absolute left-2 flex items-center">
        <CheckIcon className="size-4" />
      </MenuPrimitive.CheckboxItemIndicator>
      {children}
    </MenuPrimitive.CheckboxItem>
  )
}

function DropdownMenuSeparator({ className, ...props }: MenuPrimitive.Separator.Props) {
  return (
    <MenuPrimitive.Separator
      data-slot="dropdown-menu-separator"
      className={cn('my-1 h-px bg-gray-200 dark:bg-gray-700', className)}
      {...props}
    />
  )
}

// A labelled group. GroupLabel only works inside one — Base UI throws
// "MenuGroupRootContext is missing" otherwise — so the two travel together.
function DropdownMenuGroup(props: MenuPrimitive.Group.Props) {
  return <MenuPrimitive.Group data-slot="dropdown-menu-group" {...props} />
}

// A heading over a group of items, for a menu that mixes kinds — "Rename" is an
// action, the checkboxes below it are filters, and without a label the ticks
// read as options for the action above them.
function DropdownMenuLabel({ className, ...props }: MenuPrimitive.GroupLabel.Props) {
  return (
    <MenuPrimitive.GroupLabel
      data-slot="dropdown-menu-label"
      className={cn('px-2 py-1.5 text-xs font-medium text-muted-foreground', className)}
      {...props}
    />
  )
}

// A nested menu: a row that opens a second list beside it, for an action that
// needs a target — "Move to ▸ <dashboard>". The trigger looks like an item with
// a chevron; the content is positioned beside it rather than below.
function DropdownMenuSub(props: MenuPrimitive.SubmenuRoot.Props) {
  return <MenuPrimitive.SubmenuRoot {...props} />
}

function DropdownMenuSubTrigger({ className, children, ...props }: MenuPrimitive.SubmenuTrigger.Props) {
  return (
    <MenuPrimitive.SubmenuTrigger
      data-slot="dropdown-menu-sub-trigger"
      className={cn(
        'flex w-full cursor-pointer items-center gap-2 px-3 py-2 text-gray-700 outline-hidden select-none data-highlighted:bg-gray-100 data-popup-open:bg-gray-100 dark:text-gray-300 dark:data-highlighted:bg-gray-800 dark:data-popup-open:bg-gray-800',
        className,
      )}
      {...props}
    >
      {children}
      <ChevronRightIcon className="ml-auto size-4 text-gray-400" />
    </MenuPrimitive.SubmenuTrigger>
  )
}

function DropdownMenuSubContent({
  className,
  sideOffset = 0,
  ...props
}: MenuPrimitive.Popup.Props & Pick<MenuPrimitive.Positioner.Props, 'sideOffset'>) {
  return (
    <MenuPrimitive.Portal>
      <MenuPrimitive.Positioner sideOffset={sideOffset} className="isolate z-50">
        <MenuPrimitive.Popup
          data-slot="dropdown-menu-sub-content"
          className={cn(
            // Capped and scrollable: the list is one row per target, and an org
            // can have more dashboards than a screen is tall.
            'z-50 max-h-80 min-w-52 origin-(--transform-origin) overflow-y-auto rounded-md border border-gray-200 bg-popover py-1 text-sm shadow-lg outline-hidden dark:border-gray-700 data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0',
            className,
          )}
          {...props}
        />
      </MenuPrimitive.Positioner>
    </MenuPrimitive.Portal>
  )
}

export {
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
}
