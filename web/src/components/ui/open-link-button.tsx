// An icon-only link to another page, opened in a new tab: the external-link
// icon, with `label` as its tooltip and accessible name. It goes at the end of
// a line, after the field it belongs to (a picker's "open the picked thing"),
// where a text link would crowd the line.
import { Link } from 'react-router-dom'
import { ExternalLink } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { NEW_TAB_ATTRS } from '@/lib/new-tab'

export function OpenLinkButton({ to, label, className, 'data-testid': testId }: {
  to: string
  /** What the link opens, sentence case: "Open QA profile". */
  label: string
  className?: string
  'data-testid'?: string
}) {
  return (
    <Tooltip>
      <TooltipTrigger
        delay={200}
        render={
          <Button
            variant="ghost"
            size="icon-xs"
            color="muted"
            hoverColor="primary"
            nativeButton={false}
            // It navigates: a link to assistive tech, not Base UI's default button.
            role="link"
            render={<Link to={to} {...NEW_TAB_ATTRS} />}
            aria-label={`${label} (opens in a new tab)`}
            className={className}
            data-testid={testId}
          >
            <ExternalLink aria-hidden />
          </Button>
        }
      />
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}
