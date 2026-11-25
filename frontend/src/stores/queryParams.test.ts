import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { WorkspaceApi } from '../features/workspace/api'
import type { ParamValues } from '../features/editor/types'
import { execResult, fakeEditorApi, resultSet, samplePlan, stmt } from '../test/fakeEditorApi'
import { draftKey, loadDrafts, type QueryTabState } from './queryTabs'
import { createWorkspaceStore, type WorkspaceStore } from './workspace'
import { makeDetail } from '../test/structure'

function fakeApi(): WorkspaceApi {
  return {
    current: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listDatabases: vi.fn(async () => ['shop']),
    useDatabase: vi.fn(async (_s: string, db: string) => ({ database: db, schema: 'public' })),
    listSchemas: vi.fn(async () => ['public']),
    listTables: vi.fn(async () => []),
    fetchTable: vi.fn(async (_s, _r, req) => ({ columns: [], rows: [], offset: req.offset, limit: req.limit, hasMore: false, totalCount: 0, elapsedMs: 1 })),
    countRows: vi.fn(async () => 0),
    cancel: vi.fn(async () => true),
    tableInfo: vi.fn(async () => makeDetail()),
    tableDDL: vi.fn(async () => ''),
    listObjects: vi.fn(async () => []),
    objectDDL: vi.fn(async () => ''),
  }
}

const values: ParamValues = {
  a: { value: '1', null: false, type: 'number' },
  b: { value: '', null: true, type: 'text' },
}

