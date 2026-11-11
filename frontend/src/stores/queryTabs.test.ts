import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { WorkspaceApi } from '../features/workspace/api'
import { execResult, fakeEditorApi, resultSet, stmt } from '../test/fakeEditorApi'
import {
  MESSAGES,
  activeResultView,
  draftKey,
  firstErrorPosition,
  initialResultTab,
  loadDrafts,
  nextQueryTitle,
  resultSets,
  saveDrafts,
  type QueryTabState,
} from './queryTabs'
import { createWorkspaceStore, type TabState, type WorkspaceStore } from './workspace'

function fakeApi(): WorkspaceApi {
  return {
    current: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listDatabases: vi.fn(async () => ['shop', 'other']),
    useDatabase: vi.fn(async (_s: string, db: string) => ({ database: db, schema: 'public' })),
    listSchemas: vi.fn(async () => ['public']),
    listTables: vi.fn(async () => [{ schema: 'public', name: 'users', type: 'table' as const, estimatedRows: 1 }]),
    fetchTable: vi.fn(async (_s, _r, req) => ({ columns: [], rows: [], offset: req.offset, limit: req.limit, hasMore: false, totalCount: 0, elapsedMs: 1 })),
    countRows: vi.fn(async () => 0),
    cancel: vi.fn(async () => true),
  }
}

const flush = () => new Promise((r) => setTimeout(r, 0))

describe('query tab helpers', () => {
  it('numbers new tabs after the highest "Query N"', () => {
    expect(nextQueryTitle([])).toBe('Query 1')
    expect(nextQueryTitle(['Query 1', 'Query 7', 'Report', 'Query x'])).toBe('Query 8')
  })

  it('picks the first result tab and error positions', () => {
    const ok = execResult([stmt(0, 'a'), stmt(1, 'b', { resultSets: [resultSet('r1')] })])
    expect(initialResultTab(ok)).toBe('r1')
    expect(initialResultTab(execResult([stmt(0, 'a')]))).toBe(MESSAGES)
    const bad = execResult([stmt(0, 'a', { resultSets: [resultSet('r1')] }), stmt(1, 'b', { status: 'error', start: 5, end: 9, error: { message: 'x', code: '42601', position: 7 } })])
    expect(initialResultTab(bad)).toBe(MESSAGES)
    expect(firstErrorPosition(bad)).toEqual({ pos: 7, from: 5, to: 9 })
    expect(firstErrorPosition(ok)).toBeNull()
    expect(resultSets(null)).toEqual([])
  })

  it('sorts the active result client-side', () => {
    const rs = resultSet('r1', 3)
    const tab = { result: execResult([stmt(0, 'a', { resultSets: [rs] })]), activeResult: 'r1', sorts: { r1: [{ column: 'id', desc: true }] } }
    const view = activeResultView(tab)!
    expect(view.rows.map((r) => r[0].v)).toEqual([3, 2, 1])
    expect(view.set).toBe(rs)
    expect(activeResultView({ ...tab, activeResult: MESSAGES })).toBeNull()
  })

  it('saves and loads drafts, tolerating bad data and storage errors', () => {
    saveDrafts('c9', [{ title: 'Q', sql: 'SELECT 1' }])
    expect(loadDrafts('c9')).toEqual([{ title: 'Q', sql: 'SELECT 1' }])
    saveDrafts('c9', [])
    expect(window.localStorage.getItem(draftKey('c9'))).toBeNull()
    expect(loadDrafts('')).toEqual([])
    saveDrafts('', [{ title: 'x', sql: '' }])
    window.localStorage.setItem(draftKey('c9'), '{bad json')
    expect(loadDrafts('c9')).toEqual([])
    window.localStorage.setItem(draftKey('c9'), JSON.stringify({ not: 'an array' }))
    expect(loadDrafts('c9')).toEqual([])
    window.localStorage.setItem(draftKey('c9'), JSON.stringify([{ title: 'ok', sql: 'x' }, { title: 1 }, null]))
    expect(loadDrafts('c9')).toEqual([{ title: 'ok', sql: 'x' }])
    const spy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('quota')
    })
    expect(() => saveDrafts('c9', [{ title: 'a', sql: 'b' }])).not.toThrow()
    spy.mockRestore()
    const get = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    expect(loadDrafts('c9')).toEqual([])
    get.mockRestore()
  })
})

