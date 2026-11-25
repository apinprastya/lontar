import { vi } from 'vitest'
import type { EditorApi } from '../features/editor/api'
import type { SqlFile, SqlFileApi } from '../features/editor/sqlFiles'
import type { ExecResult, Plan, PlanNode, ResultSet, StatementResult } from '../features/editor/types'

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

export function planNode(op: string, over: Partial<PlanNode> = {}): PlanNode {
  return { op, object: '', detail: '', estRows: null, actualRows: null, loops: null, cost: null, timeMs: null, props: [], children: [], ...over }
}

/**
 * A PostgreSQL-like analyzed plan: Sort → Hash Join → (Seq Scan numbers,
 * Hash → Seq Scan items). The numbers scan is hot and misestimated.
 */
export function samplePlan(over: Partial<Plan> = {}): Plan {
  const items = planNode('Seq Scan', { object: 'items i', estRows: 3, actualRows: 3, loops: 1, cost: 1.03, timeMs: 0.01, props: [{ key: 'Node Type', value: 'Seq Scan' }] })
  const hash = planNode('Hash', { estRows: 3, actualRows: 3, loops: 1, cost: 1.03, timeMs: 0.02, children: [items] })
  const numbers = planNode('Seq Scan', {
    object: 'numbers n',
    detail: '(n < 500)',
    estRows: 5,
    actualRows: 499,
    loops: 1,
    cost: 18.5,
    timeMs: 6,
    props: [
      { key: 'Node Type', value: 'Seq Scan' },
      { key: 'Filter', value: '(n < 500)' },
    ],
  })
  const join = planNode('Hash Join', { detail: '(n.grp = i.id)', estRows: 150, actualRows: 150, loops: 1, cost: 21.43, timeMs: 7, children: [numbers, hash] })
  const sort = planNode('Sort', { detail: 'n.label', estRows: 150, actualRows: 150, loops: 1, cost: 27.23, timeMs: 10, children: [join], props: [{ key: 'Sort Key', value: 'n.label' }] })
  return {
    engine: 'postgres',
    analyzed: true,
    sql: 'EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) SELECT 1',
    planningMs: 0.5,
    executionMs: 10,
    nodes: [sort],
    raw: '[{"Plan": {}}]',
    rawFormat: 'json',
    notices: [],
    error: null,
    cancelled: false,
    sessionReset: false,
    tx: { state: 'idle', source: 'server' },
    durationMs: 12,
    ...over,
  }
}

/** A fake EditorApi with sensible defaults; every method is a vi.fn. */
export function fakeEditorApi() {
  const api = {
    openEditor: vi.fn(async () => 'ed-1'),
    closeEditor: vi.fn(async () => {}),
    execute: vi.fn(async (_e: string, _r: string, sql: string) => execResult([stmt(0, sql, { resultSets: [resultSet('rs-1')] })])),
    explain: vi.fn(async (_e: string, _r: string, sql: string, opts: { analyze: boolean }) => samplePlan({ analyzed: opts.analyze, sql: `EXPLAIN ${sql}` })),
    cancel: vi.fn(async () => true),
    fetchMore: vi.fn(async (_e: string, _r: string, id: string) => resultSet(id, 1)),
    statementAt: vi.fn(async (_k: string, sql: string) => (sql.trim() ? { text: sql.trim(), start: 0, end: sql.trimEnd().length, delimEnd: sql.length } : null)),
    paramNames: vi.fn(async (_k: string, sql: string) => [...new Set([...sql.matchAll(/(?<![\w:\])]):([A-Za-z_]\w*)/g)].map((m) => m[1]))]),
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

export function sqlFile(path: string, text: string, over: Partial<SqlFile> = {}): SqlFile {
  return { path, name: path.split(/[\\/]/).pop() ?? path, text, modTime: 1000, eol: '\n', ...over }
}

/** A fake SqlFileApi: open/read return files, save echoes the text (Save as picks /tmp/<name>). */
export function fakeSqlFileApi() {
  let mod = 2000
  const api = {
    open: vi.fn<SqlFileApi['open']>(async () => null),
    read: vi.fn<SqlFileApi['read']>(async (path: string) => sqlFile(path, `-- ${path}\n`)),
    save: vi.fn<SqlFileApi['save']>(async (path, name, text, eol) => {
      const p = path || `/tmp/${name.replace(/\.sql$/i, '')}.sql`
      mod += 1
      return sqlFile(p, text, { eol, modTime: mod })
    }),
  } satisfies SqlFileApi
  return api
}
