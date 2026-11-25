import { EditorService, HistoryService } from '../../../bindings/lontar/internal/services'
import type { Kind } from '../connections/types'
import type { Cell } from '../workspace/types'
import type { CompletionColumn, CompletionSchema, ExecOptions, ExecResult, ExplainOptions, HistoryEntry, Plan, PlanNode, ResultSet, Statement } from './types'

type BindingKind = Parameters<typeof EditorService.Split>[0]
type BindingResultSet = NonNullable<Awaited<ReturnType<typeof EditorService.FetchMore>>>
type BindingPlan = NonNullable<Awaited<ReturnType<typeof EditorService.Explain>>>
type BindingPlanNode = NonNullable<BindingPlan['nodes']>[number]

/** Backend calls of the SQL editor (injectable for tests). */
export interface EditorApi {
  openEditor(sessionId: string): Promise<string>
  closeEditor(editorId: string): Promise<void>
  execute(editorId: string, requestId: string, sql: string, opts: ExecOptions): Promise<ExecResult>
  explain(editorId: string, requestId: string, sql: string, opts: ExplainOptions): Promise<Plan>
  cancel(requestId: string): Promise<boolean>
  fetchMore(editorId: string, requestId: string, resultId: string, n: number): Promise<ResultSet>
  statementAt(kind: Kind, sql: string, cursor: number): Promise<Statement | null>
  /** The distinct :name placeholders of a script, in order of first use. */
  paramNames(kind: Kind, sql: string): Promise<string[]>
  completionSchema(sessionId: string, schema: string, refresh: boolean): Promise<CompletionSchema>
  completionColumns(sessionId: string, schema: string, table: string): Promise<CompletionColumn[]>
  historyList(connectionId: string, search: string, limit: number, offset: number): Promise<HistoryEntry[]>
  historyDelete(id: number): Promise<void>
  historyClear(connectionId: string): Promise<number>
}

/** Converts a result set from the bindings (Go nil slices arrive as null). */
export function toResultSet(rs: BindingResultSet): ResultSet {
  return {
    id: rs.id,
    columns: rs.columns ?? [],
    rows: (rs.rows ?? []).map((r) => (r ?? []) as unknown as Cell[]),
    truncated: rs.truncated,
    canLoadMore: rs.canLoadMore,
  }
}

/** Converts a plan node from the bindings (nil slices → [], missing numbers → null). */
export function toPlanNode(n: BindingPlanNode): PlanNode {
  return {
    op: n.op,
    object: n.object,
    detail: n.detail,
    estRows: n.estRows ?? null,
    actualRows: n.actualRows ?? null,
    loops: n.loops ?? null,
    cost: n.cost ?? null,
    timeMs: n.timeMs ?? null,
    props: n.props ?? [],
    children: (n.children ?? []).map(toPlanNode),
  }
}

/** Converts a plan from the bindings. */
export function toPlan(p: BindingPlan): Plan {
  return {
    ...p,
    rawFormat: p.rawFormat === 'text' ? 'text' : 'json',
    planningMs: p.planningMs ?? null,
    executionMs: p.executionMs ?? null,
    nodes: (p.nodes ?? []).map(toPlanNode),
    notices: p.notices ?? [],
    error: p.error ? { ...p.error, position: p.error.position ?? null } : null,
  } as Plan
}

/** Typed wrapper around the generated EditorService/HistoryService bindings. */
export const editorApi: EditorApi = {
  async openEditor(sessionId) {
    return EditorService.OpenEditor(sessionId)
  },
  async closeEditor(editorId) {
    await EditorService.CloseEditor(editorId)
  },
  async execute(editorId, requestId, sql, opts) {
    const res = await EditorService.Execute(editorId, requestId, sql, { ...opts, params: opts.params ?? null })
    if (!res) throw new Error('empty response')
    return {
      ...res,
      notices: res.notices ?? [],
      needsConfirm: res.needsConfirm ?? false,
      writeStatements: res.writeStatements ?? [],
      needsParams: res.needsParams ?? [],
      statements: (res.statements ?? []).map((s) => ({
        ...s,
        resultSets: (s.resultSets ?? []).map(toResultSet),
        rowsAffected: s.rowsAffected ?? null,
        error: s.error ? { ...s.error, position: s.error.position ?? null } : null,
      })),
    } as ExecResult
  },
  async explain(editorId, requestId, sql, opts) {
    const p = await EditorService.Explain(editorId, requestId, sql, { ...opts, params: opts.params ?? null })
    if (!p) throw new Error('empty response')
    return toPlan(p)
  },
  async cancel(requestId) {
    return EditorService.Cancel(requestId)
  },
  async fetchMore(editorId, requestId, resultId, n) {
    const rs = await EditorService.FetchMore(editorId, requestId, resultId, n)
    if (!rs) throw new Error('empty response')
    return toResultSet(rs)
  },
  async statementAt(kind, sql, cursor) {
    return (await EditorService.StatementAt(kind as BindingKind, sql, cursor)) ?? null
  },
  async paramNames(kind, sql) {
    return (await EditorService.ParamNames(kind as BindingKind, sql)) ?? []
  },
  async completionSchema(sessionId, schema, refresh) {
    const cs = await EditorService.CompletionSchema(sessionId, schema, refresh)
    if (!cs) throw new Error('empty response')
    return {
      ...cs,
      schemas: cs.schemas ?? [],
      tables: (cs.tables ?? []).map((t) => ({ ...t, columns: t.columns ?? null })),
    } as CompletionSchema
  },
  async completionColumns(sessionId, schema, table) {
    return (await EditorService.CompletionColumns(sessionId, schema, table)) ?? []
  },
  async historyList(connectionId, search, limit, offset) {
    return ((await HistoryService.List(connectionId, search, limit, offset)) ?? []) as unknown as HistoryEntry[]
  },
  async historyDelete(id) {
    await HistoryService.Delete(id)
  },
  async historyClear(connectionId) {
    return HistoryService.Clear(connectionId)
  },
}
