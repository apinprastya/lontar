import { describe, expect, it } from 'vitest'
import { resultSet, stmt } from '../../test/fakeEditorApi'
import { firstLine, formatDuration, historyTime, rowCountText, statementSummary } from './format'

describe('format', () => {
  it('formats durations', () => {
    expect(formatDuration(0.123)).toBe('0.12 ms')
    expect(formatDuration(12.345)).toBe('12.3 ms')
    expect(formatDuration(456.7)).toBe('457 ms')
    expect(formatDuration(1234)).toBe('1.23 s')
  })

  it('shortens statements to their first line', () => {
    expect(firstLine('SELECT 1')).toBe('SELECT 1')
    expect(firstLine('\n  SELECT a,\n b')).toBe('SELECT a, …')
    expect(firstLine('x'.repeat(10), 4)).toBe('xxxx…')
    expect(firstLine('')).toBe('')
  })

  it('summarises statements', () => {
    expect(statementSummary(stmt(0, 'x', { resultSets: [resultSet('a', 1)] }))).toBe('1 row returned')
    expect(statementSummary(stmt(0, 'x', { resultSets: [resultSet('a', 1), resultSet('b', 2)] }))).toBe('3 rows returned in 2 result sets')
    expect(statementSummary(stmt(0, 'x', { rowsAffected: 2 }))).toBe('2 rows affected')
    expect(statementSummary(stmt(0, 'x', { status: 'error' }))).toBe('Failed')
    expect(statementSummary(stmt(0, 'x', { status: 'cancelled' }))).toBe('Cancelled')
    expect(rowCountText(1, false)).toBe('1 row')
    expect(rowCountText(1000, true)).toBe('1,000+ rows')
  })

  it('formats history times', () => {
    const now = new Date(2026, 4, 1, 15, 0, 0)
    expect(historyTime(new Date(2026, 4, 1, 9, 5, 7).toISOString(), now)).toBe('09:05:07')
    expect(historyTime(new Date(2026, 3, 30, 23, 0, 0).toISOString(), now)).toBe('2026-04-30 23:00:00')
    expect(historyTime('garbage', now)).toBe('')
    expect(historyTime(new Date().toISOString())).toMatch(/^\d\d:\d\d:\d\d$/)
  })
})
