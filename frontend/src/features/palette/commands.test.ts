import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../../bindings/lontar/internal/services', () => import('../../test/mockBindings'))

import { DEFAULT_SETTINGS } from '../settings/types'
import { useSettings } from '../../stores/settings'
import { createWorkspaceStore, type WorkspaceStore } from '../../stores/workspace'
import { SettingsService, UpdateService, resetBindings } from '../../test/mockBindings'
import { useUpdate } from '../../stores/update'
import { fakeEditorApi } from '../../test/fakeEditorApi'
import { makeDetail } from '../../test/structure'
import type { WorkspaceApi } from '../workspace/api'
import { commands, conflicts, SHORTCUTS, shortcutEvent, shortcutText } from '../workspace/keymap'
import type { FetchRequest } from '../workspace/types'
import { ACTION_IDS, commandAvailable, exportSourceFor, importTargetFor, runCommand, type CommandContext } from './commands'

function fakeApi(): WorkspaceApi {
  return {
    current: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listDatabases: vi.fn(async () => ['shop']),
    useDatabase: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listSchemas: vi.fn(async () => ['public']),
    listTables: vi.fn(async () => [{ schema: 'public', name: 'users', type: 'table' as const, estimatedRows: 1 }]),
    fetchTable: vi.fn(async (_s: string, _r: string, req: FetchRequest) => ({
      columns: [{ name: 'id', type: 'integer', isPK: true, nullable: false }],
      rows: [[{ k: 'num' as const, v: 1 }]],
      offset: req.offset,
      limit: req.limit,
      hasMore: false,
      totalCount: 1,
      elapsedMs: 1,
    })),
    countRows: vi.fn(async () => 1),
    cancel: vi.fn(async () => true),
    tableInfo: vi.fn(async () => makeDetail()),
    tableDDL: vi.fn(async () => ''),
  }
}

const flush = () => new Promise((r) => setTimeout(r, 0))

