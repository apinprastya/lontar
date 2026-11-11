import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { TabState } from '../../stores/workspace'
import StatusBar, { rowRangeText } from './StatusBar'

const rows = (n: number) => Array.from({ length: n }, () => [])

const base = {
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
