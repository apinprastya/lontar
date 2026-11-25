import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../lib/clipboard', () => ({ copyText: vi.fn(async () => {}) }))

import { copyText } from '../../lib/clipboard'
import { WorkspaceContext, createWorkspaceStore, useWorkspace, type TabState, type WorkspaceStore } from '../../stores/workspace'
import { fakeStructureApi } from '../../test/fakeStructureApi'
import { editableDetail, makeColumn } from '../../test/structure'
import type { WorkspaceApi } from '../workspace/api'
import type { Caps } from './editTypes'
import StructureView from './StructureView'
import { triggerTemplate } from './StructureEditor'
import { makeCaps } from '../../test/structure'

const tick = () => new Promise((r) => setTimeout(r, 0))

function wsApi(detail = editableDetail()): WorkspaceApi {
  return {
    current: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listDatabases: vi.fn(async () => ['shop']),
    useDatabase: vi.fn(),
    listSchemas: vi.fn(async () => ['public']),
    listTables: vi.fn(async () => [
      { schema: 'public', name: 'users', type: 'table' as const, estimatedRows: 1 },
      { schema: 'public', name: 'orgs', type: 'table' as const, estimatedRows: 1 },
    ]),
    fetchTable: vi.fn(async (_s, _r, req) => ({ columns: [], rows: [], offset: req.offset, limit: req.limit, hasMore: false, totalCount: 0, elapsedMs: 1 })),
    countRows: vi.fn(async () => 0),
    cancel: vi.fn(async () => true),
    tableInfo: vi.fn(async (_s, _schema, table) => (table === 'orgs' ? { ...editableDetail(), name: 'orgs', columns: [makeColumn({ name: 'id' }), makeColumn({ name: 'code' })] } : detail)),
    tableDDL: vi.fn(async () => ''),
    listObjects: vi.fn(async () => []),
    objectDDL: vi.fn(async () => ''),
  }
}

