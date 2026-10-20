import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act, cleanup, fireEvent, within } from '@testing-library/react'
import { createRef, type RefObject } from 'react'
import type { SpellFixRequest } from '../lib/tiptap-spellcheck'

// Stubs from web/src/lib/__mocks__: the real extensions need browser APIs jsdom lacks.
vi.mock('../lib/tiptap-spellcheck')
vi.mock('../lib/tiptap-whitespace')

import RichTextCellEditor, { type RichTextCellEditorHandle, textToContent } from './RichTextCellEditor'
import { Extension, type JSONContent } from '@tiptap/core'

describe('RichTextCellEditor', () => {
  const defaultProps = {
    initialText: 'Hello world',
    readOnly: false,
    onTextChange: vi.fn(),
  }

  const activeRefs: RefObject<RichTextCellEditorHandle | null>[] = []

  beforeEach(() => { vi.clearAllMocks() })
  afterEach(() => {
    activeRefs.forEach(ref => ref.current?.destroy())
    activeRefs.length = 0
    cleanup()
  })

  function trackedRef(): RefObject<RichTextCellEditorHandle | null> {
    const ref = createRef<RichTextCellEditorHandle>()
    activeRefs.push(ref)
    return ref
  }

  it('renders without a dataTestId by default', () => {
    const ref = trackedRef()
    const { container } = render(<RichTextCellEditor {...defaultProps} ref={ref} />)
    // No data-testid attribute on the outer wrapper
    const div = container.querySelector('div[dir="auto"]')
    expect(div).toBeInTheDocument()
    expect(div?.getAttribute('data-testid')).toBeNull()
  })

  it('renders with a dataTestId when provided', () => {
    const ref = trackedRef()
    render(<RichTextCellEditor {...defaultProps} dataTestId="tm-cell-editor" ref={ref} />)
    expect(screen.getByTestId('tm-cell-editor')).toBeInTheDocument()
  })

  it('exposes the full imperative handle', async () => {
    const ref = trackedRef()
    render(<RichTextCellEditor {...defaultProps} ref={ref} />)
    await waitFor(() => expect(ref.current).not.toBeNull())

    const handle = ref.current!
    expect(typeof handle.focus).toBe('function')
    expect(typeof handle.getText).toBe('function')
    expect(typeof handle.getHTML).toBe('function')
    expect(typeof handle.getRichText).toBe('function')
    expect(typeof handle.setText).toBe('function')
    expect(typeof handle.setHTML).toBe('function')
    expect(typeof handle.getSelection).toBe('function')
    expect(typeof handle.setSelection).toBe('function')
    expect(typeof handle.insertText).toBe('function')
    expect(typeof handle.replaceRange).toBe('function')
    expect(typeof handle.getEditor).toBe('function')
    expect(typeof handle.destroy).toBe('function')
  })

  it('initializes with the provided text', async () => {
    const ref = trackedRef()
    render(<RichTextCellEditor {...defaultProps} ref={ref} />)
    await waitFor(() => expect(ref.current?.getText()).toBe('Hello world'))
  })

  it('setText replaces the editor content', async () => {
    const ref = trackedRef()
    render(<RichTextCellEditor {...defaultProps} ref={ref} />)
    await waitFor(() => expect(ref.current).not.toBeNull())

    act(() => { ref.current!.setText('Brand new content') })
    expect(ref.current!.getText()).toBe('Brand new content')
  })

  it('insertText adds text at the cursor', async () => {
    const ref = trackedRef()
    render(<RichTextCellEditor {...defaultProps} initialText="" ref={ref} />)
    await waitFor(() => expect(ref.current).not.toBeNull())

    act(() => { ref.current!.insertText('inserted') })
    expect(ref.current!.getText()).toContain('inserted')
  })

  it('getRichText returns null for plain text', async () => {
    const ref = trackedRef()
    render(<RichTextCellEditor {...defaultProps} ref={ref} />)
    await waitFor(() => expect(ref.current).not.toBeNull())

    expect(ref.current!.getRichText()).toBeNull()
  })

  // Real-time conversion: when the user TYPES the closing `/>` of
  // a self-closing XLIFF tag, the editor must replace the entire just-typed
  // markup with an atomic chip immediately — not save+reload. This test
  // simulates the final typed character via ProseMirror's `handleTextInput`
  // (the same hook the contenteditable element invokes on real keystrokes),
  // bypassing programmatic `insertContent` (which doesn't run input rules).
  //
  // Without the InputRule on `XliffTag`, the typed text stays as plain
  // characters: not highlighted, not atomic.
  it('real-time: typing the closing `/>` of `<ph id="X"/>` converts it to a chip atom', async () => {
    const ref = trackedRef()
    render(<RichTextCellEditor {...defaultProps} initialText="" ref={ref} />)
    await waitFor(() => expect(ref.current).not.toBeNull())

    const editor = ref.current!.getEditor()!
    const view = editor.view

    // Insert all but the LAST character via the normal command path (no input
    // rules), so the precondition is reproducible: doc has plain `<ph id="1"/`,
    // no chip yet.
    act(() => {
      editor.commands.insertContent('<ph id="1"/')
    })
    let preChips = 0
    editor.state.doc.descendants((n) => { if (n.type.name === 'xliffTag') preChips++ })
    expect(preChips, 'precondition: no chip before the closing `>`').toBe(0)

    // Now route the FINAL `>` through ProseMirror's handleTextInput hook —
    // exactly what the contenteditable element does when the user presses
    // a key. This is what triggers the inputrules plugin to test our regex.
    act(() => {
      // ProseMirror's handleTextInput signature is (view, from, to, text, deflt).
      // `deflt` is the fallback that inserts the typed character if no rule
      // claims it; we pass a no-op since our test asserts the rule fires.
      const handled = view.someProp('handleTextInput', (fn) =>
        fn(view, view.state.selection.from, view.state.selection.to, '>', () => view.state.tr),
      )
      if (!handled) {
        view.dispatch(view.state.tr.insertText('>'))
      }
    })

    // Post: the editor must contain exactly ONE xliffTag chip with id="1".
    const chips: Array<{ tagType: unknown; tagId: unknown }> = []
    editor.state.doc.descendants((n) => {
      if (n.type.name === 'xliffTag') {
        chips.push({ tagType: n.attrs.tagType, tagId: n.attrs.tagId })
      }
    })
    expect(chips, 'typed `<ph id="1"/>` must produce exactly 1 chip').toHaveLength(1)
    expect(chips[0]).toEqual({ tagType: 'ph', tagId: '1' })

    // The leftover plain text MUST NOT contain the original markup characters
    // (`<`, `id=`, `/>`). A loose `<` means the rule replaced only a
    // fragment of the match (as `nodeInputRule` does; hence the hand-rolled
    // `InputRule`).
    const remainingText = editor.getText()
    expect(remainingText, 'no leftover `<` after the chip swap').not.toContain('<')
    expect(remainingText, 'no leftover `id=` after the chip swap').not.toContain('id=')
    expect(remainingText, 'no leftover `/>` after the chip swap').not.toContain('/>')
  })

  // Paired bpt/ept: the seed data and most TMX imports use the
  // XLIFF 1.2 paired form `<bpt id="X">…</bpt>…<ept id="X">…</ept>` for
  // formatting markers (bold/italic from the source's underlying HTML).
  // When a user removes those chips, approves, and types them back, our
  // self-closing InputRule (`<(ph|x).../>`) DOESN'T match — bpt/ept
  // are paired, not self-closing, so they need their own rules; without them
  // a typed `<bpt id="1"><b></bpt>` stays plain characters.
  //
  // Scope: bpt and ept (the canonical paired XLIFF inline elements). Paired
  // ph/it forms (`<ph id="X">{key}</ph>`) have no typed-input rule; the
  // common form of ph is self-closing.
  it('real-time: typing closing `</bpt>` converts the full pair to a chip atom', async () => {
    const ref = trackedRef()
    render(<RichTextCellEditor {...defaultProps} initialText="" ref={ref} />)
    await waitFor(() => expect(ref.current).not.toBeNull())

    const editor = ref.current!.getEditor()!
    const view = editor.view

    // Pre-stage everything except the FINAL `>` of the closing `</bpt>`.
    act(() => {
      editor.commands.insertContent('<bpt id="1"><b></bpt')
    })
    let preChips = 0
    editor.state.doc.descendants((n) => { if (n.type.name === 'xliffTag') preChips++ })
    expect(preChips, 'precondition: no chip before the closing `>`').toBe(0)

    // Drive ProseMirror's handleTextInput for the trailing `>` — same hook
    // the contenteditable element invokes on real keystrokes.
    act(() => {
      const handled = view.someProp('handleTextInput', (fn) =>
        fn(view, view.state.selection.from, view.state.selection.to, '>', () => view.state.tr),
      )
      if (!handled) view.dispatch(view.state.tr.insertText('>'))
    })

    const chips: Array<{ tagType: unknown; tagId: unknown }> = []
    editor.state.doc.descendants((n) => {
      if (n.type.name === 'xliffTag') {
        chips.push({ tagType: n.attrs.tagType, tagId: n.attrs.tagId })
      }
    })
    expect(chips, 'typed `<bpt id="1"><b></bpt>` must produce exactly 1 chip').toHaveLength(1)
    expect(chips[0]).toEqual({ tagType: 'bpt', tagId: '1' })

    // No leftover characters from the original markup.
    const remaining = editor.getText()
    expect(remaining, 'no leftover `<` after the chip swap').not.toContain('<')
    expect(remaining, 'no leftover `bpt` after the chip swap').not.toContain('bpt')
  })

  it('real-time: typing closing `</ept>` converts the full pair to a chip atom', async () => {
    const ref = trackedRef()
    render(<RichTextCellEditor {...defaultProps} initialText="" ref={ref} />)
    await waitFor(() => expect(ref.current).not.toBeNull())

    const editor = ref.current!.getEditor()!
    const view = editor.view

    act(() => {
      editor.commands.insertContent('<ept id="1"></b></ept')
    })
    act(() => {
      const handled = view.someProp('handleTextInput', (fn) =>
        fn(view, view.state.selection.from, view.state.selection.to, '>', () => view.state.tr),
      )
      if (!handled) view.dispatch(view.state.tr.insertText('>'))
    })

    const chips: Array<{ tagType: unknown; tagId: unknown }> = []
    editor.state.doc.descendants((n) => {
      if (n.type.name === 'xliffTag') chips.push({ tagType: n.attrs.tagType, tagId: n.attrs.tagId })
    })
    expect(chips, 'typed `<ept id="1"></b></ept>` must produce exactly 1 chip').toHaveLength(1)
    expect(chips[0]).toEqual({ tagType: 'ept', tagId: '1' })

    const remaining = editor.getText()
    expect(remaining, 'no leftover `<` after the chip swap').not.toContain('<')
    expect(remaining, 'no leftover `ept` after the chip swap').not.toContain('ept')
  })

  // When the user TYPES a tag pattern (e.g. they see `<ph id="1"/>`
  // in the source pane and add the same in target), TipTap inserts it as
  // plain text — there's no xliffTag node. Pins that getRichText still
  // returns the rich form when the plain text holds valid XLIFF inline
  // markup, so typed tags round-trip to chips on reload.
  it('returns rich form when user TYPED tag patterns (no chips, but plain text has them)', async () => {
    const ref = trackedRef()
    render(<RichTextCellEditor {...defaultProps} initialText="" ref={ref} />)
    await waitFor(() => expect(ref.current).not.toBeNull())

    // Simulate "user typed a tag" by inserting via TipTap commands AT THE
    // PLAIN-TEXT LEVEL (not the chip-conversion path). insertContent of a
    // string treats it as text/HTML; `<ph>` is unknown HTML so it lands
    // as text content (or stripped — either way, no xliffTag node).
    const editor = ref.current!.getEditor()!
    act(() => {
      editor.commands.insertContent('Press <ph id="1"/> to submit')
    })

    // Verify the precondition: no xliffTag nodes exist.
    let hasChip = false
    editor.state.doc.descendants((n) => {
      if (n.type.name === 'xliffTag') hasChip = true
      return !hasChip
    })
    expect(hasChip).toBe(false)

    // Plain text DOES contain the tag pattern.
    expect(editor.getText()).toContain('<ph id="1"/>')

    // getRichText must NOT return null — it should
    // surface the typed tag pattern so the backend stores it in
    // `source_rich_text` / `target_rich_text` and the chip renders next time.
    const rich = ref.current!.getRichText()
    expect(rich, 'typed tag pattern must be detected and emitted as rich text, got null').not.toBeNull()
    expect(rich).toContain('<ph')
    expect(rich).toContain('id="1"')
  })

  it('getRichText returns XLIFF when the doc contains tag chips', async () => {
    const ref = trackedRef()
    render(
      <RichTextCellEditor
        {...defaultProps}
        initialText='Press <ph id="1">{key}</ph> to submit'
        ref={ref}
      />,
    )
    await waitFor(() => expect(ref.current).not.toBeNull())

    const richText = ref.current!.getRichText()
    expect(richText).not.toBeNull()
    expect(richText).toContain('<ph')
    expect(richText).toContain('id="1"')
  })

  it('renders xliffTag chips in the DOM for tag-bearing input', async () => {
    const ref = trackedRef()
    const { container } = render(
      <RichTextCellEditor
        {...defaultProps}
        initialText='Press <ph id="1">{key}</ph> to submit'
        dataTestId="rt-cell"
        ref={ref}
      />,
    )
    await waitFor(() => expect(ref.current).not.toBeNull())

    const chips = container.querySelectorAll('span.xliff-tag')
    expect(chips.length).toBe(1)
    expect(chips[0].getAttribute('data-tag-id')).toBe('1')
  })

  // The read-only cell and the open editor render chips through
  // two different paths: the cell tokenizes the rich text and labels the token
  // directly, the editor pushes it through the ProseMirror schema first. Only
  // the schema can silently drop an attribute the tokenizer produced — an
  // undeclared attr just vanishes — so "the source column shows {Feucht} but
  // activating the row shows {2}" is invisible to every test of the label
  // function itself.
  it('resolves a cross-reference chip to its term when the segment is activated', async () => {
    const refKey = '/tms/tmsnode[14]/metalist/meta'
    const ref = trackedRef()
    const { container } = render(
      <RichTextCellEditor
        {...defaultProps}
        initialText={`Set to <x id="2" ctype="x-xml-meta-ref" ref_key="${refKey}"/>.`}
        resolveRef={k => (k === refKey ? 'Feucht' : undefined)}
        ref={ref}
      />,
    )
    await waitFor(() => expect(ref.current).not.toBeNull())

    const chip = container.querySelector('span.xliff-tag')
    expect(chip?.textContent).toBe('{Feucht}')
  })

  it('falls back to the chip number when the reference cannot be resolved', async () => {
    const ref = trackedRef()
    const { container } = render(
      <RichTextCellEditor
        {...defaultProps}
        initialText={'Set to <x id="2" ctype="x-xml-meta-ref" ref_key="/no/such/segment"/>.'}
        resolveRef={() => undefined}
        ref={ref}
      />,
    )
    await waitFor(() => expect(ref.current).not.toBeNull())

    expect(container.querySelector('span.xliff-tag')?.textContent).toBe('{2}')
  })

  it('renders as not editable when readOnly is true', async () => {
    const ref = trackedRef()
    render(<RichTextCellEditor {...defaultProps} readOnly ref={ref} />)
    await waitFor(() => expect(ref.current).not.toBeNull())

    expect(ref.current!.getEditor()?.isEditable).toBe(false)
  })

  it('passes the className through to the outer wrapper', () => {
    const ref = trackedRef()
    const { container } = render(
      <RichTextCellEditor {...defaultProps} className="my-tm-cell" ref={ref} />,
    )
    const div = container.querySelector('div[dir="auto"]')
    expect(div).toHaveClass('my-tm-cell')
    // Confirm RichTextCellEditor doesn't inject its own base class
    expect(div?.className).toBe('my-tm-cell')
  })

  it('does not render the placeholder extension when placeholder is undefined', async () => {
    const ref = trackedRef()
    render(<RichTextCellEditor {...defaultProps} initialText="" ref={ref} />)
    await waitFor(() => expect(ref.current).not.toBeNull())

    const editor = ref.current!.getEditor()!
    expect(editor.extensionManager.extensions.find(e => e.name === 'placeholder')).toBeUndefined()
  })

  it('registers the placeholder extension when placeholder is provided', async () => {
    const ref = trackedRef()
    render(
      <RichTextCellEditor
        {...defaultProps}
        initialText=""
        placeholder="Type here…"
        ref={ref}
      />,
    )
    await waitFor(() => expect(ref.current).not.toBeNull())

    const editor = ref.current!.getEditor()!
    expect(editor.extensionManager.extensions.find(e => e.name === 'placeholder')).toBeDefined()
  })

  it('passes extra extensions to the underlying editor', async () => {
    // addStorage runs during extension setup — proves TipTap actually loaded
    // the extension and ran its lifecycle hooks, not just stored its name.
    const TestExt = Extension.create({
      name: 'testMarkerExt',
      addStorage() {
        return { marker: 'present' }
      },
    })

    const ref = trackedRef()
    render(<RichTextCellEditor {...defaultProps} extraExtensions={[TestExt]} ref={ref} />)
    await waitFor(() => expect(ref.current).not.toBeNull())

    const editor = ref.current!.getEditor()!
    expect(editor.extensionManager.extensions.find(e => e.name === 'testMarkerExt')).toBeDefined()
    const storage = editor.storage as unknown as Record<string, { marker?: string } | undefined>
    expect(storage.testMarkerExt?.marker).toBe('present')
  })

  it('calls onBlur when the editor DOM emits a blur event', async () => {
    const onBlur = vi.fn()
    const ref = trackedRef()
    render(<RichTextCellEditor {...defaultProps} onBlur={onBlur} ref={ref} />)
    await waitFor(() => expect(ref.current).not.toBeNull())

    const editor = ref.current!.getEditor()!
    act(() => {
      editor.view.dom.dispatchEvent(new FocusEvent('blur'))
    })

    expect(onBlur).toHaveBeenCalledTimes(1)
  })

  it('focuses at the end of the text on mount', async () => {
    const ref = trackedRef()
    render(<RichTextCellEditor {...defaultProps} initialText="first approval" ref={ref} />)
    const editor = ref.current!.getEditor()!

    await waitFor(() => expect(editor.view.hasFocus()).toBe(true))
    expect(editor.state.selection.from).toBe(15)
    expect(editor.state.selection.to).toBe(15)
  })

  // On a busy machine the editor can show its text well before the mount
  // focus runs. A selection made in that gap is the user's, and typing over it
  // replaces it.
  it('keeps a selection made before the mount focus runs, so typing replaces it', async () => {
    const ref = trackedRef()
    render(<RichTextCellEditor {...defaultProps} initialText="first approval" ref={ref} />)
    const editor = ref.current!.getEditor()!

    act(() => {
      editor.view.dom.focus()
      editor.commands.selectAll()
    })
    await act(() => new Promise(r => setTimeout(r, 20)))

    act(() => { editor.commands.insertContent('second edit') })
    expect(ref.current!.getText()).toBe('second edit')
  })

  it('reloads content when initialText changes externally', async () => {
    const ref = trackedRef()
    const { rerender } = render(
      <RichTextCellEditor {...defaultProps} initialText="first" ref={ref} />,
    )
    await waitFor(() => expect(ref.current?.getText()).toBe('first'))

    rerender(<RichTextCellEditor {...defaultProps} initialText="second" ref={ref} />)

    await waitFor(() => expect(ref.current!.getText()).toBe('second'))
  })

  // The inline fix menu is opened by the SpellCheck extension calling its
  // `onFixRequest` option (a right-click on an underline in the real editor).
  // The extension is mocked here, but `configure` still stores the option, so
  // invoking it drives the exact code path the plugin does.
  describe('inline spell fix menu', () => {
    function openMenu(ref: RefObject<RichTextCellEditorHandle | null>, overrides: Partial<SpellFixRequest> = {}) {
      const editor = ref.current!.getEditor()!
      const spell = editor.extensionManager.extensions.find(e => e.name === 'spellCheck')!
      const { onFixRequest } = spell.options as { onFixRequest: (r: SpellFixRequest) => void }
      act(() => {
        onFixRequest({
          message: 'Possible spelling mistake found.',
          suggestions: ['Adresse'],
          // "die adresse prüfen": 'adresse' at plain offset 4 → PM positions
          // 5..12 (single tag-free paragraph, text starts at position 1).
          from: 5,
          to: 12,
          word: 'adresse',
          rect: { top: 100, left: 100, bottom: 116, right: 160 },
          ...overrides,
        })
      })
    }

    it('prefills "Change to" with the top suggestion and applies a typed custom correction', async () => {
      const ref = trackedRef()
      render(<RichTextCellEditor {...defaultProps} initialText="die adresse prüfen" ref={ref} />)
      await waitFor(() => expect(ref.current?.getText()).toBe('die adresse prüfen'))

      openMenu(ref)
      const input = screen.getByTestId('spellcheck-replacement') as HTMLInputElement
      expect(input.value).toBe('Adresse')

      // An empty correction must not be appliable.
      fireEvent.change(input, { target: { value: '  ' } })
      expect(screen.getByTestId('spellfix-segment')).toBeDisabled()

      // A typed correction wins over the suggestions.
      fireEvent.change(input, { target: { value: 'Anschrift' } })
      fireEvent.click(screen.getByTestId('spellfix-segment'))

      await waitFor(() => expect(ref.current!.getText()).toBe('die Anschrift prüfen'))
      expect(screen.queryByTestId('spellcheck-fix-menu')).toBeNull()
    })

    it('typing inside the menu keeps it open; typing in the editor or Escape dismisses it', async () => {
      const ref = trackedRef()
      render(<RichTextCellEditor {...defaultProps} initialText="die adresse prüfen" ref={ref} />)
      await waitFor(() => expect(ref.current).not.toBeNull())

      openMenu(ref)
      const input = screen.getByTestId('spellcheck-replacement')

      // Keystrokes inside the menu are the user typing a correction.
      fireEvent.keyDown(input, { key: 's' })
      expect(screen.getByTestId('spellcheck-fix-menu')).toBeInTheDocument()

      // Escape always dismisses, even from inside the menu.
      fireEvent.keyDown(input, { key: 'Escape' })
      expect(screen.queryByTestId('spellcheck-fix-menu')).toBeNull()

      // A keystroke back in the editor means the menu is stale — it must go.
      openMenu(ref)
      expect(screen.getByTestId('spellcheck-fix-menu')).toBeInTheDocument()
      fireEvent.keyDown(ref.current!.getEditor()!.view.dom, { key: 's' })
      expect(screen.queryByTestId('spellcheck-fix-menu')).toBeNull()
    })

    // "In non-locked segments" leaves locked rows alone; "In all segments"
    // changes them too. Each button shows the count it writes.
    it.each([
      ['spellfix-nonlocked', 'nonlocked', 'In non-locked segments (3)'],
      ['spellfix-all', 'all', 'In all segments (4)'],
    ])('%s writes the %s scope', async (testId, scope, label) => {
      const applyScope = vi.fn().mockResolvedValue(1)
      const spellFixActions = {
        canWriteLocked: true,
        counts: () => ({ all: 4, nonlocked: 3, locked: 1 }),
        applyScope,
        ignoreInProject: vi.fn(),
      }
      const ref = trackedRef()
      render(<RichTextCellEditor {...defaultProps} initialText="die adresse prüfen"
        spellFixActions={spellFixActions} ref={ref} />)
      await waitFor(() => expect(ref.current?.getText()).toBe('die adresse prüfen'))

      openMenu(ref)
      const button = screen.getByTestId(testId)
      expect(button).toHaveTextContent(label)
      expect(screen.getByText('“In all segments” also changes the 1 locked segment.')).toBeInTheDocument()
      fireEvent.click(button)
      await waitFor(() => expect(applyScope).toHaveBeenCalledWith('adresse', 'Adresse', scope))
    })

    // Writing through a lock is for owners and admins, the roles that may
    // unlock. For anyone else "In all segments" stays in place, disabled with
    // the reason, and the locked note names the role instead of the button.
    it('offers "In all segments" only to owners and admins', async () => {
      const applyScope = vi.fn().mockResolvedValue(1)
      const spellFixActions = {
        canWriteLocked: false,
        counts: () => ({ all: 4, nonlocked: 3, locked: 1 }),
        applyScope,
        ignoreInProject: vi.fn(),
      }
      const ref = trackedRef()
      render(<RichTextCellEditor {...defaultProps} initialText="die adresse prüfen"
        spellFixActions={spellFixActions} ref={ref} />)
      await waitFor(() => expect(ref.current?.getText()).toBe('die adresse prüfen'))

      openMenu(ref)
      const all = screen.getByTestId('spellfix-all')
      expect(all).toHaveAttribute('aria-disabled', 'true')
      expect(screen.getByTestId('spellfix-nonlocked')).not.toHaveAttribute('aria-disabled')
      // The disabled button's tooltip opens under the menu, so the menu's
      // locked note spells the reason out.
      const menu = screen.getByRole('dialog', { name: 'Spell check' })
      expect(within(menu).getByText('Only an owner or admin can change the 1 locked segment.')).toBeVisible()
      fireEvent.click(all)
      expect(applyScope).not.toHaveBeenCalled()
    })

    it('names the role even when no segment is locked, since the button stays disabled', async () => {
      const spellFixActions = {
        canWriteLocked: false,
        counts: () => ({ all: 4, nonlocked: 4, locked: 0 }),
        applyScope: vi.fn().mockResolvedValue(1),
        ignoreInProject: vi.fn(),
      }
      const ref = trackedRef()
      render(<RichTextCellEditor {...defaultProps} initialText="die adresse prüfen"
        spellFixActions={spellFixActions} ref={ref} />)
      await waitFor(() => expect(ref.current?.getText()).toBe('die adresse prüfen'))

      openMenu(ref)
      expect(screen.getByTestId('spellfix-all')).toHaveAttribute('aria-disabled', 'true')
      const menu = screen.getByRole('dialog', { name: 'Spell check' })
      expect(within(menu).getByText('Only an owner or admin can change locked segments.')).toBeVisible()
    })
  })
})

