import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ProjectLabelFields, type LabelPick } from './ProjectLabelFields'
import type { Label } from '../../api'

const LABELS: Label[] = [
  { id: 1, name: 'Country', system: false, in_use: false, values: [{ id: 10, value: 'Germany', in_use: false, project_count: 0 }, { id: 11, value: 'Japan', in_use: false, project_count: 0 }] },
  { id: 2, name: 'Industry', system: false, in_use: false, values: [{ id: 20, value: 'Legal', in_use: false, project_count: 0 }] },
]

function setup(value: LabelPick[] = [], props: Partial<React.ComponentProps<typeof ProjectLabelFields>> = {}) {
  const onChange = vi.fn()
  render(<ProjectLabelFields labels={LABELS} value={value} onChange={onChange} {...props} />)
  return { onChange }
}

describe('ProjectLabelFields', () => {
  it('renders one row per pick, named after its label', () => {
    setup([{ definition_id: 1, value_id: 10 }])
    expect(screen.getByText('Country')).toBeInTheDocument()
    expect(screen.getByTestId('project-label-value-1')).toHaveTextContent('Germany')
    // Only the picked label gets a row.
    expect(screen.queryByTestId('project-label-value-2')).not.toBeInTheDocument()
  })

  it('adds a field with no value yet when a label is picked from the dialog', async () => {
    const user = userEvent.setup()
    const { onChange } = setup([])
    await user.click(screen.getByTestId('add-project-label'))
    expect(await screen.findByTestId('project-label-picker')).toBeInTheDocument()
    await user.click(screen.getByTestId('project-label-option-1'))
    // value_id stays null: adding the label and answering it are two steps, and
    // an unanswered field is not sent.
    expect(onChange).toHaveBeenCalledWith([{ definition_id: 1, value_id: null }])
  })

  it('lists each label with how many values it offers', async () => {
    const user = userEvent.setup()
    setup([])
    await user.click(screen.getByTestId('add-project-label'))
    expect(await screen.findByTestId('project-label-option-1')).toHaveTextContent('2 values')
    // Singular, not "1 values".
    expect(screen.getByTestId('project-label-option-2')).toHaveTextContent('1 value')
  })

  it('closes the dialog without adding anything on Cancel', async () => {
    const user = userEvent.setup()
    const { onChange } = setup([])
    await user.click(screen.getByTestId('add-project-label'))
    await user.click(await screen.findByTestId('project-label-picker-cancel'))
    expect(onChange).not.toHaveBeenCalled()
  })

  it('removing a row drops it from the value', async () => {
    const user = userEvent.setup()
    const { onChange } = setup([
      { definition_id: 1, value_id: 10 },
      { definition_id: 2, value_id: 20 },
    ])
    await user.click(screen.getByTestId('project-label-remove-1'))
    expect(onChange).toHaveBeenCalledWith([{ definition_id: 2, value_id: 20 }])
  })

  it('does not offer a label that already has a field', async () => {
    const user = userEvent.setup()
    setup([{ definition_id: 1, value_id: 10 }])
    await user.click(screen.getByTestId('add-project-label'))
    expect(await screen.findByTestId('project-label-option-2')).toBeInTheDocument()
    expect(screen.queryByTestId('project-label-option-1')).not.toBeInTheDocument()
  })

  it('hides the Add label button once every label is used', () => {
    setup([
      { definition_id: 1, value_id: 10 },
      { definition_id: 2, value_id: 20 },
    ])
    expect(screen.queryByTestId('add-project-label')).not.toBeInTheDocument()
  })

  it('skips a row whose label has been retired', () => {
    // A label deleted while the form was open: the row would render nameless
    // with an empty value picker, so it is dropped instead.
    setup([{ definition_id: 99, value_id: 990 }])
    expect(screen.queryByTestId('project-label-value-99')).not.toBeInTheDocument()
  })

  it('offers no editing controls when disabled', () => {
    setup([{ definition_id: 1, value_id: 10 }], { disabled: true })
    expect(screen.queryByTestId('add-project-label')).not.toBeInTheDocument()
    expect(screen.queryByTestId('project-label-remove-1')).not.toBeInTheDocument()
  })
})
