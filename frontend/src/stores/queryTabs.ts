import type { Kind } from '../features/connections/types'
import type { EditorApi } from '../features/editor/api'
import { cycleSort, sortOrder, sortRows } from '../features/grid/sort'
import { useSettings } from './settings'
import { DEFAULT_MAX_ROWS, type CompletionSchema, type ExecResult, type ParamValue, type ParamValues, type Plan, type ResultSet, type TxInfo } from '../features/editor/types'
import type { Cell, ResultColumn, SortSpec } from '../features/workspace/types'
import type { PreviewStatement, ResultSource, SafeMode } from '../features/edits/types'
import { errorMessage } from '../lib/errors'
import { formatSql } from '../features/editor/sqlFormat'
import { fileErrorCode, type SqlFile, type SqlFileApi } from '../features/editor/sqlFiles'
import { setUIJSON } from '../lib/uiState'
import { displayRows, emptyPending, type DisplayRow, type Pending } from './pending'

/** Key of the Messages tab in the results panel. */
export const MESSAGES = 'messages'
/** Key of the Plan tab in the results panel. */
export const PLAN = 'plan'

/** The last query plan of a tab. */
export interface PlanState {
  plan: Plan
  analyze: boolean
  /** The statement that was explained. */
  sql: string
}

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
  /**
   * Replaces from..to (which held expect) with text in one undoable
   * transaction (Format). select: select the new text, else keep the
   * cursor at the matching place.
   */
  | { type: 'replace'; from: number; to: number; expect: string; text: string; select: boolean }
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
  /** The last Explain (shown in the Plan tab). */
  plan: PlanState | null
  /** Result set ID, MESSAGES or PLAN. */
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
  /** Confirm safe mode: a run waiting for the user to confirm its writes. */
  confirm: PendingRun | null
  /** The last values of the :name placeholders (saved with the draft). */
  params: ParamValues
  /** A run or Explain waiting for the values of its placeholders. */
  paramsPrompt: ParamsPrompt | null
  /** The SQL when the tab last ran (unrun changes: sql differs). */
  ranSql: string | null
  /** Editing of a result set, like a table (null: none loaded). */
  edit: ResultEdit | null
  /** The database changed since the results were fetched (no editing). */
  resultsStale?: boolean
  /** The .sql file the tab was opened from or saved to (null: none). */
  filePath: string | null
  /** The text as last read from or written to the file. */
  savedSql: string | null
  /** The file's modification time then (Unix ms; 0: unknown). */
  fileModTime: number
  /** The file's line ending ("\n" or "\r\n"), kept on save. */
  eol: string
}

/** A query tab with changes not saved to its file. */
export function fileDirty(tab: Pick<QueryTabState, 'filePath' | 'savedSql' | 'sql'>): boolean {
  return Boolean(tab.filePath) && tab.sql !== tab.savedSql
}

/**
 * Editing state of one query result set. The result is editable when its
 * statement is a SELECT from one table that includes the primary key (see
 * Go edits.ResolveSource); staged changes work as in a table tab.
 */
export interface ResultEdit {
  resultId: string
  loading: boolean
  /** Null while loading or when the lookup failed. */
  source: ResultSource | null
  /** The lookup failed (the result is then read-only). */
  error: string | null
  /** The result's columns with key, default and read-only flags. */
  columns: ResultColumn[]
  /** Result column name → table column name (aliases). */
  rename: Record<string, string>
  pending: Pending
  committing: boolean
  commitError: string | null
  failed: { key?: string; insertId?: number } | null
  preview: PreviewStatement[] | null
  previewLoading: boolean
}

/** A fresh (loading) edit state for a result set. */
export function newResultEdit(resultId: string, columns: ResultColumn[]): ResultEdit {
  return {
    resultId,
    loading: true,
    source: null,
    error: null,
    columns,
    rename: {},
    pending: emptyPending,
    committing: false,
    commitError: null,
    failed: null,
    preview: null,
    previewLoading: false,
  }
}

/**
 * The result's columns for editing: flags from the source mapping. Columns
 * that are not a (first) reference to a table column are read-only.
 */
