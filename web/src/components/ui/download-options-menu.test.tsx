import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { FileText } from 'lucide-react'

import { DownloadOptionsMenu, type DownloadOption } from './download-options-menu'
import { menuItem } from '@/test/menu'

const options: DownloadOption<'a' | 'b'>[] = [
  { id: 'a', label: 'Option A', hint: 'First', icon: FileText, testId: 'opt-a' },
  { id: 'b', label: 'Option B', hint: 'Not now', icon: FileText, disabled: true, testId: 'opt-b' },
]

afterEach(cleanup)

describe('DownloadOptionsMenu', () => {
  it('lists every option with its hint and hands the picked id back', async () => {
    const onSelect = vi.fn()
    render(<DownloadOptionsMenu options={options} onSelect={onSelect} testId="dl" />)
    await userEvent.click(screen.getByTestId('dl'))

    expect(await menuItem('opt-a')).toHaveTextContent('Option AFirst')
    expect(await menuItem('opt-b')).toHaveTextContent('Option BNot now')
    await userEvent.click(await menuItem('opt-a'))
    expect(onSelect).toHaveBeenCalledWith('a')
  })

  it('does not run a disabled option', async () => {
    const onSelect = vi.fn()
    render(<DownloadOptionsMenu options={options} onSelect={onSelect} testId="dl" />)
    await userEvent.click(screen.getByTestId('dl'))
    const b = await menuItem('opt-b')
    expect(b).toHaveAttribute('data-disabled')
    await userEvent.click(b)
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('shows the label alone for an option without a hint', async () => {
    render(
      <DownloadOptionsMenu
        options={[{ id: 'a', label: 'Option A', icon: FileText, testId: 'opt-a' }]}
        onSelect={() => {}}
        testId="dl"
      />,
    )
    await userEvent.click(screen.getByTestId('dl'))
    expect(await menuItem('opt-a')).toHaveTextContent(/^Option A$/)
  })

  it('disables the button while busy and says so', () => {
    render(<DownloadOptionsMenu options={options} onSelect={() => {}} busy testId="dl" />)
    expect(screen.getByTestId('dl')).toBeDisabled()
    expect(screen.getByTestId('dl')).toHaveTextContent('Downloading…')
  })

  it('icon-only names the download for screen readers', () => {
    render(<DownloadOptionsMenu options={options} onSelect={() => {}} iconOnly ariaLabel="Download Report" testId="dl" />)
    expect(screen.getByTestId('dl')).toHaveAccessibleName('Download Report')
    expect(screen.getByTestId('dl')).not.toHaveTextContent('Download')
  })
})
