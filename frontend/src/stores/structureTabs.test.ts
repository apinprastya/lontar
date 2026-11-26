import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { WorkspaceApi } from '../features/workspace/api'
import { fakeStructureApi } from '../test/fakeStructureApi'
import { editableDetail } from '../test/structure'
import { addColumn, toggleDropColumn, updateColumn } from './structureEdits'
import { localFailure, structureDirty } from './structureTabs'
import { createWorkspaceStore, type TabState, type WorkspaceStore } from './workspace'
import { emptyPending } from './pending'

function fakeApi(): WorkspaceApi {
  return {
    current: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listDatabases: vi.fn(async () => ['shop']),
    useDatabase: vi.fn(async (_s: string, db: string) => ({ database: db, schema: 'public' })),
    listSchemas: vi.fn(async () => ['public']),
    listTables: vi.fn(async () => [{ schema: 'public', name: 'users', type: 'table' as const, estimatedRows: 1 }]),
    fetchTable: vi.fn(async (_s, _r, req) => ({ columns: [], rows: [], offset: req.offset, limit: req.limit, hasMore: false, totalCount: 0, elapsedMs: 1 })),
    countRows: vi.fn(async () => 0),
    cancel: vi.fn(async () => true),
    tableInfo: vi.fn(async (_s, schema, table) => ({ ...editableDetail(), schema, name: table })),
    tableDDL: vi.fn(async () => 'CREATE TABLE users ()'),
    listObjects: vi.fn(async () => []),
    objectDDL: vi.fn(async () => ''),
  }
}

const flush = () => new Promise((r) => setTimeout(r, 0))

