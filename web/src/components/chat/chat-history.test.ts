import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, it, expect } from 'vitest'
import type { ChatMessage } from '../../api'
import { toDisplayMessages } from './chat-history'

const row = (role: ChatMessage['role'], content: string): ChatMessage =>
  ({ id: 1, conversation_id: 1, role, content, tool_calls: null, created_at: '' })

// The server's stored shape, pinned on its side by TestScreenshotMessageShape.
const shape: { sent: string; stored: string } = JSON.parse(
  readFileSync(join(__dirname, '../../../../internal/chat/testdata/screenshot_message.json'), 'utf8'),
)

describe('toDisplayMessages', () => {
  it("shows a screenshot message as the user sent it, without the model's note", () => {
    expect(toDisplayMessages([row('user', shape.stored)])[0].content).toBe(shape.sent)
  })

  it('leaves other text alone', () => {
    const text = 'See [Screenshot from the user (upload_id: x). quoted] in the middle.'
    expect(toDisplayMessages([row('user', text), row('assistant', shape.stored)]).map(m => m.content)).toEqual([text, shape.stored])
  })
})
