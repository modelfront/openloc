import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { useCreatedByParam } from './use-created-by-param'

function Probe() {
  const [value, setValue] = useCreatedByParam()
  const { search } = useLocation()
  return (
    <>
      <span data-testid="value">{value == null ? 'none' : value}</span>
      <span data-testid="search">{search}</span>
      <button type="button" onClick={() => setValue(7)}>set</button>
      <button type="button" onClick={() => setValue(null)}>clear</button>
    </>
  )
}

function renderProbe(initialUrl: string) {
  render(
    <MemoryRouter initialEntries={[initialUrl]}>
      <Probe />
    </MemoryRouter>,
  )
}

describe('useCreatedByParam', () => {
  it('reads the created_by param', () => {
    renderProbe('/memories?created_by=42')
    expect(screen.getByTestId('value')).toHaveTextContent('42')
  })

  it.each(['', 'abc', '0', '-3', '1.5'])('ignores the non-id value %o', (raw) => {
    renderProbe(`/memories?created_by=${raw}`)
    expect(screen.getByTestId('value')).toHaveTextContent('none')
  })

  it('writes and clears the param without dropping unrelated ones', async () => {
    renderProbe('/memories?q=glossary')

    await userEvent.click(screen.getByText('set'))
    expect(screen.getByTestId('value')).toHaveTextContent('7')
    expect(screen.getByTestId('search')).toHaveTextContent('q=glossary')
    expect(screen.getByTestId('search')).toHaveTextContent('created_by=7')

    await userEvent.click(screen.getByText('clear'))
    expect(screen.getByTestId('value')).toHaveTextContent('none')
    expect(screen.getByTestId('search')).toHaveTextContent('q=glossary')
    expect(screen.getByTestId('search').textContent).not.toContain('created_by')
  })
})
