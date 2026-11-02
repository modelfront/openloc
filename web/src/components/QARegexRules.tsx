import { useState } from 'react'
import { Pencil, Plus } from 'lucide-react'
import type { QaRule } from '../types/generated'
import type { RegexRuleConfig } from '../types/qa'
import { deleteQARule, saveQARegexRule } from '../api'
import { queryKeys } from '../lib/query-keys'
import { parseQARuleConfig } from '../lib/qa-rule-config'
import { parseRegexConfig, regexModeLabel } from '../lib/qa-regex'
import { useMutationWithToast } from '../hooks/use-mutation-with-toast'
import ConfirmDialog from './ConfirmDialog'
import { QARegexRuleDialog } from './QARegexRuleDialog'
import { QARuleSeverityToggle } from './QARuleSeverityToggle'
import { Button } from '@/components/ui/button'
import { DeleteButton } from '@/components/ui/delete-button'
import { hoverActionClass } from '@/components/list/hover-action'

type EditTarget = { id: number; severity: string; enabled: boolean; config: RegexRuleConfig }

/**
 * The custom regex rules of a QA profile: a list with severity, edit and
 * delete, and a dialog to add or edit one. Each rule's message is what its
 * findings show.
 */
export function QARegexRules({ profileId, rules }: { profileId: number; rules: QaRule[] }) {
  const [dialog, setDialog] = useState<{ rule: EditTarget | null } | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<EditTarget | null>(null)
  const detailKey = queryKeys.qaProfile.detail(profileId)

  const regexRules: EditTarget[] = rules
    .filter(r => r.check_type === 'regex')
    .map(r => ({ id: r.id, severity: r.severity, enabled: r.enabled, config: parseRegexConfig(parseQARuleConfig(r.config)) }))

  const severityMut = useMutationWithToast({
    mutationFn: ({ rule, enabled, severity }: { rule: EditTarget; enabled: boolean; severity: string }) =>
      saveQARegexRule(profileId, { ruleId: rule.id, enabled, severity, config: rule.config }),
    successMessage: 'Rule updated',
    errorMessage: 'Failed to update rule',
    invalidateKeys: [detailKey],
  })
  const deleteMut = useMutationWithToast({
    mutationFn: (rule: EditTarget) => deleteQARule(profileId, rule.id),
    successMessage: 'Regex rule deleted',
    errorMessage: 'Failed to delete the regex rule',
    invalidateKeys: [detailKey],
    onSuccess: () => setDeleteTarget(null),
  })

  return (
    <div data-testid="qa-regex-rules">
      <div className="px-4 py-3 flex items-center justify-between gap-4 border-t border-gray-200 dark:border-gray-800">
        <div>
          <div className="text-base font-medium text-gray-900 dark:text-gray-100">Custom regex rules</div>
          <div className="text-sm text-gray-400 dark:text-gray-500">Your own checks: a pattern, where to look, and the message to show.</div>
        </div>
        <Button size="sm" variant="outline" onClick={() => setDialog({ rule: null })} startIcon={<Plus />} data-testid="add-regex-rule">
          Add regex rule
        </Button>
      </div>
      <div className="divide-y divide-gray-200 dark:divide-gray-800">
        {regexRules.map(rule => (
          <div
            key={rule.id}
            className="px-4 py-3 flex items-center gap-4 cursor-pointer hover:bg-muted transition-colors"
            onClick={() => setDialog({ rule })}
            data-testid={`regex-rule-${rule.id}`}
          >
            <div className="min-w-0 flex-1">
              <div className={`text-base font-medium truncate ${rule.enabled ? 'text-gray-900 dark:text-gray-100' : 'text-gray-400 dark:text-gray-500'}`}>
                {rule.config.name}
              </div>
              <div className="text-sm text-gray-400 dark:text-gray-500 truncate">
                {regexModeLabel(rule.config.mode)}
                {' · '}
                <code className="font-mono">{rule.config.pattern}</code>
                {' · '}
                {rule.config.target_languages.length ? rule.config.target_languages.map(l => l.toUpperCase()).join(', ') : 'All languages'}
              </div>
            </div>
            {/* The row opens the rule; its own controls must not. */}
            <div className="flex items-center gap-4" onClick={e => e.stopPropagation()}>
              <QARuleSeverityToggle
                enabled={rule.enabled}
                severity={rule.severity}
                onChange={next => severityMut.mutate({ rule, ...next })}
              />
              <Button variant="ghost" size="icon-xs" className={hoverActionClass('muted')} title="Edit" aria-label={`Edit ${rule.config.name}`} onClick={() => setDialog({ rule })}>
                <Pencil className="size-4" />
              </Button>
              <DeleteButton aria-label={`Delete ${rule.config.name}`} onClick={() => setDeleteTarget(rule)} />
            </div>
          </div>
        ))}
      </div>
      <QARegexRuleDialog
        profileId={profileId}
        rule={dialog?.rule ?? null}
        open={!!dialog}
        onOpenChange={open => !open && setDialog(null)}
      />
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={open => !open && setDeleteTarget(null)}
        title={`Delete regex rule "${deleteTarget?.config.name}"?`}
        description="Its findings disappear at the next QA run."
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </div>
  )
}
