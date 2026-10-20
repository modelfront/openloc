import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { useUrlParamSetters } from './useUrlParamSetters'

function Probe() {
  const { setParam, clearParams } = useUrlParamSetters()
  const { search } = useLocation()
  return (
    <>
      <span data-testid="search">{search}</span>
      <button type="button" onClick={() => setParam('step', 'Review')}>set</button>
      <button type="button" onClick={() => setParam('step', '')}>empty</button>
      <button type="button" onClick={() => setParam('step', '', { keepEmpty: true })}>keep-empty</button>
      <button type="button" onClick={() => setParam('step', null, { keepEmpty: true })}>keep-null</button>
      <button type="button" onClick={() => clearParams(['step', 'q'])}>clear-some</button>
      <button type="button" onClick={() => clearParams()}>clear-all</button>
    </>
  )
}

function renderProbe(url: string) {
  render(
    <MemoryRouter initialEntries={[url]}>
      <Probe />
    </MemoryRouter>,
  )
}

const search = () => screen.getByTestId('search').textContent

describe('useUrlParamSetters', () => {
  it('sets a param and keeps the others', () => {
    renderProbe('/x?q=a')
    fireEvent.click(screen.getByText('set'))
    expect(search()).toBe('?q=a&step=Review')
  })

  it('removes a param set to an empty value', () => {
    renderProbe('/x?step=Review&q=a')
    fireEvent.click(screen.getByText('empty'))
    expect(search()).toBe('?q=a')
  })

  it('keeps an empty value when asked, and still removes null', () => {
    renderProbe('/x?q=a')
    fireEvent.click(screen.getByText('keep-empty'))
    expect(search()).toBe('?q=a&step=')
    fireEvent.click(screen.getByText('keep-null'))
    expect(search()).toBe('?q=a')
  })

  it('clears only the given keys, or everything without keys', () => {
    renderProbe('/x?step=Review&q=a&file=3')
    fireEvent.click(screen.getByText('clear-some'))
    expect(search()).toBe('?file=3')
    fireEvent.click(screen.getByText('clear-all'))
    expect(search()).toBe('')
  })
})
