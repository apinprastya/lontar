import { createContext, useContext } from 'react'
import { useStore } from 'zustand'
import { createStore, type StoreApi } from 'zustand/vanilla'
import type { WorkspaceApi } from '../features/workspace/api'
import {
  PAGE_SIZE,
  opTakesValue,
  type Cell,
  type FetchRequest,
  type Filter,
  type FilterOp,
  type ResultColumn,
  type SortSpec,
  type TableInfo,
  type TableType,
} from '../features/workspace/types'
import type { Kind } from '../features/connections/types'
import { editorApi as defaultEditorApi, type EditorApi } from '../features/editor/api'
import { cycleSort } from '../features/grid/sort'
import { errorMessage } from '../lib/errors'
import { copyText } from '../lib/clipboard'
import { referenceFilters } from '../features/structure/fk'
import type { ForeignKey, StructureSection, TableDetail } from '../features/structure/types'
import {
  MESSAGES,
  createQueryActions,
  loadDrafts,
  newQueryTabState,
  newResultEdit,
  resultSets,
  sourceColumns,
  statementOf,
  type QueryState,
  type QueryTabState,
  type ResultEdit,
} from './queryTabs'
import { useSettings } from './settings'
import type { ExportSource } from '../features/export/ExportDialog'
import { editApi as defaultEditApi, type EditApi } from '../features/edits/api'
import type { EditValue, PreviewStatement, SafeMode } from '../features/edits/types'
import {
  buildChangeSet,
  deleteRows,
  displayRows,
  duplicateValues,
  emptyPending,
  failedChange,
  insertRow,
  isDirty,
  redo,
  setCells,
  undo,
  type CellChange,
  type Pending,
  type RowRef,
} from './pending'

/** One editable row of the filter bar. */
export interface FilterRow extends Filter {
  id: number
}

/** State of one table tab. */
export interface TabState {
  kind: 'table'
  id: string
  schema: string
  table: string
  type: TableType
  estimatedRows: number | null
  /** Preview tabs (italic) are replaced by the next single-click. */
  pinned: boolean

  filterOpen: boolean
  draftFilters: FilterRow[]
  rawMode: boolean
  rawWhere: string
  /** What the current rows were fetched with. */
  appliedFilters: Filter[]
  appliedRawWhere: string

  sort: SortSpec[]
  offset: number
  limit: number

  columns: ResultColumn[]
  rows: Cell[][]
  hasMore: boolean
  /** Exact total for the applied filters, once known. */
  totalCount: number | null
  counting: boolean
  loading: boolean
  error: string | null
  elapsedMs: number | null
  requestId: string | null
  countRequestId: string | null
  /** Filter signature the running count is for. */
  countKey: string | null
  selectedRow: number | null
  columnWidths: Record<string, number>

  /** Staged grid edits (see stores/pending.ts). */
  pending: Pending
  committing: boolean
  /** Last failed commit (the changes are kept). */
  commitError: string | null
  /** The staged change the failed commit names. */
  failed: { key?: string; insertId?: number } | null
  /** Open preview dialog: the generated display SQL. */
  preview: PreviewStatement[] | null
  previewLoading: boolean

  /** Which view the tab shows: the rows or the read-only structure. */
  view: TabView
  /** Selected part of the structure view. */
  section: StructureSection
  /** Cached structure (also gives the grid its foreign keys). */
  structure: TableDetail | null
  structureLoading: boolean
  structureError: string | null
  /** Cached DDL (loaded when the DDL section is first shown). */
  ddl: string | null
  ddlLoading: boolean
  ddlError: string | null
}

export type TabView = 'data' | 'structure'

/** Why a table tab cannot be edited (null: it can). */
export function readOnlyReason(tab: Pick<TabState, 'type' | 'columns'>, safeMode: SafeMode): string | null {
  if (safeMode === 'readonly') return 'This connection is in read-only safe mode. Change it in the connection settings to edit data.'
  if (tab.type !== 'table') return `This is a ${tab.type}; views are read-only.`
  if (tab.columns.length > 0 && !tab.columns.some((c) => c.isPK)) return 'This table has no primary key, so its rows cannot be identified for editing. It is read-only.'
  return null
}

/** Whether a table tab can be edited now (columns loaded, no reason against it). */
export function canEdit(tab: Pick<TabState, 'type' | 'columns'>, safeMode: SafeMode): boolean {
  return tab.columns.length > 0 && readOnlyReason(tab, safeMode) === null
}

/** A pending "unsaved changes" prompt. */
export interface Guard {
  /** Tabs with staged changes that the action would lose. */
  tabIds: string[]
  /** Runs the guarded action. */
  proceed(): void
  busy: boolean
}

export type GuardChoice = 'commit' | 'discard' | 'cancel'

/** A tab of the workspace: a table or an SQL editor. */
export type Tab = TabState | QueryTabState