describe('textToContent', () => {
  // Plain text returns TipTap JSON (doc/paragraph/text+hardBreak),
  // not an HTML string, so whitespace runs + tabs survive the
  // ProseMirror parser instead of being collapsed by HTML semantics.
  it('returns TipTap JSON for plain text with a single text node', () => {
    const result = textToContent('a < b & c') as JSONContent
    expect(result.type).toBe('doc')
    expect(result.content![0].type).toBe('paragraph')
    expect(result.content![0].content).toEqual([{ type: 'text', text: 'a < b & c' }])
  })

  it('preserves HTML formatting tags (HTML-string path unchanged)', () => {
    expect(textToContent('<strong>bold</strong>')).toBe('<p><strong>bold</strong></p>')
  })

  it('does not double-wrap content already inside <p>', () => {
    expect(textToContent('<p>already wrapped</p>')).toBe('<p>already wrapped</p>')
  })

  it('returns TipTap JSON for XLIFF rich text', () => {
    const result = textToContent('Press <ph id="1">{key}</ph> to submit')
    expect(typeof result).toBe('object')
    expect((result as { type: string }).type).toBe('doc')
  })

  it('converts newlines to hardBreak nodes', () => {
    const result = textToContent('line1\nline2') as JSONContent
    expect(result.content![0].content).toEqual([
      { type: 'text', text: 'line1' },
      { type: 'hardBreak' },
      { type: 'text', text: 'line2' },
    ])
  })

  // Tabs MUST be preserved as literal '\t' characters in the text
  // node, so they survive every editor reactivation.
  it('preserves tab characters literally', () => {
    const result = textToContent('a\tb') as JSONContent
    expect(result.content![0].content).toEqual([{ type: 'text', text: 'a\tb' }])
  })

  it('preserves runs of whitespace literally', () => {
    const result = textToContent('x   y') as JSONContent
    expect(result.content![0].content).toEqual([{ type: 'text', text: 'x   y' }])
  })

  it('produces an empty paragraph for empty input', () => {
    const result = textToContent('') as JSONContent
    // Note: callers pass '' through the `text ? textToContent(text) : ''` guard
    // upstream, so this is only the "called directly with ''" shape. Document
    // it explicitly so a future refactor can't silently regress.
    expect(result.type).toBe('doc')
    expect(result.content![0].type).toBe('paragraph')
    expect(result.content![0].content).toBeUndefined()
  })
})
