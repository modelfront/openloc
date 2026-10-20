import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TriangleAlert } from 'lucide-react'
import { Alert } from './alert'
import { chipSurfaceClass, chipTextClass } from './chip'

function classesOf(el: HTMLElement) {
  return el.className.split(/\s+/)
}

describe('Alert', () => {
  it('announces an error assertively and a warning politely', () => {
    render(
      <>
        <Alert tone="error">Wrong password.</Alert>
        <Alert tone="warning">3 segments are not yet confirmed.</Alert>
      </>,
    )
    expect(screen.getByRole('alert')).toHaveTextContent('Wrong password.')
    expect(screen.getByRole('status')).toHaveTextContent('3 segments are not yet confirmed.')
  })

  it('lets a blocking warning take role="alert"', () => {
    render(<Alert tone="warning" role="alert">Pick a version.</Alert>)
    expect(screen.getByRole('alert')).toHaveTextContent('Pick a version.')
    expect(screen.queryByRole('status')).toBeNull()
  })

  it('takes its colours from the chip palette: yellow for a warning, red for an error', () => {
    render(
      <>
        <Alert tone="warning" data-testid="w">w</Alert>
        <Alert tone="error" data-testid="e">e</Alert>
      </>,
    )
    const warning = classesOf(screen.getByTestId('w'))
    const error = classesOf(screen.getByTestId('e'))
    for (const cls of [...chipSurfaceClass('yellow').split(' '), ...chipTextClass('yellow').split(' ')]) {
      expect(warning).toContain(cls)
    }
    for (const cls of [...chipSurfaceClass('red').split(' '), ...chipTextClass('red').split(' ')]) {
      expect(error).toContain(cls)
    }
    expect(warning).not.toContain(chipSurfaceClass('red').split(' ')[0])
  })

  it('renders the title before the body', () => {
    render(<Alert tone="warning" title="Copy your key now.">It will not be shown again.</Alert>)
    const box = screen.getByRole('status')
    expect(box.textContent).toBe('Copy your key now.It will not be shown again.')
  })

  it('hides the icon from assistive tech', () => {
    render(<Alert tone="error" icon={<TriangleAlert data-testid="icon" />}>Failed.</Alert>)
    const icon = screen.getByTestId('icon')
    expect(icon.closest('[aria-hidden="true"]')).not.toBeNull()
  })

  it('passes test ids and layout classes through and keeps interactive children usable', () => {
    render(
      <Alert tone="error" data-testid="load-failed" className="mb-4">
        Couldn't load the projects. <button type="button">Retry</button>
      </Alert>,
    )
    const box = screen.getByTestId('load-failed')
    expect(classesOf(box)).toContain('mb-4')
    expect(screen.getByRole('alert')).toContainElement(screen.getByRole('button', { name: 'Retry' }))
  })
})
