import { useId, useState } from 'react'
import type { Field } from '@/lib/openapi'
import { cn } from '@/lib/utils'
import { DisclosureToggle } from '@/components/ui/disclosure-toggle'
import { InlineCode } from '@/components/ui/inline-code'
import { chipTextClass } from '@/components/ui/chip'

/** Short spec text with `code` spans, rendered inline. */
export function SpecText({ text }: { text: string }) {
  return (
    <>
      {text.split(/(`[^`]+`)/).map((part, i) =>
        part.length > 1 && part.startsWith('`') && part.endsWith('`') ? <InlineCode key={i}>{part.slice(1, -1)}</InlineCode> : part,
      )}
    </>
  )
}

const muted = 'text-gray-500 dark:text-gray-400'

/**
 * Which side of the call the fields are on. In a request, a field the spec
 * requires is one the caller must send. In a response, the server always
 * sends it, so only the fields that may be missing get a label.
 */
export type FieldSide = 'request' | 'response'

// One field: name and type on the left, what it means on the right.
function FieldRow({ field, side }: { field: Field; side: FieldSide }) {
  const [open, setOpen] = useState(false)
  const childrenId = useId()
  return (
    <li className="py-3" data-testid="api-field">
      <div className="flex flex-col gap-1 sm:flex-row sm:gap-4">
        <div className="flex shrink-0 flex-col gap-0.5 sm:w-48">
          <span className="break-all font-mono text-sm font-medium text-gray-900 dark:text-gray-100">{field.name}</span>
          <span className={cn('font-mono text-xs', muted)}>
            {field.type}
            {field.nullable && ' | null'}
          </span>
          {side === 'request' && field.required && <span className={cn('text-xs font-medium', chipTextClass('red'))}>Required</span>}
          {side === 'response' && !field.required && <span className={cn('text-xs', muted)}>Optional</span>}
        </div>
        <div className="min-w-0 flex-1 space-y-1 text-sm text-gray-700 dark:text-gray-300">
          {field.description && (
            <p>
              <SpecText text={field.description} />
            </p>
          )}
          {field.constraints.length > 0 && <p className={cn('text-xs', muted)}>{field.constraints.join(' · ')}</p>}
          {field.defaultValue !== undefined && (
            <p className={cn('text-xs', muted)}>
              Default <InlineCode>{field.defaultValue}</InlineCode>
            </p>
          )}
          {field.enumValues.length > 0 && (
            <p className={cn('flex flex-wrap items-center gap-1 text-xs', muted)}>
              One of
              {field.enumValues.map(v => (
                <InlineCode key={v}>{v}</InlineCode>
              ))}
            </p>
          )}
          {field.children.length > 0 && (
            <DisclosureToggle open={open} onToggle={() => setOpen(o => !o)} controls={childrenId}>
              {open ? 'Hide fields' : `Show ${field.children.length} fields`}
            </DisclosureToggle>
          )}
        </div>
      </div>
      {open && (
        <div id={childrenId} className="mt-2 border-l-2 border-gray-200 pl-4 dark:border-gray-800">
          <FieldList fields={field.children} side={side} />
        </div>
      )}
    </li>
  )
}

/** Documented fields, one row each, with nested objects folded away. */
export function FieldList({ fields, side = 'request' }: { fields: Field[]; side?: FieldSide }) {
  return (
    <ul className="divide-y divide-gray-200 dark:divide-gray-800">
      {fields.map(f => (
        <FieldRow key={f.name} field={f} side={side} />
      ))}
    </ul>
  )
}
