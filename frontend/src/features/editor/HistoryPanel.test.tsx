import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fakeEditorApi } from '../../test/fakeEditorApi'
import HistoryPanel, { CLICK_DELAY } from './HistoryPanel'
import type { HistoryEntry } from './types'

const entry = (id: number, sql: string, over: Partial<HistoryEntry> = {}): HistoryEntry => ({
  id,
  connectionId: 'c1',
  database: 'shop',
  sql,
  startedAt: new Date().toISOString(),
  durationMs: 2.5,
  rowCount: 1,
  rowsAffected: null,
  isError: false,
  error: '',
  truncated: false,
  ...over,
})

describe('HistoryPanel', () => {
  let api: ReturnType<typeof fakeEditorApi>
  const props = () => ({ api, connectionId: 'c1', onInsert: vi.fn(), onOpen: vi.fn(), onClose: vi.fn() })

  beforeEach(() => {
    api = fakeEditorApi()
    api.historyList.mockResolvedValue([entry(2, 'SELECT * FROM users'), entry(1, 'SELEC 1', { isError: true, error: 'syntax error' })] as never)
  })
  afterEach(() => vi.useRealTimers())

  it('lists, searches and reloads entries', async () => {
    const p = props()
    const { rerender } = render(<HistoryPanel {...p} />)
    await waitFor(() => expect(screen.getAllByRole('listitem')).toHaveLength(2))
    expect(api.historyList).toHaveBeenCalledWith('c1', '', 200, 0)
    expect(screen.getByLabelText('failed')).toHaveAttribute('title', 'syntax error')
    expect(screen.getAllByText('shop')).toHaveLength(2)

    api.historyList.mockResolvedValue([] as never)
    fireEvent.change(screen.getByLabelText('Search history'), { target: { value: 'users' } })
    await waitFor(() => expect(api.historyList).toHaveBeenLastCalledWith('c1', 'users', 200, 0))
    await waitFor(() => expect(screen.getByText('No matching queries.')).toBeInTheDocument())
    fireEvent.change(screen.getByLabelText('Search history'), { target: { value: '' } })
    await waitFor(() => expect(screen.getByText('No queries yet.')).toBeInTheDocument())

    const calls = api.historyList.mock.calls.length
    rerender(<HistoryPanel {...p} refreshKey={{}} />)
    await waitFor(() => expect(api.historyList.mock.calls.length).toBe(calls + 1))
    fireEvent.click(screen.getByLabelText('Close history'))
    expect(p.onClose).toHaveBeenCalled()
  })

  it('inserts on click and opens on double-click', async () => {
    const p = props()
    render(<HistoryPanel {...p} />)
    await waitFor(() => expect(screen.getAllByRole('listitem')).toHaveLength(2))
    vi.useFakeTimers()
    const first = screen.getByText('SELECT * FROM users')
    fireEvent.click(first)
    act(() => vi.advanceTimersByTime(CLICK_DELAY))
    expect(p.onInsert).toHaveBeenCalledWith('SELECT * FROM users')
    // A double-click opens a tab and does not insert.
    fireEvent.click(first)
    fireEvent.click(first)
    fireEvent.doubleClick(first)
    act(() => vi.advanceTimersByTime(CLICK_DELAY * 2))
    expect(p.onOpen).toHaveBeenCalledWith('SELECT * FROM users')
    expect(p.onInsert).toHaveBeenCalledTimes(1)
  })

  it('deletes and clears entries, showing errors', async () => {
    render(<HistoryPanel {...props()} />)
    await waitFor(() => expect(screen.getAllByRole('listitem')).toHaveLength(2))
    fireEvent.click(screen.getAllByLabelText('Delete from history')[0])
    await waitFor(() => expect(screen.getAllByRole('listitem')).toHaveLength(1))
    expect(api.historyDelete).toHaveBeenCalledWith(2)

    api.historyDelete.mockRejectedValueOnce(new Error('locked'))
    fireEvent.click(screen.getAllByLabelText('Delete from history')[0])
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('locked'))

    fireEvent.click(screen.getByText('Clear'))
    fireEvent.click(screen.getByText('Cancel'))
    expect(api.historyClear).not.toHaveBeenCalled()
    api.historyList.mockResolvedValue([] as never)
    fireEvent.click(screen.getByText('Clear'))
    fireEvent.click(screen.getAllByText('Clear')[1])
    await waitFor(() => expect(api.historyClear).toHaveBeenCalledWith('c1'))
    await waitFor(() => expect(screen.getByText('No queries yet.')).toBeInTheDocument())

    api.historyList.mockResolvedValue([entry(3, 'x')] as never)
    api.historyClear.mockRejectedValueOnce(new Error('disk full'))
    fireEvent.change(screen.getByLabelText('Search history'), { target: { value: 'x' } })
    await waitFor(() => expect(screen.getByText('x', { selector: '.history-sql' })).toBeInTheDocument())
    fireEvent.click(screen.getByText('Clear'))
    fireEvent.click(screen.getAllByText('Clear')[1])
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('disk full'))
  })

  it('shows list errors', async () => {
    api.historyList.mockRejectedValue(new Error('no history db'))
    render(<HistoryPanel {...props()} />)
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('no history db'))
  })
})
