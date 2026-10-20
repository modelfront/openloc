import { useState } from 'react'
import { useOnChange } from '@/hooks/useOnChange'
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
import { Input } from '@/components/ui/input'
import type { ClearScope } from '../api'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description: string
  requireTypedConfirm?: boolean
  // Upper bound per scope — server skips locked rows.
  counts?: { non_confirmed: number; all: number }
  onConfirm: (scope: ClearScope) => void
}

const TYPED_PHRASE = 'CLEAR'

export default function ClearTranslationsDialog({
  open,
  onOpenChange,
  title,
  description,
  requireTypedConfirm = false,
  counts,
  onConfirm,
}: Props) {
  const [scope, setScope] = useState<ClearScope>('non_confirmed')
  const [typed, setTyped] = useState('')

  // Reset on open so a previous typed-CLEAR doesn't carry over.
  useOnChange([open], () => {
    if (open) {
      setScope('non_confirmed')
      setTyped('')
    }
  })

  const typedOk = !requireTypedConfirm || typed === TYPED_PHRASE

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      {/*
        stopPropagation on the content wrapper: AlertDialog portals to
        document.body in the DOM, but React synthetic events traverse the
        COMPONENT tree — without this, a click inside the dialog reaches
        any ancestor onClick (e.g. the project-page job row's <Link>).
      */}
      <AlertDialogContent
        onBackdropClick={() => onOpenChange(false)}
        onClick={(e) => e.stopPropagation()}
      >
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>

        <fieldset className="space-y-2 text-sm">
          <label className="flex items-start gap-2 cursor-pointer">
            <input
              type="radio"
              name="clear-scope"
              value="non_confirmed"
              checked={scope === 'non_confirmed'}
              onChange={() => setScope('non_confirmed')}
              className="mt-0.5"
              data-testid="clear-scope-non-confirmed"
            />
            <span>
              <span className="font-medium">Non-confirmed only</span>
              <span className="block text-xs text-gray-500">Preserves human-confirmed and auto-approved segments.</span>
            </span>
          </label>
          <label className="flex items-start gap-2 cursor-pointer">
            <input
              type="radio"
              name="clear-scope"
              value="all"
              checked={scope === 'all'}
              onChange={() => setScope('all')}
              className="mt-0.5"
              data-testid="clear-scope-all"
            />
            <span>
              <span className="font-medium text-red-600">All translations</span>
              <span className="block text-xs text-gray-500">Wipes every non-locked segment, including approved ones.</span>
            </span>
          </label>
        </fieldset>

        {counts && (
          <p className="text-sm" data-testid="clear-count-estimate">
            <span className="font-semibold tabular-nums">{counts[scope]}</span>{' '}
            segment{counts[scope] === 1 ? '' : 's'} will be cleared.
          </p>
        )}
        <p className="text-xs text-gray-500">
          Locked segments, and segments someone else has open, are kept. Translation memory entries are not affected.
        </p>

        {requireTypedConfirm && (
          <div className="space-y-1 text-sm">
            <label htmlFor="clear-typed-confirm">
              Type <span className="font-mono font-semibold">{TYPED_PHRASE}</span> to confirm:
            </label>
            <Input
              id="clear-typed-confirm"
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              placeholder={TYPED_PHRASE}
              data-testid="clear-typed-confirm"
            />
          </div>
        )}

        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={!typedOk}
            onClick={() => {
              if (!typedOk) return
              onConfirm(scope)
              onOpenChange(false)
            }}
            data-testid="clear-submit"
          >
            Clear translations
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
