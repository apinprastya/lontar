import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { DropTableDialog, StructurePreviewDialog, statementsText } from './editDialogs'

const st = (sql: string) => ({ sql, section: 'table' as const, index: -1 })

describe('structure dialogs', () => {
  it('joins statements as a script', () => {
    expect(statementsText([st('A '), st('B')])).toBe('A;\n\nB;')
  })

  it('previews in one transaction and applies', () => {
    const onApply = vi.fn()
    const onClose = vi.fn()
    const { rerender } = render(<StructurePreviewDialog statements={[st('A')]} transactional applying={false} onApply={onApply} onClose={onClose} />)
    expect(screen.getByText(/1 statement will run in one transaction/)).toBeInTheDocument()
    expect(screen.queryByRole('note')).toBeNull()
    expect(screen.getByRole('button', { name: 'Apply' })).toHaveFocus()
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
    expect(onApply).toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onClose).toHaveBeenCalled()
    rerender(<StructurePreviewDialog statements={[st('A'), st('B')]} transactional={false} applying onApply={onApply} onClose={onClose} />)
    expect(screen.getByText('2 statements will run.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Applying…' })).toBeDisabled()
  })

  it('confirms dropping a table, with CASCADE where supported', () => {
    const props = { table: 't', statements: [st('DROP TABLE t')], cascade: false, canCascade: true, loading: false, busy: false, failure: null, onCascade: vi.fn(), onConfirm: vi.fn(), onCancel: vi.fn() }
    const { rerender } = render(<DropTableDialog {...props} />)
    const dlg = screen.getByRole('alertdialog', { name: 'Drop table t?' })
    expect(screen.getByTestId('drop-sql')).toHaveTextContent('DROP TABLE t;')
    fireEvent.click(screen.getByRole('checkbox', { name: /CASCADE/ }))
    expect(props.onCascade).toHaveBeenCalledWith(true)
    fireEvent.click(screen.getByRole('button', { name: 'Drop table' }))
    expect(props.onConfirm).toHaveBeenCalled()
    fireEvent.keyDown(dlg, { key: 'Escape' })
    expect(props.onCancel).toHaveBeenCalled()
    rerender(<DropTableDialog {...props} canCascade={false} statements={[]} busy failure={{ kind: 'database', section: 'table', index: -1, field: '', message: 'in use', sql: '', applied: 0 }} />)
    expect(screen.queryByRole('checkbox')).toBeNull()
    expect(screen.getByRole('alert')).toHaveTextContent('in use')
    expect(screen.getByRole('button', { name: 'Dropping…' })).toBeDisabled()
  })
})
