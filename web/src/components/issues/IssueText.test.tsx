import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { IssueAttachment } from '@/api'
import { IssueText, splitIssueText } from './IssueText'

vi.mock('@/api', async (orig) => ({
  ...(await orig<typeof import('@/api')>()),
  fetchIssueAttachment: vi.fn(() => new Promise(() => {})),
}))

const image: IssueAttachment = { id: 7, filename: 'shot.png', size_bytes: 10, is_image: true }
const log: IssueAttachment = { id: 8, filename: 'server.log', size_bytes: 10, is_image: false }

describe('splitIssueText', () => {
  it('cuts the text at the references to its own images, dropping the line breaks around them', () => {
    expect(splitIssueText('Before\n![shot.png](attachment:7)\nafter', [image, log]))
      .toEqual([{ text: 'Before' }, { image }, { text: 'after' }])
  })

  it('leaves as text a reference to another file, a non-image or markup', () => {
    const text = '![x](attachment:99) ![log](attachment:8) <img src=x onerror=alert(1)>'
    expect(splitIssueText(text, [image, log])).toEqual([{ text }])
  })
})

describe('IssueText', () => {
  it('shows the placed image in the text and lists only the files it does not place', () => {
    render(<IssueText text={'See ![shot.png](attachment:7) here'} files={[image, log]} scope="mine" issueId={1} testId="t" />)
    expect(screen.getByTestId('t')).toHaveTextContent('See here')
    expect(screen.getByTestId('t').querySelector('.animate-pulse')).not.toBeNull()
    expect(screen.getAllByTestId('issue-file')).toHaveLength(1)
    expect(screen.queryByText('shot.png')).toBeNull()
  })

  it('renders markup in the text as text', () => {
    render(<IssueText text="<b>bold</b>" files={[]} scope="platform" issueId={1} testId="t" />)
    expect(screen.getByTestId('t').textContent).toBe('<b>bold</b>')
    expect(screen.getByTestId('t').querySelector('b')).toBeNull()
  })
})
