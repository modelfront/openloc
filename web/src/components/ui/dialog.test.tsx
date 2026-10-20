import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import fs from 'node:fs'
import path from 'node:path'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from './dialog'
import { Button } from './button'

// jsdom lays nothing out and reports a 0×0 popup, which would drop the popup's
// own size out of the clamp arithmetic. Every containment test states a size,
// so the limit it asserts is half the FREE space (window minus popup) less the
// 8px edge margin — the term a size-blind clamp would get wrong.
const WINDOW_W = 1024
const WINDOW_H = 768
const EDGE_MARGIN = 8

function sizePopup(width: number, height: number) {
  const popup = screen.getByTestId('popup')
  popup.getBoundingClientRect = () => ({ width, height, top: 0, left: 0, right: width, bottom: height, x: 0, y: 0, toJSON: () => ({}) })
  return {
    x: (WINDOW_W - width) / 2 - EDGE_MARGIN,
    y: (WINDOW_H - height) / 2 - EDGE_MARGIN,
  }
}

function DragDemo({ draggable = true }: { draggable?: boolean }) {
  const [open, setOpen] = useState(true)
  return (
    <>
      <Button onClick={() => setOpen(true)}>Open</Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent draggable={draggable} data-testid="popup">
          <DialogHeader data-testid="header">
            <DialogTitle>Concordance search</DialogTitle>
            <Button onClick={() => setOpen(false)}>Close it</Button>
          </DialogHeader>
          <p>body</p>
        </DialogContent>
      </Dialog>
    </>
  )
}

function drag(from: [number, number], to: [number, number]) {
  fireEvent.pointerDown(screen.getByTestId('header'), { button: 0, clientX: from[0], clientY: from[1] })
  fireEvent.pointerMove(window, { clientX: to[0], clientY: to[1] })
  fireEvent.pointerUp(window)
}

describe('draggable Dialog', () => {
  it('moves the popup by the header drag distance', () => {
    render(<DragDemo />)
    drag([500, 300], [600, 350])
    expect(screen.getByTestId('popup').style.translate).toBe('calc(-50% + 100px) calc(-50% + 50px)')
  })

  it('accumulates a second drag on top of the first', () => {
    render(<DragDemo />)
    drag([500, 300], [560, 300])
    drag([560, 300], [600, 320])
    expect(screen.getByTestId('popup').style.translate).toBe('calc(-50% + 100px) calc(-50% + 20px)')
  })

  it('keeps the popup inside the viewport', () => {
    render(<DragDemo />)
    const limit = sizePopup(600, 400)
    expect(limit).toEqual({ x: 204, y: 176 })

    drag([500, 300], [9000, 9000])
    expect(screen.getByTestId('popup').style.translate).toBe(
      `calc(-50% + ${limit.x}px) calc(-50% + ${limit.y}px)`
    )

    drag([500, 300], [-9000, -9000])
    expect(screen.getByTestId('popup').style.translate).toBe(
      `calc(-50% + ${-limit.x}px) calc(-50% + ${-limit.y}px)`
    )
  })

  // The half of containment a 0×0 popup cannot prove: a clamp that ignores the
  // popup's own size gives both sizes the same room, and the big one hangs out
  // of the window by half its width.
  it('gives a bigger popup less room to move', () => {
    render(<DragDemo />)
    const big = sizePopup(900, 700)
    expect(big).toEqual({ x: 54, y: 26 })

    drag([500, 300], [9000, 9000])
    expect(screen.getByTestId('popup').style.translate).toBe(
      `calc(-50% + ${big.x}px) calc(-50% + ${big.y}px)`
    )
  })

  it('does not drag from a button inside the header', () => {
    render(<DragDemo />)
    fireEvent.pointerDown(screen.getByText('Close it'), { button: 0, clientX: 500, clientY: 300 })
    fireEvent.pointerMove(window, { clientX: 700, clientY: 500 })
    fireEvent.pointerUp(window)
    expect(screen.getByTestId('popup').style.translate).toBe('calc(-50% + 0px) calc(-50% + 0px)')
  })

  it('reopens centered after a close', () => {
    render(<DragDemo />)
    drag([500, 300], [600, 350])
    fireEvent.click(screen.getByText('Close it'))
    fireEvent.click(screen.getByText('Open'))
    expect(screen.getByTestId('popup').style.translate).toBe('calc(-50% + 0px) calc(-50% + 0px)')
  })

  it('leaves a non-draggable dialog alone', () => {
    render(<DragDemo draggable={false} />)
    drag([500, 300], [600, 350])
    const popup = screen.getByTestId('popup')
    expect(popup.style.translate).toBe('')
    expect(screen.getByTestId('header').className).not.toContain('cursor-grab')
  })
})

function KeepOpenDemo({ keepOpen }: { keepOpen: boolean }) {
  const [open, setOpen] = useState(true)
  return (
    <Dialog open={open} onOpenChange={setOpen} keepOpen={keepOpen}>
      <DialogContent data-testid="popup">
        <DialogHeader>
          <DialogTitle>Reject step</DialogTitle>
        </DialogHeader>
        <textarea aria-label="Reason" defaultValue="typed reason" />
      </DialogContent>
    </Dialog>
  )
}

describe('Dialog keepOpen', () => {
  it('ignores Escape and the close button while keepOpen is set', async () => {
    render(<KeepOpenDemo keepOpen />)
    await userEvent.click(screen.getByLabelText('Reason'))
    await userEvent.keyboard('{Escape}')
    await userEvent.click(screen.getByRole('button', { name: /close/i }))
    expect(screen.getByTestId('popup')).toBeInTheDocument()
    expect(screen.getByLabelText('Reason')).toHaveValue('typed reason')
  })

  it('closes on Escape without keepOpen', async () => {
    render(<KeepOpenDemo keepOpen={false} />)
    await userEvent.click(screen.getByLabelText('Reason'))
    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByTestId('popup')).not.toBeInTheDocument())
  })
})

// jsdom computes no Tailwind spacing, so the footer gap is checked in the
// source: a `gap-0` at any breakpoint makes the footer buttons touch.
describe('DialogFooter gap', () => {
  it('no DialogFooter in the app removes the gap between its buttons', () => {
    const srcRoot = path.resolve(__dirname, '../..')
    const offenders = fs.globSync('**/*.tsx', { cwd: srcRoot })
      .filter(file => !file.endsWith('.test.tsx'))
      .filter(file => /<DialogFooter\b[^>]*className="[^"]*(?<![\w-])(?:\w+:)*gap-0(?![\w-])/.test(
        fs.readFileSync(path.join(srcRoot, file), 'utf8'),
      ))
    expect(offenders).toEqual([])
  })
})
