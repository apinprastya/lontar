import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { WorkspaceApi } from '../features/workspace/api'
import type { EditApi } from '../features/edits/api'
import type { ApplyResult, SafeMode } from '../features/edits/types'
import type { FetchRequest, Page, TableInfo } from '../features/workspace/types'
import { canEdit, createWorkspaceStore, readOnlyReason, type TabState, type WorkspaceStore } from './workspace'
import { emptyPending, pendingCount } from './pending'

const T = (name: string, type: TableInfo['type'] = 'table'): TableInfo => ({ schema: 'public', name, type, estimatedRows: 10 })

function page(req: FetchRequest, over: Partial<Page> = {}): Page {
  return {
    columns: [
      { name: 'id', type: 'integer', isPK: true, nullable: false },
      { name: 'name', type: 'text', isPK: false, nullable: true },
    ],
    rows: [
      [{ k: 'num', v: req.offset + 1 }, { k: 'str', v: 'a' }],
      [{ k: 'num', v: req.offset + 2 }, { k: 'null' }],
    ],
    offset: req.offset,
    limit: req.limit,
    hasMore: true,
    totalCount: null,
    elapsedMs: 1,
    ...over,
  }
}

function fakeApi() {
  return {
    current: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listDatabases: vi.fn(async () => ['other', 'shop']),
    useDatabase: vi.fn(async (_s: string, db: string) => ({ database: db, schema: 'public' })),
    listSchemas: vi.fn(async () => ['public']),
    listTables: vi.fn(async () => [T('users'), T('orders')]),
    fetchTable: vi.fn(async (_s: string, _r: string, req: FetchRequest) => page(req)),
    countRows: vi.fn(async () => 10),
    cancel: vi.fn(async () => true),
  } satisfies WorkspaceApi
}

const ok: ApplyResult = { ok: true, result: { deleted: 0, updated: 1, inserted: 0, insertedKeys: [] }, failure: null }

function fakeEdits() {
  return {
    preview: vi.fn(async () => [{ op: 'update' as const, index: 0, sql: "UPDATE t SET name = 'b' WHERE id = 1;" }]),
    apply: vi.fn(async (): Promise<ApplyResult> => ok),
  } satisfies EditApi
}

const flush = () => new Promise((r) => setTimeout(r, 0))

