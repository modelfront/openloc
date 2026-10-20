// Dragging a column border in the CAT editor header: starts from the rendered
// widths, moves width between the two neighbours, stops at a column's minimum,
// double-click resets, and nothing is stored — a fresh mount starts from the
// defaults.
import { describe, it, expect, afterEach, beforeAll, afterAll } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { useRef } from 'react'
import { useColumnBorderDrag } from './useColumnBorderDrag'

const RENDERED = [60, 300, 300, 64, 92, 40]
const MINS = [32, 120, 120, 64, 92, 0]

function Header() {
  const ref = useRef<HTMLDivElement>(null)
  const { widths, startDrag, reset } = useColumnBorderDrag(ref, MINS)
  return (
    <>
      <div ref={ref}>
        {RENDERED.map((w, i) => (
          <div key={i} data-width={w}>
            {i < RENDERED.length - 1 && (
              <div data-testid={`handle-${i}`} onPointerDown={startDrag(i)} onDoubleClick={reset} />
            )}
          </div>
        ))}
      </div>
      <output data-testid="widths">{widths ? widths.join(',') : 'default'}</output>
    </>
  )
}

// jsdom has no layout: report each header cell's width from its data attribute.
const realRect = HTMLElement.prototype.getBoundingClientRect
beforeAll(() => {
  HTMLElement.prototype.getBoundingClientRect = function () {
    const w = Number(this.getAttribute('data-width'))
    return w ? ({ width: w } as DOMRect) : realRect.call(this)
  }
})
afterAll(() => { HTMLElement.prototype.getBoundingClientRect = realRect })

// fireEvent.pointer* in jsdom drops clientX; a MouseEvent-typed event keeps it.
const pointer = (type: string, clientX: number) =>
  Object.assign(new MouseEvent(type, { bubbles: true, clientX, button: 0 }), { pointerId: 1 })

describe('useColumnBorderDrag', () => {
  afterEach(cleanup)

  it('uses the defaults until the first drag', () => {
    render(<Header />)
    expect(screen.getByTestId('widths').textContent).toBe('default')
  })

  it('a drag moves width from one neighbour to the other, from the rendered widths', () => {
    render(<Header />)
    fireEvent(screen.getByTestId('handle-1'), pointer('pointerdown', 500))
    fireEvent(window, pointer('pointermove', 540))
    expect(screen.getByTestId('widths').textContent).toBe('60,340,260,64,92,40')

    // Released: further moves do nothing.
    fireEvent(window, pointer('pointerup', 540))
    fireEvent(window, pointer('pointermove', 600))
    expect(screen.getByTestId('widths').textContent).toBe('60,340,260,64,92,40')
  })

  it('a drag stops where the shrinking column reaches its minimum', () => {
    render(<Header />)
    // Match | Status: Match is already at its 64px minimum.
    fireEvent(screen.getByTestId('handle-3'), pointer('pointerdown', 500))
    fireEvent(window, pointer('pointermove', 400))
    expect(screen.getByTestId('widths').textContent).toBe('60,300,300,64,92,40')
    // The other way, Status is at its minimum too.
    fireEvent(window, pointer('pointermove', 600))
    expect(screen.getByTestId('widths').textContent).toBe('60,300,300,64,92,40')
  })

  it.each(['pointercancel', 'blur'])('%s ends the drag like pointerup', type => {
    render(<Header />)
    fireEvent(screen.getByTestId('handle-1'), pointer('pointerdown', 500))
    fireEvent(window, pointer('pointermove', 520))
    fireEvent(window, type === 'blur' ? new Event('blur') : pointer(type, 520))
    fireEvent(window, pointer('pointermove', 600))
    expect(screen.getByTestId('widths').textContent).toBe('60,320,280,64,92,40')
  })

  it('double-click restores the defaults', () => {
    render(<Header />)
    fireEvent(screen.getByTestId('handle-3'), pointer('pointerdown', 0))
    fireEvent(window, pointer('pointermove', -10))
    fireEvent(window, pointer('pointerup', -10))
    fireEvent.doubleClick(screen.getByTestId('handle-3'))
    expect(screen.getByTestId('widths').textContent).toBe('default')
  })

  it('is not saved anywhere: a new mount starts from the defaults', () => {
    const first = render(<Header />)
    fireEvent(screen.getByTestId('handle-1'), pointer('pointerdown', 0))
    fireEvent(window, pointer('pointermove', 30))
    fireEvent(window, pointer('pointerup', 30))
    first.unmount()

    render(<Header />)
    expect(screen.getByTestId('widths').textContent).toBe('default')
    expect(localStorage.length).toBe(0)
  })
})
