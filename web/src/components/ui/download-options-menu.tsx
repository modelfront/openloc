// DownloadOptionsMenu — the Download button and its menu of choices: each item
// is an icon, a label and an optional one-line hint. The button is either
// icon + label or, among a table row's icon actions, an icon with a tooltip.
// It only shows the choices; what a choice downloads is the caller's (DownloadMenu for files
// and jobs, AnalysisExportMenu for a saved analysis).

import type { ComponentProps } from 'react'
import { Download, type LucideIcon } from 'lucide-react'

import { Button } from './button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from './dropdown-menu'
import { Tooltip, TooltipContent, TooltipTrigger } from './tooltip'
import { cn } from '@/lib/utils'

export interface DownloadOption<K extends string> {
  id: K
  label: string
  /** A second line under the label; leave it out when the label says enough. */
  hint?: string
  icon: LucideIcon
  disabled?: boolean
  testId?: string
}

interface Props<K extends string> {
  options: DownloadOption<K>[]
  onSelect: (id: K) => void
  /** A download is running: the label says so and the button is disabled. */
  busy?: boolean
  disabled?: boolean
  size?: 'xs' | 'sm'
  variant?: 'outline' | 'default' | 'ghost'
  /** Text colour at rest and on hover; `muted` matches the icon actions
   *  beside it in a table row. Set both — a `color` alone loses its hover
   *  colour in dark mode. */
  color?: ComponentProps<typeof Button>['color']
  hoverColor?: ComponentProps<typeof Button>['hoverColor']
  /** An icon button with a tooltip instead of icon + label, for a table row's
   *  actions beside other icon buttons. */
  iconOnly?: boolean
  /** Names what is downloaded for screen readers ("Download Quote batch"). */
  ariaLabel?: string
  testId?: string
}

export function DownloadOptionsMenu<K extends string>({
  options,
  onSelect,
  busy = false,
  disabled = false,
  size = 'xs',
  variant = 'outline',
  color,
  hoverColor,
  iconOnly = false,
  ariaLabel,
  testId,
}: Props<K>) {
  const label = busy ? 'Downloading…' : 'Download'
  const hasHints = options.some(o => o.hint)
  const button = (
    <Button
      variant={variant}
      size={iconOnly ? 'icon-xs' : size}
      color={color}
      hoverColor={hoverColor}
      disabled={busy || disabled}
      aria-label={iconOnly ? (ariaLabel ?? label) : ariaLabel}
      data-testid={testId}
    >
      <Download />
      {!iconOnly && label}
    </Button>
  )

  return (
    <DropdownMenu>
      {iconOnly ? (
        <Tooltip>
          <DropdownMenuTrigger render={<TooltipTrigger render={button} />} />
          <TooltipContent>{label}</TooltipContent>
        </Tooltip>
      ) : (
        <DropdownMenuTrigger render={button} />
      )}
      <DropdownMenuContent className={cn(hasHints && 'min-w-64')}>
        {options.map(opt => {
          const Icon = opt.icon
          return (
            <DropdownMenuItem
              key={opt.id}
              disabled={opt.disabled}
              onClick={() => onSelect(opt.id)}
              data-testid={opt.testId}
              className={cn('gap-2.5 disabled:opacity-50', opt.hint && 'items-start')}
            >
              <Icon className={cn('size-4 shrink-0 text-gray-400 dark:text-gray-500', opt.hint && 'mt-0.5')} />
              {opt.hint ? (
                <span className="flex flex-col">
                  <span>{opt.label}</span>
                  <span className="text-xs text-gray-500 dark:text-gray-400">{opt.hint}</span>
                </span>
              ) : (
                <span>{opt.label}</span>
              )}
            </DropdownMenuItem>
          )
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
