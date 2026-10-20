import type { ReactNode } from 'react'
import { Button } from '../ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'

/** A selection-bar action: disabled with a reason when nothing selected is eligible. */
export interface BulkButtonProps {
  label: string
  pending: boolean
  eligibleCount: number
  disabled: boolean
  tooltip: string
  onClick: () => void
  testid: string
  variant?: 'outline' | 'ghost' | 'destructive' | 'success-outline' | 'warning-outline'
  /** Tooltip when nothing is eligible; defaults to "No selected jobs are eligible…". */
  ineligibleTooltip?: string
  icon?: ReactNode
}

export function BulkButton({
  label, pending, eligibleCount, disabled, tooltip, onClick, testid, variant = 'outline', ineligibleTooltip, icon,
}: BulkButtonProps) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            variant={variant}
            size="xs"
            onClick={onClick}
            disabled={disabled || eligibleCount === 0}
            data-testid={testid}
          >
            {icon}
            {pending ? `${label}…` : label}
          </Button>
        }
      />
      <TooltipContent>
        {eligibleCount === 0
          ? ineligibleTooltip ?? `No selected jobs are eligible for ${label}`
          : `${tooltip} (${eligibleCount} eligible)`}
      </TooltipContent>
    </Tooltip>
  )
}
