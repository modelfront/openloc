import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

vi.mock('@/api', async importOriginal => ({
  ...(await importOriginal<typeof import('@/api')>()),
  fetchIssueAttachment: vi.fn(),
}))
vi.mock('@/api/download', () => ({ triggerDownload: vi.fn() }))

import { fetchIssueAttachment, type IssueAttachment } from '@/api'
import { triggerDownload } from '@/api/download'
import { IssueImage } from './IssueAttachments'

const file = { id: 7, filename: 'screen.png', is_image: true, size_bytes: 10 } as IssueAttachment
const blob = new Blob(['png'], { type: 'image/png' })

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchIssueAttachment).mockResolvedValue(blob)
  URL.createObjectURL = vi.fn(() => 'blob:screen')
  URL.revokeObjectURL = vi.fn()
})

describe('IssueImage', () => {
  it('opens the full image over the page, and closing it leaves the page as it was', async () => {
    render(<IssueImage scope="mine" issueId={1} file={file} />)
    const thumbnail = await screen.findByTestId('issue-image')
    expect(thumbnail.closest('a')).toBeNull()
    fireEvent.click(thumbnail)
    const viewer = await screen.findByTestId('issue-image-viewer')
    expect(viewer.querySelector('img')).toHaveAttribute('src', 'blob:screen')
    expect(viewer).toHaveTextContent('screen.png')

    fireEvent.click(screen.getByRole('button', { name: 'Download' }))
    expect(triggerDownload).toHaveBeenCalledWith(blob, 'screen.png')

    fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByTestId('issue-image-viewer')).not.toBeInTheDocument())
    expect(screen.getByTestId('issue-image')).toBeInTheDocument()
    expect(fetchIssueAttachment).toHaveBeenCalledTimes(1)
  })
})
