import { SEVERITIES, SEVERITY_CHIP_COLORS, SEVERITY_LABELS } from '../lib/qa-findings'
import { ChipButton } from '@/components/ui/chip'
import { VerticalDivider } from '@/components/ui/vertical-divider'
import { cn } from '@/lib/utils'

// Only the option in force carries its colour; the rest sit back as faded grey,
// so the row reads as one setting rather than four competing labels.
const UNSET_OPTION = 'opacity-30 hover:opacity-60'

/**
 * A QA rule's severity chips plus Disabled. Clicking the active severity again
 * disables the rule; clicking any severity while disabled re-enables it there.
 */
export function QARuleSeverityToggle({ enabled, severity, disabled, onChange }: {
  enabled: boolean
  severity: string
  /** Set while a save of the rule is in flight. */
  disabled?: boolean
  onChange: (next: { enabled: boolean; severity: string }) => void
}) {
  return (
    <div className="flex items-center gap-1">
      {SEVERITIES.map(sev => (
        <ChipButton
          key={sev}
          color={enabled && severity === sev ? SEVERITY_CHIP_COLORS[sev] : 'grey'}
          disabled={disabled}
          onClick={() => onChange({ enabled: !(enabled && severity === sev), severity: sev })}
          className={cn(!(enabled && severity === sev) && UNSET_OPTION)}
        >
          {SEVERITY_LABELS[sev]}
        </ChipButton>
      ))}
      <VerticalDivider className="mx-1 bg-gray-300" />
      <ChipButton
        color="grey"
        disabled={disabled}
        onClick={() => onChange({ enabled: false, severity })}
        className={cn(enabled && UNSET_OPTION)}
      >
        Disabled
      </ChipButton>
    </div>
  )
}
