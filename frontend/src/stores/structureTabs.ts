/**
 * Structure editing in table tabs: the staged draft (see structureEdits.ts),
 * preview and apply through StructureEditService, new tables and dropping
 * tables. Part of the workspace store (createStructureActions).
 */
import type { StructureEditApi } from '../features/structure/editApi'
import type { Caps, ChangeFailure, StructureStatement } from '../features/structure/editTypes'
import type { TableDetail } from '../features/structure/types'
import { errorMessage } from '../lib/errors'
import { isDirty } from './pending'
import { draftFromDetail, failureKey, hasChanges, newTableDraft, toTableChange, type StructureDraft } from './structureEdits'

/** Most undo steps kept per structure draft. */
export const STRUCTURE_UNDO_LIMIT = 200

/** Structure editing of one table tab. */
export interface StructureEditState {
  /** The structure the draft was made from (null: a new table). */
  base: TableDetail | null
  draft: StructureDraft
  undo: StructureDraft[]
  redo: StructureDraft[]
  /** Open preview dialog: the statements Apply runs. */
  preview: StructureStatement[] | null
  previewLoading: boolean
  applying: boolean
  /** The last refused or failed preview / apply. */
  failure: ChangeFailure | null
  /** The draft row the failure is about (a key, "table" or "pk"). */
  failureKey: string | null
}

/** The open "Drop table" dialog. */
export interface DropTableAsk {
  schema: string
  table: string
  cascade: boolean
  statements: StructureStatement[]
  failure: ChangeFailure | null
  loading: boolean
  busy: boolean
}

/** The parts of a table tab structure editing reads and writes. */
interface EditableTab {
  id: string
  schema: string
  table: string
  type: string
  structure: TableDetail | null
  structureEdit: StructureEditState | null
  creating: boolean
  pending: Parameters<typeof isDirty>[0]
}

export interface StructureState {
  /** The session's capabilities (loaded on first use). */
  structureCaps: Caps | null
  /** Why the capabilities could not be loaded. */
  structureCapsError: string | null
  dropTable: DropTableAsk | null

  loadStructureCaps(): Promise<Caps | null>
  /** Starts editing the structure of a table tab (a table, not read-only; loads the structure first if needed). */
  startStructureEdit(id: string): Promise<void>
  /** Opens a table's structure view and starts editing it (sidebar menu). */
  editTableStructure(schema: string, table: string): Promise<void>
  stageStructure(id: string, fn: (d: StructureDraft) => StructureDraft): void
  undoStructure(id: string): void
  redoStructure(id: string): void
  /** Discards the draft (a new table's tab is closed). */
  cancelStructureEdit(id: string): void
  previewStructure(id: string): Promise<void>
  closeStructurePreview(id: string): void
  /** Applies the draft; true on success. */
  applyStructure(id: string): Promise<boolean>
  /** Opens a tab that creates a table in the current schema. */
  newTable(): Promise<void>
  requestDropTable(schema: string, table: string): Promise<void>
  setDropCascade(cascade: boolean): Promise<void>
  confirmDropTable(): Promise<boolean>
  cancelDropTable(): void
}

export type StructureFields = 'structureCaps' | 'structureCapsError' | 'dropTable'

/** Whether a tab has structure changes to apply (a new table always has). */
export function structureDirty(tab: Pick<EditableTab, 'schema' | 'structureEdit' | 'creating'>, caps: Caps | null): boolean {
  const e = tab.structureEdit
  if (!e) return false
  if (tab.creating) return true
  return hasChanges(toTableChange(tab.schema, e.base, e.draft, caps).change)
}

/** A failure made in the UI (not from Go). */
export function localFailure(message: string, kind: ChangeFailure['kind'] = 'invalid'): ChangeFailure {
  return { kind, section: 'table', index: -1, field: '', message, sql: '', applied: 0 }
}

type Host<T extends EditableTab> = {
  sessionId(): string
  schema(): string
  safeMode(): string
  findTab(id: string): T | undefined
  patchTab(id: string, patch: Partial<T>): void
  /** A new pinned table tab in schema (not loaded), made active. */
  openNewTableTab(schema: string, edit: StructureEditState): void
  closeTabNow(id: string): void
  /** Opens (pinned) the structure view of a table; its tab id. */
  openStructureTab(schema: string, table: string): string | null
  /** Ids of the open tabs of a table. */
  tabsOf(schema: string, table: string): string[]
  refreshTables(): Promise<void>
  loadStructure(id: string): Promise<void>
  loadTab(id: string): Promise<void>
  isDisposed(): boolean
}

