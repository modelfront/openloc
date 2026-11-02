import { describe, it, expect, vi, afterEach } from 'vitest'
import { act, render, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom'
import { useDebouncedUrlParam } from './useDebouncedUrlParam'

function Probe({ delay = 300 }: { delay?: number }) {
  const [value, setValue] = useDebouncedUrlParam('q', delay)
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

function renderProbe(url: string, delay?: number) {
  render(
    <MemoryRouter initialEntries={[url]}>
      <Probe delay={delay} />
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

  // The router applies each write as a transition, so with no delay the URL
  // trails the input by a write or two while the keys keep coming.
  it('keeps every key while its own writes are still reaching the URL', async () => {
    const user = userEvent.setup()
    renderProbe('/list', 0)

    await user.type(screen.getByLabelText('search'), 'legal en-fr')

    expect(screen.getByLabelText('search')).toHaveValue('legal en-fr')
    await waitFor(() => expect(screen.getByTestId('search')).toHaveTextContent('?q=legal+en-fr'))
  })

  it('follows the URL when the param changes from outside', () => {
    renderProbe('/list?q=manual')
    fireEvent.click(screen.getByText('reset'))
    expect(screen.getByLabelText('search')).toHaveValue('')
  })
})