describe('query tabs in the workspace store', () => {
  let store: WorkspaceStore
  let editor: ReturnType<typeof fakeEditorApi>
  const st = () => store.getState()
  const q = () => st().activeTab() as QueryTabState

  beforeEach(() => {
    window.localStorage.clear()
    editor = fakeEditorApi()
    store = createWorkspaceStore({ id: 's1', kind: 'postgres', connectionId: 'c1' }, fakeApi(), editor)
  })
  afterEach(() => st().dispose())

  it('creates, renames and closes query tabs, keeping drafts', async () => {
    const id = st().newQueryTab()
    expect(q()).toMatchObject({ kind: 'query', id, title: 'Query 1', sql: '', pinned: true, editorId: null })
    st().newQueryTab('SELECT 2')
    expect(q().title).toBe('Query 2')
    expect(q().sql).toBe('SELECT 2')
    st().renameTab(id, '  Report  ')
    st().renameTab(id, '   ') // ignored
    st().renameTab('nope', 'x')
    expect((st().tabs[0] as QueryTabState).title).toBe('Report')
    st().setSql(id, 'SELECT 1')
    st().setSql('nope', 'x')
    expect(loadDrafts('c1')).toEqual([
      { title: 'Report', sql: 'SELECT 1' },
      { title: 'Query 2', sql: 'SELECT 2' },
    ])

    // Reopening the same connection restores the tabs.
    const again = createWorkspaceStore({ id: 's2', kind: 'postgres', connectionId: 'c1' }, fakeApi(), editor)
    expect(again.getState().tabs.map((t) => (t as QueryTabState).title)).toEqual(['Report', 'Query 2'])
    expect(again.getState().activeTabId).toBe(again.getState().tabs[0].id)

    // Closing releases the backend editor.
    await st().runQuery(id, 'all')
    st().closeTab(id)
    expect(editor.closeEditor).toHaveBeenCalledWith('ed-1')
    expect(loadDrafts('c1')).toEqual([{ title: 'Query 2', sql: 'SELECT 2' }])
  })

  it('runs the selection, the current statement or everything', async () => {
    const id = st().newQueryTab()
    st().setSql(id, 'SELECT 1;\nSELECT 2;')
    // Selection wins for "current".
    st().setEditorSelection(id, 17, { from: 10, to: 18 })
    await st().runQuery(id, 'current')
    expect(editor.openEditor).toHaveBeenCalledWith('s1')
    expect(editor.execute).toHaveBeenLastCalledWith('ed-1', expect.stringMatching(/^run-/), 'SELECT 2', { maxRows: 1000, continueOnError: false, baseOffset: 10, confirmed: false })
    expect(editor.statementAt).not.toHaveBeenCalled()

    // No selection: the statement under the cursor (from Go).
    editor.statementAt.mockResolvedValueOnce({ text: 'SELECT 1', start: 0, end: 8, delimEnd: 9 })
    st().setEditorSelection(id, 3, { from: 3, to: 3 }) // empty selection
    expect(q().selection).toBeNull()
    st().setMaxRows(id, 50)
    st().setContinueOnError(id, true)
    await st().runQuery(id, 'current')
    expect(editor.statementAt).toHaveBeenCalledWith('postgres', 'SELECT 1;\nSELECT 2;', 3)
    expect(editor.execute).toHaveBeenLastCalledWith('ed-1', expect.any(String), 'SELECT 1', { maxRows: 50, continueOnError: true, baseOffset: 0, confirmed: false })
    expect(editor.openEditor).toHaveBeenCalledTimes(1) // reused

    // All ignores the selection.
    st().setEditorSelection(id, 2, { from: 0, to: 2 })
    await st().runQuery(id, 'all')
    expect(editor.execute).toHaveBeenLastCalledWith('ed-1', expect.any(String), 'SELECT 1;\nSELECT 2;', expect.objectContaining({ baseOffset: 0, confirmed: false }))
    expect(q().result?.statements).toHaveLength(1)
    expect(q().activeResult).toBe('rs-1')
    expect(q().running).toBe(false)
    expect(q().tx).toEqual({ state: 'idle', source: 'server' })

    // Nothing to run.
    editor.execute.mockClear()
    editor.statementAt.mockResolvedValueOnce(null)
    st().setEditorSelection(id, 0, null)
    await st().runQuery(id, 'current')
    st().setSql(id, '   ')
    await st().runQuery(id, 'all')
    await st().runQuery('nope', 'all')
    expect(editor.execute).not.toHaveBeenCalled()
  })

  it('reports errors with positions to the editor', async () => {
    const id = st().newQueryTab('SELEC 1')
    editor.execute.mockResolvedValueOnce(
      execResult([stmt(0, 'SELEC 1', { status: 'error', error: { message: 'syntax error', code: '42601', position: 0 } })]),
    )
    await st().runQuery(id, 'all')
    expect(q().activeResult).toBe(MESSAGES)
    expect(q().editorRequest).toMatchObject({ type: 'error', pos: 0, from: 0, to: 7 })

    // Run-level failures.
    editor.statementAt.mockRejectedValueOnce(new Error('split failed'))
    await st().runQuery(id, 'current')
    expect(q().error).toBe('split failed')
    editor.execute.mockRejectedValueOnce(new Error('editor not found (it was closed)'))
    await st().runQuery(id, 'all')
    expect(q().error).toMatch(/editor not found/)
    expect(q().editorId).toBeNull()
    editor.execute.mockRejectedValueOnce(new Error('boom'))
    await st().runQuery(id, 'all')
    expect(q().error).toBe('boom')
    expect(q().editorId).toBe('ed-1')
  })

  it('cancels and ignores superseded results', async () => {
    const id = st().newQueryTab('SELECT pg_sleep(30)')
    let finish: (v: ReturnType<typeof execResult>) => void = () => {}
    editor.execute.mockImplementationOnce(() => new Promise((r) => (finish = r)))
    const run = st().runQuery(id, 'all')
    await flush()
    expect(q().running).toBe(true)
    await st().runQuery(id, 'all') // already running: ignored
    st().cancelQuery(id)
    expect(editor.cancel).toHaveBeenCalledWith(q().requestId)
    st().cancelQuery('nope')
    finish(execResult([stmt(0, 'x', { status: 'cancelled', error: { message: 'Query cancelled', code: '', position: null } })], { cancelled: true }))
    await run
    expect(q().running).toBe(false)
    expect(q().result?.cancelled).toBe(true)
    expect(q().activeResult).toBe(MESSAGES)

    // Disposed while running: the late result is dropped.
    editor.execute.mockImplementationOnce(() => new Promise((r) => (finish = r)))
    const late = st().runQuery(id, 'all')
    await flush()
    st().dispose()
    expect(editor.cancel).toHaveBeenCalledTimes(2)
    finish(execResult([]))
    await late
    expect(q().running).toBe(true)
  })

  it('loads more rows of a truncated result', async () => {
    const id = st().newQueryTab('SELECT * FROM big')
    editor.execute.mockResolvedValueOnce(execResult([stmt(0, 'x', { resultSets: [resultSet('rs-9', 2, { truncated: true, canLoadMore: true })] })]))
    await st().runQuery(id, 'all')
    editor.fetchMore.mockResolvedValueOnce(resultSet('rs-9', 3, { truncated: true, canLoadMore: true }))
    await st().loadMore(id, 'rs-9')
    expect(editor.fetchMore).toHaveBeenCalledWith('ed-1', expect.stringMatching(/^more-/), 'rs-9', 1000)
    let rs = q().result!.statements[0].resultSets[0]
    expect(rs.rows).toHaveLength(5)
    expect(rs.canLoadMore).toBe(true)
    editor.fetchMore.mockRejectedValueOnce(new Error('this result is closed'))
    await st().loadMore(id, 'rs-9')
    rs = q().result!.statements[0].resultSets[0]
    expect(rs.canLoadMore).toBe(false)
    expect(q().error).toMatch(/closed/)
    expect(q().loadingMore).toBe(false)
    await st().loadMore('nope', 'rs-9') // no-op
  })

  it('switches, sorts and selects results and shows statements', async () => {
    const id = st().newQueryTab('SELECT 1; SELEC')
    editor.execute.mockResolvedValueOnce(
      execResult([
        stmt(0, 'SELECT 1', { start: 0, end: 8, resultSets: [resultSet('a'), resultSet('b')] }),
        stmt(1, 'SELEC', { start: 10, end: 15, status: 'error', error: { message: 'x', code: '1', position: 10 } }),
        stmt(2, 'SELECT 3', { start: 17, end: 25, status: 'skipped' }),
      ]),
    )
    await st().runQuery(id, 'all')
    st().setActiveResult(id, 'b')
    expect(q().activeResult).toBe('b')
    st().selectResultRow(id, 1)
    expect(q().selectedRow).toBe(1)
    st().sortResult(id, 'b', 'name', false)
    expect(q().sorts.b).toEqual([{ column: 'name', desc: false }])
    expect(q().selectedRow).toBeNull()
    st().showStatement(id, 0)
    expect(q().editorRequest).toMatchObject({ type: 'select', from: 0, to: 8 })
    st().showStatement(id, 1)
    expect(q().editorRequest).toMatchObject({ type: 'error', pos: 10 })
    const seq = q().editorRequest!.seq
    st().showStatement(id, 9) // unknown: unchanged
    expect(q().editorRequest!.seq).toBe(seq)
  })

  it('inserts history into the active query tab or a new one', () => {
    st().insertIntoEditor('SELECT 42')
    expect(q().sql).toBe('SELECT 42')
    st().insertIntoEditor('SELECT 43')
    expect(q().editorRequest).toMatchObject({ type: 'insert', text: 'SELECT 43' })
    st().toggleHistory()
    expect(st().historyOpen).toBe(true)
    st().toggleHistory(false)
    expect(st().historyOpen).toBe(false)
  })

  it('loads completion data per schema, lazily', async () => {
    await st().loadCompletion()
    expect(st().completion).toBeNull() // no schema yet
    await st().init()
    await st().loadCompletion()
    expect(editor.completionSchema).toHaveBeenCalledWith('s1', 'public', false)
    expect(st().completion?.tables[0].name).toBe('users')
    await st().loadCompletion() // cached
    expect(editor.completionSchema).toHaveBeenCalledTimes(1)
    await st().loadCompletion(true)
    expect(editor.completionSchema).toHaveBeenLastCalledWith('s1', 'public', true)
    editor.completionSchema.mockRejectedValueOnce(new Error('x'))
    await st().loadCompletion(true)
    expect(st().completionLoading).toBe(false)

    // Columns on demand for big schemas.
    store.setState({ completion: { database: 'shop', schema: 'public', schemas: [], tables: [{ name: 'big', type: 'table', columns: null }], columnsLoaded: false } })
    await st().loadCompletionColumns('big')
    expect(editor.completionColumns).toHaveBeenCalledWith('s1', 'public', 'big')
    expect(st().completion?.tables[0].columns).toEqual([{ name: 'x', type: 'text' }])
    await st().loadCompletionColumns('big') // loaded
    await st().loadCompletionColumns('nope')
    expect(editor.completionColumns).toHaveBeenCalledTimes(1)
    store.setState({ completion: { database: 'shop', schema: 'public', schemas: [], tables: [{ name: 'big', type: 'table', columns: null }], columnsLoaded: false } })
    editor.completionColumns.mockRejectedValueOnce(new Error('x'))
    await st().loadCompletionColumns('big')
    expect(st().completion?.tables[0].columns).toBeNull()

    // Switching schema or database drops it.
    await st().selectSchema('public')
    expect(st().completion).toBeNull()
  })

  it('highlights the current statement', async () => {
    const id = st().newQueryTab('SELECT 1;')
    st().setEditorSelection(id, 3, null)
    await st().updateCurrentStatement(id)
    expect(q().current).toEqual({ from: 0, to: 9 })
    editor.statementAt.mockResolvedValueOnce(null)
    await st().updateCurrentStatement(id)
    expect(q().current).toBeNull()
    editor.statementAt.mockRejectedValueOnce(new Error('x'))
    await st().updateCurrentStatement(id)
    // Stale answers (the cursor moved meanwhile) are dropped.
    let answer: (v: null) => void = () => {}
    editor.statementAt.mockImplementationOnce(() => new Promise((r) => (answer = r)))
    const p = st().updateCurrentStatement(id)
    st().setEditorSelection(id, 5, null)
    st().setSql(id, 'SELECT 1; SELECT 2;')
    answer(null)
    await p
    await st().updateCurrentStatement('nope')
  })

  it('keeps query tabs when the database changes and refreshes completion', async () => {
    await st().init()
    st().openTable({ schema: 'public', name: 'users', type: 'table', estimatedRows: 1 }, true)
    const id = st().newQueryTab('SELECT 1')
    await st().selectDatabase('other')
    expect(st().tabs.map((t) => t.kind)).toEqual(['query'])
    expect(st().activeTabId).toBe(id)
    await st().loadCompletion()
    await st().refresh()
    expect(editor.completionSchema).toHaveBeenLastCalledWith('s1', 'public', true)

    // A table tab active when the database changes: the query tab takes over.
    st().openTable({ schema: 'public', name: 'users', type: 'table', estimatedRows: 1 }, true)
    expect((st().activeTab() as TabState).table).toBe('users')
    await st().selectDatabase('other') // the fake reports "shop" as current
    expect(st().activeTabId).toBe(id)
  })
})
