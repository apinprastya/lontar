import type { Kind } from '../features/connections/types'
import type { EditorApi } from '../features/editor/api'
import { cycleSort, sortRows } from '../features/grid/sort'
import { DEFAULT_MAX_ROWS, type CompletionSchema, type ExecResult, type ResultSet, type TxInfo } from '../features/editor/types'
import type { SortSpec } from '../features/workspace/types'
import { errorMessage } from '../lib/errors'

/** Key of the Messages tab in the results panel. */
export const MESSAGES = 'messages'

export interface Range {
  from: number
  to: number
}

/**
 * One-shot request to the editor view (select text, place the cursor,
 * insert text, open search). The view applies each seq once.
 */
export type EditorRequest = { seq: number } & (
  | { type: 'select'; from: number; to: number }
  | { type: 'error'; pos: number; from: number; to: number }
  | { type: 'insert'; text: string }
  | { type: 'search' }
)

/** State of one SQL editor tab. */
export interface QueryTabState {
  kind: 'query'
  id: string
  title: string
  /** Query tabs are never previews (kept for the shared tab bar). */
  pinned: true
  sql: string
  /** Cursor and selection in the editor (UTF-16 offsets). */
  cursor: number
  selection: Range | null
  /** The statement under the cursor (highlighted). */
  current: Range | null
  /** Backend editor (pinned connection); opened on the first run. */
  editorId: string | null
  running: boolean
  requestId: string | null
  result: ExecResult | null
  /** Result set ID or MESSAGES. */
  activeResult: string
  /** Client-side sort per result set. */
  sorts: Record<string, SortSpec[]>
  selectedRow: number | null
  loadingMore: boolean
  /** Run-level failure (not a statement error). */
  error: string | null
  tx: TxInfo | null
  maxRows: number
  continueOnError: boolean
  editorRequest: EditorRequest | null
}

export type RunMode = 'current' | 'all'

/** Query tab state and actions, part of the workspace store. */
export interface QueryState {
  historyOpen: boolean
  completion: CompletionSchema | null
  completionLoading: boolean

  newQueryTab(sql?: string): string
  renameTab(id: string, title: string): void
  setSql(id: string, sql: string): void
  setEditorSelection(id: string, cursor: number, selection: Range | null): void
  updateCurrentStatement(id: string): Promise<void>
  runQuery(id: string, mode: RunMode): Promise<void>
  cancelQuery(id: string): void
  loadMore(id: string, resultId: string): Promise<void>
  setActiveResult(id: string, key: string): void
  sortResult(id: string, resultId: string, column: string, additive: boolean): void
  selectResultRow(id: string, row: number | null): void
  setMaxRows(id: string, n: number): void
  setContinueOnError(id: string, on: boolean): void
  showStatement(id: string, index: number): void
  requestEditor(id: string, req: DistributiveOmit<EditorRequest, 'seq'>): void
  insertIntoEditor(text: string): void
  toggleHistory(open?: boolean): void
  loadCompletion(refresh?: boolean): Promise<void>
  loadCompletionColumns(table: string): Promise<void>
  /** Cleans up a query tab that is being closed. */
  releaseQueryTab(tab: QueryTabState): void
}

type DistributiveOmit<T, K extends keyof T> = T extends unknown ? Omit<T, K> : never

/** What the query actions need from the workspace store. */
export interface QueryHost {
  sessionId: string
  kind: Kind
  connectionId: string
  schema: string
  tabs: { kind: string; id: string }[]
  activeTabId: string | null
}

let seq = 0
function nextSeq() {
  seq += 1
  return seq
}

let idSeq = 0
function newTabId() {
  idSeq += 1
  return `query-${Date.now().toString(36)}-${idSeq}`
}

/** "Query N" with N one more than the highest number in use. */
export function nextQueryTitle(titles: string[]): string {
  let n = 0
  for (const t of titles) {
    const m = /^Query (\d+)$/.exec(t)
    if (m) n = Math.max(n, Number(m[1]))
  }
  return `Query ${n + 1}`
}

