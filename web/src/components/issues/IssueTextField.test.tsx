import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { IssueTextField } from './IssueTextField'

function Harness({ initial = '' }: { initial?: string }) {
  const [text, setText] = useState(initial)
  const [files, setFiles] = useState<File[]>([])
  return (
    <>
      <IssueTextField value={text} onChange={setText} files={files} onFilesChange={setFiles}
        data-testid="field" filesTestId="files" />
      <output data-testid="names">{files.map(f => f.name).join(',')}</output>
    </>
  )
}

const png = (name: string) => new File(['png'], name, { type: 'image/png' })
const field = () => screen.getByTestId('field') as HTMLTextAreaElement

describe('IssueTextField', () => {
  it('puts a pasted image where the cursor is and attaches it', async () => {
    render(<Harness initial="Click Export. Nothing." />)
    field().focus()
    field().setSelectionRange(13, 13)
    fireEvent.paste(field(), { clipboardData: { files: [png('shot.png')] } })
    expect(field().value).toBe('Click Export.\n![shot.png](upload:0)\n Nothing.')
    expect(screen.getByTestId('names')).toHaveTextContent('shot.png')
  })

  it('puts a dropped image in the text and attaches any other file without a reference', () => {
    render(<Harness />)
    const log = new File(['panic'], 'server.log', { type: 'text/plain' })
    fireEvent.drop(field(), { dataTransfer: { types: ['Files'], files: [log, png('a.png')] } })
    expect(field().value).toBe('![a.png](upload:1)')
    expect(screen.getByTestId('names')).toHaveTextContent('server.log,a.png')
  })

  it('puts a picked image where the cursor was before the field lost focus', async () => {
    render(<Harness initial="ab" />)
    field().focus()
    field().setSelectionRange(1, 1)
    field().blur()
    fireEvent.change(screen.getByTestId('files-input'), { target: { files: [png('p.png')] } })
    expect(field().value).toBe('a\n![p.png](upload:0)\nb')
  })

  it("removing a file removes its reference and renumbers the rest", async () => {
    render(<Harness />)
    fireEvent.paste(field(), { clipboardData: { files: [png('a.png'), png('b.png')] } })
    expect(field().value).toBe('![a.png](upload:0)\n![b.png](upload:1)')
    await userEvent.click(screen.getByRole('button', { name: 'Remove a.png' }))
    expect(field().value).toBe('\n![b.png](upload:0)')
    expect(screen.getByTestId('names')).toHaveTextContent('b.png')
  })
})
