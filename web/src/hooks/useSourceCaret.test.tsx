import { describe, expect, it } from 'vitest'
import { useEffect } from 'react'
import { act, render, screen } from '@testing-library/react'
import { useSourceCaret } from './useSourceCaret'

let caret: { current: number | null } = { current: null }

/** A source box as the row draws it: a label on its border first, then the
 *  field that takes the caret. */
function Box({ text }: { text: string }) {
  const { caretRef, hasCaret, fieldProps, fieldKey } = useSourceCaret(true)
  useEffect(() => { caret = caretRef })
  return (
    <div>
      <span>✂ Split</span>
      {hasCaret && <span>has caret</span>}
      <div key={fieldKey} data-testid="field" tabIndex={-1} {...fieldProps}>{text}</div>
    </div>
  )
}

/** A caret the user puts in the source: the field has focus. */
function caretAt(offset: number, focus = true) {
  const field = screen.getByTestId('field')
  act(() => {
    if (focus) field.focus()
    document.getSelection()!.collapse(field.firstChild!, offset)
    document.dispatchEvent(new Event('selectionchange'))
  })
}

describe('useSourceCaret', () => {
  it('cancels every edit the browser announces, deletions included', () => {
    render(<Box text="Press the button" />)
    for (const inputType of ['insertText', 'deleteContentBackward', 'deleteContentForward', 'insertParagraph', 'insertFromPaste']) {
      const e = new InputEvent('beforeinput', { inputType, cancelable: true, bubbles: true })
      screen.getByTestId('field').dispatchEvent(e)
      expect(e.defaultPrevented, inputType).toBe(true)
    }
  })

  it('counts the caret in the source only, not the label on the border', () => {
    render(<Box text="Press the button" />)
    caretAt(5)
    expect(screen.getByText('✂ Split')).toBeTruthy()
    caretAt(9)
    expect(caret.current).toBe(9)
  })

  // The click that opens a row lands in its source text before the row's
  // translation editor takes focus; that is not a caret the user put there.
  it('ignores a selection in the source while the source does not have focus', () => {
    render(<Box text="Press the button" />)
    caretAt(3, false)
    expect(caret.current).toBeNull()
    expect(screen.queryByText('has caret')).toBeNull()
  })

  it('reports a caret once the user puts one in the source', () => {
    render(<Box text="Press the button" />)
    expect(screen.queryByText('has caret')).toBeNull()
    caretAt(5)
    expect(screen.getByText('has caret')).toBeTruthy()
  })

  it('puts the source back, and the caret where it was, after an edit it could not cancel', () => {
    render(<Box text="Press the button" />)
    caretAt(5)
    const field = screen.getByTestId('field')
    act(() => {
      field.firstChild!.textContent = 'Pressxx the button'
      field.dispatchEvent(new InputEvent('input', { inputType: 'insertCompositionText', bubbles: true }))
    })
    expect(screen.getByTestId('field').textContent).toBe('Press the button')
    const sel = document.getSelection()!
    expect(screen.getByTestId('field').contains(sel.focusNode)).toBe(true)
    expect(sel.focusOffset).toBe(5)
  })
})
