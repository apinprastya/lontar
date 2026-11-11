import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { execResult, resultSet, stmt } from '../../test/fakeEditorApi'
import Messages from './Messages'

describe('Messages', () => {
  it('lists every statement with its outcome and selects it on click', () => {
    const onShow = vi.fn()
    const result = execResult(
      [
        stmt(0, 'SELECT *\nFROM users', { resultSets: [resultSet('a', 3, { truncated: true })], durationMs: 12.34 }),
        stmt(1, 'UPDATE users SET a = 1', { rowsAffected: 1 }),
        stmt(2, 'CREATE TABLE x (a int)'),
        stmt(3, 'SELEC 1', { status: 'error', error: { message: 'syntax error at or near "SELEC"', code: '42601', position: 30, detail: 'd', hint: 'h' } }),
        stmt(4, 'SELECT 5', { status: 'skipped' }),
      ],
      { notices: ['The editor reconnected.'] },
    )
    render(<Messages result={result} onShow={onShow} />)
    expect(screen.getByRole('alert')).toHaveTextContent('The editor reconnected.')
    const items = screen.getAllByRole('listitem')
    expect(items).toHaveLength(5)
    expect(items[0]).toHaveTextContent('✓')
    expect(items[0]).toHaveTextContent('SELECT * …')
    expect(items[0]).toHaveTextContent('3+ rows returned')
    expect(items[0]).toHaveTextContent('12.3 ms')
    expect(items[1]).toHaveTextContent('1 row affected')
    expect(items[2]).toHaveTextContent('OK')
    expect(items[3]).toHaveTextContent('✗')
    expect(items[3]).toHaveTextContent('[42601] syntax error at or near "SELEC"')
    expect(items[3]).toHaveTextContent('Detail: d')
    expect(items[3]).toHaveTextContent('Hint: h')
    expect(items[3]).toHaveAttribute('title', 'Show the error position in the editor')
    expect(items[4]).toHaveTextContent('Not run')
    expect(screen.getByText(/Total time/)).toHaveTextContent('3.25 ms')
    fireEvent.click(items[3])
    expect(onShow).toHaveBeenCalledWith(3)
  })

  it('shows empty and cancelled runs', () => {
    const { rerender } = render(<Messages result={execResult([])} onShow={vi.fn()} />)
    expect(screen.getByText('No statements to run.')).toBeInTheDocument()
    rerender(<Messages result={execResult([stmt(0, 'SELECT pg_sleep(9)', { status: 'cancelled' })], { cancelled: true })} onShow={vi.fn()} />)
    expect(screen.getByText(/Cancelled after/)).toBeInTheDocument()
    expect(screen.getByRole('listitem')).toHaveTextContent('Cancelled')
  })
})
