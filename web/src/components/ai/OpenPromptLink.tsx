import { ExternalLink } from 'lucide-react'
import { cn } from '@/lib/utils'
import { TextLink } from '@/components/ui/text-link'
import type { OutputFlags } from './output-mode'
import { promptPath } from './prompt-routes'

function NewTabLink({
  href, label, onClick, className, testId,
}: {
  href: string
  label: string
  onClick?: () => void
  className?: string
  testId?: string
}) {
  return (
    <TextLink
      href={href}
      tone="muted"
      target="_blank"
      rel="noopener noreferrer"
      onClick={onClick}
      className={cn('inline-flex shrink-0 items-center gap-1 text-[11px] normal-case', className)}
      data-testid={testId}
    >
      {label}
      <ExternalLink className="size-3" aria-hidden="true" />
      <span className="sr-only">(opens in a new tab)</span>
    </TextLink>
  )
}

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
  return <NewTabLink href={promptPath(prompt)} label={label} className={className} testId={testId} />
}
