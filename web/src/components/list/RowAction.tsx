import type { ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { DeleteButton } from '@/components/ui/delete-button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'

/**
 * An icon action at the end of a clickable table row (Copy, Export), named by
 * its tooltip and aria-label. The click stays on the button: it does not open
 * the row. Needs a TooltipProvider above the table.
 */
export function RowAction({ label, onClick, disabled, testId, children }: {
  label: string; onClick: () => void; disabled?: boolean; testId: string; children: ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger render={
        <Button variant="ghost" size="icon" aria-label={label} disabled={disabled} data-testid={testId}
          onClick={e => { e.stopPropagation(); onClick() }} />
      }>
        {children}
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

/** The row's trailing delete: a DeleteButton with a "Delete" tooltip that does not open the row. */
export function RowDeleteAction({ label, onClick, disabled, testId }: {
  /** The aria-label, naming what goes: "Delete Default". */
  label: string; onClick: () => void; disabled?: boolean; testId: string
}) {
  return (
    <Tooltip>
      <TooltipTrigger render={
        <DeleteButton aria-label={label} disabled={disabled} data-testid={testId}
          onClick={e => { e.stopPropagation(); onClick() }} />
      } />
      <TooltipContent>Delete</TooltipContent>
    </Tooltip>
  )
}