export function sourceColumns(columns: ResultColumn[], src: ResultSource): { columns: ResultColumn[]; rename: Record<string, string> } {
  const rename: Record<string, string> = {}
  const out = columns.map((c, i) => {
    const m = src.editable ? src.columns[i] : undefined
    if (!m || !m.column || rename[c.name] !== undefined) return { ...c, isPK: false, readOnly: true }
    rename[c.name] = m.column
    return { ...c, isPK: m.isPK, nullable: m.nullable, hasDefault: m.hasDefault, enumValues: m.enumValues ?? null }
  })
  return { columns: out, rename }
}

/** The statement that produced a result set. */
export function statementOf(result: ExecResult | null, resultId: string) {
  return result?.statements.find((st) => st.resultSets.some((rs) => rs.id === resultId)) ?? null
}

/** A run that needs confirmation before it executes. */
export interface PendingRun {
  text: string
  base: number
  statements: string[]
  /** The values its placeholders were given. */
  params?: ParamValues
}

/** A run (or Explain) that waits for the values of its :name placeholders. */
export interface ParamsPrompt {
  names: string[]
  text: string
  base: number
  action: 'run' | 'explain'
  /** Explain analyze (action "explain"). */
  analyze?: boolean
}

export type RunMode = 'current' | 'all'

/** Query tab state and actions, part of the workspace store. */
export interface QueryState {
  historyOpen: boolean
  completion: CompletionSchema | null
  completionLoading: boolean
  /** A query tab whose save found its file changed on disk (Overwrite / Cancel). */
  fileConflict: string | null
  /** Why opening or saving a file failed (workspace banner). */
  fileError: string | null

