import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, render, screen, cleanup } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { useEffect, useState } from 'react'
import { AttentionProvider, useAttentionTarget } from './useAttention'

beforeEach(() => vi.useFakeTimers())
afterEach(() => { vi.useRealTimers(); cleanup() })

function Target({ id }: { id: string }) {
  return <div data-testid={id} ref={useAttentionTarget(id)}>{id}</div>
}

function Url() {
  const { pathname, search } = useLocation()
  return <output data-testid="url">{pathname}{search}</output>
}

/** Mounts its target only after `ms`, like a row that waits for its query. */
function LateTarget({ id, ms = 500 }: { id: string; ms?: number }) {
  const [ready, setReady] = useState(false)
  useEffect(() => {
    const t = setTimeout(() => setReady(true), ms)
    return () => clearTimeout(t)
  }, [ms])
  return ready ? <Target id={id} /> : null
}

/** Leaves for /mt right away; /mt mounts a target with the same key late. */
function GoToMT() {
  const navigate = useNavigate()
  useEffect(() => { navigate('/mt') }, [navigate])
  return null
}

function renderAt(url: string, page: React.ReactNode) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <AttentionProvider>
        <Routes>
          <Route path="/ai" element={<>{page}<Url /></>} />
          <Route path="/mt" element={<><LateTarget id="ai-router-9" /><Url /></>} />
        </Routes>
      </AttentionProvider>
    </MemoryRouter>,
  )
}

describe('AttentionProvider + useAttentionTarget', () => {
  it('rings the target named in the URL and strips the parameter', () => {
    renderAt('/ai?attention=ai-router-2&tab=x', <><Target id="ai-router-1" /><Target id="ai-router-2" /></>)
    act(() => { vi.advanceTimersByTime(0) })
    expect(screen.getByTestId('ai-router-2').dataset.attention).toBe('')
    expect(screen.getByTestId('ai-router-1').dataset.attention).toBeUndefined()
    expect(screen.getByTestId('url')).toHaveTextContent('/ai?tab=x')
  })

  it('waits for a target that renders after its data arrives', async () => {
    renderAt('/ai?attention=ai-router-9', <LateTarget id="ai-router-9" />)
    expect(screen.queryByTestId('ai-router-9')).not.toBeInTheDocument()
    await act(async () => { await vi.advanceTimersByTimeAsync(600) })
    expect(screen.getByTestId('ai-router-9').dataset.attention).toBe('')
  })

  it('does nothing for a key no element declares', () => {
    renderAt('/ai?attention=ai-router-404', <Target id="ai-router-1" />)
    act(() => { vi.advanceTimersByTime(0) })
    expect(document.querySelector('[data-attention]')).toBeNull()
    expect(screen.getByTestId('url').textContent).toBe('/ai')
  })

  it('drops a key nothing claimed within its time', async () => {
    renderAt('/ai?attention=ai-router-9', <LateTarget id="ai-router-9" ms={10_500} />)
    await act(async () => { await vi.advanceTimersByTimeAsync(11_000) })
    expect(screen.getByTestId('ai-router-9').dataset.attention).toBeUndefined()
  })

  it('drops a key nothing claimed when the user moves to another page', async () => {
    renderAt('/ai?attention=ai-router-9', <GoToMT />)
    await act(async () => { await vi.advanceTimersByTimeAsync(600) })
    expect(screen.getByTestId('url').textContent).toBe('/mt')
    expect(screen.getByTestId('ai-router-9').dataset.attention).toBeUndefined()
  })
})
