// A chart's legend: one button per series or slice. Hovering (or focusing) an
// entry highlights it in the chart and shows its number in a tooltip; clicking
// hides it, and clicking again shows it. Drawn under the chart as plain,
// keyboard-reachable HTML; a tall chart tooltip can still reach over it, and
// stacks above it there.
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

export interface LegendEntry {
  name: string
  color: string
  hidden: boolean
  /** Shown on hover or focus: the entry's value, or a series' total. */
  tooltip?: string
}

export function ChartLegend({ entries, onHighlight, onToggle }: {
  entries: LegendEntry[]
  onHighlight: (name: string | null) => void
  onToggle: (name: string) => void
}) {
  return (
    <TooltipProvider delay={200}>
      <div className="flex flex-wrap justify-center gap-x-1" role="group" aria-label="Legend" data-testid="chart-legend">
        {entries.map(e => {
          const highlight = () => onHighlight(e.hidden ? null : e.name)
          const props = {
            variant: 'ghost' as const,
            size: 'xs' as const,
            'aria-pressed': !e.hidden,
            'data-testid': 'chart-legend-item',
            onClick: () => onToggle(e.name),
            onMouseEnter: highlight,
            onMouseLeave: () => onHighlight(null),
            onFocus: highlight,
            onBlur: () => onHighlight(null),
          }
          const content = (
            <>
              <span
                aria-hidden
                className={cn('size-2 rounded-full', e.hidden && 'opacity-30')}
                style={{ backgroundColor: e.color }}
              />
              <span className={cn('font-normal', e.hidden && 'text-muted-foreground line-through')}>{e.name}</span>
            </>
          )
          if (!e.tooltip || e.hidden) return <Button key={e.name} {...props}>{content}</Button>
          return (
            <Tooltip key={e.name}>
              <TooltipTrigger render={<Button {...props} />}>{content}</TooltipTrigger>
              <TooltipContent data-testid="chart-legend-tooltip">{e.tooltip}</TooltipContent>
            </Tooltip>
          )
        })}
      </div>
    </TooltipProvider>
  )
}
