// A task state with its count — "In progress 3" — in that state's colour, for
// the matrix legend. A toggle: on, the matrix shows only that state.

import { TASK_STATE_DISPLAY, type TaskState } from '@/lib/task-matrix'
import { ChipButton } from '@/components/ui/chip'

interface Props {
  state: TaskState
  count: number
  /** The Status filter is set to this state. */
  pressed: boolean
  onClick: () => void
  'data-testid'?: string
}

export function TaskStateChip({ state, count, pressed, onClick, 'data-testid': testId }: Props) {
  const { label, color } = TASK_STATE_DISPLAY[state]
  return (
    <ChipButton
      color={color}
      pressed={pressed}
      onClick={onClick}
      title={pressed ? 'Show every state' : `Show only ${label.toLowerCase()} tasks`}
      data-testid={testId}
    >
      {label} {count.toLocaleString()}
    </ChipButton>
  )
}