export interface WorkspaceState extends QueryState {
  sessionId: string
  kind: Kind
  /** Saved connection ID (query drafts and history are kept per connection). */
  connectionId: string
  databases: string[]
  database: string
  schemas: string[]
  schema: string
  showSystem: boolean
  tables: TableInfo[]
  tablesLoading: boolean
  /** Errors of sidebar actions (listing, switching); shown inline. */
  sidebarError: string | null
  search: string

  tabs: Tab[]
  activeTabId: string | null
  showDetail: boolean

  init(): Promise<void>
  refreshTables(): Promise<void>
  selectDatabase(database: string): Promise<void>
  selectSchema(schema: string): Promise<void>
  setShowSystem(show: boolean): Promise<void>
  setSearch(search: string): void

  openTable(table: TableInfo, pin?: boolean): void
  /** Opens (pinned) and shows the data or structure view (sidebar menu, links). */
  openTableView(schema: string, name: string, view?: TabView): void
  setTabView(id: string, view: TabView): void
  toggleTabView(id: string): void
  setStructureSection(id: string, section: StructureSection): void
  loadStructure(id: string): Promise<void>
  loadDDL(id: string): Promise<void>
  /** Copies a table's DDL (sidebar menu); errors go to the sidebar. */
  copyDDL(schema: string, name: string): Promise<boolean>
  /**
   * Opens the row a foreign key of the row points at: the referenced
   * table's tab (reused, after the unsaved-changes guard, or new) filtered
   * by refColumn = value for every key column.
   */
  followReference(id: string, fk: ForeignKey, row: Cell[]): boolean
  pinTab(id: string): void
  closeTab(id: string): void
  activateTab(id: string): void
  activateIndex(index: number): void
  activeTab(): Tab | null

  loadTab(id: string): Promise<void>
  cancelLoad(id: string): void
  refresh(): Promise<void>
  nextPage(id: string): void
  prevPage(id: string): void
  toggleSort(id: string, column: string, additive: boolean): void

  toggleFilterBar(id: string, open?: boolean): void
  setDraftFilters(id: string, rows: FilterRow[]): void
  addFilterRow(id: string): void
  setRawMode(id: string, raw: boolean): void
  setRawWhere(id: string, raw: string): void
  applyFilters(id: string): void
  clearFilters(id: string): void

  selectRow(id: string, row: number | null): void
  toggleDetail(): void
  setColumnWidth(id: string, column: string, width: number): void
  dispose(): void

  /** The connection's safe mode (fixed for the session). */
  safeMode: SafeMode
  guard: Guard | null
  /** Runs proceed now, or first asks Commit / Discard / Cancel when any of the tabs has staged changes. */
  guarded(tabIds: string[], proceed: () => void): void
  resolveGuard(choice: GuardChoice): Promise<void>

  stageCells(id: string, changes: CellChange[]): void
  addRow(id: string, values?: Record<string, EditValue>): void
  duplicateRow(id: string, ref: RowRef): void
  markDeleted(id: string, refs: RowRef[]): void
  undoEdit(id: string): void
  redoEdit(id: string): void
  discardChanges(id: string): void
  openPreview(id: string): Promise<void>
  closePreview(id: string): void
  /** Applies the staged changes; true on success. */
  commit(id: string): Promise<boolean>
  /** Mod+S: commits, or opens the preview first in confirm safe mode. */
  requestCommit(id: string): Promise<void>
  /** Looks up whether a query tab's active result set can be edited. */
  loadResultSource(id: string): Promise<void>

  /** The export dialog to show (table, query result or selection). */
  exportRequest: ExportSource | null
  requestExport(source: ExportSource | null): void
  /** The CSV import wizard to show (target table). */
  importRequest: { schema: string; table: string } | null
  requestImport(target: { schema: string; table: string } | null): void
  /** A query tab with unrun changes waiting for "close anyway" (setting). */
  closeConfirm: string | null
  /** Closes a tab; force skips the unrun-changes question. */
  closeTabNow(id: string): void
  resolveCloseConfirm(close: boolean): void
}

/** A query tab whose SQL changed since it last ran. */
export function queryDirty(tab: QueryTabState): boolean {
  return tab.sql.trim() !== '' && tab.sql !== (tab.ranSql ?? '')
}

export type WorkspaceStore = StoreApi<WorkspaceState>

let seq = 0
/** Unique ID for a backend request (cancellable with api.cancel). */
export function newRequestId(prefix = 'req'): string {
  seq += 1
  return `${prefix}-${Date.now().toString(36)}-${seq}`
}

export { cycleSort }

/** Filters from the draft rows that are complete enough to send. */
export function effectiveFilters(rows: FilterRow[]): Filter[] {
  return rows
    .filter((r) => r.column !== '')
    .map(({ column, op, value, value2 }) => ({
      column,
      op,
      value: opTakesValue(op) ? value : '',
      value2: op === 'between' ? value2 : '',
    }))
}

