import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ParamsDialog, isNumberText } from './ParamsDialog'

describe('ParamsDialog', () => {
  it('pre-fills the last values and submits with Enter', () => {
    const onSubmit = vi.fn()
    render(
      <ParamsDialog names={['id', 'since']} initial={{ id: { value: '42', null: false, type: 'number' }, other: { value: 'x', null: false, type: 'text' } }} onSubmit={onSubmit} onCancel={vi.fn()} />,
    )
    const dlg = screen.getByRole('dialog', { name: 'Query parameters' })
    const id = screen.getByRole('textbox', { name: ':id' })
    expect(id).toHaveValue('42')
    expect(id).toHaveFocus()
    expect(screen.getByRole('combobox', { name: 'Type of :id' })).toHaveValue('number')
    expect(screen.getByRole('textbox', { name: ':since' })).toHaveValue('')
    fireEvent.change(screen.getByRole('textbox', { name: ':since' }), { target: { value: '2024-01-01' } })
    fireEvent.submit(dlg)
    expect(onSubmit).toHaveBeenCalledWith({
      id: { value: '42', null: false, type: 'number' },
      since: { value: '2024-01-01', null: false, type: 'text' },
    })
    expect(screen.getByRole('button', { name: 'Run' })).toHaveAttribute('type', 'submit')
  })

  it('binds NULL and checks numbers', () => {
    const onSubmit = vi.fn()
    render(<ParamsDialog names={['n', 'x']} initial={{}} explain onSubmit={onSubmit} onCancel={vi.fn()} />)
    fireEvent.change(screen.getByRole('combobox', { name: 'Type of :n' }), { target: { value: 'number' } })
    fireEvent.change(screen.getByRole('textbox', { name: ':n' }), { target: { value: 'ten' } })
    fireEvent.click(screen.getByRole('button', { name: 'Explain' }))
    expect(onSubmit).not.toHaveBeenCalled()
    expect(screen.getByRole('textbox', { name: ':n' })).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByText('Not a number')).toBeInTheDocument()

    fireEvent.change(screen.getByRole('textbox', { name: ':n' }), { target: { value: '10' } })
    fireEvent.click(screen.getByRole('checkbox', { name: ':x is NULL' }))
    expect(screen.getByRole('textbox', { name: ':x' })).toBeDisabled()
    expect(screen.getByRole('textbox', { name: ':x' })).toHaveAttribute('placeholder', 'NULL')
    expect(screen.queryByText('Not a number')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Explain' }))
    expect(onSubmit).toHaveBeenCalledWith({ n: { value: '10', null: false, type: 'number' }, x: { value: '', null: true, type: 'text' } })
  })

  it('cancels with Escape or the Cancel button', () => {
    const onCancel = vi.fn()
    render(<ParamsDialog names={['a']} initial={{}} onSubmit={vi.fn()} onCancel={onCancel} />)
    fireEvent.keyDown(screen.getByRole('textbox', { name: ':a' }), { key: 'Escape' })
    fireEvent.keyDown(screen.getByRole('textbox', { name: ':a' }), { key: 'a' })
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onCancel).toHaveBeenCalledTimes(2)
  })

  it('recognises numbers like Go does', () => {
    for (const ok of ['1', ' -2 ', '+3.5', '.5', '1e3', '2.', '1E-2']) expect(isNumberText(ok)).toBe(true)
    for (const bad of ['', 'x', '1,5', '1e', '--1', '0x10']) expect(isNumberText(bad)).toBe(false)
  })
})
