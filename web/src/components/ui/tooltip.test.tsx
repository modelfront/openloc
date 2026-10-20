// A tooltip is portaled to <body>, but React events from it still bubble
// through the React tree — into whatever wraps the trigger. Selecting a long
// name in a tooltip to copy it ends in a click, and that click must not act on
// the row, cell or link the trigger sits in.

import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'

import { Tooltip, TooltipContent, TooltipTrigger } from './tooltip'

afterEach(cleanup)

describe('TooltipContent', () => {
  it('keeps a click inside the tooltip from reaching what wraps its trigger', async () => {
    const onRowClick = vi.fn()
    render(
      <div onClick={onRowClick}>
        <Tooltip open>
          <TooltipTrigger render={<span>short…</span>} />
          <TooltipContent>A_very_long_file_name.docx</TooltipContent>
        </Tooltip>
      </div>,
    )
    fireEvent.click(await screen.findByText('A_very_long_file_name.docx'))
    expect(onRowClick).not.toHaveBeenCalled()

    // The trigger itself still clicks through as before.
    fireEvent.click(screen.getByText('short…'))
    expect(onRowClick).toHaveBeenCalledTimes(1)
  })

  it('still runs an onClick passed to the tooltip content', async () => {
    const onContentClick = vi.fn()
    render(
      <Tooltip open>
        <TooltipTrigger render={<span>x</span>} />
        <TooltipContent onClick={onContentClick}>Tip</TooltipContent>
      </Tooltip>,
    )
    fireEvent.click(await screen.findByText('Tip'))
    expect(onContentClick).toHaveBeenCalledTimes(1)
  })
})
