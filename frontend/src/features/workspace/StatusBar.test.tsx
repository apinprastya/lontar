import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { TabState } from '../../stores/workspace'
import StatusBar, { pendingText, rowRangeText } from './StatusBar'

const rows = (n: number) => Array.from({ length: n }, () => [])

const base = {
  kind: "table" as const,
  offset: 0,
  rows: rows(300),
  totalCount: null as number | null,
  estimatedRows: 12345 as number | null,
  appliedFilters: [],
  appliedRawWhere: '',
  hasMore: true,
}

describe('rowRangeText', () => {
  it('shows the estimate until the count is known', () => {
    expect(rowRangeText(base)).toBe('1–300 of ~12,345')
    expect(rowRangeText({ ...base, totalCount: 12001 })).toBe('1–300 of 12,001')
    expect(rowRangeText({ ...base, offset: 300 })).toBe('301–600 of ~12,345')
  })
  it('does not show the estimate for filtered or out-of-range pages', () => {
    expect(rowRangeText({ ...base, appliedRawWhere: 'x' })).toBe('1–300 of many')
    expect(rowRangeText({ ...base, appliedFilters: [{ column: 'a', op: '=', value: '', value2: '' }], hasMore: false })).toBe('1–300')
    expect(rowRangeText({ ...base, estimatedRows: 10 })).toBe('1–300 of many')
    expect(rowRangeText({ ...base, estimatedRows: null, hasMore: false })).toBe('1–300')
  })
  it('handles empty pages', () => {
    expect(rowRangeText({ ...base, rows: [] })).toBe('No rows')
    expect(rowRangeText({ ...base, rows: [], offset: 1200 })).toBe('No rows after 1,200')
  })
})

describe('StatusBar', () => {
  afterEach(cleanup)

  it('shows range, time and paging', async () => {
    const onPrev = vi.fn()
    const onNext = vi.fn()
    const tab = { ...base, elapsedMs: 1234, loading: false, counting: true } as unknown as TabState
    render(<StatusBar tab={tab} serverVersion="PostgreSQL 16.4" onPrev={onPrev} onNext={onNext} />)
    expect(screen.getByTestId('row-range')).toHaveTextContent('1–300 of ~12,345')
    expect(screen.getByTestId('query-time')).toHaveTextContent('1,234 ms')
    expect(screen.getByText('counting…')).toBeInTheDocument()
    expect(screen.getByTestId('statusbar-session')).toHaveTextContent('PostgreSQL 16.4')
    expect(screen.getByRole('button', { name: 'Previous page' })).toBeDisabled()
    await userEvent.setup().click(screen.getByRole('button', { name: 'Next page' }))
    expect(onNext).toHaveBeenCalled()
  })

  it('renders without a tab', () => {
    render(<StatusBar tab={null} serverVersion="SQLite 3" onPrev={() => {}} onNext={() => {}} />)
    expect(screen.queryByTestId('row-range')).toBeNull()
    expect(screen.getByRole('status')).toHaveTextContent('SQLite 3')
  })
})

describe('StatusBar for SQL tabs', () => {
  it('shows rows of the active result and the run time', async () => {
    const { newQueryTabState } = await import('../../stores/queryTabs')
    const { execResult, resultSet, stmt } = await import('../../test/fakeEditorApi')
    const tab = newQueryTabState('Query 1')
    const { rerender } = render(<StatusBar tab={tab} serverVersion="v" onPrev={() => {}} onNext={() => {}} />)
    expect(screen.queryByTestId('row-range')).not.toBeInTheDocument()
    rerender(<StatusBar tab={{ ...tab, running: true }} serverVersion="v" onPrev={() => {}} onNext={() => {}} />)
    expect(screen.getByText('Running…')).toBeInTheDocument()
    const result = execResult([stmt(0, 'x', { resultSets: [resultSet('r1', 1000, { truncated: true })] }), stmt(1, 'y')])
    rerender(<StatusBar tab={{ ...tab, result, activeResult: 'r1' }} serverVersion="v" onPrev={() => {}} onNext={() => {}} />)
    expect(screen.getByTestId('row-range')).toHaveTextContent('1,000+ rows')
    expect(screen.getByTestId('query-time')).toHaveTextContent('3.25 ms')
    rerender(<StatusBar tab={{ ...tab, result, activeResult: 'messages', running: true }} serverVersion="v" onPrev={() => {}} onNext={() => {}} />)
    expect(screen.getByTestId('row-range')).toHaveTextContent('2 statements')
    expect(screen.getByText('running…')).toBeInTheDocument()
    rerender(<StatusBar tab={{ ...tab, result: execResult([stmt(0, 'x')]), activeResult: 'messages' }} serverVersion="v" onPrev={() => {}} onNext={() => {}} />)
    expect(screen.getByTestId('row-range')).toHaveTextContent('1 statement')
  })
})

describe('pending changes in the status bar', () => {
  afterEach(cleanup)
  it('shows the count and the actions', async () => {
    const user = userEvent.setup()
    const pending = { count: 3, committing: false, previewLoading: false, commitHint: 'Ctrl+S', onPreview: vi.fn(), onCommit: vi.fn(), onDiscard: vi.fn() }
    const { rerender } = render(<StatusBar tab={null} serverVersion="v" onPrev={vi.fn()} onNext={vi.fn()} pending={pending} />)
    expect(screen.getByTestId('pending')).toHaveTextContent('3 pending changes')
    await user.click(screen.getByRole('button', { name: 'Discard' }))
    await user.click(screen.getByRole('button', { name: 'Preview SQL' }))
    await user.click(screen.getByRole('button', { name: 'Commit' }))
    expect(pending.onDiscard).toHaveBeenCalled()
    expect(pending.onPreview).toHaveBeenCalled()
    expect(pending.onCommit).toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Commit' })).toHaveAttribute('title', 'Commit (Ctrl+S)')
    rerender(<StatusBar tab={null} serverVersion="v" onPrev={vi.fn()} onNext={vi.fn()} pending={{ ...pending, count: 1, committing: true }} />)
    expect(screen.getByTestId('pending')).toHaveTextContent('1 pending change')
    expect(screen.getByRole('button', { name: 'Committing…' })).toBeDisabled()
    rerender(<StatusBar tab={null} serverVersion="v" onPrev={vi.fn()} onNext={vi.fn()} pending={{ ...pending, count: 0 }} />)
    expect(screen.queryByTestId('pending')).toBeNull()
    expect(pendingText(1200)).toBe('1,200 pending changes')
  })
})
