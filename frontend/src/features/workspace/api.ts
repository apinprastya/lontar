import { BrowseService, SchemaService } from '../../../bindings/dbedit/internal/services'
import type { Cell, FetchRequest, Location, Page, TableInfo } from './types'

type BindingRequest = Parameters<typeof BrowseService.FetchTable>[2]

/** Backend calls used by the workspace store (injectable for tests). */
export interface WorkspaceApi {
  current(sessionId: string): Promise<Location>
  listDatabases(sessionId: string, includeSystem: boolean): Promise<string[]>
  useDatabase(sessionId: string, database: string): Promise<Location>
  listSchemas(sessionId: string, includeSystem: boolean): Promise<string[]>
  listTables(sessionId: string, schema: string): Promise<TableInfo[]>
  fetchTable(sessionId: string, requestId: string, req: FetchRequest): Promise<Page>
  countRows(sessionId: string, requestId: string, req: FetchRequest): Promise<number>
  cancel(requestId: string): Promise<boolean>
}

/** Typed wrapper around the generated SchemaService/BrowseService bindings. */
export const workspaceApi: WorkspaceApi = {
  async current(sessionId) {
    return (await SchemaService.Current(sessionId)) as Location
  },
  async listDatabases(sessionId, includeSystem) {
    return (await SchemaService.ListDatabases(sessionId, includeSystem)) ?? []
  },
  async useDatabase(sessionId, database) {
    return (await SchemaService.UseDatabase(sessionId, database)) as Location
  },
  async listSchemas(sessionId, includeSystem) {
    return (await SchemaService.ListSchemas(sessionId, includeSystem)) ?? []
  },
  async listTables(sessionId, schema) {
    return ((await SchemaService.ListTables(sessionId, schema)) ?? []) as unknown as TableInfo[]
  },
  async fetchTable(sessionId, requestId, req) {
    const page = await BrowseService.FetchTable(sessionId, requestId, req as unknown as BindingRequest)
    if (!page) throw new Error('empty response')
    return {
      ...page,
      columns: page.columns ?? [],
      rows: (page.rows ?? []) as unknown as Cell[][],
      totalCount: page.totalCount ?? null,
    } as Page
  },
  async countRows(sessionId, requestId, req) {
    return BrowseService.CountRows(sessionId, requestId, req as unknown as BindingRequest)
  },
  async cancel(requestId) {
    return BrowseService.CancelFetch(requestId)
  },
}