describe('StructureEditor', () => {
  let store: WorkspaceStore
  let structure: ReturnType<typeof fakeStructureApi>
  const tab = () => store.getState().tabs.find((t): t is TabState => t.kind === 'table') as TabState
  const draft = () => tab().structureEdit!.draft

  function Live() {
    const t = useWorkspace((s) => s.tabs.find((x): x is TabState => x.id === s.activeTabId && x.kind === 'table') ?? null)
    return t ? <StructureView tab={t} /> : null
  }

  async function mount(caps: Partial<Caps> = {}, detail = editableDetail()) {
    structure = fakeStructureApi(caps)
    store = createWorkspaceStore({ id: 's1', kind: 'postgres', safeMode: 'off' }, wsApi(detail), undefined, undefined, undefined, structure)
    await store.getState().init()
    store.getState().openTableView('public', 'users', 'structure')
    render(
      <WorkspaceContext.Provider value={store}>
        <Live />
      </WorkspaceContext.Provider>,
    )
    await act(tick)
  }

  async function edit(caps: Partial<Caps> = {}, detail = editableDetail()) {
    await mount(caps, detail)
    fireEvent.click(screen.getByRole('button', { name: 'Edit structure' }))
    await act(tick)
    return screen.getByTestId('structure-editor')
  }

  beforeEach(() => vi.mocked(copyText).mockClear())
  afterEach(() => store?.getState().dispose())

  it('edits columns and applies them after the preview', async () => {
    await edit()
    expect(screen.getByRole('button', { name: 'Done' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Apply changes…' })).toBeDisabled()

    fireEvent.change(screen.getByRole('textbox', { name: 'Name of email' }), { target: { value: 'mail' } })
    expect(draft().columns[1].name).toBe('mail')
    fireEvent.change(screen.getByRole('combobox', { name: 'Type of mail' }), { target: { value: 'varchar(10)' } })
    fireEvent.change(screen.getByRole('textbox', { name: 'USING for mail' }), { target: { value: 'left(email, 10)' } })
    fireEvent.click(screen.getByRole('checkbox', { name: 'mail is nullable' }))
    fireEvent.change(screen.getByRole('combobox', { name: 'Default of mail' }), { target: { value: 'value' } })
    fireEvent.change(screen.getByRole('textbox', { name: 'Default value of mail' }), { target: { value: 'x' } })
    fireEvent.change(screen.getByRole('textbox', { name: 'Comment of mail' }), { target: { value: '' } })
    fireEvent.click(screen.getByRole('checkbox', { name: 'mail auto-increments' }))
    expect(draft().columns[1]).toMatchObject({ type: 'varchar(10)', using: 'left(email, 10)', nullable: true, defaultMode: 'value', defaultText: 'x', autoIncrement: true })
    expect(screen.getByTestId('column-c:email')).toHaveClass('struct-row-modified')

    // Keys, new and dropped columns.
    fireEvent.click(screen.getByRole('button', { name: 'Primary key: mail' }))
    expect(draft().pk).toEqual(['c:id', 'c:email'])
    fireEvent.click(screen.getByRole('button', { name: 'Add column' }))
    fireEvent.change(screen.getByRole('textbox', { name: 'Name of column 5' }), { target: { value: 'g' } })
    fireEvent.change(screen.getByRole('combobox', { name: 'Default of g' }), { target: { value: 'generatedStored' } })
    fireEvent.change(screen.getByRole('textbox', { name: 'Generation expression of g' }), { target: { value: 'id * 3' } })
    expect(screen.getByTestId('column-n:1')).toHaveClass('struct-row-new')
    fireEvent.click(screen.getByRole('button', { name: 'Drop org_id' }))
    expect(screen.getByTestId('column-c:org_id')).toHaveClass('struct-row-dropped')
    fireEvent.click(screen.getByRole('button', { name: 'Restore org_id' }))
    fireEvent.click(screen.getByRole('button', { name: 'Drop org_id' }))
    // The generated column keeps its expression read-only.
    expect(screen.getByRole('combobox', { name: 'Default of total' })).toBeDisabled()

    // Table name and comment.
    fireEvent.change(screen.getByRole('textbox', { name: 'Table name' }), { target: { value: 'people' } })
    fireEvent.change(screen.getByRole('textbox', { name: 'Table comment' }), { target: { value: 'Folks' } })

    // Undo / redo.
    fireEvent.click(screen.getByRole('button', { name: 'Undo' }))
    expect(draft().comment).toBe('People')
    fireEvent.click(screen.getByRole('button', { name: 'Redo' }))
    expect(draft().comment).toBe('Folks')

    fireEvent.click(screen.getByRole('button', { name: 'Apply changes…' }))
    const dlg = await screen.findByRole('dialog', { name: 'Apply structure changes?' })
    expect(within(dlg).getByTestId('structure-sql')).toHaveTextContent('-- ALTER users;')
    fireEvent.click(within(dlg).getByRole('button', { name: 'Copy' }))
    await act(tick)
    expect(copyText).toHaveBeenCalledWith('-- ALTER users;')
    expect(within(dlg).getByRole('button', { name: 'Copied' })).toBeInTheDocument()
    fireEvent.click(within(dlg).getByRole('button', { name: 'Apply' }))
    await waitFor(() => expect(tab().structureEdit).toBeNull())
    const tc = structure.apply.mock.calls[0][1]
    expect(tc).toMatchObject({ newName: 'people', comment: 'Folks', primaryKey: ['id', 'mail'] })
    expect(tc.columns.map((c) => [c.op, c.name])).toEqual([
      ['alter', 'email'],
      ['drop', 'org_id'],
      ['add', 'g'],
    ])
    expect(screen.queryByTestId('structure-editor')).toBeNull()
  })

  it('closes the preview with Escape and discards changes', async () => {
    await edit()
    fireEvent.click(screen.getByRole('button', { name: 'Add column' }))
    fireEvent.click(screen.getByRole('button', { name: 'Apply changes…' }))
    const dlg = await screen.findByRole('dialog')
    fireEvent.keyDown(dlg, { key: 'Escape' })
    expect(screen.queryByRole('dialog')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Remove column 5' }))
    fireEvent.click(screen.getByRole('button', { name: 'Add column' }))
    fireEvent.click(screen.getByRole('button', { name: 'Discard changes' }))
    expect(tab().structureEdit).toBeNull()
  })

  it('warns that MySQL DDL is not transactional and shows enum values', async () => {
    const detail = { ...editableDetail(), columns: [makeColumn({ name: 'k', dataType: "enum('a','b')", enumValues: ['a', 'b'] })] }
    await edit({ flavor: 'mysql', transactionalDdl: false, enumTypes: true, collation: true, usingExpr: false }, detail)
    const values = screen.getByRole('textbox', { name: 'Values of k' })
    expect(values).toHaveValue('a, b')
    fireEvent.change(values, { target: { value: 'a, b , c,' } })
    expect(draft().columns[0].enumValues).toEqual(['a', 'b', 'c'])
    fireEvent.change(screen.getByRole('textbox', { name: 'Collation of k' }), { target: { value: 'utf8mb4_bin' } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply changes…' }))
    const dlg = await screen.findByRole('dialog')
    expect(within(dlg).getByRole('note')).toHaveTextContent('commit each DDL statement')
  })

  it('disables what SQLite cannot change and explains why', async () => {
    await edit({ flavor: 'sqlite', alterColumn: false, primaryKey: false, foreignKeys: false, checks: false, comments: false, renameIndex: false, reason: 'SQLite limits' })
    expect(screen.getAllByRole('note')[0]).toHaveTextContent('SQLite limits')
    expect(screen.getByRole('combobox', { name: 'Type of email' })).toBeDisabled()
    expect(screen.getByRole('checkbox', { name: 'email is nullable' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Primary key: email' })).toBeDisabled()
    expect(screen.getByRole('textbox', { name: 'Name of email' })).toBeEnabled()
    expect(screen.queryByRole('textbox', { name: 'Table comment' })).toBeNull()
    fireEvent.click(screen.getByRole('tab', { name: 'Foreign keys' }))
    expect(screen.getByRole('button', { name: 'Add foreign key' })).toBeDisabled()
    fireEvent.click(screen.getByRole('tab', { name: 'Checks' }))
    expect(screen.getByRole('button', { name: 'Add check' })).toBeDisabled()
    fireEvent.click(screen.getByRole('tab', { name: 'Indexes' }))
    expect(screen.queryByRole('textbox', { name: 'Name of index users_email_idx' })).toBeNull()
  })

  it('shows failures on the row and offers a reload', async () => {
    await edit()
    fireEvent.change(screen.getByRole('combobox', { name: 'Type of email' }), { target: { value: 'bad;' } })
    structure.preview.mockResolvedValueOnce({ statements: [], failure: { kind: 'invalid', section: 'columns', index: 0, field: 'type', message: 'not a type', sql: '', applied: 0 } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply changes…' }))
    await act(tick)
    expect(screen.getByTestId('structure-failure')).toHaveTextContent('not a type')
    expect(screen.getByTestId('column-c:email')).toHaveClass('struct-row-failed')
    expect(screen.getByRole('combobox', { name: 'Type of email' })).toHaveAttribute('aria-invalid', 'true')

    structure.preview.mockResolvedValueOnce({ statements: [], failure: { kind: 'database', section: 'table', index: -1, field: '', message: 'boom', sql: 'ALTER TABLE x', applied: 2 } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply changes…' }))
    await act(tick)
    expect(screen.getByTestId('structure-failure')).toHaveTextContent('ALTER TABLE x')
    structure.preview.mockResolvedValueOnce({ statements: [], failure: { kind: 'invalid', section: 'primaryKey', index: -1, field: '', message: 'pk', sql: '', applied: 0 } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply changes…' }))
    await act(tick)
    expect(screen.getByText('The primary key change failed.')).toBeInTheDocument()
    structure.preview.mockResolvedValueOnce({ statements: [], failure: { kind: 'structure_changed', section: 'table', index: -1, field: '', message: 'changed', sql: '', applied: 0 } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply changes…' }))
    await act(tick)
    fireEvent.click(screen.getByRole('button', { name: 'Reload structure' }))
    await act(tick)
    expect(tab().structureEdit).toBeNull()
  })

  it('renames, drops and adds indexes', async () => {
    await edit({ indexPrefix: true })
    fireEvent.click(screen.getByRole('tab', { name: 'Indexes' }))
    fireEvent.change(screen.getByRole('textbox', { name: 'Name of index users_email_idx' }), { target: { value: 'mail_idx' } })
    expect(draft().indexes[0].name).toBe('mail_idx')
    fireEvent.click(screen.getByRole('button', { name: 'Drop index mail_idx' }))
    expect(draft().indexes[0].dropped).toBe(true)

    fireEvent.click(screen.getByRole('button', { name: 'Add index' }))
    const dlg = screen.getByRole('dialog', { name: 'Add index' })
    expect(within(dlg).getByRole('textbox', { name: 'Name' })).toHaveAttribute('placeholder', 'users_id_idx')
    fireEvent.click(within(dlg).getByRole('checkbox', { name: 'Unique values only' }))
    fireEvent.change(within(dlg).getByRole('combobox', { name: 'Method' }), { target: { value: 'gin' } })
    fireEvent.change(within(dlg).getByRole('combobox', { name: 'Key part 1' }), { target: { value: 'c:email' } })
    fireEvent.change(within(dlg).getByRole('spinbutton', { name: 'Prefix length of key part 1' }), { target: { value: '5' } })
    fireEvent.click(within(dlg).getByRole('checkbox', { name: 'Descending key part 1' }))
    fireEvent.click(within(dlg).getByRole('button', { name: 'Add key part' }))
    fireEvent.change(within(dlg).getByRole('combobox', { name: 'Key part 2' }), { target: { value: '' } })
    expect(within(dlg).getByRole('button', { name: 'Add index' })).toBeDisabled()
    fireEvent.change(within(dlg).getByRole('textbox', { name: 'Expression of key part 2' }), { target: { value: 'lower(email)' } })
    fireEvent.click(within(dlg).getByRole('button', { name: 'Add key part' }))
    fireEvent.click(within(dlg).getByRole('button', { name: 'Remove key part 3' }))
    fireEvent.change(within(dlg).getByRole('textbox', { name: 'Where' }), { target: { value: ' id > 0 ' } })
    expect(within(dlg).getByRole('textbox', { name: 'Name' })).toHaveAttribute('placeholder', 'users_email_expr_key')
    fireEvent.change(within(dlg).getByRole('textbox', { name: 'Name' }), { target: { value: 'ix' } })
    fireEvent.click(within(dlg).getByRole('button', { name: 'Add index' }))
    expect(draft().indexes[1]).toMatchObject({
      name: 'ix',
      unique: true,
      method: 'gin',
      predicate: 'id > 0',
      parts: [
        { column: 'c:email', expr: '', desc: true, length: 5 },
        { column: '', expr: 'lower(email)', desc: false, length: 0 },
      ],
    })
    expect(screen.getByRole('table', { name: 'Indexes' })).toHaveTextContent('email(5) DESC, (lower(email))')
    fireEvent.click(screen.getByRole('button', { name: 'Remove index ix' }))
    expect(draft().indexes).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: 'Add index' }))
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
    fireEvent.click(screen.getByRole('button', { name: 'Add index' }))
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('adds and drops foreign keys', async () => {
    await edit()
    fireEvent.click(screen.getByRole('tab', { name: 'Foreign keys' }))
    fireEvent.click(screen.getByRole('button', { name: 'Drop foreign key users_org_fk' }))
    expect(draft().fks[0].dropped).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: 'Add foreign key' }))
    const dlg = screen.getByRole('dialog', { name: 'Add foreign key' })
    // Another table first; its columns are loaded.
    expect(within(dlg).getByRole('combobox', { name: 'References' })).toHaveValue('orgs')
    await act(tick)
    expect(within(dlg).getByRole('combobox', { name: 'Referenced column 1' })).toHaveValue('id')
    fireEvent.change(within(dlg).getByRole('combobox', { name: 'Column 1' }), { target: { value: 'c:org_id' } })
    fireEvent.change(within(dlg).getByRole('combobox', { name: 'Referenced column 1' }), { target: { value: 'code' } })
    fireEvent.click(within(dlg).getByRole('button', { name: 'Add column' }))
    fireEvent.click(within(dlg).getByRole('button', { name: 'Remove column 2' }))
    fireEvent.change(within(dlg).getByRole('combobox', { name: 'On delete' }), { target: { value: 'CASCADE' } })
    fireEvent.change(within(dlg).getByRole('combobox', { name: 'On update' }), { target: { value: 'SET NULL' } })
    fireEvent.change(within(dlg).getByRole('textbox', { name: 'Name' }), { target: { value: ' fk2 ' } })
    fireEvent.click(within(dlg).getByRole('button', { name: 'Add foreign key' }))
    expect(draft().fks[1]).toMatchObject({ name: 'fk2', columns: ['c:org_id'], refTable: 'orgs', refColumns: ['code'], onDelete: 'CASCADE', onUpdate: 'SET NULL', refSchema: 'public' })
    expect(screen.getByRole('table', { name: 'Foreign keys' })).toHaveTextContent('orgs (code)')

    // A self-reference uses this table's draft columns.
    fireEvent.click(screen.getByRole('button', { name: 'Add foreign key' }))
    const self = screen.getByRole('dialog')
    fireEvent.change(within(self).getByRole('combobox', { name: 'References' }), { target: { value: 'users' } })
    await act(tick)
    expect(within(self).getAllByRole('option', { name: 'total' }).length).toBeGreaterThan(0)
    fireEvent.click(within(self).getByRole('button', { name: 'Cancel' }))
  })

  it('adds, edits and drops checks', async () => {
    await edit()
    fireEvent.click(screen.getByRole('tab', { name: 'Checks' }))
    fireEvent.click(screen.getByRole('button', { name: 'Drop check users_email_check' }))
    fireEvent.click(screen.getByRole('button', { name: 'Add check' }))
    fireEvent.change(screen.getByRole('textbox', { name: 'Name of check 2' }), { target: { value: 'c2' } })
    fireEvent.change(screen.getByRole('textbox', { name: 'Expression of check 2' }), { target: { value: 'id > 0' } })
    expect(draft().checks[1]).toMatchObject({ name: 'c2', expression: 'id > 0' })
    fireEvent.click(screen.getByRole('button', { name: 'Remove check c2' }))
    expect(draft().checks).toHaveLength(1)
  })

  it('drops triggers and opens trigger SQL in query tabs', async () => {
    await edit()
    fireEvent.click(screen.getByRole('tab', { name: 'Triggers' }))
    fireEvent.click(screen.getByRole('button', { name: 'Drop trigger users_touch' }))
    expect(draft().triggers[0].dropped).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: 'Edit trigger users_touch in a query tab' }))
    expect(store.getState().activeTab()).toMatchObject({ kind: 'query', sql: 'CREATE TRIGGER users_touch …' })
  })

  it('opens new trigger templates', async () => {
    await edit()
    fireEvent.click(screen.getByRole('tab', { name: 'Triggers' }))
    fireEvent.click(screen.getByRole('button', { name: 'New trigger…' }))
    expect(store.getState().activeTab()).toMatchObject({ kind: 'query' })
    expect(triggerTemplate(makeCaps({ flavor: 'mysql' }), 's', 't')).toContain('DELIMITER //')
    expect(triggerTemplate(makeCaps({ flavor: 'sqlite' }), 's', '')).toContain('ON my_table')
    expect(triggerTemplate(null, 's', 't')).toContain('ON s.t')
  })

  it('creates a new table', async () => {
    await mount()
    await act(() => store.getState().newTable())
    expect(screen.getByRole('textbox', { name: 'Table name' })).toHaveFocus()
    expect(screen.queryByRole('tab', { name: 'Triggers' })).toBeNull()
    fireEvent.change(screen.getByRole('textbox', { name: 'Table name' }), { target: { value: 'orgs2' } })
    fireEvent.change(screen.getByRole('combobox', { name: 'Default of id' }), { target: { value: 'generatedVirtual' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create table…' }))
    await screen.findByRole('dialog')
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
    await waitFor(() => expect(structure.apply).toHaveBeenCalled())
    expect(structure.apply.mock.calls[0][1]).toMatchObject({ create: true, table: 'orgs2' })
  })

  it('hides editing for views and in read-only mode, and shows capability errors', async () => {
    await mount({}, { ...editableDetail(), type: 'view' })
    expect(screen.queryByRole('button', { name: 'Edit structure' })).toBeNull()
    store.getState().dispose()
    await mount()
    structure.capabilities.mockRejectedValueOnce(new Error('offline'))
    fireEvent.click(screen.getByRole('button', { name: 'Edit structure' }))
    await act(tick)
    expect(screen.getByRole('alert')).toHaveTextContent('Structure editing is not available: offline')
    fireEvent.click(screen.getByRole('button', { name: 'Drop table…' }))
    await act(tick)
    expect(store.getState().dropTable?.table).toBe('users')
  })
})
