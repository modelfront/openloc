import { Extension } from '@tiptap/core'
import type * as Real from '../tiptap-spellcheck'

// Stand-in for a factory-less `vi.mock('../lib/tiptap-spellcheck')`: the real
// extension needs browser APIs jsdom lacks. Every named export the components
// import is here, or the mock shadows it with undefined and the mount effect
// throws; the functions are no-ops typed against the real signatures.
export type { SpellFixRequest } from '../tiptap-spellcheck'
export const SpellCheck = Extension.create({ name: 'spellCheck' })
export const toggleSpellCheck: typeof Real.toggleSpellCheck = () => {}
export const clearSpellWord: typeof Real.clearSpellWord = () => {}
