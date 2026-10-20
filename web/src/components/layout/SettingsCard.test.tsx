import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { Mail } from 'lucide-react'
import { SettingsCard } from './SettingsCard'

describe('SettingsCard', () => {
  it('is a section an e2e test can find by its heading, with the body inside it', () => {
    render(
      <SettingsCard title="Notification rules" description="Who gets which email." data-testid="rules" data-guide="org.rules">
        <button type="button">Add rule</button>
      </SettingsCard>,
    )
    const heading = screen.getByRole('heading', { level: 2, name: 'Notification rules' })
    const section = heading.closest('section')!
    expect(section).toBe(screen.getByTestId('rules'))
    expect(section).toHaveAttribute('data-guide', 'org.rules')
    expect(within(section).getByText('Who gets which email.')).toBeInTheDocument()
    expect(within(section).getByRole('button', { name: 'Add rule' })).toBeInTheDocument()
  })

  it('keeps the heading name to the title when an icon and an info element sit beside it', () => {
    render(
      <SettingsCard title="Members" icon={<Mail data-testid="icon" />} info={<span>(3)</span>}>
        body
      </SettingsCard>,
    )
    expect(screen.getByRole('heading', { level: 2, name: 'Members' })).toBeInTheDocument()
    expect(screen.getByText('(3)')).toBeInTheDocument()
    // The icon is decoration: hidden from assistive tech, not part of the heading.
    const icon = screen.getByTestId('icon')
    expect(icon.closest('[aria-hidden="true"]')).not.toBeNull()
    expect(icon.closest('h2')).toBeNull()
  })

  it('renders no description paragraph when none is given', () => {
    const { container } = render(<SettingsCard title="Profile"><div>form</div></SettingsCard>)
    expect(container.querySelector('section > p')).toBeNull()
  })
})
