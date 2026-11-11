import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../../bindings/dbedit/internal/services', () => import('../../test/mockBindings'))

import { EditorService, HistoryService, resetBindings } from '../../test/mockBindings'
import { editorApi, toResultSet } from './api'

describe('editorApi', () => {
  beforeEach(resetBindings)

  it('normalises execution results', async () => {
    EditorService.Execute.mockResolvedValue({
      statements: [
        { index: 0, sql: 'x', start: 0, end: 1, status: 'ok', resultSets: [{ id: 'r', columns: null, rows: [null, [{ k: 'num', v: 1 }]], truncated: false, canLoadMore: false }], rowsAffected: undefined, durationMs: 1, error: null },
        { index: 1, sql: 'y', start: 2, end: 3, status: 'error', resultSets: null, rowsAffected: 2, durationMs: 1, error: { message: 'm', code: 'c' } },
      ],
      durationMs: 2,
      tx: { state: 'idle', source: 'server' },
      notices: null,
      sessionReset: false,
      cancelled: false,
    })
    const res = await editorApi.execute('e', 'r', 'x; y', { maxRows: 10, continueOnError: true, baseOffset: 4 })
    expect(EditorService.Execute).toHaveBeenCalledWith('e', 'r', 'x; y', { maxRows: 10, continueOnError: true, baseOffset: 4 })
    expect(res.notices).toEqual([])
    expect(res.statements[0].resultSets[0]).toEqual({ id: 'r', columns: [], rows: [[], [{ k: 'num', v: 1 }]], truncated: false, canLoadMore: false })
    expect(res.statements[0].rowsAffected).toBeNull()
    expect(res.statements[1].resultSets).toEqual([])
    expect(res.statements[1].error).toEqual({ message: 'm', code: 'c', position: null })
    EditorService.Execute.mockResolvedValue({ statements: null, notices: [] })
    expect((await editorApi.execute('e', 'r', '', { maxRows: 1, continueOnError: false, baseOffset: 0 })).statements).toEqual([])
    EditorService.Execute.mockResolvedValue(null)
    await expect(editorApi.execute('e', 'r', '', { maxRows: 1, continueOnError: false, baseOffset: 0 })).rejects.toThrow('empty response')
  })

  it('wraps the other editor calls', async () => {
    EditorService.OpenEditor.mockResolvedValue('ed')
    expect(await editorApi.openEditor('s')).toBe('ed')
    EditorService.CloseEditor.mockResolvedValue()
    await editorApi.closeEditor('ed')
    expect(EditorService.CloseEditor).toHaveBeenCalledWith('ed')
    EditorService.Cancel.mockResolvedValue(true)
    expect(await editorApi.cancel('r')).toBe(true)
    EditorService.FetchMore.mockResolvedValue({ id: 'r', columns: [], rows: null, truncated: true, canLoadMore: true })
    expect(await editorApi.fetchMore('ed', 'q', 'r', 5)).toEqual({ id: 'r', columns: [], rows: [], truncated: true, canLoadMore: true })
    EditorService.FetchMore.mockResolvedValue(null)
    await expect(editorApi.fetchMore('ed', 'q', 'r', 5)).rejects.toThrow('empty response')
    EditorService.StatementAt.mockResolvedValue(null)
    expect(await editorApi.statementAt('postgres', 'x', 0)).toBeNull()
    EditorService.StatementAt.mockResolvedValue({ text: 'x', start: 0, end: 1, delimEnd: 1 })
    expect(await editorApi.statementAt('postgres', 'x', 0)).toEqual({ text: 'x', start: 0, end: 1, delimEnd: 1 })
    EditorService.CompletionSchema.mockResolvedValue({ database: 'd', schema: 's', schemas: null, tables: [{ name: 't', type: 'table', columns: null }], columnsLoaded: false })
    expect(await editorApi.completionSchema('s1', 's', true)).toEqual({ database: 'd', schema: 's', schemas: [], tables: [{ name: 't', type: 'table', columns: null }], columnsLoaded: false })
    EditorService.CompletionSchema.mockResolvedValue({ database: 'd', schema: 's', schemas: [], tables: null, columnsLoaded: true })
    expect((await editorApi.completionSchema('s1', 's', false)).tables).toEqual([])
    EditorService.CompletionSchema.mockResolvedValue(null)
    await expect(editorApi.completionSchema('s1', 's', false)).rejects.toThrow('empty response')
    EditorService.CompletionColumns.mockResolvedValue(null)
    expect(await editorApi.completionColumns('s1', 's', 't')).toEqual([])
    expect(toResultSet({ id: 'x', columns: null, rows: null, truncated: false, canLoadMore: false } as never)).toEqual({ id: 'x', columns: [], rows: [], truncated: false, canLoadMore: false })
  })

  it('wraps the history service', async () => {
    HistoryService.List.mockResolvedValue(null)
    expect(await editorApi.historyList('c', 'q', 10, 0)).toEqual([])
    expect(HistoryService.List).toHaveBeenCalledWith('c', 'q', 10, 0)
    HistoryService.Delete.mockResolvedValue()
    await editorApi.historyDelete(3)
    expect(HistoryService.Delete).toHaveBeenCalledWith(3)
    HistoryService.Clear.mockResolvedValue(4)
    expect(await editorApi.historyClear('c')).toBe(4)
  })
})