let filterRowSeq = 0
export function newFilterRow(column: string, op: FilterOp = '='): FilterRow {
  filterRowSeq += 1
  return { id: filterRowSeq, column, op, value: '', value2: '' }
}

function newTab(t: TableInfo, pinned: boolean): TabState {
  return {
    kind: 'table',
    id: newRequestId('tab'),
    schema: t.schema,
    table: t.name,
    type: t.type,
    estimatedRows: t.estimatedRows,
    pinned,
    filterOpen: false,
    draftFilters: [],
    rawMode: false,
    rawWhere: '',
    appliedFilters: [],
    appliedRawWhere: '',
    sort: [],
    offset: 0,
    limit: useSettings.getState().settings.gridPageSize || PAGE_SIZE,
    columns: [],
    rows: [],
    hasMore: false,
    totalCount: null,
    counting: false,
    loading: false,
    error: null,
    elapsedMs: null,
    requestId: null,
    countRequestId: null,
    countKey: null,
    selectedRow: null,
    columnWidths: {},
    pending: emptyPending,
    committing: false,
    commitError: null,
    failed: null,
    preview: null,
    previewLoading: false,
    view: 'data',
    section: 'columns',
    structure: null,
    structureLoading: false,
    structureError: null,
    ddl: null,
    ddlLoading: false,
    ddlError: null,
  }
}

/** Signature of the applied filters (what a row count depends on). */
export function filterKey(tab: Pick<TabState, 'appliedFilters' | 'appliedRawWhere'>): string {
  return JSON.stringify([tab.appliedFilters, tab.appliedRawWhere])
}

/** The request for a tab's current page. */
export function tabRequest(tab: TabState): FetchRequest {
  return {
    schema: tab.schema,
    table: tab.table,
    filters: tab.appliedFilters,
    rawWhere: tab.appliedRawWhere,
    sort: tab.sort,
    offset: tab.offset,
    limit: tab.limit,
  }
}

export interface WorkspaceSession {
  id: string
  kind: Kind
  connectionId?: string
  safeMode?: SafeMode
}

