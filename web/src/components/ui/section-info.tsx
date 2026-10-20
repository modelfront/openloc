import { type ReactNode } from 'react'
import { CircleAlert, Info, TriangleAlert, type LucideIcon } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

// The small info affordance shown next to a title — an icon that reveals a
// short explanatory tooltip on hover/focus, so every piece of inline help looks
// and behaves identically. `tone="amber"` is the warning form: a triangle in
// amber, for a note that must not be overlooked; `tone="red"` is the error
// form, an exclamation in a circle (an ⊗ would read as a close button). `icon` swaps the tone's icon for a
// note of another kind (a task sent back, an AI step running). `size="sm"`
// sits beside small text, such as a chart card's definition line.
const TONES = {
  muted: { icon: Info, className: 'text-muted-foreground/70 hover:text-muted-foreground' },
  amber: { icon: TriangleAlert, className: 'text-amber-600 dark:text-amber-500' },
  red: { icon: CircleAlert, className: 'text-rose-600 dark:text-rose-400' },
} as const

export function SectionInfo({ label, children, tone = 'muted', icon, size = 'default', ...props }: {
  label: string
  children: ReactNode
  tone?: keyof typeof TONES
  icon?: LucideIcon
  size?: 'default' | 'sm'
  'data-testid'?: string
}) {
  const { icon: toneIcon, className } = TONES[tone]
  const Icon = icon ?? toneIcon
  return (
    <TooltipProvider delay={100}>
      <Tooltip>
        <TooltipTrigger render={<span className={cn('inline-flex cursor-help', className)} tabIndex={0} {...props} />}>
          <Icon className={size === 'sm' ? 'h-3.5 w-3.5' : 'h-4 w-4'} aria-label={label} />
        </TooltipTrigger>
        <TooltipContent className="max-w-72 text-xs leading-relaxed">{children}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}
