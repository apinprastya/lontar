import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { WorkspaceApi } from '../features/workspace/api'
import type { EditApi } from '../features/edits/api'
import type { ApplyResult, ResultSource, SafeMode } from '../features/edits/types'
import { execResult, fakeEditorApi, resultSet, stmt } from '../test/fakeEditorApi'
import { makeDetail } from '../test/structure'
import { handleShortcut } from '../features/workspace/shortcuts'
import { commandAvailable } from '../features/palette/commands'
import { MESSAGES, newResultEdit, resultEditView, sourceColumns, statementOf, type QueryTabState } from './queryTabs'
import { createWorkspaceStore, type WorkspaceStore } from './workspace'
import { pendingCount } from './pending'

function fakeApi(): WorkspaceApi {
  return {
    current: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listDatabases: vi.fn(async () => ['shop', 'other']),
    useDatabase: vi.fn(async (_s: string, db: string) => ({ database: db, schema: 'public' })),
    listSchemas: vi.fn(async () => ['public']),
    listTables: vi.fn(async () => []),
    fetchTable: vi.fn(async (_s, _r, req) => ({ columns: [], rows: [], offset: req.offset, limit: req.limit, hasMore: false, totalCount: 0, elapsedMs: 1 })),
    countRows: vi.fn(async () => 0),
    cancel: vi.fn(async () => true),
    tableInfo: vi.fn(async () => makeDetail()),
    tableDDL: vi.fn(async () => ''),
  }
}

/** users(id PK, name) selected as `SELECT id, name AS who FROM users`. */
const source: ResultSource = {
  editable: true,
  reason: '',
  schema: 'public',
  table: 'users',
  columns: [
    { column: 'id', isPK: true, nullable: false, hasDefault: true },
    { column: 'name', isPK: false, nullable: true, hasDefault: false, enumValues: null },
  ],
}

const aliased = () => resultSet('rs-1', 2, { columns: [{ name: 'id', type: 'int4', isPK: false, nullable: true }, { name: 'who', type: 'text', isPK: false, nullable: true }] })

const ok: ApplyResult = { ok: true, result: { deleted: 0, updated: 1, inserted: 0, insertedKeys: [] }, failure: null }

function fakeEdits(src: ResultSource = source) {
  return {
    preview: vi.fn(async () => [{ op: 'update' as const, index: 0, sql: 'UPDATE …' }]),
    apply: vi.fn(async (): Promise<ApplyResult> => ok),
    resultSource: vi.fn(async (): Promise<ResultSource> => src),
  } satisfies EditApi
}

const flush = () => new Promise((r) => setTimeout(r, 0))
const SQL = 'SELECT id, name AS who FROM users'

