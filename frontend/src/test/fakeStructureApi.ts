import { vi } from 'vitest'
import type { StructureEditApi } from '../features/structure/editApi'
import type { Caps, TableChange } from '../features/structure/editTypes'
import { makeCaps } from './structure'

/** A fake StructureEditApi: previews one statement per change, applies them. */
export function fakeStructureApi(caps: Partial<Caps> = {}) {
  const sqlOf = (tc: TableChange) => [`-- ${tc.create ? 'CREATE' : 'ALTER'} ${tc.table}`]
  const api = {
    capabilities: vi.fn<StructureEditApi['capabilities']>(async () => makeCaps(caps)),
    preview: vi.fn<StructureEditApi['preview']>(async (_s, tc) => ({ statements: sqlOf(tc).map((sql) => ({ sql, section: 'table' as const, index: -1 })), failure: null })),
    apply: vi.fn<StructureEditApi['apply']>(async (_s, tc) => ({ ok: true, statements: sqlOf(tc).map((sql) => ({ sql, section: 'table' as const, index: -1 })), failure: null })),
    dropTablePreview: vi.fn<StructureEditApi['dropTablePreview']>(async (_s, schema, table, cascade) => ({
      statements: [{ sql: `DROP TABLE ${schema}.${table}${cascade ? ' CASCADE' : ''}`, section: 'table' as const, index: -1 }],
      failure: null,
    })),
    dropTable: vi.fn<StructureEditApi['dropTable']>(async (_s, schema, table) => ({ ok: true, statements: [{ sql: `DROP TABLE ${schema}.${table}`, section: 'table' as const, index: -1 }], failure: null })),
  } satisfies StructureEditApi
  return api
}
