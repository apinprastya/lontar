import { vi } from 'vitest'
import type { EditorApi } from '../features/editor/api'
import type { ExecResult, ResultSet, StatementResult } from '../features/editor/types'

export function resultSet(id: string, rows = 2, over: Partial<ResultSet> = {}): ResultSet {
  return {
    id,
    columns: [
      { name: 'id', type: 'int4', isPK: false, nullable: true },
      { name: 'name', type: 'text', isPK: false, nullable: true },
    ],
    rows: Array.from({ length: rows }, (_, i) => [
      { k: 'num' as const, v: i + 1 },
      { k: 'str' as const, v: `n${i + 1}` },
    ]),
    truncated: false,
    canLoadMore: false,
    ...over,
  }
}

export function stmt(index: number, sql: string, over: Partial<StatementResult> = {}): StatementResult {
  return {
    index,
    sql,
    start: 0,
    end: sql.length,
    status: 'ok',
    resultSets: [],
    rowsAffected: null,
    durationMs: 1.5,
    error: null,
    ...over,
  }
}

export function execResult(statements: StatementResult[], over: Partial<ExecResult> = {}): ExecResult {
  return { statements, durationMs: 3.25, tx: { state: 'idle', source: 'server' }, notices: [], sessionReset: false, cancelled: false, ...over }
}

/** A fake EditorApi with sensible defaults; every method is a vi.fn. */
export function fakeEditorApi() {
  const api = {
    openEditor: vi.fn(async () => 'ed-1'),
    closeEditor: vi.fn(async () => {}),
    execute: vi.fn(async (_e: string, _r: string, sql: string) => execResult([stmt(0, sql, { resultSets: [resultSet('rs-1')] })])),
    cancel: vi.fn(async () => true),
    fetchMore: vi.fn(async (_e: string, _r: string, id: string) => resultSet(id, 1)),
    statementAt: vi.fn(async (_k: string, sql: string) => (sql.trim() ? { text: sql.trim(), start: 0, end: sql.trimEnd().length, delimEnd: sql.length } : null)),
    completionSchema: vi.fn(async (_s: string, schema: string) => ({
      database: 'shop',
      schema,
      schemas: [schema, 'audit'],
      tables: [{ name: 'users', type: 'table' as const, columns: [{ name: 'id', type: 'int4' }] }],
      columnsLoaded: true,
    })),
    completionColumns: vi.fn(async () => [{ name: 'x', type: 'text' }]),
    historyList: vi.fn(async () => []),
    historyDelete: vi.fn(async () => {}),
    historyClear: vi.fn(async () => 0),
  } satisfies EditorApi
  return api
}