describe('editing in the workspace store', () => {
  let api: ReturnType<typeof fakeApi>
  let edits: ReturnType<typeof fakeEdits>
  let store: WorkspaceStore
  const st = () => store.getState()
  const tab = () => st().tabs.find((t) => t.kind === 'table' && t.id === st().activeTabId) as TabState
  const make = async (safeMode: SafeMode = 'off') => {
    store = createWorkspaceStore({ id: 's1', kind: 'postgres', connectionId: 'c1', safeMode }, api, undefined, edits)
    await st().init()
    st().openTable(T('users'))
    await flush()
  }
  const editName = (text = 'b', index = 0) => st().stageCells(tab().id, [{ ref: { type: 'row', index }, column: 'name', value: { kind: 'value', text } }])

  beforeEach(async () => {
    api = fakeApi()
    edits = fakeEdits()
    await make()
  })

  it('explains read-only tables', () => {
    const t = tab()
    expect(readOnlyReason(t, 'off')).toBeNull()
    expect(canEdit(t, 'off')).toBe(true)
    expect(readOnlyReason(t, 'readonly')).toMatch(/read-only safe mode/)
    expect(readOnlyReason({ ...t, type: 'view' }, 'off')).toMatch(/views are read-only/)
    expect(readOnlyReason({ ...t, columns: t.columns.map((c) => ({ ...c, isPK: false })) }, 'off')).toMatch(/no primary key/)
    expect(canEdit({ ...t, columns: [] }, 'off')).toBe(false)
  })

  it('stages, undoes, redoes and discards; staging pins the tab', () => {
    expect(tab().pinned).toBe(false)
    editName()
    expect(pendingCount(tab().pending)).toBe(1)
    expect(tab().pinned).toBe(true)
    // A no-op change keeps the state object.
    const before = tab().pending
    st().stageCells(tab().id, [{ ref: { type: 'row', index: 9 }, column: 'name', value: { kind: 'null' } }])
    expect(tab().pending).toBe(before)
    st().addRow(tab().id)
    st().duplicateRow(tab().id, { type: 'row', index: 0 })
    st().duplicateRow(tab().id, { type: 'insert', id: 1 })
    st().duplicateRow(tab().id, { type: 'row', index: 42 })
    expect(tab().pending.inserts.map((r) => r.values)).toEqual([{}, { name: { kind: 'value', text: 'b' } }, {}])
    st().markDeleted(tab().id, [{ type: 'row', index: 1 }])
    expect(pendingCount(tab().pending)).toBe(5)
    st().undoEdit(tab().id)
    expect(pendingCount(tab().pending)).toBe(4)
    st().redoEdit(tab().id)
    expect(pendingCount(tab().pending)).toBe(5)
    st().discardChanges(tab().id)
    expect(tab().pending).toBe(emptyPending)
  })

  it('does not stage on read-only connections', async () => {
    await make('readonly')
    editName()
    st().addRow(tab().id)
    expect(tab().pending).toBe(emptyPending)
    // Commit is refused even if something were staged.
    store.setState({ tabs: st().tabs.map((t) => (t.kind === 'table' ? { ...t, pending: { ...emptyPending, inserts: [{ id: 1, values: {} }] } } : t)) })
    expect(await st().commit(tab().id)).toBe(false)
    expect(tab().commitError).toMatch(/read-only/)
    expect(edits.apply).not.toHaveBeenCalled()
  })

  it('commits: success clears and reloads; Mod+S commits directly in off mode', async () => {
    expect(await st().commit(tab().id)).toBe(true) // nothing staged
    editName()
    const loads = api.fetchTable.mock.calls.length
    await st().requestCommit(tab().id)
    await flush()
    expect(edits.apply).toHaveBeenCalledWith('s1', {
      schema: 'public',
      table: 'users',
      updates: [{ pk: { id: { k: 'num', v: 1 } }, set: { name: { kind: 'value', text: 'b' } }, old: { name: { k: 'str', v: 'a' } } }],
      inserts: [],
      deletes: [],
    })
    expect(tab().pending).toBe(emptyPending)
    expect(api.fetchTable.mock.calls.length).toBe(loads + 1)
    expect(edits.preview).not.toHaveBeenCalled()
    await st().requestCommit(tab().id) // nothing to do
    expect(edits.apply).toHaveBeenCalledTimes(1)
  })

  it('keeps changes and highlights the failing change on failure', async () => {
    editName('b', 0)
    editName('c', 1)
    edits.apply.mockResolvedValueOnce({ ok: false, result: null, failure: { op: 'update', index: 1, kind: 'stale', message: 'update #2 failed: gone.' } })
    expect(await st().commit(tab().id)).toBe(false)
    expect(pendingCount(tab().pending)).toBe(2)
    expect(tab().commitError).toBe('update #2 failed: gone.')
    expect(tab().failed).toEqual({ key: '[["num",2]]' })
    // A new edit clears the highlight.
    editName('d', 0)
    expect(tab().failed).toBeNull()
    edits.apply.mockResolvedValueOnce({ ok: false, result: null, failure: null })
    await st().commit(tab().id)
    expect(tab().commitError).toMatch(/nothing was saved/)
    edits.apply.mockRejectedValueOnce(new Error('session not found'))
    expect(await st().commit(tab().id)).toBe(false)
    expect(tab().commitError).toBe('session not found')
    expect(tab().committing).toBe(false)
  })

  it('previews, and in confirm mode Mod+S opens the preview first', async () => {
    await make('confirm')
    await st().openPreview(tab().id) // nothing staged
    expect(edits.preview).not.toHaveBeenCalled()
    editName()
    await st().requestCommit(tab().id)
    expect(edits.apply).not.toHaveBeenCalled()
    expect(tab().preview).toEqual([{ op: 'update', index: 0, sql: "UPDATE t SET name = 'b' WHERE id = 1;" }])
    st().closePreview(tab().id)
    expect(tab().preview).toBeNull()
    edits.preview.mockRejectedValueOnce(new Error('view'))
    await st().openPreview(tab().id)
    expect(tab().commitError).toBe('view')
    expect(tab().previewLoading).toBe(false)
  })

  it('guards paging, sorting, filters, refresh and closing', async () => {
    editName()
    const id = tab().id
    const loads = () => api.fetchTable.mock.calls.length
    const n = loads()
    st().nextPage(id)
    expect(st().guard?.tabIds).toEqual([id])
    await st().resolveGuard('cancel')
    expect(st().guard).toBeNull()
    expect(loads()).toBe(n)
    for (const act of [
      () => st().prevPage(id),
      () => st().toggleSort(id, 'name', false),
      () => st().applyFilters(id),
      () => st().clearFilters(id),
      () => void st().refresh(),
      () => st().closeTab(id),
    ]) {
      act()
      if (st().guard) await st().resolveGuard('cancel')
    }
    expect(st().tabs.some((t) => t.id === id)).toBe(true)
    expect(loads()).toBe(n)

    // Discard runs the action without saving.
    st().toggleSort(id, 'name', false)
    await st().resolveGuard('discard')
    await flush()
    expect(tab().sort).toEqual([{ column: 'name', desc: false }])
    expect(tab().pending).toBe(emptyPending)
    expect(edits.apply).not.toHaveBeenCalled()

    // Commit saves first, then runs the action.
    editName('z')
    st().nextPage(id)
    await st().resolveGuard('commit')
    await flush()
    expect(edits.apply).toHaveBeenCalledTimes(1)
    expect(tab().offset).toBe(300)

    // A failed commit keeps the changes and cancels the action.
    editName('y')
    edits.apply.mockResolvedValueOnce({ ok: false, result: null, failure: { op: 'update', index: 0, kind: 'database', message: 'boom' } })
    st().closeTab(id)
    await st().resolveGuard('commit')
    expect(st().guard).toBeNull()
    expect(st().tabs.some((t) => t.id === id)).toBe(true)
    expect(tab().commitError).toBe('boom')
    await st().resolveGuard('commit') // no guard: no-op
  })

  it('guards switching databases and runs actions immediately without changes', async () => {
    editName()
    await st().selectDatabase('other')
    expect(api.useDatabase).not.toHaveBeenCalled()
    expect(st().guard).not.toBeNull()
    await st().resolveGuard('discard')
    await flush()
    expect(api.useDatabase).toHaveBeenCalledWith('s1', 'other')
    const proceed = vi.fn()
    st().guarded(['missing'], proceed)
    expect(proceed).toHaveBeenCalled()
    // A busy guard ignores further choices.
    store.setState({ guard: { tabIds: [], proceed, busy: true } })
    await st().resolveGuard('discard')
    expect(proceed).toHaveBeenCalledTimes(1)
  })
})