describe('result edit helpers', () => {
  it('maps result columns onto the source table', () => {
    const cols = [...aliased().columns, { name: 'who', type: 'text', isPK: false, nullable: true }, { name: 'n', type: 'int8', isPK: false, nullable: true }]
    const src = { ...source, columns: [...source.columns, { column: 'name', isPK: false, nullable: true, hasDefault: false }, { column: '', isPK: false, nullable: true, hasDefault: false }] }
    const { columns, rename } = sourceColumns(cols, src)
    expect(rename).toEqual({ id: 'id', who: 'name' })
    expect(columns.map((c) => [c.isPK, Boolean(c.readOnly)])).toEqual([
      [true, false],
      [false, false],
      [false, true], // a second "who"
      [false, true], // expression
    ])
    expect(columns[0].hasDefault).toBe(true)
    // A read-only source locks everything.
    expect(sourceColumns(cols, { ...src, editable: false }).columns.every((c) => c.readOnly)).toBe(true)
  })

  it('finds a result set statement and builds the display view', () => {
    const rs = aliased()
    const result = execResult([stmt(0, 'SELECT 1'), stmt(1, SQL, { resultSets: [rs] })])
    expect(statementOf(result, 'rs-1')?.index).toBe(1)
    expect(statementOf(result, 'nope')).toBeNull()
    expect(statementOf(null, 'rs-1')).toBeNull()

    const { columns, rename } = sourceColumns(rs.columns, source)
    const edit = { ...newResultEdit('rs-1', rs.columns), loading: false, source, columns, rename }
    edit.pending = { ...edit.pending, inserts: [{ id: 1, values: {} }] }
    const tab = { result, activeResult: 'rs-1', sorts: { 'rs-1': [{ column: 'id', desc: true }] }, edit }
    const v = resultEditView(tab, 'off')!
    expect(v.editable).toBe(true)
    // Sorted fetched rows keep their refs; the staged insert comes last.
    expect(v.rows.map((r) => r.ref)).toEqual([{ type: 'row', index: 1 }, { type: 'row', index: 0 }, { type: 'insert', id: 1 }])
    expect(v.view.rows[0][0].v).toBe(2)
    expect(v.columns[0].isPK).toBe(true)
    expect(resultEditView(tab, 'readonly')!.editable).toBe(false)
    // While loading, the plain result columns are shown and nothing is editable.
    const loading = resultEditView({ ...tab, edit: newResultEdit('rs-1', rs.columns) }, 'off')!
    expect(loading.editable).toBe(false)
    expect(loading.columns).toBe(rs.columns)
    expect(resultEditView({ ...tab, activeResult: MESSAGES }, 'off')).toBeNull()
  })
})

