import { EditService } from '../../../bindings/lontar/internal/services'
import type { ApplyResult, ChangeSet, PreviewStatement } from './types'

type BindingChangeSet = Parameters<typeof EditService.Preview>[1]

/** Backend calls for committing grid edits (injectable for tests). */
export interface EditApi {
  preview(sessionId: string, cs: ChangeSet): Promise<PreviewStatement[]>
  apply(sessionId: string, cs: ChangeSet): Promise<ApplyResult>
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
}
