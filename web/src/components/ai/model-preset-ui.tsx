import { Info, SlidersHorizontal } from 'lucide-react'
import type { AIModelParam, ModelSettings } from '../../api'
import { Input } from '@/components/ui/input'
import { Combobox } from '@/components/ui/combobox'
import { Switch } from '@/components/ui/switch'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

// One editable control for a model parameter. Undefined ("Default") omits the
// key so the provider's own default applies.
function ModelParamControl({
  param, value, onChange,
}: {
  param: AIModelParam
  value: number | boolean | string | undefined
  onChange: (v: number | boolean | string | undefined) => void
}) {
  if (param.type === 'boolean') {
    return (
      // `justify-end` so the switch sits on the same right edge as the inputs
      // and pickers in the other cells; it is narrower than its column, so
      // without this it floats in the middle of the row.
      <div className="flex h-9 items-center justify-end">
        {/* Off removes the key rather than sending `false`, so the provider's
            own default applies — the same "unset" every other control here
            means by showing "Default". */}
        <Switch
          checked={!!value}
          onCheckedChange={v => onChange(v || undefined)}
          aria-label={param.label}
        />
      </div>
    )
  }
  if (param.type === 'enum') {
    return (
      <Combobox
        value={value != null ? String(value) : ''}
        onChange={v => onChange(v || undefined)}
        options={param.options ?? []}
        placeholder="Default"
        className="w-full"
      />
    )
  }
  if (param.type === 'string') {
    return (
      <Input
        value={value === undefined ? '' : String(value)}
        placeholder="Default"
        onChange={e => onChange(e.target.value || undefined)}
      />
    )
  }
  return (
    <Input
      type="number"
      min={param.min}
      max={param.max}
      step={param.step ?? (param.type === 'integer' ? 1 : 'any')}
      value={value === undefined ? '' : String(value)}
      placeholder="Default"
      onChange={e => onChange(e.target.value === '' ? undefined : Number(e.target.value))}
    />
  )
}

// The editable "Model settings" panel: one control per provider parameter, bound
// to the preset's settings. Key it off the selected provider so it re-renders
// with a fresh param set when the provider changes.
export function ModelSettingsPanel({
  params, value, onChange, showHeader = true, title = 'Model settings', columns = 3,
}: {
  params: AIModelParam[]
  value: ModelSettings
  onChange: (v: ModelSettings) => void
  /** Off when the caller already labels the panel (e.g. a collapse toggle). */
  showHeader?: boolean
  /** What the panel tunes — an MT engine's panel tunes a provider, not a model. */
  title?: string
  /**
   * Widest column count. 3 suits the AI engine form, which sits in a page-width
   * card; a dialog is narrower, and at 3 the label truncates to "Form…" while
   * the control keeps its fixed width. Drop to 2 there.
   */
  columns?: 2 | 3
}) {
  const set = (key: string, v: number | boolean | string | undefined) => {
    const next = { ...value }
    if (v === undefined) delete next[key]
    else next[key] = v
    onChange(next)
  }
  if (params.length === 0) return null
  return (
    <div className="rounded-lg border border-dashed border-input bg-muted/30 p-3">
      {showHeader && (
        <div className="mb-2 flex items-center gap-1.5 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
          <SlidersHorizontal className="h-3 w-3" aria-hidden="true" />
          {title}
        </div>
      )}
      <div className={cn(
        'grid gap-x-4 gap-y-2',
        columns === 3 ? 'grid-cols-2 sm:grid-cols-3' : 'grid-cols-1 sm:grid-cols-2',
      )}>
        {params.map(p => (
          <div key={p.key} className="flex min-w-0 items-center gap-2">
            <span className="flex min-w-0 flex-1 items-center gap-1 text-xs font-medium text-gray-700 dark:text-gray-300">
              <span className="truncate">{p.label}</span>
              <TooltipProvider delay={100}>
                <Tooltip>
                  <TooltipTrigger render={<span className="inline-flex shrink-0 cursor-help text-muted-foreground/60 hover:text-muted-foreground" tabIndex={0} />}>
                    <Info className="h-3 w-3" aria-label={`About ${p.label}`} />
                  </TooltipTrigger>
                  <TooltipContent className="max-w-72 text-xs font-normal normal-case leading-relaxed tracking-normal">
                    {p.description}
                  </TooltipContent>
                </Tooltip>
              </TooltipProvider>
            </span>
            {/* Enums carry word labels ("Extra high", "Adaptive (default)"),
                so they need more room than a numeric input. */}
            <div className={cn('shrink-0', p.type === 'enum' || p.type === 'string' ? 'w-32' : 'w-24')}>
              <ModelParamControl param={p} value={value[p.key]} onChange={v => set(p.key, v)} />
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
