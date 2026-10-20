// CMS integrations.
//
// Nothing self-serve yet: every connector ships disconnected and Connect opens
// a "talk to us" dialog rather than a credential form. That is the honest state
// — setup is a conversation today — and it avoids inventing auth fields for
// flows (Adobe IMS, Sitecore XM Cloud OAuth2) the backend hasn't defined.
import { useState } from 'react'
import {
  siContentful,
  siContentstack,
  siDrupal,
  siGithub,
  siSitecore,
  siWordpress,
} from 'simple-icons'
import { CMS_CONNECTORS, type CmsConnector } from '../lib/cms-connectors'
import { PageContainer } from '@/components/layout/PageContainer'
import { PageHeader } from '@/components/layout/PageHeader'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Chip } from '@/components/ui/chip'
import { TextLink } from '@/components/ui/text-link'

// Adobe's corporate "A", inlined because the current simple-icons registry
// carries nothing Adobe — they had their marks removed, exactly as OpenAI did
// (same situation, same handling, as the note in components/chat/ModelPicker.tsx).
// Taken verbatim from simple-icons v5.24.0 via jsDelivr, which is CC0.
//
// One mark covers all three Adobe products. AEM, Commerce and Marketo each have
// their own product logo in reality; the shared corporate mark is a deliberate
// stand-in so the rows read as Adobe without inventing three logos.
const ADOBE = {
  path: 'M13.966 22.624l-1.69-4.281H8.122l3.892-9.144 5.662 13.425zM8.884 1.376H0v21.248zm15.116 0h-8.884L24 22.624Z',
  hex: 'FF0000',
}

/** Official brand marks, by connector id. */
const BRAND_ICONS: Record<string, { path: string; hex: string }> = {
  aem: ADOBE,
  'adobe-commerce': ADOBE,
  marketo: ADOBE,
  wordpress: siWordpress,
  drupal: siDrupal,
  contentful: siContentful,
  contentstack: siContentstack,
  sitecore: siSitecore,
  github: siGithub,
}

/**
 * Relative luminance (WCAG). Used to spot brand marks that are so dark they
 * vanish on the dark-theme tile — GitHub's #181717 on gray-900 is about 1:1.
 */
function luminance(hex: string): number {
  const channels = [0, 2, 4]
    .map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map(v => (v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4)))
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2]
}

/**
 * Near-black marks get a theme-aware fill instead of their fixed hex, matching
 * how the brands publish them (GitHub ships a black mark and a white one for
 * exactly this reason).
 *
 * The 0.05 cut has wide margins: GitHub sits at 0.009, and the next darkest
 * mark here — WordPress — is at 0.154, so no coloured logo gets caught.
 */
const DARK_MARK_LUMINANCE = 0.05

function ConnectorMark({ connector }: { connector: CmsConnector }) {
  const icon = BRAND_ICONS[connector.id]
  const nearBlack = icon ? luminance(icon.hex) < DARK_MARK_LUMINANCE : false
  return (
    <span
      className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-900"
      aria-hidden
    >
      {icon ? (
        // Coloured marks use the registry's official hex. Near-black ones flip
        // to white in dark mode, otherwise the tile renders empty.
        <svg
          viewBox="0 0 24 24"
          className={nearBlack ? 'size-5 text-[#181717] dark:text-white' : 'size-5'}
          fill={nearBlack ? 'currentColor' : `#${icon.hex}`}
          data-mark={nearBlack ? 'adaptive' : 'brand'}
        >
          <path d={icon.path} />
        </svg>
      ) : (
        // Unreachable today (all eight are mapped) — kept so a connector added
        // without a licensed mark degrades to a visible initial, not a blank tile.
        <span className="text-xs font-semibold text-gray-500">{connector.name.charAt(0)}</span>
      )}
    </span>
  )
}

function ConnectorRow({
  connector,
  onConnect,
}: {
  connector: CmsConnector
  onConnect: () => void
}) {
  const connected = connector.status === 'connected'
  return (
    <li
      className="flex flex-wrap items-center gap-3 gap-y-1 px-4 py-3 md:flex-nowrap"
      data-testid="connector-row"
      data-connector={connector.id}
    >
      <ConnectorMark connector={connector} />

      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2 gap-y-1">
        <span className="font-medium">{connector.name}</span>
        {/* Deliberately grey: the Connected/Disconnected chip is the one thing
            worth scanning for, so the category must not compete. */}
        <Chip data-testid="connector-category">{connector.category}</Chip>
      </div>

      {/* A phone has no room for a status column: the chip drops to its own
          line under the name. */}
      <div className="order-last basis-full pl-12 md:order-none md:basis-auto md:w-28 md:shrink-0 md:pl-0 md:text-right">
        {connected ? (
          <Chip color="green" data-testid="connector-status">Connected</Chip>
        ) : (
          <Chip color="grey" data-testid="connector-status">Disconnected</Chip>
        )}
      </div>

      <div className="shrink-0 text-right md:w-28">
        <Button size="sm" onClick={onConnect} data-testid="connector-toggle">
          Connect
        </Button>
      </div>
    </li>
  )
}

export function IntegrationsPage() {
  const [contactOpen, setContactOpen] = useState(false)

  return (
    <PageContainer>
      <PageHeader
        title="Integrations"
      />

      <ul
        className="divide-y divide-gray-100 rounded-lg border border-gray-200 dark:divide-gray-800 dark:border-gray-800"
        data-testid="integrations-list"
        data-guide="integrations.list"
      >
        {CMS_CONNECTORS.map(c => (
          <ConnectorRow key={c.id} connector={c} onConnect={() => setContactOpen(true)} />
        ))}
      </ul>

      {/* One dialog for the whole list rather than one per row: the copy is
          identical either way, and setup is a conversation, not a form. */}
      <Dialog open={contactOpen} onOpenChange={setContactOpen}>
        <DialogContent data-testid="contact-dialog">
          <DialogHeader>
            <DialogTitle>Request an integration</DialogTitle>
            {/* The email line doubles as the dialog's description so it stays
                wired to aria-describedby — the only body copy left. */}
            <DialogDescription>
              Email{' '}
              <TextLink href="mailto:support@modelfront.com?subject=CMS%20integration" underlined className="font-medium">
                support@modelfront.com
              </TextLink>{' '}
              and we'll get you connected.
            </DialogDescription>
          </DialogHeader>

          <DialogFooter>
            <Button onClick={() => setContactOpen(false)} data-testid="contact-close">
              Got it
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </PageContainer>
  )
}
