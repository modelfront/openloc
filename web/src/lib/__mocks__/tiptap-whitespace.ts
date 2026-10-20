import { Extension } from '@tiptap/core'
import type * as Real from '../tiptap-whitespace'

// Stand-in for a factory-less `vi.mock('../lib/tiptap-whitespace')`: the real
// extension needs browser APIs jsdom lacks. The toggle is a no-op typed against
// the real signature.
export const WhitespaceMarkers = Extension.create({ name: 'whitespaceMarkers' })
export const toggleWhitespace: typeof Real.toggleWhitespace = () => {}