type SetFn<S> = (partial: Partial<S> | ((s: S) => Partial<S>)) => void

function newEdit(base: TableDetail | null, draft: StructureDraft): StructureEditState {
  return { base, draft, undo: [], redo: [], preview: null, previewLoading: false, applying: false, failure: null, failureKey: null }
}

export function createStructureActions<T extends EditableTab, S extends StructureState>(
  set: SetFn<S>,
  get: () => S,
  api: StructureEditApi,
  host: Host<T>,
): Omit<StructureState, StructureFields> {
  const patchEdit = (id: string, fn: (e: StructureEditState) => Partial<StructureEditState>) => {
    const e = host.findTab(id)?.structureEdit
    if (e) host.patchTab(id, { structureEdit: { ...e, ...fn(e) } } as Partial<T>)
  }
  const changeOf = (tab: T, e: StructureEditState) => toTableChange(tab.schema, e.base, e.draft, get().structureCaps)

  const loadDropPreview = async (ask: DropTableAsk) => {
    try {
      const pv = await api.dropTablePreview(host.sessionId(), ask.schema, ask.table, ask.cascade)
      const cur = get().dropTable
      if (host.isDisposed() || !cur || cur.table !== ask.table || cur.cascade !== ask.cascade) return
      set({ dropTable: { ...cur, statements: pv.statements, failure: pv.failure, loading: false } } as Partial<S>)
    } catch (err) {
      const cur = get().dropTable
      if (!host.isDisposed() && cur) set({ dropTable: { ...cur, failure: localFailure(errorMessage(err), 'database'), loading: false } } as Partial<S>)
    }
  }

  return {
    async loadStructureCaps() {
      const have = get().structureCaps
      if (have) return have
      try {
        const caps = await api.capabilities(host.sessionId())
        if (!host.isDisposed()) set({ structureCaps: caps, structureCapsError: null } as Partial<S>)
        return caps
      } catch (err) {
        if (!host.isDisposed()) set({ structureCapsError: errorMessage(err) } as Partial<S>)
        return null
      }
    },

    async startStructureEdit(id) {
      let tab = host.findTab(id)
      if (!tab || tab.structureEdit || host.safeMode() === 'readonly') return
      if (!tab.structure) {
        await host.loadStructure(id)
        tab = host.findTab(id)
      }
      if (!tab?.structure || tab.structure.type !== 'table') return
      const caps = await get().loadStructureCaps()
      const cur = host.findTab(id)
      if (!caps || !cur?.structure || cur.structureEdit) return
      host.patchTab(id, { structureEdit: newEdit(cur.structure, draftFromDetail(cur.structure, caps)) } as Partial<T>)
    },

    async editTableStructure(schema, table) {
      const id = host.openStructureTab(schema, table)
      if (id) await get().startStructureEdit(id)
    },

    stageStructure(id, fn) {
      patchEdit(id, (e) => {
        const draft = fn(e.draft)
        if (draft === e.draft) return {}
        return { draft, undo: [...e.undo, e.draft].slice(-STRUCTURE_UNDO_LIMIT), redo: [], failure: null, failureKey: null }
      })
    },

    undoStructure(id) {
      patchEdit(id, (e) => {
        const prev = e.undo[e.undo.length - 1]
        if (!prev) return {}
        return { draft: prev, undo: e.undo.slice(0, -1), redo: [...e.redo, e.draft], failure: null, failureKey: null }
      })
    },

    redoStructure(id) {
      patchEdit(id, (e) => {
        const next = e.redo[e.redo.length - 1]
        if (!next) return {}
        return { draft: next, redo: e.redo.slice(0, -1), undo: [...e.undo, e.draft], failure: null, failureKey: null }
      })
    },

    cancelStructureEdit(id) {
      const tab = host.findTab(id)
      if (!tab?.structureEdit) return
      if (tab.creating) host.closeTabNow(id)
      else host.patchTab(id, { structureEdit: null } as Partial<T>)
    },

    async previewStructure(id) {
      const tab = host.findTab(id)
      const e = tab?.structureEdit
      if (!tab || !e || e.previewLoading || e.applying) return
      const { change, origins } = changeOf(tab, e)
      if (!hasChanges(change)) return
      patchEdit(id, () => ({ previewLoading: true, failure: null, failureKey: null }))
      try {
        const pv = await api.preview(host.sessionId(), change)
        if (host.isDisposed()) return
        if (pv.failure) patchEdit(id, () => ({ previewLoading: false, preview: null, failure: pv.failure, failureKey: failureKey(pv.failure!, origins) }))
        else patchEdit(id, () => ({ previewLoading: false, preview: pv.statements }))
      } catch (err) {
        if (!host.isDisposed()) patchEdit(id, () => ({ previewLoading: false, failure: localFailure(errorMessage(err), 'database'), failureKey: null }))
      }
    },

    closeStructurePreview(id) {
      patchEdit(id, () => ({ preview: null }))
    },

    async applyStructure(id) {
      const tab = host.findTab(id)
      const e = tab?.structureEdit
      if (!tab || !e || e.applying) return false
      if (isDirty(tab.pending)) {
        patchEdit(id, () => ({ failure: localFailure('Commit or discard the staged row changes of this table first.'), failureKey: 'table' }))
        return false
      }
      const { change, origins } = changeOf(tab, e)
      if (!hasChanges(change)) {
        host.patchTab(id, { structureEdit: null } as Partial<T>)
        return true
      }
      patchEdit(id, () => ({ applying: true, failure: null, failureKey: null }))
      try {
        const res = await api.apply(host.sessionId(), change)
        if (host.isDisposed()) return false
        if (!res.ok) {
          const f = res.failure ?? localFailure('The change failed.', 'database')
          patchEdit(id, () => ({ applying: false, preview: null, failure: f, failureKey: failureKey(f, origins) }))
          return false
        }
        const table = change.create ? change.table : (change.newName ?? tab.table)
        host.patchTab(id, { structureEdit: null, creating: false, table, structure: null, ddl: null, totalCount: null, pinned: true } as unknown as Partial<T>)
        await Promise.all([host.refreshTables(), host.loadStructure(id), host.loadTab(id)])
        return true
      } catch (err) {
        if (!host.isDisposed()) patchEdit(id, () => ({ applying: false, preview: null, failure: localFailure(errorMessage(err), 'database'), failureKey: null }))
        return false
      }
    },

    async newTable() {
      if (host.safeMode() === 'readonly') return
      const caps = await get().loadStructureCaps()
      if (!caps || host.isDisposed()) return
      host.openNewTableTab(host.schema(), newEdit(null, newTableDraft(caps)))
    },

    async requestDropTable(schema, table) {
      if (host.safeMode() === 'readonly') return
      const ask: DropTableAsk = { schema, table, cascade: false, statements: [], failure: null, loading: true, busy: false }
      set({ dropTable: ask } as Partial<S>)
      await loadDropPreview(ask)
    },

    async setDropCascade(cascade) {
      const cur = get().dropTable
      if (!cur || cur.cascade === cascade) return
      const ask = { ...cur, cascade, loading: true, failure: null }
      set({ dropTable: ask } as Partial<S>)
      await loadDropPreview(ask)
    },

    async confirmDropTable() {
      const ask = get().dropTable
      if (!ask || ask.busy || ask.loading) return false
      set({ dropTable: { ...ask, busy: true, failure: null } } as Partial<S>)
      try {
        const res = await api.dropTable(host.sessionId(), ask.schema, ask.table, ask.cascade)
        if (host.isDisposed()) return false
        if (!res.ok) {
          set({ dropTable: { ...ask, busy: false, failure: res.failure ?? localFailure('Dropping the table failed.', 'database') } } as Partial<S>)
          return false
        }
        for (const id of host.tabsOf(ask.schema, ask.table)) host.closeTabNow(id)
        set({ dropTable: null } as Partial<S>)
        await host.refreshTables()
        return true
      } catch (err) {
        if (!host.isDisposed()) set({ dropTable: { ...ask, busy: false, failure: localFailure(errorMessage(err), 'database') } } as Partial<S>)
        return false
      }
    },

    cancelDropTable() {
      set({ dropTable: null } as Partial<S>)
    },
  }
}
