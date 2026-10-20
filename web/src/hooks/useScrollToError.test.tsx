import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { Controller, useForm } from 'react-hook-form'
import { useScrollToError } from './useScrollToError'

function Form({ only }: { only?: 'lang' }) {
  const { register, control, handleSubmit, formState: { errors } } = useForm<{ name: string; lang: string }>({
    defaultValues: { name: '', lang: '' },
  })
  useScrollToError(errors)
  // Registered only when rendered: a registered field with no input would be
  // the first error and leave nothing to focus.
  const name = only === 'lang' ? null : <input key="name" aria-label="name" {...register('name', { required: true })} />
  const lang = (
    <Controller
      key="lang"
      name="lang"
      control={control}
      rules={{ required: true }}
      render={() => (
        <div data-field="lang" data-testid="lang-field">
          <button type="button">Pick a language</button>
        </div>
      )}
    />
  )
  return (
    <form onSubmit={handleSubmit(() => {})}>
      {only === 'lang' ? lang : [name, lang]}
      <button type="submit">Save</button>
    </form>
  )
}

describe('useScrollToError', () => {
  it('focuses and rings the first invalid registered input', async () => {
    render(<Form />)
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    const input = screen.getByLabelText('name')
    await waitFor(() => expect(document.activeElement).toBe(input))
    expect(input.dataset.attention).toBe('')
  })

  it('for a Controller field, rings the wrapper and focuses the control inside it', async () => {
    render(<Form only="lang" />)
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    const wrapper = screen.getByTestId('lang-field')
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Pick a language' })))
    expect(wrapper.dataset.attention).toBe('')
  })
})
