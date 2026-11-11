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
import { createQueryActions, loadDrafts, newQueryTabState, type QueryState, type QueryTabState } from './queryTabs'

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
}

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
    limit: PAGE_SIZE,
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
}

export function createWorkspaceStore(session: WorkspaceSession, api: WorkspaceApi, editor: EditorApi = defaultEditorApi): WorkspaceStore {
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
      const rest = tabs.filter((t) => t.kind !== 'table')
      set({ tabs: rest, activeTabId: rest.some((t) => t.id === activeTabId) ? activeTabId : (rest[0]?.id ?? null) })
    }

    return {
      ...createQueryActions<WorkspaceState>(set, get, editor, () => disposed),
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
      },

      pinTab(id) {
        patchTab(id, { pinned: true })
      },

      closeTab(id) {
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

      activateTab(id) {
        if (findTab(id)) set({ activeTabId: id })
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
        } else if (tab) {
          patchTab(tab.id, { totalCount: null })
          await Promise.all([get().refreshTables(), get().loadTab(tab.id)])
        } else {
          await get().refreshTables()
        }
      },

      nextPage(id) {
        const tab = findTab(id)
        if (!tab || !tab.hasMore || tab.loading) return
        reload(id, { offset: tab.offset + tab.limit, selectedRow: null })
      },

      prevPage(id) {
        const tab = findTab(id)
        if (!tab || tab.offset === 0 || tab.loading) return
        reload(id, { offset: Math.max(0, tab.offset - tab.limit), selectedRow: null })
      },

      toggleSort(id, column, additive) {
        const tab = findTab(id)
        if (!tab) return
        reload(id, { sort: cycleSort(tab.sort, column, additive), offset: 0, selectedRow: null })
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
        const tab = findTab(id)
        if (!tab) return
        // Raw mode sends only the typed condition; otherwise only the rows.
        const appliedFilters = tab.rawMode ? [] : effectiveFilters(tab.draftFilters)
        const appliedRawWhere = tab.rawMode ? tab.rawWhere.trim() : ''
        reload(id, { appliedFilters, appliedRawWhere, offset: 0, totalCount: null, selectedRow: null })
      },

      clearFilters(id) {
        const tab = findTab(id)
        if (!tab) return
        const first = tab.columns[0]?.name ?? ''
        reload(id, {
          draftFilters: [newFilterRow(first)],
          rawWhere: '',
          appliedFilters: [],
          appliedRawWhere: '',
          offset: 0,
          totalCount: null,
          selectedRow: null,
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