export function newQueryTabState(title: string, sql = ''): QueryTabState {
  return {
    kind: 'query',
    id: newTabId(),
    title,
    pinned: true,
    sql,
    cursor: sql.length,
    selection: null,
    current: null,
    editorId: null,
    running: false,
    requestId: null,
    result: null,
    activeResult: MESSAGES,
    sorts: {},
    selectedRow: null,
    loadingMore: false,
    error: null,
    tx: null,
    maxRows: DEFAULT_MAX_ROWS,
    continueOnError: false,
    editorRequest: null,
  }
}

/** All result sets of a run, in statement order. */
export function resultSets(result: ExecResult | null): ResultSet[] {
  return result ? result.statements.flatMap((s) => s.resultSets) : []
}

/** The tab to show after a run: the first error → Messages, else the first result set. */
export function initialResultTab(result: ExecResult): string {
  if (result.statements.some((s) => s.status === 'error' || s.status === 'cancelled')) return MESSAGES
  return resultSets(result)[0]?.id ?? MESSAGES
}

/** The first statement error with a position, if any. */
export function firstErrorPosition(result: ExecResult): { pos: number; from: number; to: number } | null {
  for (const s of result.statements) {
    if (s.error && s.error.position !== null) return { pos: s.error.position, from: s.start, to: s.end }
  }
  return null
}

// ---- Drafts (localStorage, per connection) ------------------------------

export interface Draft {
  title: string
  sql: string
}

export function draftKey(connectionId: string): string {
  return `dbedit.queryDrafts.${connectionId}`
}

/** Saved query tabs of a connection ([] when none or storage fails). */
export function loadDrafts(connectionId: string): Draft[] {
  if (!connectionId) return []
  try {
    const raw = window.localStorage.getItem(draftKey(connectionId))
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed
      .filter((d): d is Draft => typeof d === 'object' && d !== null && typeof d.title === 'string' && typeof d.sql === 'string')
      .slice(0, 50)
  } catch {
    return []
  }
}

export function saveDrafts(connectionId: string, drafts: Draft[]): void {
  if (!connectionId) return
  try {
    if (drafts.length === 0) window.localStorage.removeItem(draftKey(connectionId))
    else window.localStorage.setItem(draftKey(connectionId), JSON.stringify(drafts))
  } catch {
    // Storage full or unavailable: drafts are a convenience only.
  }
}

// ---- Actions ------------------------------------------------------------

type Tab = { kind: string; id: string }
type SetFn<S> = (partial: Partial<S> | ((s: S) => Partial<S>)) => void

/**
 * Creates the query-tab actions of the workspace store. isDisposed reports
 * a store whose workspace was unmounted (late responses are ignored).
 */
