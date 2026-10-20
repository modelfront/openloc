import type { PaletteAnchors } from '../../lib/palettes'

/** A tiny mock (page → surface card → text/muted bars + accent chip) rendered
 *  straight from a palette's anchors, for palette cards and the editor. */
export function PalettePreview({ anchors: a, className = '' }: { anchors: PaletteAnchors; className?: string }) {
  return (
    <div
      className={`w-full overflow-hidden rounded-md p-2 ${className}`}
      style={{ background: a.bg, border: `1px solid ${a.border}` }}
    >
      <div className="flex h-full gap-2">
        <div className="flex flex-1 flex-col justify-between rounded p-1.5" style={{ background: a.surface }}>
          <div className="space-y-1">
            <div className="h-1.5 w-3/5 rounded-full" style={{ background: a.text }} />
            <div className="h-1.5 w-4/5 rounded-full" style={{ background: a.muted }} />
          </div>
          <div className="flex items-center gap-1.5">
            <div className="h-3 w-8 rounded" style={{ background: a.accent }} />
            <div className="h-3 w-5 rounded" style={{ background: a.elevated, border: `1px solid ${a.border}` }} />
          </div>
        </div>
      </div>
    </div>
  )
}
