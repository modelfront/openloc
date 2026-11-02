import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, waitFor, act, cleanup, fireEvent } from '@testing-library/react'
import { createRef, useState, type RefObject } from 'react'
import type { TermLookupResult } from '../api/terminology'
import type { Placeable } from '../lib/placeables'

// Stubs from web/src/lib/__mocks__: the real extensions need browser APIs jsdom lacks.
vi.mock('../lib/tiptap-spellcheck')
vi.mock('../lib/tiptap-whitespace')

import SegmentEditor, { type SegmentEditorHandle } from '../components/SegmentEditor'
import { useSourceInserts } from './useSourceInserts'

const term = (over: Partial<TermLookupResult> = {}): TermLookupResult => ({
  id: 1, concept_id: 1, source_term: 'password', target_term: 'Passwort', mt_translation: '',
  status: 'approved', notes: '', domain: '', term_base_id: 1,
  term_base_name: 'tb', priority: 0, ...over,
})

/** An editable target wired to F8 / Alt+F8 the way SegmentRow wires it. */
function Row({ source, placeables, terms, editorRef }: {
  source: string
  placeables: Placeable[]
  terms: TermLookupResult[]
  editorRef: RefObject<SegmentEditorHandle | null>
}) {
  const [target, setTarget] = useState('')
  const { insertNext, insertAll } = useSourceInserts({
    sourceRich: null, flatSource: source, placeables, terms, missingTags: [],
    isActive: true, readOnly: false, target, editorRef,
  })
  return (
    <SegmentEditor
      ref={editorRef}
      initialText=""
      readOnly={false}
      onTextChange={setTarget}
      onConfirmAndAdvance={() => {}}
      onNavigate={() => {}}
      onDeactivate={() => {}}
      onCopySource={() => {}}
      onInsertNextFromSource={insertNext}
      onInsertAllFromSource={insertAll}
    />
  )
}

describe('useSourceInserts with terms', () => {
  const editorRef = createRef<SegmentEditorHandle>() as RefObject<SegmentEditorHandle | null>
  afterEach(() => { editorRef.current?.destroy(); cleanup() })

  async function mount(source: string, placeables: Placeable[], terms: TermLookupResult[]) {
    render(<Row source={source} placeables={placeables} terms={terms} editorRef={editorRef} />)
    await waitFor(() => expect(editorRef.current?.getEditor()).toBeTruthy())
    return editorRef.current!.getEditor()!.view.dom
  }

  it('F8 walks the placeable and the term target in source order, then stops', async () => {
    const dom = await mount('Send {name} your password', [{ kind: 'variable', text: '{name}' }], [term()])
    act(() => { fireEvent.keyDown(dom, { key: 'F8' }) })
    expect(editorRef.current!.getText()).toBe('{name}')
    act(() => { fireEvent.keyDown(dom, { key: 'F8' }) })
    expect(editorRef.current!.getText()).toBe('{name}Passwort')
    act(() => { fireEvent.keyDown(dom, { key: 'F8' }) })
    expect(editorRef.current!.getText()).toBe('{name}Passwort')
  })

  it('Alt+F8 inserts all of them, a space between text items, and no forbidden term', async () => {
    const terms = [
      term({ id: 2, concept_id: 2, source_term: 'account', target_term: 'Konto' }),
      term({ id: 3, concept_id: 2, source_term: 'account', target_term: 'Account', status: 'forbidden' }),
      term(),
    ]
    const dom = await mount('Your account password is {pw}', [{ kind: 'variable', text: '{pw}' }], terms)
    act(() => { fireEvent.keyDown(dom, { key: 'F8', altKey: true }) })
    expect(editorRef.current!.getText()).toBe('Konto Passwort {pw}')
  })
})
