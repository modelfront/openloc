import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act, cleanup, fireEvent } from '@testing-library/react'
import { createRef, type RefObject } from 'react'

// Stubs from web/src/lib/__mocks__: the real extensions need browser APIs jsdom lacks.
vi.mock('../lib/tiptap-spellcheck')
vi.mock('../lib/tiptap-whitespace')

import SegmentEditor, { type SegmentEditorHandle } from './SegmentEditor'

describe('SegmentEditor (component)', () => {
  const defaultProps = {
    initialText: 'Hello world',
    readOnly: false,
    onTextChange: vi.fn(),
    onConfirmAndAdvance: vi.fn(),
    onNavigate: vi.fn(),
    onDeactivate: vi.fn(),
    onCopySource: vi.fn(),
  }

  // Track editor refs so we can destroy TipTap before React unmount.
  // Prevents async TipTap cleanup from firing after jsdom teardown.
  const activeRefs: RefObject<SegmentEditorHandle | null>[] = []

  beforeEach(() => { vi.clearAllMocks() })
  afterEach(() => {
    activeRefs.forEach(ref => ref.current?.destroy())
    activeRefs.length = 0
    cleanup()
  })

  function createTrackedRef(): RefObject<SegmentEditorHandle | null> {
    const ref = createRef<SegmentEditorHandle>()
    activeRefs.push(ref)
    return ref
  }

  it('renders the editor container with data-testid', async () => {
    const ref = createTrackedRef()
    render(<SegmentEditor {...defaultProps} ref={ref} />)

    const container = screen.getByTestId('segment-editor')
    expect(container).toBeInTheDocument()
    expect(container).toHaveClass('segment-editor')
  })

  it('getText returns the initial text content', async () => {
    const ref = createTrackedRef()
    render(<SegmentEditor {...defaultProps} ref={ref} />)

    await waitFor(() => {
      expect(ref.current?.getText()).toBe('Hello world')
    })
  })

  it('setText updates editor content', async () => {
    const ref = createTrackedRef()
    render(<SegmentEditor {...defaultProps} ref={ref} />)

    await waitFor(() => expect(ref.current).not.toBeNull())

    act(() => {
      ref.current!.setText('New content')
    })

    expect(ref.current!.getText()).toBe('New content')
  })

  it('setSelection selects the given range and getSelection reports it', async () => {
    const ref = createTrackedRef()
    render(<SegmentEditor {...defaultProps} ref={ref} />)

    await waitFor(() => expect(ref.current).not.toBeNull())

    act(() => {
      ref.current!.setSelection(2, 5)
    })

    expect(ref.current!.getSelection()).toEqual({ from: 2, to: 5 })
  })

  it('applies custom className', () => {
    const ref = createTrackedRef()
    render(<SegmentEditor {...defaultProps} className="my-custom-class" ref={ref} />)
    const container = screen.getByTestId('segment-editor')
    expect(container).toHaveClass('my-custom-class')
  })

  it('renders as read-only when readOnly is true', async () => {
    const ref = createTrackedRef()
    render(<SegmentEditor {...defaultProps} readOnly ref={ref} />)

    await waitFor(() => expect(ref.current).not.toBeNull())

    const editor = ref.current!.getEditor()
    expect(editor?.isEditable).toBe(false)
  })

  it('insertText adds text at cursor', async () => {
    const ref = createTrackedRef()
    render(<SegmentEditor {...defaultProps} initialText="" ref={ref} />)

    await waitFor(() => expect(ref.current).not.toBeNull())

    act(() => {
      ref.current!.insertText('inserted')
    })

    expect(ref.current!.getText()).toContain('inserted')
  })
  // Pasted text can still hold paragraphs; each break reads back as one '\n',
  // never TipTap's default '\n\n', which would save as two line breaks.
  it('getText separates paragraphs with a single newline', async () => {
    const ref = createTrackedRef()
    render(<SegmentEditor {...defaultProps} ref={ref} />)
    await waitFor(() => expect(ref.current?.getEditor()).toBeTruthy())
    act(() => { ref.current!.getEditor()!.commands.setContent('<p>A</p><p>B</p>') })
    expect(ref.current!.getText()).toBe('A\nB')
  })

  // A segment is one paragraph: plain Enter adds no
  // break. Shift+Enter is the line break.
  it('plain Enter does not split the segment; Shift+Enter adds a line break', async () => {
    const ref = createTrackedRef()
    render(<SegmentEditor {...defaultProps} ref={ref} />)
    await waitFor(() => expect(ref.current?.getEditor()).toBeTruthy())
    const editor = ref.current!.getEditor()!
    act(() => { editor.commands.setTextSelection(6) })

    fireEvent.keyDown(editor.view.dom, { key: 'Enter' })
    expect(editor.getHTML()).toBe('<p>Hello world</p>')
    expect(defaultProps.onConfirmAndAdvance).not.toHaveBeenCalled()

    fireEvent.keyDown(editor.view.dom, { key: 'Enter', shiftKey: true })
    expect(editor.getHTML()).toBe('<p>Hello<br> world</p>')
  })
})
