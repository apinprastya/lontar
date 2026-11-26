import { StructureEditService } from '../../../bindings/lontar/internal/services'
import type { Caps, StructureApplyResult, StructurePreview, TableChange } from './editTypes'

type BindingChange = Parameters<typeof StructureEditService.Preview>[1]
type BindingPreview = NonNullable<Awaited<ReturnType<typeof StructureEditService.Preview>>>
type BindingApply = NonNullable<Awaited<ReturnType<typeof StructureEditService.Apply>>>

/** Backend calls of structure editing (injectable for tests). */
export interface StructureEditApi {
  capabilities(sessionId: string): Promise<Caps>
  preview(sessionId: string, tc: TableChange): Promise<StructurePreview>
  apply(sessionId: string, tc: TableChange): Promise<StructureApplyResult>
  dropTablePreview(sessionId: string, schema: string, table: string, cascade: boolean): Promise<StructurePreview>
  dropTable(sessionId: string, schema: string, table: string, cascade: boolean): Promise<StructureApplyResult>
}

function toPreview(p: BindingPreview | null): StructurePreview {
  if (!p) throw new Error('empty response')
  return { statements: (p.statements ?? []) as StructurePreview['statements'], failure: (p.failure ?? null) as StructurePreview['failure'] }
}

function toApply(r: BindingApply | null): StructureApplyResult {
  if (!r) throw new Error('empty response')
  return {
    ok: r.ok,
    statements: (r.result?.statements ?? []) as StructureApplyResult['statements'],
    failure: (r.failure ?? null) as StructureApplyResult['failure'],
  }
}

export const structureEditApi: StructureEditApi = {
  async capabilities(sessionId) {
    const c = await StructureEditService.Capabilities(sessionId)
    if (!c) throw new Error('empty response')
    return { ...c, indexMethods: c.indexMethods ?? [], types: c.types ?? [] } as Caps
  },
  async preview(sessionId, tc) {
    return toPreview(await StructureEditService.Preview(sessionId, tc as unknown as BindingChange))
  },
  async apply(sessionId, tc) {
    return toApply(await StructureEditService.Apply(sessionId, tc as unknown as BindingChange))
  },
  async dropTablePreview(sessionId, schema, table, cascade) {
    return toPreview(await StructureEditService.DropTablePreview(sessionId, schema, table, cascade))
  },
  async dropTable(sessionId, schema, table, cascade) {
    return toApply(await StructureEditService.DropTable(sessionId, schema, table, cascade))
  },
}
