import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import type { ReactNode } from 'react'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description: string
  /** Shown under the description, e.g. a list of what the action affects. */
  details?: ReactNode
  actionLabel?: string
  cancelLabel?: string
  // 'destructive' by default (delete confirms). Pass 'default' for positive
  // actions like Complete that warn before proceeding but aren't destructive.
  variant?: 'destructive' | 'default'
  onConfirm: () => void
  /** `data-testid` on the confirm button. */
  testId?: string
}

export default function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  details,
  actionLabel = 'Delete',
  cancelLabel = 'Cancel',
  variant = 'destructive',
  onConfirm,
  testId,
}: Props) {
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent
        // Outside click cancels; Base UI AlertDialog is
        // non-dismissible by default.
        onBackdropClick={() => onOpenChange(false)}
        // React events from the portaled dialog bubble up the React
        // component tree (NOT the DOM tree), so inside a clickable wrapper
        // like the project-row <Link> any click in the dialog would trigger
        // that wrapper's navigation. Dialogs never bleed events to parents.
        onClick={(e) => e.stopPropagation()}
      >
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
          {details && <div className="text-sm text-muted-foreground">{details}</div>}
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{cancelLabel}</AlertDialogCancel>
          <AlertDialogAction
            variant={variant}
            data-testid={testId}
            onClick={(e) => {
              // Defensive: also stop on the action itself in case some future
              // AlertDialogContent change re-introduces event leakage.
              e.stopPropagation()
              onConfirm()
              onOpenChange(false)
            }}
          >
            {actionLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
