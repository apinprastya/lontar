import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

const copyText = vi.fn<(t: string) => Promise<void>>()
vi.mock('../../lib/clipboard', () => ({ copyText: (t: string) => copyText(t) }))

import { ConfirmRunDialog, PreviewDialog, UnsavedDialog } from './dialogs'

afterEach(cleanup)

describe('edit dialogs', () => {
  it('previews SQL, copies it and commits', async () => {
    copyText.mockResolvedValue()
    const user = userEvent.setup()
    const onCommit = vi.fn()
    const onClose = vi.fn()
    const statements = [
      { op: 'delete' as const, index: 0, sql: 'DELETE FROM t WHERE id = 1;' },
      { op: 'update' as const, index: 0, sql: "UPDATE t SET a = 'x' WHERE id = 2;" },
    ]
    const { rerender } = render(<PreviewDialog statements={statements} safeMode="confirm" committing={false} onCommit={onCommit} onClose={onClose} />)
    expect(screen.getByTestId('preview-sql')).toHaveTextContent("DELETE FROM t WHERE id = 1; UPDATE t SET a = 'x' WHERE id = 2;")
    expect(screen.getByText(/2 statements/)).toHaveTextContent('asks for confirmation')
    expect(screen.getByRole('button', { name: 'Commit' })).toHaveFocus()
    await user.click(screen.getByRole('button', { name: 'Copy SQL' }))
    expect(copyText).toHaveBeenCalledWith(statements.map((s) => s.sql).join('\n'))
    expect(await screen.findByRole('button', { name: 'Copied' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Commit' }))
    expect(onCommit).toHaveBeenCalled()
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onClose).toHaveBeenCalledTimes(2)
    rerender(<PreviewDialog statements={statements.slice(0, 1)} safeMode="off" committing onCommit={onCommit} onClose={onClose} />)
    expect(screen.getByRole('button', { name: 'Committing…' })).toBeDisabled()
    expect(screen.getByText(/1 statement,/)).not.toHaveTextContent('confirmation')
  })

  it('asks Commit / Discard / Cancel', async () => {
    const user = userEvent.setup()
    const onChoose = vi.fn()
    const { rerender } = render(<UnsavedDialog count={3} busy={false} onChoose={onChoose} />)
    expect(screen.getByRole('alertdialog')).toHaveTextContent('3 uncommitted changes')
    for (const name of ['Commit', 'Discard', 'Cancel']) await user.click(screen.getByRole('button', { name }))
    fireEvent.keyDown(screen.getByRole('alertdialog'), { key: 'Escape' })
    expect(onChoose.mock.calls.map((c) => c[0])).toEqual(['commit', 'discard', 'cancel', 'cancel'])
    rerender(<UnsavedDialog count={1} busy onChoose={onChoose} />)
    expect(screen.getByRole('alertdialog')).toHaveTextContent('1 uncommitted change.')
    expect(screen.getByRole('button', { name: 'Committing…' })).toBeDisabled()
  })

  it('lists the write statements of a confirm-mode run', async () => {
    const user = userEvent.setup()
    const onConfirm = vi.fn()
    const onCancel = vi.fn()
    render(<ConfirmRunDialog statements={['UPDATE t SET a = 1', ' DELETE FROM t ']} onConfirm={onConfirm} onCancel={onCancel} />)
    expect(screen.getByTestId('write-statements').textContent).toBe('UPDATE t SET a = 1;\n\nDELETE FROM t;')
    expect(screen.getByRole('button', { name: 'Run' })).toHaveFocus()
    await user.click(screen.getByRole('button', { name: 'Run' }))
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    fireEvent.keyDown(screen.getByRole('alertdialog'), { key: 'Escape' })
    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onCancel).toHaveBeenCalledTimes(2)
  })
})