describe('structure editing in the workspace store', () => {
  let store: WorkspaceStore
  let api: WorkspaceApi
  let structure: ReturnType<typeof fakeStructureApi>
  const st = () => store.getState()
  const tab = () => st().tabs.find((t): t is TabState => t.kind === 'table') as TabState

  beforeEach(async () => {
    api = fakeApi()
    structure = fakeStructureApi()
    store = createWorkspaceStore({ id: 's1', kind: 'postgres', connectionId: 'c1', safeMode: 'off' }, api, undefined, undefined, undefined, structure)
    await st().init()
  })
  afterEach(() => st().dispose())

  const openEditing = async () => {
    await st().editTableStructure('public', 'users')
    await flush()
    return tab().id
  }

  it('stages, previews and applies a change, then reloads', async () => {
    const id = await openEditing()
    expect(tab().view).toBe('structure')
    expect(tab().structureEdit?.draft.name).toBe('users')
    expect(st().structureCaps?.flavor).toBe('postgres')
    expect(structureDirty(tab(), st().structureCaps)).toBe(false)
    // Nothing to preview yet.
    await st().previewStructure(id)
    expect(structure.preview).not.toHaveBeenCalled()

    st().stageStructure(id, (d) => addColumn(d, { name: 'age', type: 'integer' }))
    st().stageStructure(id, (d) => d) // no change: no undo step
    expect(tab().structureEdit?.undo).toHaveLength(1)
    expect(structureDirty(tab(), st().structureCaps)).toBe(true)

    await st().previewStructure(id)
    expect(structure.preview).toHaveBeenCalledWith('s1', expect.objectContaining({ table: 'users', columns: [expect.objectContaining({ op: 'add', name: 'age' })] }))
    expect(tab().structureEdit?.preview).toEqual([{ sql: '-- ALTER users', section: 'table', index: -1 }])
    st().closeStructurePreview(id)
    expect(tab().structureEdit?.preview).toBeNull()

    vi.mocked(api.fetchTable).mockClear()
    expect(await st().applyStructure(id)).toBe(true)
    expect(structure.apply).toHaveBeenCalledWith('s1', expect.objectContaining({ base: expect.objectContaining({ name: 'users' }) }))
    expect(tab().structureEdit).toBeNull()
    expect(api.fetchTable).toHaveBeenCalled()
    expect(tab().structure).not.toBeNull()
  })

  it('undoes and redoes the draft', async () => {
    const id = await openEditing()
    st().undoStructure(id) // nothing to undo
    st().redoStructure(id)
    st().stageStructure(id, (d) => updateColumn(d, 'c:email', { name: 'mail' }))
    st().undoStructure(id)
    expect(tab().structureEdit?.draft.columns[1].name).toBe('email')
    st().redoStructure(id)
    expect(tab().structureEdit?.draft.columns[1].name).toBe('mail')
    // Stage on a tab that is not being edited: ignored.
    st().cancelStructureEdit(id)
    st().stageStructure(id, (d) => toggleDropColumn(d, 'c:id'))
    expect(tab().structureEdit).toBeNull()
    st().cancelStructureEdit(id)
  })

  it('shows refused and failed changes on the row they are about', async () => {
    const id = await openEditing()
    st().stageStructure(id, (d) => updateColumn(d, 'c:email', { type: 'bad;' }))
    structure.preview.mockResolvedValueOnce({ statements: [], failure: { kind: 'invalid', section: 'columns', index: 0, field: 'type', message: 'not a type', sql: '', applied: 0 } })
    await st().previewStructure(id)
    expect(tab().structureEdit).toMatchObject({ preview: null, failureKey: 'c:email', failure: { field: 'type' } })
    structure.preview.mockRejectedValueOnce(new Error('offline'))
    await st().previewStructure(id)
    expect(tab().structureEdit?.failure?.message).toBe('offline')

    structure.apply.mockResolvedValueOnce({ ok: false, statements: [], failure: { kind: 'structure_changed', section: 'table', index: -1, field: '', message: 'changed', sql: '', applied: 0 } })
    expect(await st().applyStructure(id)).toBe(false)
    expect(tab().structureEdit).toMatchObject({ applying: false, failureKey: 'table' })
    structure.apply.mockResolvedValueOnce({ ok: false, statements: [], failure: null })
    expect(await st().applyStructure(id)).toBe(false)
    expect(tab().structureEdit?.failure?.message).toBe('The change failed.')
    structure.apply.mockRejectedValueOnce(new Error('boom'))
    expect(await st().applyStructure(id)).toBe(false)
    expect(tab().structureEdit?.failure?.message).toBe('boom')
    // Editing again clears the failure.
    st().stageStructure(id, (d) => updateColumn(d, 'c:email', { type: 'text' }))
    expect(tab().structureEdit?.failure).toBeNull()
    // Applying with nothing changed just leaves edit mode.
    expect(await st().applyStructure(id)).toBe(true)
    expect(tab().structureEdit).toBeNull()
    expect(await st().applyStructure(id)).toBe(false)
  })

  it('refuses to apply over staged row edits', async () => {
    const id = await openEditing()
    st().stageStructure(id, (d) => addColumn(d, { name: 'x', type: 'text' }))
    store.setState({ tabs: st().tabs.map((t) => (t.id === id ? { ...t, pending: { ...emptyPending, inserts: [{ id: 1, values: {} }] } } : t)) })
    expect(await st().applyStructure(id)).toBe(false)
    expect(tab().structureEdit?.failure?.message).toMatch(/staged row changes/)
    expect(structure.apply).not.toHaveBeenCalled()
  })

  it('guards closing and refreshing a tab with a draft', async () => {
    const id = await openEditing()
    st().stageStructure(id, (d) => addColumn(d, { name: 'x', type: 'text' }))
    st().closeTab(id)
    expect(st().guard?.tabIds).toEqual([id])
    await st().resolveGuard('cancel')
    await st().refresh()
    expect(st().guard?.tabIds).toEqual([id])
    // Discard drops the draft and refreshes.
    await st().resolveGuard('discard')
    await flush()
    expect(tab().structureEdit).toBeNull()
    // Commit applies it.
    await st().startStructureEdit(id)
    st().stageStructure(id, (d) => addColumn(d, { name: 'y', type: 'text' }))
    st().closeTab(id)
    await st().resolveGuard('commit')
    expect(structure.apply).toHaveBeenCalledTimes(1)
    expect(st().tabs.some((t) => t.id === id)).toBe(false)
    // A failed apply keeps the tab.
    await openEditing()
    const id2 = tab().id
    st().stageStructure(id2, (d) => addColumn(d, { name: 'z', type: 'text' }))
    structure.apply.mockResolvedValueOnce({ ok: false, statements: [], failure: localFailure('no') })
    st().closeTab(id2)
    await st().resolveGuard('commit')
    expect(st().tabs.some((t) => t.id === id2)).toBe(true)
    expect(st().activeTabId).toBe(id2)
    // A refresh without changes leaves edit mode.
    st().cancelStructureEdit(id2)
    await st().startStructureEdit(id2)
    await st().refresh()
    expect(tab().structureEdit).toBeNull()
  })

  it('creates a table in a new tab', async () => {
    await st().newTable()
    const t = st().activeTab() as TabState
    expect(t).toMatchObject({ creating: true, view: 'structure', schema: 'public', table: '' })
    expect(structureDirty(t, st().structureCaps)).toBe(true)
    // A new table cannot switch to data or refresh its rows.
    st().setTabView(t.id, 'data')
    expect(tab().view).toBe('structure')
    await st().refresh()
    st().stageStructure(t.id, (d) => ({ ...d, name: 'orgs' }))
    expect(await st().applyStructure(t.id)).toBe(true)
    expect(structure.apply).toHaveBeenCalledWith('s1', expect.objectContaining({ create: true, table: 'orgs', base: null }))
    expect(tab()).toMatchObject({ creating: false, table: 'orgs', structureEdit: null })
    // Discarding a new table closes its tab.
    await st().newTable()
    const n = st().activeTab() as TabState
    st().cancelStructureEdit(n.id)
    expect(st().tabs.some((x) => x.id === n.id)).toBe(false)
  })

  it('caches capabilities and reports a failure', async () => {
    structure.capabilities.mockRejectedValueOnce(new Error('no caps'))
    expect(await st().loadStructureCaps()).toBeNull()
    expect(st().structureCapsError).toBe('no caps')
    await st().newTable()
    expect(st().tabs.some((t) => t.kind === 'table')).toBe(true)
    const caps = await st().loadStructureCaps()
    expect(await st().loadStructureCaps()).toBe(caps)
    expect(structure.capabilities).toHaveBeenCalledTimes(2)
  })

  it('drops tables with a preview, cascade and failures', async () => {
    st().openTableView('public', 'users', 'data')
    await st().requestDropTable('public', 'users')
    expect(st().dropTable).toMatchObject({ statements: [{ sql: 'DROP TABLE public.users' }], loading: false })
    await st().setDropCascade(true)
    expect(st().dropTable?.statements[0].sql).toBe('DROP TABLE public.users CASCADE')
    await st().setDropCascade(true) // unchanged
    expect(structure.dropTablePreview).toHaveBeenCalledTimes(2)
    structure.dropTable.mockResolvedValueOnce({ ok: false, statements: [], failure: localFailure('in use', 'database') })
    expect(await st().confirmDropTable()).toBe(false)
    expect(st().dropTable?.failure?.message).toBe('in use')
    structure.dropTable.mockResolvedValueOnce({ ok: false, statements: [], failure: null })
    expect(await st().confirmDropTable()).toBe(false)
    structure.dropTable.mockRejectedValueOnce(new Error('gone'))
    expect(await st().confirmDropTable()).toBe(false)
    expect(st().dropTable?.failure?.message).toBe('gone')
    expect(await st().confirmDropTable()).toBe(true)
    expect(st().dropTable).toBeNull()
    expect(st().tabs.some((t) => t.kind === 'table')).toBe(false)
    expect(await st().confirmDropTable()).toBe(false)
    await st().setDropCascade(false)

    structure.dropTablePreview.mockRejectedValueOnce(new Error('denied'))
    await st().requestDropTable('public', 'users')
    expect(st().dropTable?.failure?.message).toBe('denied')
    st().cancelDropTable()
    expect(st().dropTable).toBeNull()
  })

  it('does nothing in read-only safe mode', async () => {
    const ro = createWorkspaceStore({ id: 's2', kind: 'postgres', safeMode: 'readonly' }, api, undefined, undefined, undefined, structure)
    await ro.getState().init()
    await ro.getState().newTable()
    await ro.getState().requestDropTable('public', 'users')
    await ro.getState().editTableStructure('public', 'users')
    await flush()
    expect(ro.getState().dropTable).toBeNull()
    expect(ro.getState().tabs.every((t) => t.kind !== 'table' || !t.structureEdit)).toBe(true)
    ro.getState().dispose()
  })

  it('does not edit views', async () => {
    vi.mocked(api.tableInfo).mockResolvedValue({ ...editableDetail(), type: 'view' })
    await st().editTableStructure('public', 'users')
    await flush()
    expect(tab().structureEdit).toBeNull()
  })
})
