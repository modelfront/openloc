import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { IntegrationsPage } from './IntegrationsPage'
import { CMS_CONNECTORS } from '../lib/cms-connectors'

afterEach(cleanup)

/** Address a row by its stable id — names also appear in badges and headings. */
function row(id: string) {
  return document.querySelector(`[data-connector="${id}"]`) as HTMLElement
}

describe('IntegrationsPage', () => {
  it('renders every connector as one row in a single list', () => {
    render(<IntegrationsPage />)
    expect(screen.getAllByTestId('connector-row')).toHaveLength(CMS_CONNECTORS.length)
    expect(screen.getByTestId('integrations-list')).toBeInTheDocument()
    expect(row('github')).toBeInTheDocument()
  })

  it('ships every connector disconnected', () => {
    render(<IntegrationsPage />)
    // Nothing is wired up yet, so a green "Connected" badge anywhere would be a
    // lie — most likely leftover demo data in the catalogue.
    for (const c of CMS_CONNECTORS) {
      expect(
        within(row(c.id)).getByTestId('connector-status'),
        `${c.name} should ship disconnected`,
      ).toHaveTextContent('Disconnected')
      expect(within(row(c.id)).getByTestId('connector-toggle')).toHaveTextContent('Connect')
    }
  })

  it('renders the category as a chip', () => {
    render(<IntegrationsPage />)
    expect(within(row('wordpress')).getByTestId('connector-category')).toHaveTextContent('Traditional CMS')
    expect(within(row('github')).getByTestId('connector-category')).toHaveTextContent('Code & docs')
  })

  it('gives every connector a real brand mark', () => {
    render(<IntegrationsPage />)
    // No lettered placeholders left: six marks come from simple-icons and
    // Adobe's is inlined, so an empty tile means a mapping was dropped.
    for (const c of CMS_CONNECTORS) {
      expect(row(c.id).querySelector('svg'), `${c.name} has no brand mark`).toBeTruthy()
    }
  })

  it('flips near-black marks to a theme-aware fill so they survive dark mode', () => {
    render(<IntegrationsPage />)

    // GitHub's brand hex is #181717. Painted literally it disappears against
    // the dark-theme tile (gray-900) — roughly 1:1 contrast, an empty square.
    const github = row('github').querySelector('svg')!
    expect(github.getAttribute('fill')).toBe('currentColor')
    expect(github.getAttribute('data-mark')).toBe('adaptive')
    expect(github.getAttribute('class')).toContain('dark:text-white')

    // Coloured marks must NOT be swept up by the rule — they keep their own
    // brand hex, which is the whole point of using real logos.
    const wordpress = row('wordpress').querySelector('svg')!
    expect(wordpress.getAttribute('data-mark')).toBe('brand')
    expect(wordpress.getAttribute('fill')).toBe('#21759B')
  })

  it('shares one Adobe mark across the three Adobe products', () => {
    render(<IntegrationsPage />)
    const paths = ['aem', 'adobe-commerce', 'marketo'].map(
      id => row(id).querySelector('path')?.getAttribute('d'),
    )
    expect(paths[0]).toBeTruthy()
    // Deliberate: Adobe's registry marks were withdrawn, so the corporate "A"
    // stands in for all three rather than three invented product logos.
    expect(new Set(paths).size).toBe(1)
    // ...and it must not be silently reused for a non-Adobe row.
    expect(row('wordpress').querySelector('path')?.getAttribute('d')).not.toBe(paths[0])
  })

  it('Connect opens the contact dialog rather than pretending to connect', async () => {
    const user = userEvent.setup()
    render(<IntegrationsPage />)

    expect(screen.queryByTestId('contact-dialog')).not.toBeInTheDocument()
    await user.click(within(row('contentful')).getByTestId('connector-toggle'))

    const dialog = screen.getByTestId('contact-dialog')
    expect(dialog).toBeInTheDocument()
    // The address is the whole point of the dialog, and it has to be actionable.
    const mail = within(dialog).getByRole('link', { name: 'support@modelfront.com' })
    expect(mail.getAttribute('href')).toContain('mailto:support@modelfront.com')

    // The row stays Disconnected — Connect only opens the contact dialog.
    expect(within(row('contentful')).getByTestId('connector-status')).toHaveTextContent('Disconnected')
  })

  it('every row opens the same dialog', async () => {
    const user = userEvent.setup()
    render(<IntegrationsPage />)

    // Guards the shared-dialog wiring: a per-row handler that captured the
    // wrong connector would still open something, so check more than one.
    for (const id of ['github', 'aem']) {
      await user.click(within(row(id)).getByTestId('connector-toggle'))
      expect(screen.getByTestId('contact-dialog')).toBeInTheDocument()
      await user.click(screen.getByTestId('contact-close'))
    }
  })
})
