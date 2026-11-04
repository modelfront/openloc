import type { ComponentProps } from 'react'
import { ChevronLeft } from 'lucide-react'
import EditorSidebar from '@/components/EditorSidebar'
import { cn } from '@/lib/utils'
import type { EditorSidebarState } from './useEditorSidebar'

type SidebarProps = Omit<
  ComponentProps<typeof EditorSidebar>,
  'onInsertMatch' | 'onTermClick' | 'onTermHover' | 'onClose'
>

interface EditorSidebarPaneProps extends SidebarProps {
  sidebar: EditorSidebarState
}

/**
 * The Resources panel beside the segment grid: its drag handle, the panel,
 * and the tab that reopens it once closed.
 */
export function EditorSidebarPane({ sidebar, ...panelProps }: EditorSidebarPaneProps) {
  const { sidebarOpen, sidebarResize, openSidebarByClick } = sidebar
  return (
    <>
      {/* Drag handle for the panel's width. Kept in flow (not absolute) so
          the grid and the panel simply share what's left of the row. */}
      {sidebarOpen && (
        <div
          {...sidebarResize.separatorProps}
          data-testid="resize-sidebar"
          title="Drag to resize — double-click to reset"
          className="group w-2 shrink-0 flex justify-center cursor-col-resize focus-visible:outline-none"
        >
          {/* 2px at full primary: a 1px half-transparent hairline reads as
              nothing at all on either theme. */}
          <span
            className={cn(
              'w-0.5 h-full rounded-full transition-colors group-hover:bg-primary group-focus-visible:bg-primary',
              sidebarResize.isResizing && 'bg-primary',
            )}
          />
        </div>
      )}
      <div
        className={cn('overflow-hidden shrink-0', !sidebarResize.isResizing && 'transition-all duration-200 ease-in-out')}
        style={{
          width: sidebarOpen ? `${sidebarResize.width}px` : '0px',
          opacity: sidebarOpen ? 1 : 0,
        }}
      >
        {sidebarOpen && (
          <EditorSidebar
            {...panelProps}
            onInsertMatch={sidebar.handleSidebarInsert}
            onTermClick={sidebar.handleTermClick}
            onTermHover={sidebar.handleTermHover}
            onClose={sidebar.handleSidebarClose}
          />
        )}
      </div>
      {/* Collapsed handle — the reopen affordance once the sidebar is
          closed, sitting right where the panel was. Click it, or drag it
          leftwards to pull the panel back out at the width you want. */}
      {!sidebarOpen && (
        // eslint-disable-next-line no-restricted-syntax -- open-sidebar launcher, not an action button
        <button
          onClick={openSidebarByClick}
          {...sidebarResize.reopenDragProps}
          data-testid="open-sidebar"
          title="Open TM & terminology sidebar — drag to set its width"
          // The open panel carries the same anchor; only one renders.
          data-guide="editor.sidebar"
          className="group relative ml-2 self-stretch flex flex-col items-center justify-center gap-2 px-1.5 rounded-lg border border-gray-200 dark:border-gray-800 bg-card text-gray-500 hover:text-gray-900 dark:hover:text-gray-100 hover:bg-gray-50 dark:hover:bg-gray-800 transition-colors cursor-col-resize"
        >
          {/* Same grab line the open panel's handle shows, on the edge the
              drag starts from — the gesture is the same one. */}
          <span
            aria-hidden
            className={cn(
              '-left-1 absolute inset-y-0 w-0.5 rounded-full transition-colors group-hover:bg-primary group-focus-visible:bg-primary',
              sidebarResize.isResizing && 'bg-primary',
            )}
          />
          <ChevronLeft className="size-4" />
          <span className="text-[10px] font-medium uppercase tracking-wider [writing-mode:vertical-rl] rotate-180">Resources</span>
        </button>
      )}
    </>
  )
}
