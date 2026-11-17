import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { WorkspaceApi } from '../features/workspace/api'
import { fakeEditorApi, fakeSqlFileApi, sqlFile } from '../test/fakeEditorApi'
import { makeDetail } from '../test/structure'
import { draftKey, fileDirty, loadDrafts, type QueryTabState } from './queryTabs'
import { useSettings } from './settings'
import { DEFAULT_SETTINGS } from '../features/settings/types'
import { createWorkspaceStore, type WorkspaceStore } from './workspace'

function fakeApi(): WorkspaceApi {
  return {
    current: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listDatabases: vi.fn(async () => ['shop']),
    useDatabase: vi.fn(async (_s: string, db: string) => ({ database: db, schema: 'public' })),
    listSchemas: vi.fn(async () => ['public']),
    listTables: vi.fn(async () => []),
    fetchTable: vi.fn(),
    countRows: vi.fn(async () => 0),
    cancel: vi.fn(async () => true),
    tableInfo: vi.fn(async () => makeDetail()),
    tableDDL: vi.fn(async () => ''),
  }
}

/** A rejected binding call carrying a Go textfile.FileError. */
function fileError(code: string, message = `${code} happened`) {
  return Object.assign(new Error(message), { cause: { kind: 'fileError', code, path: '/x', message } })
}

