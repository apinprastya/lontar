import { EditService } from '../../../bindings/lontar/internal/services'
import type { ApplyResult, ChangeSet, PreviewStatement, ResultSource } from './types'

type BindingChangeSet = Parameters<typeof EditService.Preview>[1]

/** Backend calls for committing grid edits (injectable for tests). */
export interface EditApi {
  preview(sessionId: string, cs: ChangeSet): Promise<PreviewStatement[]>
  apply(sessionId: string, cs: ChangeSet): Promise<ApplyResult>
  /** Whether a SQL editor result (from sql, with n columns) maps onto one table. */
  resultSource(sessionId: string, sql: string, columns: number): Promise<ResultSource>
}

export const editApi: EditApi = {
  async preview(sessionId, cs) {
    return ((await EditService.Preview(sessionId, cs as unknown as BindingChangeSet)) ?? []) as unknown as PreviewStatement[]
  },
  async apply(sessionId, cs) {
    const res = await EditService.Apply(sessionId, cs as unknown as BindingChangeSet)
    if (!res) throw new Error('empty response')
    return {
      ok: res.ok,
      result: res.result ? ({ ...res.result, insertedKeys: res.result.insertedKeys ?? [] } as unknown as ApplyResult['result']) : null,
      failure: (res.failure ?? null) as ApplyResult['failure'],
    }
  },
  async resultSource(sessionId, sql, columns) {
    const res = await EditService.ResultSource(sessionId, sql, columns)
    if (!res) throw new Error('empty response')
    return { ...res, columns: res.columns ?? [] } as unknown as ResultSource
  },
}