describe('editing query results in the workspace store', () => {
  let store: WorkspaceStore
  let editor: ReturnType<typeof fakeEditorApi>
  let edits: ReturnType<typeof fakeEdits>
  const st = () => store.getState()
  const q = () => st().activeTab() as QueryTabState

  const make = async (safeMode: SafeMode = 'off', src: ResultSource = source) => {
    edits = fakeEdits(src)
    store = createWorkspaceStore({ id: 's1', kind: 'postgres', connectionId: 'c1', safeMode }, fakeApi(), editor, edits)
    const id = st().newQueryTab(SQL)
    await st().runQuery(id, 'all')
    await st().loadResultSource(id)
    return id
  }
  const editWho = (id: string, text = 'zed', index = 0) => st().stageCells(id, [{ ref: { type: 'row', index }, column: 'who', value: { kind: 'value', text } }])

  beforeEach(() => {
    window.localStorage.clear()
    editor = fakeEditorApi()
    editor.execute.mockImplementation(async (_e: string, _r: string, sql: string) => execResult([stmt(0, sql, { resultSets: [aliased()] })]))
  })
  afterEach(() => st().dispose())

  it('looks up the source once per result set', async () => {
    const id = await make()
    expect(edits.resultSource).toHaveBeenCalledWith('s1', SQL, 2)
    expect(q().edit).toMatchObject({ resultId: 'rs-1', loading: false, rename: { id: 'id', who: 'name' } })
    await st().loadResultSource(id)
    expect(edits.resultSource).toHaveBeenCalledTimes(1)
    st().setActiveResult(id, MESSAGES)
    await st().loadResultSource(id) // Messages: nothing to look up
    expect(edits.resultSource).toHaveBeenCalledTimes(1)
    await st().loadResultSource('nope')
  })

  it('stages, previews and commits through the table, then re-runs the statement', async () => {
    const id = await make()
    editWho(id)
    st().addRow(id, { who: { kind: 'value', text: 'new' } })
    expect(pendingCount(q().edit!.pending)).toBe(2)

    await st().openPreview(id)
    const cs = (edits.preview.mock.calls[0] as unknown as [string, import('../features/edits/types').ChangeSet])[1]
    expect(cs).toMatchObject({ schema: 'public', table: 'users' })
    expect(cs.updates[0]).toEqual({ pk: { id: { k: 'num', v: 1 } }, set: { name: { kind: 'value', text: 'zed' } }, old: { name: { k: 'str', v: 'n1' } } })
    expect(cs.inserts[0]).toEqual({ values: { name: { kind: 'value', text: 'new' } } })
    expect(q().edit!.preview).toHaveLength(1)
    st().closePreview(id)
    expect(q().edit!.preview).toBeNull()

    editor.execute.mockClear()
    editor.execute.mockResolvedValueOnce(execResult([stmt(0, SQL, { resultSets: [{ ...aliased(), id: 'rs-2' }] })]))
    await st().requestCommit(id)
    expect(edits.apply).toHaveBeenCalledTimes(1)
    // Only the statement is re-run, at its place in the editor.
    expect(editor.execute).toHaveBeenCalledWith('ed-1', expect.any(String), SQL, expect.objectContaining({ baseOffset: 0, maxRows: q().maxRows }))
    expect(q().activeResult).toBe('rs-2')
    expect(q().edit).toBeNull()
    expect(q().running).toBe(false)
  })

  it('keeps changes and explains failed commits', async () => {
    const id = await make()
    editWho(id)
    edits.apply.mockResolvedValueOnce({ ok: false, result: null, failure: { op: 'update', index: 0, kind: 'stale', message: 'changed meanwhile' } })
    expect(await st().commit(id)).toBe(false)
    expect(q().edit).toMatchObject({ commitError: 'changed meanwhile', failed: { key: expect.any(String) } })
    edits.apply.mockRejectedValueOnce(new Error('boom'))
    expect(await st().commit(id)).toBe(false)
    expect(q().edit!.commitError).toBe('boom')
    edits.preview.mockRejectedValueOnce(new Error('nope'))
    await st().openPreview(id)
    expect(q().edit!.commitError).toBe('nope')
    st().discardChanges(id)
    expect(pendingCount(q().edit!.pending)).toBe(0)
    expect(await st().commit(id)).toBe(true) // nothing to do
  })

  it('refuses to commit while the tab has an open transaction', async () => {
    const id = await make()
    editWho(id)
    store.setState({ tabs: st().tabs.map((t) => (t.id === id ? { ...t, tx: { state: 'active', source: 'server' } } : t)) })
    expect(await st().commit(id)).toBe(false)
    expect(q().edit!.commitError).toMatch(/open transaction/)
    expect(edits.apply).not.toHaveBeenCalled()
  })

  it('opens the preview first in confirm safe mode and edits nothing in read-only mode', async () => {
    let id = await make('confirm')
    editWho(id)
    await st().requestCommit(id)
    expect(edits.apply).not.toHaveBeenCalled()
    expect(q().edit!.preview).not.toBeNull()
    st().dispose()

    id = await make('readonly')
    editWho(id)
    expect(pendingCount(q().edit!.pending)).toBe(0)
    expect(resultEditView(q(), 'readonly')!.editable).toBe(false)
  })

  it('does not stage on read-only results', async () => {
    const id = await make('off', { ...source, editable: false, reason: 'Grouped results are read-only.' })
    editWho(id)
    expect(pendingCount(q().edit!.pending)).toBe(0)
    expect(q().edit!.source!.reason).toMatch(/Grouped/)
  })

  it('asks before a run or a result switch drops staged changes', async () => {
    const id = await make()
    editWho(id)
    editor.execute.mockClear()
    await st().runQuery(id, 'all')
    expect(st().guard).not.toBeNull()
    expect(editor.execute).not.toHaveBeenCalled()
    await st().resolveGuard('cancel')

    // Messages and back keeps the changes; another result set asks.
    st().setActiveResult(id, MESSAGES)
    expect(st().guard).toBeNull()
    st().setActiveResult(id, 'rs-1')
    st().setActiveResult(id, 'rs-9')
    expect(st().guard).not.toBeNull()
    await st().resolveGuard('discard')
    expect(q().activeResult).toBe('rs-9')

    // Closing the tab asks too.
    st().setActiveResult(id, 'rs-1')
    editWho(id)
    st().closeTab(id)
    expect(st().guard?.tabIds).toEqual([id])
  })

  it('saves from the unsaved-changes prompt, then runs', async () => {
    const id = await make()
    editWho(id)
    await st().runQuery(id, 'all')
    await st().resolveGuard('commit')
    await flush()
    expect(edits.apply).toHaveBeenCalledTimes(1)
    expect(st().guard).toBeNull()
    expect(q().running).toBe(false)
  })

  it('reports lookups that cannot run', async () => {
    edits = fakeEdits()
    store = createWorkspaceStore({ id: 's1', kind: 'postgres', connectionId: 'c1' }, fakeApi(), editor, edits)
    const id = st().newQueryTab(SQL)
    edits.resultSource.mockRejectedValueOnce(new Error('meta down'))
    await st().runQuery(id, 'all')
    await st().loadResultSource(id)
    expect(q().edit).toMatchObject({ loading: false, error: 'meta down', source: null })

    // A statement with several result sets (MySQL CALL) is read-only.
    editor.execute.mockResolvedValueOnce(execResult([stmt(0, 'CALL p()', { resultSets: [resultSet('a'), resultSet('b')] })]))
    await st().runQuery(id, 'all')
    await st().loadResultSource(id)
    expect(q().edit!.error).toMatch(/several result sets/)

    // After a database switch the old results cannot be edited.
    await st().runQuery(id, 'all')
    await st().init()
    await st().selectDatabase('other')
    await flush()
    expect(q().resultsStale).toBe(true)
    await st().loadResultSource(id)
    expect(q().edit!.error).toMatch(/database changed/)
  })

  it('handles refresh edge cases', async () => {
    const id = await make()
    await st().refreshResult(id, 'nope') // unknown result
    editor.execute.mockClear()
    editor.execute.mockResolvedValueOnce(execResult([], { needsConfirm: true }))
    await st().refreshResult(id, 'rs-1')
    expect(q().running).toBe(false)
    expect(q().activeResult).toBe('rs-1')
    editor.execute.mockRejectedValueOnce(new Error('gone'))
    await st().refreshResult(id, 'rs-1')
    expect(q().error).toBe('gone')
    // The sort follows the refreshed result.
    st().sortResult(id, 'rs-1', 'id', false)
    editor.execute.mockResolvedValueOnce(execResult([stmt(0, SQL, { resultSets: [{ ...aliased(), id: 'rs-3' }] })]))
    await st().refreshResult(id, 'rs-1')
    expect(q().sorts['rs-3']).toEqual([{ column: 'id', desc: false }])
  })

  it('wires shortcuts and palette commands to an editable result', async () => {
    const id = await make()
    const key = (k: string, shift = false) => ({ key: k, ctrlKey: true, metaKey: false, shiftKey: shift, altKey: false })
    expect(commandAvailable('insertRow', st())).toBe(true)
    expect(commandAvailable('commit', st())).toBe(true)
    expect(handleShortcut(key('n'), 'linux', st())).toBe(true)
    expect(q().edit!.pending.inserts).toHaveLength(1)
    expect(handleShortcut(key('n'), 'linux', st(), true)).toBe(false) // typing in the SQL editor
    expect(handleShortcut(key('z'), 'linux', st())).toBe(true)
    expect(q().edit!.pending.inserts).toHaveLength(0)
    expect(handleShortcut(key('z', true), 'linux', st())).toBe(true)
    expect(q().edit!.pending.inserts).toHaveLength(1)
    expect(handleShortcut(key('s'), 'linux', st())).toBe(true)
    await flush()
    expect(edits.apply).toHaveBeenCalled()

    st().setActiveResult(id, MESSAGES)
    expect(commandAvailable('insertRow', st())).toBe(false)
  })
})