  newQueryTab(sql?: string): string
  renameTab(id: string, title: string): void
  setSql(id: string, sql: string): void
  setEditorSelection(id: string, cursor: number, selection: Range | null): void
  updateCurrentStatement(id: string): Promise<void>
  runQuery(id: string, mode: RunMode): Promise<void>
  /** Shows the plan of the selection or the statement under the cursor. */
  explainQuery(id: string, analyze: boolean): Promise<void>
  /**
   * Formats the selection, or the whole script without one (settings:
   * keyword case, indent). A failure is shown in the tab's error banner and
   * leaves the text unchanged.
   */
  formatQuery(id: string): Promise<void>
  /**
   * Opens a .sql file (dialog): an open tab of that file is activated; the
   * file goes into the active tab when that is empty and untitled, else into
   * a new tab.
   */
  openSqlFile(): Promise<void>
  /** Opens .sql files by path (dropped files), each like openSqlFile. */
  openSqlFiles(paths: string[]): Promise<void>
  /** Saves to the tab's file (Save as without one); true when saved. */
  saveSqlFile(id: string): Promise<boolean>
  /** Asks for a file and saves the tab there; true when saved. */
  saveSqlFileAs(id: string): Promise<boolean>
  /** A save found the file changed on disk: Overwrite (true) or Cancel. */
  resolveFileConflict(overwrite: boolean): Promise<void>
  dismissFileError(): void
  /** Runs the run or Explain waiting for parameter values with these values (also kept for the next run). */
  submitParams(id: string, values: ParamValues): Promise<void>
  cancelParams(id: string): void
  /** Runs the run waiting for confirmation (confirm safe mode). */
  confirmRun(id: string): Promise<void>
  cancelConfirm(id: string): void
  cancelQuery(id: string): void
  loadMore(id: string, resultId: string): Promise<void>
  /**
   * Re-runs the statement that produced a result set (after its edits were
   * committed) and replaces just that statement's result.
   */
  refreshResult(id: string, resultId: string): Promise<void>
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

/** The state fields of QueryState (the rest are actions). */
export type QueryFields = 'historyOpen' | 'completion' | 'completionLoading' | 'fileConflict' | 'fileError'

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
    plan: null,
    activeResult: MESSAGES,
    sorts: {},
    selectedRow: null,
    loadingMore: false,
    error: null,
    tx: null,
    maxRows: useSettings.getState().settings.editorMaxRows || DEFAULT_MAX_ROWS,
    continueOnError: false,
    editorRequest: null,
    confirm: null,
    params: {},
    paramsPrompt: null,
    ranSql: null,
    edit: null,
    filePath: null,
    savedSql: null,
    fileModTime: 0,
    eol: '\n',
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

/** UI state key of the query history panel toggle (see lib/uiState). */
export const HISTORY_OPEN_KEY = 'query.historyOpen'

// ---- Drafts (localStorage, per connection) ------------------------------

export interface Draft {
  title: string
  sql: string
  /** Tabs of a .sql file (absent in drafts of older versions). */
  filePath?: string
  savedSql?: string
  fileModTime?: number
  eol?: string
  /** The tab's last parameter values (absent in drafts of older versions). */
  params?: ParamValues
}

/** At most this many parameter values are kept per draft. */
const MAX_DRAFT_PARAMS = 100

/** The draft's parameter values, if they are valid. */
function draftParams(d: Record<string, unknown>): Partial<Draft> {
  const raw = d.params
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return {}
  const params: ParamValues = {}
  for (const [name, v] of Object.entries(raw as Record<string, unknown>).slice(0, MAX_DRAFT_PARAMS)) {
    if (typeof v !== 'object' || v === null) continue
    const p = v as Partial<ParamValue>
    params[name] = { value: typeof p.value === 'string' ? p.value : '', null: p.null === true, type: p.type === 'number' ? 'number' : 'text' }
  }
  return Object.keys(params).length ? { params } : {}
}

/** The draft's file fields, if they are valid. */
function draftFile(d: Record<string, unknown>): Partial<Draft> {
  if (typeof d.filePath !== 'string' || !d.filePath) return {}
  return {
    filePath: d.filePath,
    savedSql: typeof d.savedSql === 'string' ? d.savedSql : '',
    fileModTime: typeof d.fileModTime === 'number' ? d.fileModTime : 0,
    eol: d.eol === '\r\n' ? '\r\n' : '\n',
  }
}

/** The tab state restored from a draft. */
export function tabFromDraft(d: Draft): QueryTabState {
  const tab = { ...newQueryTabState(d.title, d.sql), cursor: 0, params: d.params ?? {} }
  if (!d.filePath) return tab
  return { ...tab, filePath: d.filePath, savedSql: d.savedSql ?? '', fileModTime: d.fileModTime ?? 0, eol: d.eol ?? '\n' }
}

function toDraft(t: QueryTabState): Draft {
  const d: Draft = { title: t.title, sql: t.sql }
  if (t.filePath) Object.assign(d, { filePath: t.filePath, savedSql: t.savedSql ?? '', fileModTime: t.fileModTime, eol: t.eol })
  if (Object.keys(t.params).length) d.params = t.params
  return d
}

export function draftKey(connectionId: string): string {
  return `lontar.queryDrafts.${connectionId}`
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
      .map((d) => ({ title: d.title, sql: d.sql, ...draftFile(d as unknown as Record<string, unknown>), ...draftParams(d as unknown as Record<string, unknown>) }))
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
  files: SqlFileApi,
): Omit<QueryState, QueryFields> {
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
  const persist = () => saveDrafts(get().connectionId, queryTabs().map(toDraft))

  /** The tab state of a file that was just read or written. */
  const fileState = (f: SqlFile, text: string): Partial<QueryTabState> => ({
    title: f.name,
    filePath: f.path,
    savedSql: text,
    fileModTime: f.modTime,
    eol: f.eol === '\r\n' ? '\r\n' : '\n',
  })

  /** Shows a file in a tab: the tab of that file, the empty active tab, or a new one. */
  const showFile = (f: SqlFile) => {
    const open = queryTabs().find((t) => t.filePath === f.path)
    if (open) {
      set({ activeTabId: open.id } as Partial<S>)
      return
    }
    const { activeTabId } = get()
    const active = activeTabId ? findQuery(activeTabId) : undefined
    const reuse = active && !active.filePath && active.sql === '' && /^Query \d+$/.test(active.title) && !active.running
    const content = { ...fileState(f, f.text), sql: f.text, cursor: 0, selection: null, current: null, ranSql: null }
    if (reuse) patch(active.id, content)
    else {
      const tab = { ...newQueryTabState(f.name, f.text), ...content }
      set((s) => ({ tabs: [...s.tabs, tab], activeTabId: tab.id }) as Partial<S>)
    }
    persist()
  }

  /** A failed file operation: shown in the workspace banner. */
  const fileFailed = (err: unknown) => {
    if (!isDisposed()) set({ fileError: errorMessage(err) } as Partial<S>)
  }

  /** Saves a tab (path '' = Save as); true when written. */
  const save = async (id: string, path: string, expectedModTime: number): Promise<boolean> => {
    const tab = findQuery(id)
    if (!tab) return false
    const text = tab.sql
    try {
      const f = await files.save(path, tab.title.replace(/\.sql$/i, ''), text, tab.eol, expectedModTime)
      if (!f || isDisposed() || !findQuery(id)) return false
      patch(id, fileState(f, text))
      set({ fileError: null } as Partial<S>)
      persist()
      return true
    } catch (err) {
      if (fileErrorCode(err) === 'changed_on_disk') {
        if (!isDisposed()) set({ fileConflict: id } as Partial<S>)
        return false
      }
      fileFailed(err)
      return false
    }
  }

  /** The tab's backend editor, opened on first use. */
  const ensureEditor = async (id: string, tab: QueryTabState) => {
    if (tab.editorId) return tab.editorId
    const editorId = await api.openEditor(get().sessionId)
    patch(id, { editorId })
    return editorId
  }

  /** A failed run: show the message; a lost backend editor is reopened next time. */
  const runFailed = (id: string, err: unknown) => {
    const message = errorMessage(err)
    const lost = /editor not found/i.test(message)
    patch(id, { running: false, requestId: null, error: message, ...(lost ? { editorId: null, tx: null } : {}) })
  }

  /**
   * The text a run works on: everything (mode "all"), the selection, or the
   * statement under the cursor; base is its offset in the editor. Null when
   * there is nothing to run (a failed lookup sets the tab's error).
   */
  const targetText = async (id: string, tab: QueryTabState, mode: RunMode): Promise<{ text: string; base: number } | null> => {
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
          return null
        }
        if (!st) return null
        base = st.start
        text = tab.sql.slice(st.start, st.end)
      }
    }
    return text.trim() ? { text, base } : null
  }

  /**
   * The :name placeholders of text, or null when the lookup failed (the
   * tab's error is set).
   */
  const placeholders = async (id: string, text: string): Promise<string[] | null> => {
    try {
      return await api.paramNames(get().kind, text)
    } catch (err) {
      patch(id, { error: errorMessage(err) })
      return null
    }
  }

  /** Asks for the values of names before prompt's action runs. */
  const askParams = (id: string, prompt: ParamsPrompt) => patch(id, { running: false, requestId: null, error: null, paramsPrompt: prompt })

  /** Runs text (base = its offset in the editor); confirmed answers confirm safe mode. */
  const execute = async (id: string, text: string, base: number, confirmed: boolean, params?: ParamValues) => {
    const tab = findQuery(id)
    if (!tab) return
    const requestId = `run-${Date.now().toString(36)}-${nextSeq()}`
    patch(id, { running: true, requestId, error: null, confirm: null, paramsPrompt: null })
    try {
      const editorId = await ensureEditor(id, tab)
      const result = await api.execute(editorId, requestId, text, {
        maxRows: tab.maxRows,
        continueOnError: tab.continueOnError,
        baseOffset: base,
        confirmed,
        ...(params ? { params } : {}),
      })
      if (isDisposed() || findQuery(id)?.requestId !== requestId) return
      if (result.needsParams?.length) {
        askParams(id, { names: result.needsParams, text, base, action: 'run' })
        return
      }
      if (result.needsConfirm) {
        patch(id, { running: false, requestId: null, confirm: { text, base, statements: result.writeStatements ?? [], ...(params ? { params } : {}) } })
        return
      }
      const errPos = firstErrorPosition(result)
      patch(id, {
        running: false,
        requestId: null,
        result,
        activeResult: initialResultTab(result),
        sorts: {},
        selectedRow: null,
        edit: null,
        resultsStale: false,
        tx: result.tx,
        editorRequest: errPos ? { seq: nextSeq(), type: 'error', ...errPos } : findQuery(id)?.editorRequest ?? null,
      })
    } catch (err) {
      if (isDisposed() || findQuery(id)?.requestId !== requestId) return
      runFailed(id, err)
    }
  }

  /** Shows the plan of text (base = its offset in the editor). */
  const explain = async (id: string, text: string, base: number, analyze: boolean, params?: ParamValues) => {
    const tab = findQuery(id)
    if (!tab) return
    const requestId = `explain-${Date.now().toString(36)}-${nextSeq()}`
    patch(id, { running: true, requestId, error: null, confirm: null, paramsPrompt: null })
    try {
      const editorId = await ensureEditor(id, tab)
      const plan = await api.explain(editorId, requestId, text, { analyze, baseOffset: base, ...(params ? { params } : {}) })
      if (isDisposed() || findQuery(id)?.requestId !== requestId) return
      if (plan.needsParams?.length) {
        askParams(id, { names: plan.needsParams, text, base, action: 'explain', analyze })
        return
      }
      const pos = plan.error?.position ?? null
      patch(id, (t) => ({
        running: false,
        requestId: null,
        // Explain runs on the tab's connection, which closes an open cursor.
        result: t.result && {
          ...t.result,
          statements: t.result.statements.map((s) => ({ ...s, resultSets: s.resultSets.map((rs) => ({ ...rs, canLoadMore: false })) })),
        },
        plan: { plan, analyze, sql: text },
        activeResult: PLAN,
        selectedRow: null,
        tx: plan.tx,
        editorRequest:
          pos !== null ? { seq: nextSeq(), type: 'error', pos, from: base, to: base + text.length } : t.editorRequest,
      }))
    } catch (err) {
      if (isDisposed() || findQuery(id)?.requestId !== requestId) return
      runFailed(id, err)
    }
  }

  const actions: Omit<QueryState, QueryFields> = {
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
      patch(id, { ranSql: tab.sql })
      const target = await targetText(id, tab, mode)
      if (!target) return
      const names = await placeholders(id, target.text)
      if (!names) return
      if (names.length) {
        askParams(id, { names, text: target.text, base: target.base, action: 'run' })
        return
      }
      await execute(id, target.text, target.base, false)
    },

    async explainQuery(id, analyze) {
      const tab = findQuery(id)
      if (!tab || tab.running) return
      const target = await targetText(id, tab, 'current')
      if (!target) return
      const names = await placeholders(id, target.text)
      if (!names) return
      if (names.length) {
        askParams(id, { names, text: target.text, base: target.base, action: 'explain', analyze })
        return
      }
      await explain(id, target.text, target.base, analyze)
    },

    async submitParams(id, values) {
      const tab = findQuery(id)
      const prompt = tab?.paramsPrompt
      if (!tab || !prompt || tab.running) return
      const params = { ...tab.params, ...values }
      patch(id, { params, paramsPrompt: null })
      persist()
      if (prompt.action === 'explain') await explain(id, prompt.text, prompt.base, prompt.analyze ?? false, params)
      else await execute(id, prompt.text, prompt.base, false, params)
    },

    cancelParams(id) {
      patch(id, { paramsPrompt: null })
    },

    async formatQuery(id) {
      const tab = findQuery(id)
      if (!tab) return
      const sel = tab.selection
      const from = sel ? sel.from : 0
      const to = sel ? sel.to : tab.sql.length
      const original = tab.sql.slice(from, to)
      const { sqlFormat } = useSettings.getState().settings
      const res = await formatSql(
        original,
        get().kind,
        { keywordCase: sqlFormat?.keywordCase ?? 'upper', tabWidth: sqlFormat?.indent ?? 2 },
      )
      if (isDisposed() || !findQuery(id)) return
      if (!res.ok) {
        patch(id, { error: res.reason })
        return
      }
      if (res.text === original) return
      patch(id, { error: null, editorRequest: { seq: nextSeq(), type: 'replace', from, to, expect: original, text: res.text, select: Boolean(sel) } })
    },

    async openSqlFile() {
      try {
        const f = await files.open()
        if (f && !isDisposed()) showFile(f)
      } catch (err) {
        fileFailed(err)
      }
    },

    async openSqlFiles(paths) {
      for (const p of paths) {
        try {
          const f = await files.read(p)
          if (isDisposed()) return
          showFile(f)
        } catch (err) {
          fileFailed(err)
        }
      }
    },

    async saveSqlFile(id) {
      const tab = findQuery(id)
      if (!tab) return false
      return save(id, tab.filePath ?? '', tab.filePath ? tab.fileModTime : 0)
    },

    async saveSqlFileAs(id) {
      return save(id, '', 0)
    },

    async resolveFileConflict(overwrite) {
      const id = get().fileConflict
      set({ fileConflict: null } as Partial<S>)
      const tab = id ? findQuery(id) : undefined
      if (overwrite && tab?.filePath) await save(tab.id, tab.filePath, 0)
    },

    dismissFileError() {
      set({ fileError: null } as Partial<S>)
    },

    async confirmRun(id) {
      const tab = findQuery(id)
      if (!tab?.confirm || tab.running) return
      const { text, base, params } = tab.confirm
      patch(id, { confirm: null })
      await execute(id, text, base, true, params)
    },

    cancelConfirm(id) {
      patch(id, { confirm: null })
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

    async refreshResult(id, resultId) {
      const tab = findQuery(id)
      const st = statementOf(tab?.result ?? null, resultId)
      const old = tab?.result ? resultSets(tab.result).find((rs) => rs.id === resultId) : undefined
      if (!tab?.editorId || !st || !old || tab.running) return
      const requestId = `refresh-${Date.now().toString(36)}-${nextSeq()}`
      patch(id, { running: true, requestId, error: null })
      try {
        const res = await api.execute(tab.editorId, requestId, st.sql, {
          maxRows: Math.max(tab.maxRows, old.rows.length),
          continueOnError: false,
          baseOffset: st.start,
          confirmed: false,
          ...(Object.keys(tab.params).length ? { params: tab.params } : {}),
        })
        if (isDisposed() || findQuery(id)?.requestId !== requestId) return
        const fresh = res.statements[0]
        if (res.needsConfirm || !fresh) {
          patch(id, { running: false, requestId: null })
          return
        }
        patch(id, (t) => {
          const next = { ...fresh, index: st.index, start: st.start, end: st.end }
          const statements = (t.result?.statements ?? []).map((x) => (x.index === st.index ? next : x))
          const set = fresh.resultSets[0]
          const sorts = { ...t.sorts }
          if (set && sorts[resultId]) sorts[set.id] = sorts[resultId]
          return {
            running: false,
            requestId: null,
            result: t.result && { ...t.result, statements },
            activeResult: t.activeResult === resultId ? (set?.id ?? MESSAGES) : t.activeResult,
            sorts,
            selectedRow: null,
            edit: null,
            tx: res.tx,
          }
        })
      } catch (err) {
        if (isDisposed() || findQuery(id)?.requestId !== requestId) return
        patch(id, { running: false, requestId: null, error: errorMessage(err) })
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
      const historyOpen = open ?? !get().historyOpen
      set({ historyOpen } as Partial<S>)
      setUIJSON(HISTORY_OPEN_KEY, historyOpen)
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

/** The active result set with its staged edits, in display order. */
export interface ResultEditView {
  view: ResultView
  /** The edit state of this result set, once its source lookup started. */
  edit: ResultEdit | null
  columns: ResultColumn[]
  /** Fetched rows (sorted) with staged values, then staged inserts. */
  rows: DisplayRow[]
  cells: Cell[][]
  /** The result can be edited now. */
  editable: boolean
}

export function resultEditView(tab: Pick<QueryTabState, 'result' | 'activeResult' | 'sorts' | 'edit'>, safeMode: SafeMode): ResultEditView | null {
  const sets = resultSets(tab.result)
  const index = sets.findIndex((r) => r.id === tab.activeResult)
  if (index < 0) return null
  const set = sets[index]
  const sort = tab.sorts[set.id] ?? []
  const edit = tab.edit?.resultId === set.id ? tab.edit : null
  const ready = edit && !edit.loading && edit.source ? edit : null
  const columns = ready ? ready.columns : set.columns
  const all = displayRows(columns, set.rows, ready?.pending ?? emptyPending)
  const order = sortOrder(set.rows, set.columns.map((c) => c.name), sort)
  const rows = order ? [...order.map((i) => all[i]), ...all.slice(set.rows.length)] : all
  const cells = rows.map((r) => r.cells)
  const view: ResultView = { set, index, sort, rows: order ? order.map((i) => set.rows[i]) : set.rows }
  return { view, edit, columns, rows, cells, editable: Boolean(ready?.source?.editable) && safeMode !== 'readonly' }
}
