import { Input } from '@/components/ui/input'
import { useAutosaveDraft } from '@/hooks/useAutosaveDraft'

// A whole number clamped to 0–100 like the backend (settings.ClampPct): a stray
// value becomes 0 or 100, not a failed save. Empty or non-numeric saves nothing.
const parsePenalty = (raw: string): number | null => {
  if (raw.trim() === '') return null
  const n = Number(raw)
  return Number.isFinite(n) ? Math.min(100, Math.max(0, Math.round(n))) : null
}

// A TM row's penalty field. Saves after a pause in typing and on blur (Enter
// blurs); left empty, it reverts.
export function PenaltyInput({ pct, label, testId, onSave }: {
  pct: number
  label: string
  testId: string
  onSave: (pct: number) => Promise<boolean> | undefined
}) {
  const field = useAutosaveDraft({ stored: String(pct), parse: parsePenalty, format: String, onSave })
  return (
    <Input
      type="number"
      min={0}
      max={100}
      step={1}
      value={field.value}
      onChange={e => field.onChange(e.target.value)}
      onBlur={field.onBlur}
      onKeyDown={e => { if (e.key === 'Enter') (e.target as HTMLInputElement).blur() }}
      aria-label={label}
      title="Subtracted from every match from this TM. Any penalty also means its matches are filled in for review rather than approved."
      data-testid={testId}
      className="h-7 w-16 px-2"
      action={<span className="text-xs text-gray-500">%</span>}
    />
  )
}
