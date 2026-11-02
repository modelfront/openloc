import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { useState } from 'react'
import { toast } from 'sonner'

vi.mock('../../api', () => ({ uploadChatFile: vi.fn() }))

import { uploadChatFile, type ChatUpload } from '../../api'
import { CHAT_UPLOAD_TIMEOUT_MS, ChatAttachButton, MAX_CHAT_ATTACHMENTS, useChatUploads } from './ChatAttachments'

const uploadMock = vi.mocked(uploadChatFile)

const uploaded = (file: File): ChatUpload => ({ id: `id-${file.name}`, filename: file.name, size_bytes: file.size, expires_at: '' })
const files = (n: number, prefix = 'f') => Array.from({ length: n }, (_, i) => new File(['x'], `${prefix}${i + 1}.txt`))

// The button with the hook, as the panel wires them: the list shows what
// was added.
function Harness({ already = 0 }: { already?: number }) {
  const [added, setAdded] = useState<ChatUpload[]>([])
  const uploads = useChatUploads(already + added.length, u => setAdded(prev => [...prev, u]))
  return (
    <>
      <ChatAttachButton onPick={f => void uploads.add(f)} progress={uploads.progress} full={already + added.length >= MAX_CHAT_ATTACHMENTS} />
      <ul data-testid="added">{added.map(a => <li key={a.id}>{a.filename}</li>)}</ul>
    </>
  )
}

const pick = (picked: File[]) => fireEvent.change(screen.getByTestId('chat-attach-input'), { target: { files: picked } })
const addedNames = () => Array.from(screen.getByTestId('added').querySelectorAll('li')).map(li => li.textContent)

describe('ChatAttachButton + useChatUploads', () => {
  beforeEach(() => {
    uploadMock.mockReset()
    uploadMock.mockImplementation(async f => uploaded(f))
  })
  afterEach(() => vi.restoreAllMocks())

  it('the picker takes several files and uploads each one, one at a time', async () => {
    let inFlight = 0
    let most = 0
    uploadMock.mockImplementation(async f => {
      inFlight++
      most = Math.max(most, inFlight)
      await new Promise(r => setTimeout(r, 1))
      inFlight--
      return uploaded(f)
    })
    render(<Harness />)
    expect(screen.getByTestId('chat-attach-input')).toHaveAttribute('multiple')

    pick(files(3))
    await waitFor(() => expect(addedNames()).toEqual(['f1.txt', 'f2.txt', 'f3.txt']))
    expect(most).toBe(1)
  })

  it('shows the progress on the button while files upload', async () => {
    const pending: Array<() => void> = []
    uploadMock.mockImplementation(f => new Promise(res => pending.push(() => res(uploaded(f)))))
    render(<Harness />)
    pick(files(2))
    await waitFor(() => expect(screen.getByTestId('chat-attach')).toHaveAttribute('title', 'Uploading 1 of 2…'))
    await act(async () => pending.shift()!())
    await waitFor(() => expect(screen.getByTestId('chat-attach')).toHaveAttribute('title', 'Uploading 2 of 2…'))
    await act(async () => pending.shift()!())
    await waitFor(() => expect(screen.getByTestId('chat-attach')).toHaveAttribute('title', 'Attach a file (TMX, terms, a document to translate)'))
  })

  it('leaves out files past the limit with a toast, counting the ones already attached', async () => {
    const toastError = vi.spyOn(toast, 'error')
    render(<Harness already={8} />)
    pick(files(3))
    await waitFor(() => expect(addedNames()).toEqual(['f1.txt', 'f2.txt']))
    expect(toastError).toHaveBeenCalledWith('Up to 10 files per message')
    expect(uploadMock).toHaveBeenCalledTimes(2)
    expect(screen.getByTestId('chat-attach')).toBeDisabled()
    expect(screen.getByTestId('chat-attach')).toHaveAttribute('title', 'Up to 10 files per message')
  })

  it('a failed upload names its file and the others are still added', async () => {
    const toastError = vi.spyOn(toast, 'error')
    uploadMock.mockImplementation(async f => {
      if (f.name === 'f2.txt') throw new Error('The file is too large.')
      return uploaded(f)
    })
    render(<Harness />)
    pick(files(3))
    await waitFor(() => expect(addedNames()).toEqual(['f1.txt', 'f3.txt']))
    expect(toastError).toHaveBeenCalledWith('f2.txt: The file is too large.')
  })

  it('gives up on an upload that does not finish, so the panel is not stuck', async () => {
    vi.useFakeTimers()
    try {
      const toastError = vi.spyOn(toast, 'error')
      uploadMock.mockImplementation((_f, signal) => new Promise((_res, rej) => {
        signal?.addEventListener('abort', () => rej(new DOMException('The operation was aborted.', 'AbortError')))
      }))
      render(<Harness />)
      pick(files(1, 'slow'))
      expect(screen.getByTestId('chat-attach')).toHaveAttribute('title', 'Uploading 1 of 1…')
      await act(async () => { await vi.advanceTimersByTimeAsync(CHAT_UPLOAD_TIMEOUT_MS) })
      expect(toastError).toHaveBeenCalledWith('slow1.txt: The upload took too long. Try again.')
      expect(screen.getByTestId('chat-attach')).toHaveAttribute('title', 'Attach a file (TMX, terms, a document to translate)')
    } finally {
      vi.useRealTimers()
    }
  })

  it('refuses a second batch while one is uploading', async () => {
    const toastError = vi.spyOn(toast, 'error')
    const pending: Array<() => void> = []
    uploadMock.mockImplementation(f => new Promise(res => pending.push(() => res(uploaded(f)))))
    render(<Harness />)
    pick(files(1, 'a'))
    await waitFor(() => expect(uploadMock).toHaveBeenCalledTimes(1))
    pick(files(1, 'b'))
    expect(toastError).toHaveBeenCalledWith('Wait until the files being uploaded are done')
    await act(async () => pending.shift()!())
    await waitFor(() => expect(addedNames()).toEqual(['a1.txt']))
    expect(uploadMock).toHaveBeenCalledTimes(1)
  })
})
