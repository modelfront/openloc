import { cn } from '@/lib/utils'
import { TextLink } from '@/components/ui/text-link'
import type { OutputFlags } from './output-mode'
import { promptPath } from './prompt-routes'

/**
 * A link to a prompt's own page, opened in a new tab.
 *
 * A new tab rather than a navigation: both places this appears sit inside an
 * engine form holding unsaved config, and going to the prompt in place would
 * throw that away. The destination follows the PROMPT's own output flags, so a
 * link never lands on a section whose list filters the prompt back out.
 */
export function OpenPromptLink({
  prompt, label = 'Open prompt', className, 'data-testid': testId,
}: {
  prompt: OutputFlags & { id: number }
  label?: string
  className?: string
  'data-testid'?: string
}) {
  return (
    <TextLink
      href={promptPath(prompt)}
      tone="muted"
      newTab
      className={cn('shrink-0 text-[11px] normal-case', className)}
      data-testid={testId}
    >
      {label}
    </TextLink>
  )
}
