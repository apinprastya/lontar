import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { WorkspaceApi } from '../features/workspace/api'
import { execResult, fakeEditorApi, resultSet, samplePlan, stmt } from '../test/fakeEditorApi'
import { getUIState } from '../lib/uiState'
import {
  HISTORY_OPEN_KEY,
  MESSAGES,
  PLAN,
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
import { makeDetail } from '../test/structure'
import { useSettings } from './settings'
import { DEFAULT_SETTINGS } from '../features/settings/types'

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
    tableInfo: vi.fn(async () => makeDetail()),
    tableDDL: vi.fn(async () => ''),
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
    expect(getUIState(HISTORY_OPEN_KEY)).toBe('true')
    // The next workspace opens with the history panel as it was left.
    const next = createWorkspaceStore({ id: 's9', kind: 'postgres' }, fakeApi(), fakeEditorApi())
    expect(next.getState().historyOpen).toBe(true)
    next.getState().dispose()
    st().toggleHistory(false)
    expect(st().historyOpen).toBe(false)
    expect(getUIState(HISTORY_OPEN_KEY)).toBe('false')
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

describe('explain in query tabs', () => {
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

  it('explains the statement under the cursor and shows the Plan tab', async () => {
    const id = st().newQueryTab('SELECT 1;\nSELECT 2')
    st().setEditorSelection(id, 0, null)
    editor.statementAt.mockResolvedValueOnce({ text: 'SELECT 2', start: 10, end: 18, delimEnd: 18 })
    await st().explainQuery(id, false)
    expect(editor.openEditor).toHaveBeenCalledWith('s1')
    expect(editor.explain).toHaveBeenCalledWith('ed-1', expect.stringMatching(/^explain-/), 'SELECT 2', { analyze: false, baseOffset: 10 })
    expect(q()).toMatchObject({ running: false, requestId: null, activeResult: PLAN, editorId: 'ed-1', ranSql: null })
    expect(q().plan).toMatchObject({ analyze: false, sql: 'SELECT 2' })
    expect(q().plan?.plan.sql).toBe('EXPLAIN SELECT 2')
    expect(q().tx).toEqual({ state: 'idle', source: 'server' })

    // Analyze of the selection reuses the editor.
    st().setEditorSelection(id, 0, { from: 0, to: 8 })
    await st().explainQuery(id, true)
    expect(editor.openEditor).toHaveBeenCalledTimes(1)
    expect(editor.explain).toHaveBeenLastCalledWith('ed-1', expect.any(String), 'SELECT 1', { analyze: true, baseOffset: 0 })
    expect(q().plan?.analyze).toBe(true)

    // A run afterwards keeps the plan but shows its results.
    editor.execute.mockResolvedValueOnce(execResult([stmt(0, 'SELECT 1', { resultSets: [resultSet('rs-1', 2, { truncated: true, canLoadMore: true })] })]))
    await st().runQuery(id, 'current')
    expect(q().plan).not.toBeNull()
    expect(q().activeResult).toBe('rs-1')
    // Explain closes the open cursor of a truncated result (same connection).
    await st().explainQuery(id, false)
    expect(q().result?.statements[0].resultSets[0]).toMatchObject({ truncated: true, canLoadMore: false })
    st().setActiveResult(id, PLAN)
    expect(q().activeResult).toBe(PLAN)
  })

  it('jumps to an error position', async () => {
    const id = st().newQueryTab('  SELEC 1')
    editor.statementAt.mockResolvedValueOnce({ text: 'SELEC 1', start: 2, end: 9, delimEnd: 9 })
    editor.explain.mockResolvedValueOnce(samplePlan({ nodes: [], error: { message: 'syntax error', code: '42601', position: 2 } }))
    await st().explainQuery(id, false)
    expect(q().editorRequest).toMatchObject({ type: 'error', pos: 2, from: 2, to: 9 })
    expect(q().plan?.plan.error?.message).toBe('syntax error')
  })

  it('does nothing without a statement, while running or for unknown tabs', async () => {
    const id = st().newQueryTab('   ')
    editor.statementAt.mockResolvedValueOnce(null)
    await st().explainQuery(id, false)
    st().setSql(id, '   ')
    st().setEditorSelection(id, 0, { from: 0, to: 2 })
    await st().explainQuery(id, false)
    await st().explainQuery('nope', false)
    expect(editor.explain).not.toHaveBeenCalled()
    editor.statementAt.mockRejectedValueOnce(new Error('split failed'))
    st().setSql(id, 'SELECT 1')
    st().setEditorSelection(id, 0, null)
    await st().explainQuery(id, false)
    expect(q().error).toBe('split failed')
  })

  it('reports failures and reopens a lost editor next time', async () => {
    const id = st().newQueryTab('UPDATE t SET a = 1')
    editor.explain.mockRejectedValueOnce(new Error('Explain analyze runs the statement, so it only runs reads.'))
    await st().explainQuery(id, true)
    expect(q()).toMatchObject({ running: false, error: 'Explain analyze runs the statement, so it only runs reads.', editorId: 'ed-1', plan: null })
    editor.explain.mockRejectedValueOnce(new Error('editor not found (it was closed)'))
    await st().explainQuery(id, false)
    expect(q()).toMatchObject({ editorId: null, tx: null })
  })

  it('can be cancelled and ignores late answers', async () => {
    const id = st().newQueryTab('SELECT pg_sleep(10)')
    let resolve: (p: ReturnType<typeof samplePlan>) => void = () => {}
    editor.explain.mockImplementationOnce(() => new Promise((r) => (resolve = r)))
    const running = st().explainQuery(id, true)
    await new Promise((r) => setTimeout(r, 0))
    expect(q().running).toBe(true)
    await st().explainQuery(id, false) // already running: ignored
    st().cancelQuery(id)
    expect(editor.cancel).toHaveBeenCalledWith(expect.stringMatching(/^explain-/))
    resolve(samplePlan({ cancelled: true, nodes: [], error: { message: 'Query cancelled', code: '', position: null } }))
    await running
    expect(q().plan?.plan.cancelled).toBe(true)
    expect(editor.explain).toHaveBeenCalledTimes(1)

    // A disposed workspace ignores the answer.
    editor.explain.mockImplementationOnce(() => new Promise((r) => (resolve = r)))
    const late = st().explainQuery(id, false)
    await new Promise((r) => setTimeout(r, 0))
    st().dispose()
    resolve(samplePlan())
    await late
    expect(q().running).toBe(true)
  })

  it('ignores a late failure after the workspace is gone', async () => {
    const id = st().newQueryTab('SELECT 1')
    let reject: (e: Error) => void = () => {}
    editor.explain.mockImplementationOnce(() => new Promise((_r, j) => (reject = j)))
    const late = st().explainQuery(id, false)
    await new Promise((r) => setTimeout(r, 0))
    st().dispose()
    reject(new Error('boom'))
    await late
    expect(q().error).toBeNull()
  })
})

describe('format in query tabs', () => {
  let store: WorkspaceStore
  const st = () => store.getState()
  const q = () => st().activeTab() as QueryTabState

  beforeEach(() => {
    window.localStorage.clear()
    useSettings.setState({ settings: DEFAULT_SETTINGS })
    store = createWorkspaceStore({ id: 's1', kind: 'mysql', connectionId: 'c1' }, fakeApi(), fakeEditorApi())
  })
  afterEach(() => {
    st().dispose()
    useSettings.setState({ settings: DEFAULT_SETTINGS })
  })

  it('formats the whole script, or the selection, with the settings', async () => {
    const id = st().newQueryTab('select a from t\n')
    await st().formatQuery(id)
    expect(q().editorRequest).toMatchObject({ type: 'replace', from: 0, to: 16, expect: 'select a from t\n', text: 'SELECT\n  a\nFROM\n  t\n', select: false })
    expect(q().error).toBeNull()

    useSettings.setState({ settings: { ...DEFAULT_SETTINGS, sqlFormat: { keywordCase: 'lower', indent: 4 } } })
    st().setSql(id, 'x; SELECT b FROM u; y')
    st().setEditorSelection(id, 18, { from: 3, to: 18 })
    await st().formatQuery(id)
    expect(q().editorRequest).toMatchObject({ type: 'replace', from: 3, to: 18, text: 'select\n    b\nfrom\n    u', select: true })
  })

  it('uses the defaults for settings without format options', async () => {
    const { sqlFormat: _omit, ...old } = DEFAULT_SETTINGS
    useSettings.setState({ settings: old as typeof DEFAULT_SETTINGS })
    const id = st().newQueryTab('select 1')
    await st().formatQuery(id)
    expect(q().editorRequest).toMatchObject({ text: 'SELECT\n  1' })
  })

  it('shows why formatting failed and leaves the text alone', async () => {
    const id = st().newQueryTab('DELIMITER //\nselect 1//\n')
    await st().formatQuery(id)
    expect(q().error).toMatch(/DELIMITER/)
    expect(q().editorRequest).toBeNull()
    expect(q().sql).toBe('DELIMITER //\nselect 1//\n')
  })

  it('does nothing for formatted text, unknown or closed tabs', async () => {
    const id = st().newQueryTab('SELECT\n  1')
    await st().formatQuery(id)
    expect(q().editorRequest).toBeNull()
    await st().formatQuery('nope')
    const p = st().formatQuery(id)
    st().closeTabNow(id)
    await p
    expect(st().tabs).toHaveLength(0)
  })
})
