import { describe, expect, it } from 'vitest'
import { dropUploadRef, insertUploadRefs, uploadRef } from './attachments'

describe('inline image references', () => {
  it('puts the references at the cursor, each on its own line', () => {
    expect(insertUploadRefs('Click Export then it fails', 13, [{ name: 'a.png', index: 0 }, { name: 'b.png', index: 1 }]))
      .toEqual({ text: 'Click Export \n![a.png](upload:0)\n![b.png](upload:1)\nthen it fails', cursor: 51 })
    expect(insertUploadRefs('', 0, [{ name: 'a.png', index: 2 }])).toEqual({ text: '![a.png](upload:2)', cursor: 18 })
    expect(insertUploadRefs('text', 2, [])).toEqual({ text: 'text', cursor: 2 })
  })

  it('keeps brackets and line breaks of a file name out of the reference', () => {
    expect(uploadRef('shot [1]\n.png', 3)).toBe('![shot  1  .png](upload:3)')
  })

  it("drops a removed file's references and moves later ones down", () => {
    const text = 'a ![x](upload:0) b ![y](upload:1) c ![z](upload:2) ![y](upload:1)'
    expect(dropUploadRef(text, 1)).toBe('a ![x](upload:0) b  c ![z](upload:1) ')
    expect(dropUploadRef('typed ![x](attachment:5)', 0)).toBe('typed ![x](attachment:5)')
  })
})