describe('palette commands', () => {
  let store: WorkspaceStore
  let ctx: CommandContext

  beforeEach(async () => {
    resetBindings()
    useSettings.setState({ settings: DEFAULT_SETTINGS, systemDark: true, open: false })
    store = createWorkspaceStore({ id: 's1', kind: 'postgres', connectionId: 'c1', safeMode: 'off' }, fakeApi(), fakeEditorApi())
    await store.getState().init()
    ctx = { platform: 'linux', store, openPalette: vi.fn(), disconnect: vi.fn() }
  })

  it('every command can run: its own action or its shortcut (no drift)', () => {
    for (const c of commands()) {
      if (!c.key) expect(ACTION_IDS).toContain(c.id)
    }
    expect(conflicts()).toEqual([])
    expect(runCommand('nope', ctx)).toBe(false)
  })

  it('runs commands like their shortcuts', async () => {
    expect(runCommand('newQuery', ctx)).toBe(true)
    expect(store.getState().tabs).toHaveLength(1)
    expect(store.getState().activeTab()?.kind).toBe('query')
    expect(commandAvailable('runCurrent', store.getState())).toBe(true)
    expect(commandAvailable('commit', store.getState())).toBe(false)
    expect(commandAvailable('stop', store.getState())).toBe(false)
    expect(commandAvailable('filter', store.getState())).toBe(true)
    expect(exportSourceFor(store.getState())).toBeNull() // no result yet
    expect(runCommand('closeTab', ctx)).toBe(true)
    expect(store.getState().tabs).toHaveLength(0)
    expect(commandAvailable('closeTab', store.getState())).toBe(false)

    store.getState().openTable(store.getState().tables[0], true)
    await flush()
    const s = store.getState()
    expect(commandAvailable('commit', s)).toBe(true)
    expect(commandAvailable('insertRow', s)).toBe(true)
    expect(commandAvailable('runAll', s)).toBe(false)
    expect(commandAvailable('settings', s)).toBe(true)
    expect(runCommand('toggleStructure', ctx)).toBe(true)
    expect(store.getState().activeTab()).toMatchObject({ view: 'structure' })
  })

  it('opens the palette, settings, export, import; toggles the theme; disconnects', async () => {
    runCommand('quickOpen', ctx)
    expect(ctx.openPalette).toHaveBeenCalledWith('tables')
    runCommand('commandPalette', ctx)
    expect(ctx.openPalette).toHaveBeenCalledWith('commands')
    runCommand('settings', ctx)
    expect(useSettings.getState().open).toBe(true)
    runCommand('disconnect', ctx)
    expect(ctx.disconnect).toHaveBeenCalled()
    UpdateService.Check.mockReturnValue(new Promise(() => {}))
    runCommand('checkUpdates', ctx)
    expect(useUpdate.getState()).toMatchObject({ open: true, phase: 'checking' })
    useUpdate.setState({ open: false, phase: 'idle' })

    // Nothing to export or import without a table tab.
    runCommand('exportData', ctx)
    runCommand('importCsv', ctx)
    expect(store.getState().exportRequest).toBeNull()
    expect(store.getState().importRequest).toBeNull()

    store.getState().openTable(store.getState().tables[0], true)
    await flush()
    runCommand('exportData', ctx)
    expect(store.getState().exportRequest).toMatchObject({ type: 'table', schema: 'public', table: 'users' })
    runCommand('importCsv', ctx)
    expect(store.getState().importRequest).toEqual({ schema: 'public', table: 'users' })
    expect(importTargetFor(store.getState())).not.toBeNull()

    runCommand('toggleTheme', ctx)
    await flush()
    expect(SettingsService.Save).toHaveBeenCalledWith(expect.objectContaining({ theme: 'light' }))
  })

  it('exports the active query result with its statement', async () => {
    store.getState().newQueryTab()
    const id = store.getState().activeTabId!
    store.setState({
      tabs: store.getState().tabs.map((t) =>
        t.id === id && t.kind === 'query'
          ? {
              ...t,
              activeResult: 'r1',
              result: {
                statements: [{ index: 0, sql: 'SELECT 1 AS a', start: 0, end: 13, status: 'ok', resultSets: [{ id: 'r1', columns: [{ name: 'a', type: 'int4', isPK: false, nullable: true }], rows: [[{ k: 'num', v: 1 }]], truncated: false, canLoadMore: false }], rowsAffected: null, durationMs: 1, error: null }],
                durationMs: 1,
                tx: { state: 'idle', source: 'server' },
                notices: [],
                sessionReset: false,
                cancelled: false,
              },
            }
          : t,
      ),
    })
    expect(exportSourceFor(store.getState())).toMatchObject({ type: 'query', sql: 'SELECT 1 AS a', rows: [[{ k: 'num', v: 1 }]] })
  })

  it('formats shortcuts and synthetic events', () => {
    expect(shortcutText({ key: 'p', mod: true, shift: true }, 'linux')).toBe('Ctrl+Shift+P')
    expect(shortcutText({ key: ',', mod: true }, 'darwin')).toBe('⌘,')
    expect(shortcutText({ key: 'escape', mod: false }, 'linux')).toBe('Esc')
    expect(shortcutText({ key: '', mod: false })).toBe('')
    expect(shortcutEvent({ key: 'enter', mod: true, shift: true }, 'darwin')).toEqual({ key: 'Enter', metaKey: true, ctrlKey: false, shiftKey: true, altKey: false })
    expect(shortcutEvent({ key: 'escape', mod: false }, 'linux')).toMatchObject({ key: 'Escape', ctrlKey: false })
    expect(SHORTCUTS.find((s) => s.id === 'quickOpen')).toMatchObject({ key: 'p', mod: true, scope: 'global' })
    expect(SHORTCUTS.find((s) => s.id === 'commandPalette')).toMatchObject({ key: 'p', mod: true, shift: true })
    expect(SHORTCUTS.find((s) => s.id === 'settings')).toMatchObject({ key: ',', mod: true })
  })
})