export function createWorkspaceStore(
  session: WorkspaceSession,
  api: WorkspaceApi,
  editor: EditorApi = defaultEditorApi,
  edits: EditApi = defaultEditApi,
): WorkspaceStore {
  let disposed = false
  const connectionId = session.connectionId ?? ''
  // Query tabs of this connection from the previous visit.
  const restored = (() => {
    const tabs: QueryTabState[] = []
    for (const d of loadDrafts(connectionId)) tabs.push({ ...newQueryTabState(d.title, d.sql), cursor: 0 })
    return tabs
  })()

  const store = createStore<WorkspaceState>()((set, get) => {
    /** A table tab by ID (query tabs are handled in queryTabs.ts). */
    const findTab = (id: string) => get().tabs.find((t): t is TabState => t.id === id && t.kind === 'table')
    const patchTab = (id: string, patch: Partial<TabState> | ((t: TabState) => Partial<TabState>)) => {
      set((s) => ({
        tabs: s.tabs.map((t) => (t.id === id && t.kind === 'table' ? { ...t, ...(typeof patch === 'function' ? patch(t) : patch) } : t)),
      }))
    }
    const cancelRequests = (tab: TabState | undefined) => {
      if (!tab) return
      for (const r of [tab.requestId, tab.countRequestId]) if (r) void api.cancel(r).catch(() => {})
    }
    /** Changes what is shown (sort, filters, page) and reloads. */
    const reload = (id: string, patch: Partial<TabState>) => {
      patchTab(id, { ...patch, pinned: true })
      void get().loadTab(id)
    }

    const clearNow = (tab: TabState) => {
      reload(tab.id, {
        draftFilters: [newFilterRow(tab.columns[0]?.name ?? '')],
        rawWhere: '',
        appliedFilters: [],
        appliedRawWhere: '',
        offset: 0,
        totalCount: null,
        selectedRow: null,
      })
    }

    const countRows = async (id: string) => {
      const tab = findTab(id)
      if (!tab) return
      if (tab.countRequestId) void api.cancel(tab.countRequestId).catch(() => {})
      const requestId = newRequestId('count')
      const key = filterKey(tab)
      patchTab(id, { counting: true, countRequestId: requestId, countKey: key })
      try {
        const n = await api.countRows(get().sessionId, requestId, tabRequest(tab))
        const cur = findTab(id)
        if (!disposed && cur?.countRequestId === requestId && filterKey(cur) === key) {
          patchTab(id, { totalCount: n, counting: false, countRequestId: null, countKey: null })
        }
      } catch {
        // The count is optional (the estimate stays); never hide rows.
        if (findTab(id)?.countRequestId === requestId) patchTab(id, { counting: false, countRequestId: null, countKey: null })
      }
    }

    const loadTables = async () => {
      const { sessionId, schema } = get()
      set({ tablesLoading: true })
      try {
        const tables = schema ? await api.listTables(sessionId, schema) : []
        set({ tables, tablesLoading: false })
      } catch (err) {
        set({ tables: [], tablesLoading: false, sidebarError: errorMessage(err) })
      }
    }

    /** Closes the table tabs (they belong to the previous database); query tabs stay. */
    const closeTableTabs = () => {
      const { tabs, activeTabId } = get()
      for (const t of tabs) if (t.kind === 'table') cancelRequests(t)
      // Query results came from the previous database: no editing them.
      const rest = tabs.filter((t) => t.kind !== 'table').map((t) => (t.kind === 'query' && t.result ? { ...t, edit: null, resultsStale: true } : t))
      set({ tabs: rest, activeTabId: rest.some((t) => t.id === activeTabId) ? activeTabId : (rest[0]?.id ?? null) })
    }

    /** Sidebar info of schema.name (a plain table when it is not listed, e.g. another schema). */
    const tableInfoOf = (schema: string, name: string): TableInfo =>
      get().tables.find((t) => t.schema === schema && t.name === name) ?? { schema, name, type: 'table', estimatedRows: null }
    const findTableTab = (schema: string, name: string) =>
      get().tabs.find((x): x is TabState => x.kind === 'table' && x.schema === schema && x.table === name)

    const findQuery = (id: string) => get().tabs.find((t): t is QueryTabState => t.id === id && t.kind === 'query')
    const patchQuery = (id: string, patch: (t: QueryTabState) => Partial<QueryTabState>) => {
      set((s) => ({ tabs: s.tabs.map((t) => (t.id === id && t.kind === 'query' ? { ...t, ...patch(t) } : t)) }))
    }
    /** Patches the edit state of a query tab (when it is still for resultId). */
    const patchResultEdit = (id: string, resultId: string, patch: Partial<ResultEdit>) => {
      patchQuery(id, (t) => (t.edit?.resultId === resultId ? { edit: { ...t.edit, ...patch } } : {}))
    }

    /**
     * What staging and committing need, for a table tab or for the editable
     * result set of a query tab.
     */
    interface EditTarget {
      columns: ResultColumn[]
      rows: Cell[][]
      pending: Pending
      committing: boolean
      schema: string
      table: string
      rename?: Record<string, string>
      editable: boolean
      /** An open transaction in the query tab (edits would not see it). */
      blocked: string | null
      patch(p: Partial<Pick<TabState, 'pending' | 'committing' | 'commitError' | 'failed' | 'preview' | 'previewLoading'>>, staged?: boolean): void
      /** Reloads the rows after a successful commit (query results: awaited). */
      reload(): Promise<void> | void
    }
    const target = (id: string): EditTarget | null => {
      const tab = findTab(id)
      if (tab) {
        return {
          columns: tab.columns,
          rows: tab.rows,
          pending: tab.pending,
          committing: tab.committing,
          schema: tab.schema,
          table: tab.table,
          editable: canEdit(tab, get().safeMode),
          blocked: null,
          patch: (p, staged) => patchTab(id, staged ? { ...p, pinned: true } : p),
          reload: () => {
            patchTab(id, { totalCount: null })
            void get().loadTab(id)
          },
        }
      }
      const q = findQuery(id)
      const e = q?.edit
      const set = e && q.result ? resultSets(q.result).find((rs) => rs.id === e.resultId) : undefined
      if (!q || !e || !set || !e.source) return null
      const txOpen = q.tx?.state === 'active' || q.tx?.state === 'failed'
      return {
        columns: e.columns,
        rows: set.rows,
        pending: e.pending,
        committing: e.committing,
        schema: e.source.schema,
        table: e.source.table,
        rename: e.rename,
        editable: e.source.editable && get().safeMode !== 'readonly',
        blocked: txOpen
          ? 'This query tab has an open transaction. Run COMMIT or ROLLBACK first: grid changes are saved on a separate connection.'
          : null,
        patch: (p) => patchResultEdit(id, e.resultId, p),
        reload: () => get().refreshResult(id, e.resultId),
      }
    }

    /** Tabs among ids with staged changes (table tabs and query results). */
    const dirtyTabs = (ids: string[]) => ids.filter((id) => {
      const t = target(id)
      return t ? isDirty(t.pending) : false
    })
    const allTabIds = () => get().tabs.map((t) => t.id)
    /** Stages a pending change: pins the tab and clears a stale failure. */
    const stage = (id: string, fn: (t: EditTarget) => Pending) => {
      const t = target(id)
      if (!t || !t.editable) return
      const pending = fn(t)
      if (pending === t.pending) return
      t.patch({ pending, failed: null }, true)
    }

    const queryActions = createQueryActions<WorkspaceState>(set, get, editor, () => disposed)

    return {
      ...queryActions,

      // Runs and result switches drop staged result edits: ask first.
      async runQuery(id, mode) {
        if (dirtyTabs([id]).length > 0) get().guarded([id], () => void queryActions.runQuery(id, mode))
        else await queryActions.runQuery(id, mode)
      },

      setActiveResult(id, key) {
        const e = findQuery(id)?.edit
        if (key !== MESSAGES && e && key !== e.resultId && dirtyTabs([id]).length > 0) {
          get().guarded([id], () => queryActions.setActiveResult(id, key))
        } else queryActions.setActiveResult(id, key)
      },

      async loadResultSource(id) {
        const q = findQuery(id)
        if (!q?.result || q.activeResult === MESSAGES) return
        const resultId = q.activeResult
        if (q.edit?.resultId === resultId) return
        const set = resultSets(q.result).find((rs) => rs.id === resultId)
        const st = statementOf(q.result, resultId)
        if (!set || !st || set.columns.length === 0) return
        const edit = newResultEdit(resultId, set.columns)
        if (q.resultsStale) {
          patchQuery(id, () => ({ edit: { ...edit, loading: false, error: 'The database changed since this query ran. Run it again to edit the results.' } }))
          return
        }
        if (st.resultSets.length !== 1) {
          patchQuery(id, () => ({ edit: { ...edit, loading: false, error: 'Results of a statement that returns several result sets are read-only.' } }))
          return
        }
        patchQuery(id, () => ({ edit }))
        try {
          const source = await edits.resultSource(get().sessionId, st.sql, set.columns.length)
          if (disposed) return
          patchResultEdit(id, resultId, { loading: false, source, ...sourceColumns(set.columns, source) })
        } catch (err) {
          if (!disposed) patchResultEdit(id, resultId, { loading: false, error: errorMessage(err) })
        }
      },

      safeMode: session.safeMode ?? 'off',
      guard: null,

      guarded(tabIds, proceed) {
        const dirty = dirtyTabs(tabIds)
        if (dirty.length === 0) proceed()
        else set({ guard: { tabIds: dirty, proceed, busy: false } })
      },

      async resolveGuard(choice) {
        const g = get().guard
        if (!g || g.busy) return
        if (choice === 'cancel') {
          set({ guard: null })
          return
        }
        if (choice === 'discard') {
          for (const id of g.tabIds) target(id)?.patch({ pending: emptyPending, commitError: null, failed: null })
          set({ guard: null })
          g.proceed()
          return
        }
        set({ guard: { ...g, busy: true } })
        for (const id of g.tabIds) {
          if (!(await get().commit(id))) {
            // The failure is shown in the tab; stay where we are.
            set({ guard: null, activeTabId: id })
            return
          }
        }
        set({ guard: null })
        g.proceed()
      },

      stageCells(id, changes) {
        stage(id, (t) => setCells(t.pending, t.columns, t.rows, changes))
      },

      addRow(id, values) {
        stage(id, (t) => insertRow(t.pending, values).pending)
      },

      duplicateRow(id, ref) {
        stage(id, (t) => {
          const d = displayRows(t.columns, t.rows, t.pending).find((r) =>
            ref.type === 'row' ? r.ref.type === 'row' && r.ref.index === ref.index : r.ref.type === 'insert' && r.ref.id === ref.id,
          )
          return d ? insertRow(t.pending, duplicateValues(t.columns, d, t.pending)).pending : t.pending
        })
      },

      markDeleted(id, refs) {
        stage(id, (t) => deleteRows(t.pending, t.columns, t.rows, refs))
      },

      undoEdit(id) {
        stage(id, (t) => undo(t.pending))
      },

      redoEdit(id) {
        stage(id, (t) => redo(t.pending))
      },

      discardChanges(id) {
        target(id)?.patch({ pending: emptyPending, commitError: null, failed: null, preview: null })
      },

      async openPreview(id) {
        const t = target(id)
        if (!t || !isDirty(t.pending)) return
        t.patch({ previewLoading: true, commitError: null })
        try {
          const preview = await edits.preview(get().sessionId, buildChangeSet(t.schema, t.table, t.pending, t.rename).cs)
          if (!disposed) target(id)?.patch({ preview, previewLoading: false })
        } catch (err) {
          if (!disposed) target(id)?.patch({ previewLoading: false, preview: null, commitError: errorMessage(err) })
        }
      },

      closePreview(id) {
        target(id)?.patch({ preview: null })
      },

      async commit(id) {
        const t = target(id)
        if (!t || t.committing) return false
        if (!isDirty(t.pending)) return true
        if (get().safeMode === 'readonly') {
          t.patch({ commitError: 'This connection is in read-only safe mode; nothing was saved.' })
          return false
        }
        if (t.blocked) {
          t.patch({ commitError: t.blocked, preview: null })
          return false
        }
        const { cs, refs } = buildChangeSet(t.schema, t.table, t.pending, t.rename)
        const pending = t.pending
        t.patch({ committing: true, commitError: null, failed: null })
        try {
          const res = await edits.apply(get().sessionId, cs)
          if (disposed) return false
          if (res.ok) {
            // Changes staged while the commit ran stay staged.
            const cur = target(id)
            const rest = cur && cur.pending !== pending ? cur.pending : emptyPending
            const left = rest === pending ? emptyPending : rest
            t.patch({ committing: false, pending: left, preview: null })
            // Re-running a query would drop changes staged meanwhile.
            if (findTab(id) || !isDirty(left)) await t.reload()
            return true
          }
          const f = res.failure
          t.patch({ committing: false, preview: null, commitError: f?.message ?? 'The commit failed; nothing was saved.', failed: failedChange(refs, f) })
          return false
        } catch (err) {
          if (!disposed) t.patch({ committing: false, preview: null, commitError: errorMessage(err) })
          return false
        }
      },

      async requestCommit(id) {
        const tab = target(id)
        if (!tab || !isDirty(tab.pending) || tab.committing) return
        if (get().safeMode === 'confirm') await get().openPreview(id)
        else await get().commit(id)
      },

      historyOpen: false,
      completion: null,
      completionLoading: false,
      sessionId: session.id,
      kind: session.kind,
      connectionId,
      databases: [],
      database: '',
      schemas: [],
      schema: '',
      showSystem: false,
      tables: [],
      tablesLoading: false,
      sidebarError: null,
      search: '',
      tabs: restored,
      activeTabId: restored[0]?.id ?? null,
      showDetail: false,

      async init() {
        const { sessionId, showSystem } = get()
        set({ sidebarError: null, tablesLoading: true })
        try {
          const loc = await api.current(sessionId)
          const [databases, schemas] = await Promise.all([
            api.listDatabases(sessionId, showSystem),
            api.listSchemas(sessionId, showSystem),
          ])
          const schema = schemas.includes(get().schema) ? get().schema : schemas.includes(loc.schema) ? loc.schema : (schemas[0] ?? '')
          set({ database: loc.database, databases, schemas, schema })
        } catch (err) {
          set({ sidebarError: errorMessage(err), tablesLoading: false })
          return
        }
        await loadTables()
      },

      async refreshTables() {
        set({ sidebarError: null })
        await get().init()
      },

      async selectDatabase(database) {
        if (database === get().database) return
        if (dirtyTabs(allTabIds()).length > 0) {
          // Switching closes the table tabs: ask first.
          get().guarded(allTabIds(), () => void get().selectDatabase(database))
          return
        }
        set({ sidebarError: null, tablesLoading: true })
        try {
          const loc = await api.useDatabase(get().sessionId, database)
          // Table tabs belong to the previous database's pool.
          closeTableTabs()
          set({ database: loc.database, schema: loc.schema, tables: [], completion: null })
        } catch (err) {
          set({ sidebarError: errorMessage(err), tablesLoading: false })
          return
        }
        await get().init()
      },

      async selectSchema(schema) {
        set({ schema, sidebarError: null, completion: null })
        await loadTables()
      },

      async setShowSystem(show) {
        set({ showSystem: show })
        await get().init()
      },

      setSearch(search) {
        set({ search })
      },

      openTable(t, pin = false) {
        const { tabs } = get()
        const existing = tabs.find((x): x is TabState => x.kind === 'table' && x.schema === t.schema && x.table === t.name)
        if (existing) {
          set({ activeTabId: existing.id })
          if (pin && !existing.pinned) patchTab(existing.id, { pinned: true })
          return
        }
        const tab = newTab(t, pin)
        const preview = pin ? undefined : tabs.find((x): x is TabState => x.kind === 'table' && !x.pinned)
        if (preview) {
          cancelRequests(preview)
          set({ tabs: tabs.map((x) => (x.id === preview.id ? tab : x)), activeTabId: tab.id })
        } else {
          set({ tabs: [...tabs, tab], activeTabId: tab.id })
        }
        void get().loadTab(tab.id)
        void get().loadStructure(tab.id)
      },

      openTableView(schema, name, view = 'data') {
        get().openTable(tableInfoOf(schema, name), true)
        const tab = findTableTab(schema, name)
        if (tab) get().setTabView(tab.id, view)
      },

      setTabView(id, view) {
        const tab = findTab(id)
        if (!tab || tab.view === view) return
        patchTab(id, { view })
        if (view === 'structure' && !tab.structure && !tab.structureLoading) void get().loadStructure(id)
        if (view === 'structure' && tab.section === 'ddl' && tab.ddl === null && !tab.ddlLoading) void get().loadDDL(id)
      },

      toggleTabView(id) {
        const tab = findTab(id)
        if (tab) get().setTabView(id, tab.view === 'data' ? 'structure' : 'data')
      },

      setStructureSection(id, section) {
        const tab = findTab(id)
        if (!tab) return
        patchTab(id, { section })
        if (section === 'ddl' && tab.ddl === null && !tab.ddlLoading) void get().loadDDL(id)
      },

      async loadStructure(id) {
        const tab = findTab(id)
        if (!tab) return
        patchTab(id, { structureLoading: true, structureError: null })
        try {
          const structure = await api.tableInfo(get().sessionId, tab.schema, tab.table)
          if (!disposed) patchTab(id, { structure, structureLoading: false })
        } catch (err) {
          if (!disposed) patchTab(id, { structureLoading: false, structureError: errorMessage(err) })
        }
      },

      async loadDDL(id) {
        const tab = findTab(id)
        if (!tab) return
        patchTab(id, { ddlLoading: true, ddlError: null })
        try {
          const ddl = await api.tableDDL(get().sessionId, tab.schema, tab.table)
          if (!disposed) patchTab(id, { ddl, ddlLoading: false })
        } catch (err) {
          if (!disposed) patchTab(id, { ddlLoading: false, ddlError: errorMessage(err) })
        }
      },

      async copyDDL(schema, name) {
        set({ sidebarError: null })
        try {
          await copyText(await api.tableDDL(get().sessionId, schema, name))
          return true
        } catch (err) {
          set({ sidebarError: errorMessage(err) })
          return false
        }
      },

      followReference(id, fk, row) {
        const tab = findTab(id)
        if (!tab) return false
        const filters = referenceFilters(fk, tab.columns, row)
        if (!filters) return false
        const show = {
          view: 'data' as const,
          filterOpen: true,
          rawMode: false,
          rawWhere: '',
          draftFilters: filters.map((f) => ({ ...newFilterRow(f.column), value: f.value })),
          appliedFilters: filters,
          appliedRawWhere: '',
          offset: 0,
          totalCount: null,
          selectedRow: null,
          pinned: true,
        }
        const target = findTableTab(fk.refSchema, fk.refTable)
        if (target) {
          // Reusing a tab replaces its rows: ask first when it has staged changes.
          get().guarded([target.id], () => {
            if (!findTab(target.id)) return
            patchTab(target.id, show)
            set({ activeTabId: target.id })
            void get().loadTab(target.id)
          })
          return true
        }
        const created = { ...newTab(tableInfoOf(fk.refSchema, fk.refTable), true), ...show }
        set({ tabs: [...get().tabs, created], activeTabId: created.id })
        void get().loadTab(created.id)
        void get().loadStructure(created.id)
        return true
      },

      pinTab(id) {
        patchTab(id, { pinned: true })
      },

      closeTab(id) {
        if (dirtyTabs([id]).length > 0) {
          get().guarded([id], () => get().closeTab(id))
          return
        }
        const tab = findTab(id) ?? get().tabs.find((t) => t.id === id)
        if (tab?.kind === 'query' && useSettings.getState().settings.confirmCloseDirtyQuery && queryDirty(tab)) {
          set({ closeConfirm: id })
          return
        }
        get().closeTabNow(id)
      },

      closeTabNow(id) {
        const { tabs, activeTabId } = get()
        const i = tabs.findIndex((t) => t.id === id)
        if (i < 0) return
        const tab = tabs[i]
        const rest = tabs.filter((t) => t.id !== id)
        let active = activeTabId
        if (activeTabId === id) active = (rest[i] ?? rest[i - 1])?.id ?? null
        set({ tabs: rest, activeTabId: active })
        if (tab.kind === 'table') cancelRequests(tab)
        else get().releaseQueryTab(tab)
      },

      resolveCloseConfirm(close) {
        const id = get().closeConfirm
        set({ closeConfirm: null })
        if (close && id) get().closeTabNow(id)
      },

      exportRequest: null,
      requestExport(source) {
        set({ exportRequest: source })
      },
      importRequest: null,
      requestImport(target) {
        set({ importRequest: target })
      },
      closeConfirm: null,

      activateTab(id) {
        // Any tab: table or query (findTab only finds table tabs).
        if (get().tabs.some((t) => t.id === id)) set({ activeTabId: id })
      },

      activateIndex(index) {
        const { tabs } = get()
        // Mod+9 always goes to the last tab, as in browsers.
        const tab = index >= 8 ? tabs[tabs.length - 1] : tabs[index]
        if (tab) set({ activeTabId: tab.id })
      },

      activeTab() {
        const { tabs, activeTabId } = get()
        return tabs.find((t) => t.id === activeTabId) ?? null
      },

      async loadTab(id) {
        const tab = findTab(id)
        if (!tab) return
        if (tab.requestId) void api.cancel(tab.requestId).catch(() => {})
        const requestId = newRequestId()
        patchTab(id, { loading: true, error: null, requestId })
        try {
          const page = await api.fetchTable(get().sessionId, requestId, tabRequest(tab))
          if (disposed || findTab(id)?.requestId !== requestId) return
          const cur = findTab(id) as TabState
          const total = page.totalCount ?? cur.totalCount
          patchTab(id, {
            loading: false,
            requestId: null,
            columns: page.columns,
            rows: page.rows,
            hasMore: page.hasMore,
            offset: page.offset,
            elapsedMs: page.elapsedMs,
            totalCount: total,
            selectedRow: page.rows.length > 0 ? Math.min(cur.selectedRow ?? 0, page.rows.length - 1) : null,
          })
          if (total === null && !(cur.counting && cur.countKey === filterKey(cur))) void countRows(id)
        } catch (err) {
          if (disposed || findTab(id)?.requestId !== requestId) return
          patchTab(id, { loading: false, requestId: null, error: errorMessage(err) })
        }
      },

      cancelLoad(id) {
        const tab = findTab(id)
        if (!tab?.requestId) return
        void api.cancel(tab.requestId).catch(() => {})
        patchTab(id, { loading: false, requestId: null, error: 'Query cancelled.' })
      },

      async refresh() {
        const tab = get().activeTab()
        if (tab?.kind === 'query') {
          await Promise.all([get().refreshTables(), get().completion ? get().loadCompletion(true) : Promise.resolve()])
        } else if (tab?.view === 'structure') {
          // Structure only: nothing staged is lost, so no guard.
          await Promise.all([get().refreshTables(), get().loadStructure(tab.id), tab.ddl !== null ? get().loadDDL(tab.id) : Promise.resolve()])
        } else if (tab) {
          if (dirtyTabs([tab.id]).length > 0) {
            get().guarded([tab.id], () => void get().refresh())
            return
          }
          patchTab(tab.id, { totalCount: null, ddl: null })
          await Promise.all([get().refreshTables(), get().loadTab(tab.id), get().loadStructure(tab.id)])
        } else {
          await get().refreshTables()
        }
      },

      nextPage(id) {
        const tab = findTab(id)
        if (!tab || !tab.hasMore || tab.loading) return
        get().guarded([id], () => {
          const t = findTab(id)
          if (t) reload(id, { offset: t.offset + t.limit, selectedRow: null })
        })
      },

      prevPage(id) {
        const tab = findTab(id)
        if (!tab || tab.offset === 0 || tab.loading) return
        get().guarded([id], () => {
          const t = findTab(id)
          if (t) reload(id, { offset: Math.max(0, t.offset - t.limit), selectedRow: null })
        })
      },

      toggleSort(id, column, additive) {
        if (!findTab(id)) return
        get().guarded([id], () => {
          const t = findTab(id)
          if (t) reload(id, { sort: cycleSort(t.sort, column, additive), offset: 0, selectedRow: null })
        })
      },

      toggleFilterBar(id, open) {
        const tab = findTab(id)
        if (!tab) return
        const next = open ?? !tab.filterOpen
        const patch: Partial<TabState> = { filterOpen: next }
        if (next && tab.draftFilters.length === 0) {
          const first = tab.columns[0]?.name ?? ''
          patch.draftFilters = [newFilterRow(first)]
        }
        patchTab(id, patch)
      },

      setDraftFilters(id, rows) {
        patchTab(id, { draftFilters: rows })
      },

      addFilterRow(id) {
        patchTab(id, (t) => ({ draftFilters: [...t.draftFilters, newFilterRow(t.columns[0]?.name ?? '')] }))
      },

      setRawMode(id, raw) {
        patchTab(id, { rawMode: raw })
      },

      setRawWhere(id, raw) {
        patchTab(id, { rawWhere: raw })
      },

      applyFilters(id) {
        if (!findTab(id)) return
        get().guarded([id], () => {
          const tab = findTab(id)
          if (!tab) return
          // Raw mode sends only the typed condition; otherwise only the rows.
          const appliedFilters = tab.rawMode ? [] : effectiveFilters(tab.draftFilters)
          const appliedRawWhere = tab.rawMode ? tab.rawWhere.trim() : ''
          reload(id, { appliedFilters, appliedRawWhere, offset: 0, totalCount: null, selectedRow: null })
        })
      },

      clearFilters(id) {
        if (!findTab(id)) return
        get().guarded([id], () => {
          const tab = findTab(id)
          if (tab) clearNow(tab)
        })
      },

      selectRow(id, row) {
        patchTab(id, { selectedRow: row })
      },

      toggleDetail() {
        set({ showDetail: !get().showDetail })
      },

      setColumnWidth(id, column, width) {
        patchTab(id, (t) => ({ columnWidths: { ...t.columnWidths, [column]: width } }))
      },

      dispose() {
        disposed = true
        for (const t of get().tabs) {
          if (t.kind === 'table') cancelRequests(t)
          else if (t.requestId) void editor.cancel(t.requestId).catch(() => {})
        }
      },
    }
  })
  return store
}

export const WorkspaceContext = createContext<WorkspaceStore | null>(null)

/** Selects from the workspace store of the enclosing Workspace. */
export function useWorkspace<T>(selector: (s: WorkspaceState) => T): T {
  const store = useContext(WorkspaceContext)
  if (!store) throw new Error('useWorkspace must be used inside a Workspace')
  return useStore(store, selector)
}

/** The active tab of the enclosing Workspace. */
export function useActiveTab(): Tab | null {
  return useWorkspace((s) => s.tabs.find((t) => t.id === s.activeTabId) ?? null)
}