describe('.sql files in query tabs', () => {
  let store: WorkspaceStore
  let files: ReturnType<typeof fakeSqlFileApi>
  const st = () => store.getState()
  const q = () => st().activeTab() as QueryTabState
  const queries = () => st().tabs as QueryTabState[]

  beforeEach(() => {
    window.localStorage.clear()
    files = fakeSqlFileApi()
    store = createWorkspaceStore({ id: 's1', kind: 'postgres', connectionId: 'c1' }, fakeApi(), fakeEditorApi(), undefined, files)
  })
  afterEach(() => {
    st().dispose()
    useSettings.setState({ settings: DEFAULT_SETTINGS })
  })

  it('opens a file into a new tab, the empty tab, or the tab that has it', async () => {
    files.open.mockResolvedValueOnce(null)
    await st().openSqlFile()
    expect(st().tabs).toHaveLength(0)

    files.open.mockResolvedValueOnce(sqlFile('/w/a.sql', 'SELECT 1;\n', { eol: '\r\n', modTime: 77 }))
    await st().openSqlFile()
    expect(q()).toMatchObject({ title: 'a.sql', sql: 'SELECT 1;\n', filePath: '/w/a.sql', savedSql: 'SELECT 1;\n', fileModTime: 77, eol: '\r\n', cursor: 0 })
    expect(fileDirty(q())).toBe(false)

    // An empty, untitled tab takes the next file.
    const empty = st().newQueryTab()
    files.open.mockResolvedValueOnce(sqlFile('/w/b.sql', 'SELECT 2;'))
    await st().openSqlFile()
    expect(queries()).toHaveLength(2)
    expect(q()).toMatchObject({ id: empty, title: 'b.sql', filePath: '/w/b.sql' })

    // A tab with text does not.
    st().newQueryTab('SELECT 3')
    files.open.mockResolvedValueOnce(sqlFile('/w/c.sql', 'c'))
    await st().openSqlFile()
    expect(queries()).toHaveLength(4)
    expect(q().title).toBe('c.sql')

    // A file that is open already: its tab is activated, nothing is replaced.
    const first = queries()[0].id
    st().setSql(first, 'edited')
    files.open.mockResolvedValueOnce(sqlFile('/w/a.sql', 'from disk'))
    await st().openSqlFile()
    expect(queries()).toHaveLength(4)
    expect(q()).toMatchObject({ id: first, sql: 'edited' })
    expect(fileDirty(q())).toBe(true)
  })

  it('opens dropped files one by one and reports failures', async () => {
    files.read.mockImplementation(async (p: string) => {
      if (p.endsWith('bad.sql')) throw fileError('not_text', 'bad.sql is not a UTF-8 text file')
      return sqlFile(p, `-- ${p}`)
    })
    await st().openSqlFiles(['/d/one.sql', '/d/bad.sql', '/d/two.sql'])
    expect(queries().map((t) => t.title)).toEqual(['one.sql', 'two.sql'])
    expect(st().fileError).toBe('bad.sql is not a UTF-8 text file')
    st().dismissFileError()
    expect(st().fileError).toBeNull()

    files.open.mockRejectedValueOnce(new Error('file dialogs are not available'))
    await st().openSqlFile()
    expect(st().fileError).toMatch(/not available/)
  })

  it('saves to the file, keeping its line ending, and tracks dirty state', async () => {
    files.open.mockResolvedValueOnce(sqlFile('/w/a.sql', 'one', { eol: '\r\n', modTime: 50 }))
    await st().openSqlFile()
    const id = q().id
    st().setSql(id, 'two')
    expect(fileDirty(q())).toBe(true)
    expect(await st().saveSqlFile(id)).toBe(true)
    expect(files.save).toHaveBeenLastCalledWith('/w/a.sql', 'a', 'two', '\r\n', 50)
    expect(q()).toMatchObject({ savedSql: 'two', fileModTime: 2001, eol: '\r\n', title: 'a.sql' })
    expect(fileDirty(q())).toBe(false)
    // Typing back to the saved text is clean again.
    st().setSql(id, 'three')
    st().setSql(id, 'two')
    expect(fileDirty(q())).toBe(false)
    expect(await st().saveSqlFile('nope')).toBe(false)
  })

  it('saves a new tab with Save as, and Save as moves a file tab', async () => {
    const id = st().newQueryTab('SELECT 1')
    expect(fileDirty(q())).toBe(false)
    // Cancelled dialog.
    files.save.mockResolvedValueOnce(null)
    expect(await st().saveSqlFile(id)).toBe(false)
    expect(q().filePath).toBeNull()
    expect(await st().saveSqlFile(id)).toBe(true)
    expect(files.save).toHaveBeenLastCalledWith('', 'Query 1', 'SELECT 1', '\n', 0)
    expect(q()).toMatchObject({ title: 'Query 1.sql', filePath: '/tmp/Query 1.sql', savedSql: 'SELECT 1' })

    expect(await st().saveSqlFileAs(id)).toBe(true)
    expect(files.save).toHaveBeenLastCalledWith('', 'Query 1', 'SELECT 1', '\n', 0)
    files.save.mockRejectedValueOnce(new Error('permission denied'))
    expect(await st().saveSqlFileAs(id)).toBe(false)
    expect(st().fileError).toBe('permission denied')
    // A successful save clears the message.
    expect(await st().saveSqlFile(id)).toBe(true)
    expect(st().fileError).toBeNull()
  })

  it('asks before overwriting a file changed on disk', async () => {
    files.open.mockResolvedValueOnce(sqlFile('/w/a.sql', 'one', { modTime: 50 }))
    await st().openSqlFile()
    const id = q().id
    st().setSql(id, 'mine')
    files.save.mockRejectedValueOnce(fileError('changed_on_disk'))
    expect(await st().saveSqlFile(id)).toBe(false)
    expect(st().fileConflict).toBe(id)
    expect(st().fileError).toBeNull()

    // Cancel keeps the tab dirty.
    await st().resolveFileConflict(false)
    expect(st().fileConflict).toBeNull()
    expect(fileDirty(q())).toBe(true)

    files.save.mockRejectedValueOnce(fileError('changed_on_disk'))
    await st().saveSqlFile(id)
    await st().resolveFileConflict(true)
    expect(files.save).toHaveBeenLastCalledWith('/w/a.sql', 'a', 'mine', '\n', 0)
    expect(fileDirty(q())).toBe(false)
    // Nothing pending: nothing to do.
    await st().resolveFileConflict(true)
    expect(files.save).toHaveBeenCalledTimes(3)
  })

  it('keeps file tabs in drafts and loads drafts of older versions', async () => {
    window.localStorage.setItem(
      draftKey('c2'),
      JSON.stringify([
        { title: 'Old', sql: 'SELECT 1' },
        { title: 'a.sql', sql: 'edited', filePath: '/w/a.sql', savedSql: 'saved', fileModTime: 9, eol: '\r\n' },
        { title: 'b.sql', sql: 'x', filePath: '/w/b.sql', savedSql: 3, eol: 'bogus' },
        { title: 'c', sql: 'y', filePath: '' },
      ]),
    )
    expect(loadDrafts('c2')).toEqual([
      { title: 'Old', sql: 'SELECT 1' },
      { title: 'a.sql', sql: 'edited', filePath: '/w/a.sql', savedSql: 'saved', fileModTime: 9, eol: '\r\n' },
      { title: 'b.sql', sql: 'x', filePath: '/w/b.sql', savedSql: '', fileModTime: 0, eol: '\n' },
      { title: 'c', sql: 'y' },
    ])
    const again = createWorkspaceStore({ id: 's2', kind: 'postgres', connectionId: 'c2' }, fakeApi(), fakeEditorApi(), undefined, files)
    const tabs = again.getState().tabs as QueryTabState[]
    expect(tabs[0]).toMatchObject({ title: 'Old', filePath: null, savedSql: null, eol: '\n' })
    expect(tabs[1]).toMatchObject({ filePath: '/w/a.sql', savedSql: 'saved', fileModTime: 9, eol: '\r\n' })
    expect(fileDirty(tabs[1])).toBe(true)

    // Saving writes the file fields back.
    files.open.mockResolvedValueOnce(sqlFile('/w/z.sql', 'z', { modTime: 5 }))
    await st().openSqlFile()
    expect(loadDrafts('c1')).toEqual([{ title: 'z.sql', sql: 'z', filePath: '/w/z.sql', savedSql: 'z', fileModTime: 5, eol: '\n' }])
    again.getState().dispose()
  })

  it('asks to save a dirty file tab when it closes', async () => {
    useSettings.setState({ settings: { ...DEFAULT_SETTINGS, confirmCloseDirtyQuery: true } })
    files.open.mockResolvedValueOnce(sqlFile('/w/a.sql', 'one'))
    await st().openSqlFile()
    const id = q().id
    // A clean file tab closes without asking.
    st().closeTab(id)
    expect(st().tabs).toHaveLength(0)

    files.open.mockResolvedValueOnce(sqlFile('/w/a.sql', 'one'))
    await st().openSqlFile()
    const id2 = q().id
    st().setSql(id2, 'two')
    st().closeTab(id2)
    expect(st().saveAsk).toBe(id2)
    expect(st().closeConfirm).toBeNull()
    await st().resolveSaveAsk('cancel')
    expect(st().saveAsk).toBeNull()
    expect(st().tabs).toHaveLength(1)

    // Save fails (conflict): the tab stays open.
    st().closeTab(id2)
    files.save.mockRejectedValueOnce(fileError('changed_on_disk'))
    await st().resolveSaveAsk('save')
    expect(st().tabs).toHaveLength(1)
    expect(st().fileConflict).toBe(id2)
    await st().resolveFileConflict(false)

    st().closeTab(id2)
    await st().resolveSaveAsk('save')
    expect(files.save).toHaveBeenLastCalledWith('/w/a.sql', 'a', 'two', '\n', 1000)
    expect(st().tabs).toHaveLength(0)

    files.open.mockResolvedValueOnce(sqlFile('/w/b.sql', 'one'))
    await st().openSqlFile()
    st().setSql(q().id, 'changed')
    st().closeTab(q().id)
    await st().resolveSaveAsk('discard')
    expect(st().tabs).toHaveLength(0)
    await st().resolveSaveAsk('save') // nothing asked: no-op
  })

  it('ignores answers that arrive after the workspace is gone', async () => {
    let finish: (f: ReturnType<typeof sqlFile>) => void = () => {}
    files.open.mockImplementationOnce(() => new Promise((r) => (finish = r)))
    const p = st().openSqlFile()
    st().dispose()
    finish(sqlFile('/w/a.sql', 'x'))
    await p
    expect(st().tabs).toHaveLength(0)

    files.read.mockRejectedValueOnce(new Error('late'))
    await st().openSqlFiles(['/w/late.sql'])
    expect(st().fileError).toBeNull()
    files.read.mockResolvedValueOnce(sqlFile('/w/late.sql', 'x'))
    await st().openSqlFiles(['/w/late.sql', '/w/never.sql'])
    expect(files.read).toHaveBeenCalledTimes(2)
  })
})
