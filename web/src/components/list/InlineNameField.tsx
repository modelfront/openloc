import { useState, type ReactNode } from 'react'
import { Pencil } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import type { UseFormRegisterReturn } from 'react-hook-form'

/**
 * An inline, react-hook-form-bound name field: shows the name as a title with a
 * small pencil; clicking the pencil swaps in a *seamless* edit input — identical
 * typography (size/weight/colour) to the title, no border/ring/background — so
 * editing happens in place rather than a boxy input popping in. Enter or blur
 * leaves edit mode (the value already lives in the form; the outer form commits
 * on submit). The react-hook-form-bound sibling of InlineRenameField (local
 * state) and EditableTitle (name + pencil, local state); use this one inside an
 * RHF form.
 */
export function InlineNameField({
  value, register, placeholder, className, trailing, canEdit = true, startEditing = false,
}: {
  /** Current (watched) value — shown as the title and used for the empty check. */
  value: string
  /** react-hook-form register(...) result for the name field. */
  register: UseFormRegisterReturn
  placeholder?: string
  /** Extra typography for the title AND the seamless input (size/weight/colour). */
  className?: string
  /** Optional element shown right after the name/input (e.g. a mode chip). */
  trailing?: ReactNode
  /** Hide the pencil + lock editing (e.g. read-only defaults). */
  canEdit?: boolean
  /** Open straight into the input (e.g. brand-new, unnamed entities). */
  startEditing?: boolean
}) {
  const [editing, setEditing] = useState(startEditing)
  const typography = cn('text-base font-medium text-gray-900 dark:text-gray-100', className)
  const leave = () => { if (value.trim()) setEditing(false) }

  return (
    <span className="flex min-w-0 items-center gap-2">
      {editing && canEdit ? (
        <Input
          {...register}
          variant="seamless"
          autoFocus
          placeholder={placeholder}
          aria-label="Rename"
          className={cn(typography, 'min-w-0 flex-1')}
          // `placeholder:` targets ::placeholder on the element it sits on, so
          // it has to reach the input rather than the box.
          inputClassName="placeholder:font-normal"
          onBlur={e => { register.onBlur(e); leave() }}
          onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); leave() } }}
        />
      ) : (
        <span className={cn('truncate', typography)}>{value}</span>
      )}
      {trailing}
      {!editing && canEdit && (
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          onClick={() => setEditing(true)}
          title="Rename"
          aria-label="Rename"
          className="shrink-0 text-gray-400 hover:text-gray-600 dark:text-gray-500 dark:hover:text-gray-300"
        >
          <Pencil className="w-4 h-4" />
        </Button>
      )}
    </span>
  )
}