describe('query parameters in query tabs', () => {
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

  it('asks for the values, runs with them and keeps them with the draft', async () => {
    const id = st().newQueryTab('SELECT :a, :b, :a')
    await st().runQuery(id, 'all')
    expect(editor.paramNames).toHaveBeenCalledWith('postgres', 'SELECT :a, :b, :a')
    expect(editor.execute).not.toHaveBeenCalled()
    expect(q().paramsPrompt).toEqual({ names: ['a', 'b'], text: 'SELECT :a, :b, :a', base: 0, action: 'run' })

    await st().submitParams(id, values)
    expect(editor.execute).toHaveBeenCalledWith('ed-1', expect.any(String), 'SELECT :a, :b, :a', expect.objectContaining({ params: values, confirmed: false }))
    expect(q()).toMatchObject({ paramsPrompt: null, params: values, running: false })
    expect(loadDrafts('c1')).toEqual([{ title: 'Query 1', sql: 'SELECT :a, :b, :a', params: values }])

    // The values come back with the tab.
    const again = createWorkspaceStore({ id: 's2', kind: 'postgres', connectionId: 'c1' }, fakeApi(), editor)
    expect((again.getState().tabs[0] as QueryTabState).params).toEqual(values)
    again.getState().dispose()

    // Asking again; Cancel runs nothing.
    editor.execute.mockClear()
    await st().runQuery(id, 'all')
    expect(q().paramsPrompt?.names).toEqual(['a', 'b'])
    st().cancelParams(id)
    expect(q().paramsPrompt).toBeNull()
    await st().submitParams(id, values) // no prompt: ignored
    expect(editor.execute).not.toHaveBeenCalled()
  })

  it('runs without asking when there are no placeholders', async () => {
    const id = st().newQueryTab('SELECT 1')
    await st().runQuery(id, 'all')
    expect(q().paramsPrompt).toBeNull()
    expect((editor.execute.mock.calls[0] as unknown[])[3]).not.toHaveProperty('params')
  })

  it('shows a failed placeholder lookup', async () => {
    const id = st().newQueryTab('SELECT :a')
    editor.paramNames.mockRejectedValueOnce(new Error('boom'))
    await st().runQuery(id, 'all')
    expect(q().error).toBe('boom')
    expect(editor.execute).not.toHaveBeenCalled()
    editor.paramNames.mockRejectedValueOnce(new Error('again'))
    await st().explainQuery(id, false)
    expect(q().error).toBe('again')
  })

  it('asks when the backend reports missing values', async () => {
    const id = st().newQueryTab('SELECT 1')
    editor.execute.mockResolvedValueOnce(execResult([], { needsParams: ['x'] }))
    await st().runQuery(id, 'all')
    expect(q()).toMatchObject({ running: false, paramsPrompt: { names: ['x'], action: 'run' } })
  })

  it('keeps the values through the safe-mode confirmation', async () => {
    const id = st().newQueryTab('UPDATE t SET b = :b WHERE a = :a')
    await st().runQuery(id, 'all')
    editor.execute.mockResolvedValueOnce(execResult([], { needsConfirm: true, writeStatements: ['UPDATE t SET b = :b WHERE a = :a'] }))
    await st().submitParams(id, values)
    expect(q().confirm).toMatchObject({ statements: ['UPDATE t SET b = :b WHERE a = :a'], params: values })
    await st().confirmRun(id)
    expect(editor.execute).toHaveBeenLastCalledWith('ed-1', expect.any(String), 'UPDATE t SET b = :b WHERE a = :a', expect.objectContaining({ params: values, confirmed: true }))
  })

  it('re-binds the values when a result is refreshed', async () => {
    const id = st().newQueryTab('SELECT * FROM t WHERE a = :a')
    await st().runQuery(id, 'all')
    await st().submitParams(id, values)
    editor.execute.mockResolvedValueOnce(execResult([stmt(0, 'SELECT * FROM t WHERE a = :a', { resultSets: [resultSet('rs-2')] })]))
    await st().refreshResult(id, 'rs-1')
    expect(editor.execute).toHaveBeenLastCalledWith('ed-1', expect.any(String), 'SELECT * FROM t WHERE a = :a', expect.objectContaining({ params: values }))
    expect(q().running).toBe(false)
  })

  it('asks for the values of Explain', async () => {
    const id = st().newQueryTab('SELECT :a')
    await st().explainQuery(id, true)
    expect(editor.explain).not.toHaveBeenCalled()
    expect(q().paramsPrompt).toEqual({ names: ['a'], text: 'SELECT :a', base: 0, action: 'explain', analyze: true })
    await st().submitParams(id, values)
    expect(editor.explain).toHaveBeenCalledWith('ed-1', expect.any(String), 'SELECT :a', { analyze: true, baseOffset: 0, params: values })
    expect(q().plan?.analyze).toBe(true)

    // The backend asks too (a fallback).
    editor.explain.mockResolvedValueOnce(samplePlan({ needsParams: ['z'] }))
    await st().explainQuery(id, false)
    await st().submitParams(id, values)
    expect(q().paramsPrompt).toMatchObject({ names: ['z'], action: 'explain', analyze: false })

    // Without placeholders Explain runs directly.
    st().cancelParams(id)
    st().setSql(id, 'SELECT 1')
    await st().explainQuery(id, false)
    expect(editor.explain).toHaveBeenLastCalledWith('ed-1', expect.any(String), 'SELECT 1', { analyze: false, baseOffset: 0 })
  })

  it('loads only valid parameter values from drafts', () => {
    window.localStorage.setItem(
      draftKey('c2'),
      JSON.stringify([
        { title: 'a', sql: 'x', params: { p: { value: 'v', null: false, type: 'number' }, q: { null: true, type: 'weird' }, r: 5, s: null } },
        { title: 'b', sql: 'y', params: ['not', 'a', 'map'] },
        { title: 'c', sql: 'z', params: { only: 'junk' } },
      ]),
    )
    expect(loadDrafts('c2')).toEqual([
      { title: 'a', sql: 'x', params: { p: { value: 'v', null: false, type: 'number' }, q: { value: '', null: true, type: 'text' } } },
      { title: 'b', sql: 'y' },
      { title: 'c', sql: 'z' },
    ])
  })
})
