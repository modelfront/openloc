import { StrictMode } from 'react'
import { describe, it, expect, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'

vi.mock('../../lib/screenshot', () => ({
  capturePage: vi.fn(async () => new File(['jpeg'], 'screenshot.jpg', { type: 'image/jpeg' })),
}))

import { capturePage } from '../../lib/screenshot'
import { recordTrail } from '../../lib/action-trail'
import { clearHeldRecording, heldRecording } from '../../lib/watch-recording'
import { ChatRepeatRequest, RECORDING_ATTACHED } from './ChatRepeatRequest'

describe('ChatRepeatRequest', () => {
  // Development mounts a component twice; the second mount still answers.
  it('sends the fixed answer when the user presses Done, in strict mode', async () => {
    const onSend = vi.fn()
    render(<StrictMode><ChatRepeatRequest reason="Once more, please." onSend={onSend} /></StrictMode>)
    fireEvent.click(screen.getByTestId('chat-repeat-start'))
    fireEvent.click(screen.getByTestId('chat-recording-done'))
    await waitFor(() => expect(onSend).toHaveBeenCalledWith(RECORDING_ATTACHED))
    expect(screen.queryByTestId('chat-recording-banner')).toBeNull()
    expect(screen.getByTestId('chat-ask-repeat')).toHaveAttribute('data-state', 'recorded')
  })

  // The screenshot taken at the first error is held for the report; the
  // answer to the assistant carries the fixed sentence and nothing else.
  it("keeps the recording's screenshot for the ticket only", async () => {
    const onSend = vi.fn(async () => true)
    render(<ChatRepeatRequest reason="Once more, please." onSend={onSend} />)
    fireEvent.click(screen.getByTestId('chat-repeat-start'))
    recordTrail({ kind: 'console', message: 'TypeError: boom' })
    expect(capturePage).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByTestId('chat-recording-done'))
    await waitFor(() => expect(onSend).toHaveBeenCalled())
    expect(onSend.mock.calls).toEqual([[RECORDING_ATTACHED]])
    expect((await heldRecording()?.screenshot)?.name).toBe('recording-first-error.jpg')
  })

  it('a skipped recording records nothing and says so', async () => {
    const onSend = vi.fn(async () => true)
    clearHeldRecording()
    render(<ChatRepeatRequest reason="Once more, please." onSend={onSend} />)
    fireEvent.click(screen.getByTestId('chat-repeat-skip'))
    expect(onSend.mock.calls).toEqual([["I'll skip the recording."]])
    expect(screen.getByTestId('chat-ask-repeat')).toHaveAttribute('data-state', 'skipped')
    expect(screen.queryByTestId('chat-recording-banner')).toBeNull()
    expect(heldRecording()).toBeUndefined()
  })
})
