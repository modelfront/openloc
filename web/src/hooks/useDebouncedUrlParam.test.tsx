import { describe, it, expect, vi, afterEach } from 'vitest'
import { act, render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom'
import { useDebouncedUrlParam } from './useDebouncedUrlParam'

function Probe() {
  const [value, setValue] = useDebouncedUrlParam('q', 300)
  const { search } = useLocation()
  const navigate = useNavigate()
  return (
    <>
      <input aria-label="search" value={value} onChange={e => setValue(e.target.value)} />
      <span data-testid="search">{search}</span>
      <button type="button" onClick={() => navigate('/list?step=Review', { replace: true })}>reset</button>
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

describe('useDebouncedUrlParam', () => {
  afterEach(() => { vi.useRealTimers() })

  it('starts from the URL value', () => {
    renderProbe('/list?q=manual')
    expect(screen.getByLabelText('search')).toHaveValue('manual')
  })

  it('writes the param only after the delay, keeping other params', () => {
    vi.useFakeTimers()
    renderProbe('/list?step=Review')
    fireEvent.change(screen.getByLabelText('search'), { target: { value: 'slides' } })
    act(() => { vi.advanceTimersByTime(299) })
    expect(screen.getByTestId('search')).toHaveTextContent('?step=Review')
    expect(screen.getByTestId('search')).not.toHaveTextContent('q=')
    act(() => { vi.advanceTimersByTime(1) })
    expect(screen.getByTestId('search')).toHaveTextContent('?step=Review&q=slides')
  })

  it('removes the param when the input is emptied', () => {
    vi.useFakeTimers()
    renderProbe('/list?q=manual&step=Review')
    fireEvent.change(screen.getByLabelText('search'), { target: { value: '' } })
    act(() => { vi.advanceTimersByTime(300) })
    expect(screen.getByTestId('search')).toHaveTextContent('?step=Review')
    expect(screen.getByTestId('search')).not.toHaveTextContent('q=')
  })

  it('follows the URL when the param changes from outside', () => {
    renderProbe('/list?q=manual')
    fireEvent.click(screen.getByText('reset'))
    expect(screen.getByLabelText('search')).toHaveValue('')
  })
})