export function createQueryActions<S extends QueryHost & QueryState & { tabs: Tab[] }>(
  set: SetFn<S>,
  get: () => S,
  api: EditorApi,
  isDisposed: () => boolean,
): Omit<QueryState, 'historyOpen' | 'completion' | 'completionLoading'> {
  const queryTabs = () => get().tabs.filter((t): t is QueryTabState => t.kind === 'query')
  const findQuery = (id: string) => queryTabs().find((t) => t.id === id)
  const patch = (id: string, p: Partial<QueryTabState> | ((t: QueryTabState) => Partial<QueryTabState>)) => {
    set(
      (s) =>
        ({
          tabs: s.tabs.map((t) => (t.id === id && t.kind === 'query' ? { ...t, ...(typeof p === 'function' ? p(t as QueryTabState) : p) } : t)),
        }) as Partial<S>,
    )
  }
  const persist = () => saveDrafts(get().connectionId, queryTabs().map((t) => ({ title: t.title, sql: t.sql })))

  const actions: Omit<QueryState, 'historyOpen' | 'completion' | 'completionLoading'> = {
    newQueryTab(sql = '') {
      const tab = newQueryTabState(nextQueryTitle(queryTabs().map((t) => t.title)), sql)
      set((s) => ({ tabs: [...s.tabs, tab], activeTabId: tab.id }) as Partial<S>)
      persist()
      return tab.id
    },

    renameTab(id, title) {
      const t = title.trim()
      if (!t || !findQuery(id)) return
      patch(id, { title: t })
      persist()
    },

    setSql(id, sql) {
      if (!findQuery(id)) return
      patch(id, { sql })
      persist()
    },

    setEditorSelection(id, cursor, selection) {
      patch(id, { cursor, selection: selection && selection.to > selection.from ? selection : null })
    },

    async updateCurrentStatement(id) {
      const tab = findQuery(id)
      if (!tab) return
      const { sql, cursor } = tab
      try {
        const st = await api.statementAt(get().kind, sql, cursor)
        const cur = findQuery(id)
        if (isDisposed() || !cur || cur.sql !== sql || cur.cursor !== cursor) return
        patch(id, { current: st ? { from: st.start, to: st.end } : null })
      } catch {
        // Highlighting is cosmetic.
      }
    },

    async runQuery(id, mode) {
      const tab = findQuery(id)
      if (!tab || tab.running) return
      let text = tab.sql
      let base = 0
      if (mode === 'current') {
        if (tab.selection) {
          base = tab.selection.from
          text = tab.sql.slice(tab.selection.from, tab.selection.to)
        } else {
          let st
          try {
            st = await api.statementAt(get().kind, tab.sql, tab.cursor)
          } catch (err) {
            patch(id, { error: errorMessage(err) })
            return
          }
          if (!st) return
          base = st.start
          text = tab.sql.slice(st.start, st.end)
        }
      }
      if (!text.trim()) return
      const requestId = `run-${Date.now().toString(36)}-${nextSeq()}`
      patch(id, { running: true, requestId, error: null })
      try {
        let editorId = tab.editorId
        if (!editorId) {
          editorId = await api.openEditor(get().sessionId)
          patch(id, { editorId })
        }
        const result = await api.execute(editorId, requestId, text, {
          maxRows: tab.maxRows,
          continueOnError: tab.continueOnError,
          baseOffset: base,
        })
        if (isDisposed() || findQuery(id)?.requestId !== requestId) return
        const errPos = firstErrorPosition(result)
        patch(id, {
          running: false,
          requestId: null,
          result,
          activeResult: initialResultTab(result),
          sorts: {},
          selectedRow: null,
          tx: result.tx,
          editorRequest: errPos ? { seq: nextSeq(), type: 'error', ...errPos } : findQuery(id)?.editorRequest ?? null,
        })
      } catch (err) {
        if (isDisposed() || findQuery(id)?.requestId !== requestId) return
        const message = errorMessage(err)
        // The backend editor is gone (e.g. closed): open a new one next time.
        const lost = /editor not found/i.test(message)
        patch(id, { running: false, requestId: null, error: message, ...(lost ? { editorId: null, tx: null } : {}) })
      }
    },

    cancelQuery(id) {
      const tab = findQuery(id)
      if (tab?.requestId) void api.cancel(tab.requestId).catch(() => {})
    },

    async loadMore(id, resultId) {
      const tab = findQuery(id)
      if (!tab?.editorId || !tab.result || tab.loadingMore || tab.running) return
      patch(id, { loadingMore: true, error: null })
      const requestId = `more-${Date.now().toString(36)}-${nextSeq()}`
      const update = (fn: (rs: ResultSet) => ResultSet) => (t: QueryTabState) => ({
        loadingMore: false,
        result: t.result && {
          ...t.result,
          statements: t.result.statements.map((s) => ({
            ...s,
            resultSets: s.resultSets.map((rs) => (rs.id === resultId ? fn(rs) : rs)),
          })),
        },
      })
      try {
        const more = await api.fetchMore(tab.editorId, requestId, resultId, tab.maxRows)
        if (isDisposed() || !findQuery(id)) return
        patch(id, update((rs) => ({ ...rs, rows: [...rs.rows, ...more.rows], truncated: more.truncated, canLoadMore: more.canLoadMore })))
      } catch (err) {
        if (isDisposed() || !findQuery(id)) return
        patch(id, (t) => ({ ...update((rs) => ({ ...rs, canLoadMore: false }))(t), error: errorMessage(err) }))
      }
    },

    setActiveResult(id, key) {
      patch(id, { activeResult: key, selectedRow: null })
    },

    sortResult(id, resultId, column, additive) {
      patch(id, (t) => ({ sorts: { ...t.sorts, [resultId]: cycleSort(t.sorts[resultId] ?? [], column, additive) }, selectedRow: null }))
    },

    selectResultRow(id, row) {
      patch(id, { selectedRow: row })
    },

    setMaxRows(id, n) {
      patch(id, { maxRows: n })
    },

    setContinueOnError(id, on) {
      patch(id, { continueOnError: on })
    },

    showStatement(id, index) {
      const st = findQuery(id)?.result?.statements[index]
      if (!st) return
      if (st.error && st.error.position !== null) {
        actions.requestEditor(id, { type: 'error', pos: st.error.position, from: st.start, to: st.end })
      } else {
        actions.requestEditor(id, { type: 'select', from: st.start, to: st.end })
      }
    },

    requestEditor(id, req) {
      patch(id, { editorRequest: { ...req, seq: nextSeq() } as EditorRequest })
    },

    insertIntoEditor(text) {
      const { activeTabId } = get()
      const active = activeTabId ? findQuery(activeTabId) : undefined
      if (active) actions.requestEditor(active.id, { type: 'insert', text })
      else actions.newQueryTab(text)
    },

    toggleHistory(open) {
      set((s) => ({ historyOpen: open ?? !s.historyOpen }) as Partial<S>)
    },

    async loadCompletion(refresh = false) {
      const { sessionId, schema, completion, completionLoading } = get()
      if (!schema || completionLoading || (!refresh && completion?.schema === schema)) return
      set({ completionLoading: true } as Partial<S>)
      try {
        const cs = await api.completionSchema(sessionId, schema, refresh)
        if (isDisposed() || get().schema !== schema) return
        set({ completion: cs, completionLoading: false } as Partial<S>)
      } catch {
        set({ completionLoading: false } as Partial<S>)
      }
    },

    async loadCompletionColumns(table) {
      const { sessionId, completion } = get()
      const t = completion?.tables.find((x) => x.name === table)
      if (!completion || !t || t.columns !== null) return
      try {
        const columns = await api.completionColumns(sessionId, completion.schema, table)
        const cur = get().completion
        if (isDisposed() || !cur || cur.schema !== completion.schema) return
        set({ completion: { ...cur, tables: cur.tables.map((x) => (x.name === table ? { ...x, columns } : x)) } } as Partial<S>)
      } catch {
        // Completion is best effort.
      }
    },

    releaseQueryTab(tab) {
      if (tab.requestId) void api.cancel(tab.requestId).catch(() => {})
      if (tab.editorId) void api.closeEditor(tab.editorId).catch(() => {})
      // The tab is already removed from the store when this runs.
      persist()
    },
  }
  return actions
}

/** The active result set of a query tab with its rows in display (sorted) order. */
export interface ResultView {
  set: ResultSet
  index: number
  rows: ResultSet['rows']
  sort: SortSpec[]
}

export function activeResultView(tab: Pick<QueryTabState, 'result' | 'activeResult' | 'sorts'>): ResultView | null {
  const sets = resultSets(tab.result)
  const index = sets.findIndex((r) => r.id === tab.activeResult)
  if (index < 0) return null
  const set = sets[index]
  const sort = tab.sorts[set.id] ?? []
  return { set, index, sort, rows: sortRows(set.rows, set.columns.map((c) => c.name), sort) }
}
