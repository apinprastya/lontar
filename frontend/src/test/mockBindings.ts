/**
 * Shared mock of the generated Go service bindings. Use from a test with:
 *
 *   vi.mock('<relative path>/bindings/dbedit/internal/services', () => import('<relative path>/test/mockBindings'))
 *
 * and import { ConnectionService, AppService, resetBindings } from this file.
 */
import { vi } from 'vitest'
import type { Connection, DriverInfo, SessionInfo } from '../features/connections/types'
import type { Location, Page, TableInfo } from '../features/workspace/types'

export const AppService = {
  Version: vi.fn<() => Promise<string>>(),
  Platform: vi.fn<() => Promise<string>>(),
}

export const ConnectionService = {
  Drivers: vi.fn<() => Promise<DriverInfo[] | null>>(),
  List: vi.fn<() => Promise<Connection[] | null>>(),
  Get: vi.fn<(id: string) => Promise<Connection>>(),
  Save: vi.fn<(c: Connection, password: string | null) => Promise<Connection>>(),
  Delete: vi.fn<(id: string) => Promise<void>>(),
  Duplicate: vi.fn<(id: string) => Promise<Connection>>(),
  Test: vi.fn<(c: Connection, password: string | null) => Promise<string>>(),
  Connect: vi.fn<(id: string) => Promise<SessionInfo>>(),
  Disconnect: vi.fn<(id: string) => Promise<void>>(),
}

export const SchemaService = {
  Current: vi.fn<(sessionId: string) => Promise<Location>>(),
  ListDatabases: vi.fn<(sessionId: string, includeSystem: boolean) => Promise<string[] | null>>(),
  UseDatabase: vi.fn<(sessionId: string, db: string) => Promise<Location>>(),
  ListSchemas: vi.fn<(sessionId: string, includeSystem: boolean) => Promise<string[] | null>>(),
  ListTables: vi.fn<(sessionId: string, schema: string) => Promise<TableInfo[] | null>>(),
  Columns: vi.fn<(sessionId: string, schema: string, table: string) => Promise<unknown[] | null>>(),
}

export const BrowseService = {
  FetchTable: vi.fn<(sessionId: string, requestId: string, req: unknown) => Promise<Page | null>>(),
  CountRows: vi.fn<(sessionId: string, requestId: string, req: unknown) => Promise<number>>(),
  CancelFetch: vi.fn<(requestId: string) => Promise<boolean>>(),
}

export const DRIVERS: DriverInfo[] = [
  { kind: 'postgres', displayName: 'PostgreSQL', defaultPort: 5432, fileBased: false },
  { kind: 'mysql', displayName: 'MySQL', defaultPort: 3306, fileBased: false },
  { kind: 'mariadb', displayName: 'MariaDB', defaultPort: 3306, fileBased: false },
  { kind: 'sqlite', displayName: 'SQLite', defaultPort: 0, fileBased: true },
]

export function makeConnection(over: Partial<Connection> = {}): Connection {
  return {
    id: 'c1',
    name: 'Production',
    kind: 'postgres',
    host: 'db.example.com',
    port: 5432,
    user: 'app',
    database: 'shop',
    tag: 'prod',
    hasPassword: true,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    ...over,
  }
}

export function makeSession(over: Partial<SessionInfo> = {}): SessionInfo {
  return {
    id: 's1',
    connectionId: 'c1',
    name: 'Production',
    kind: 'postgres',
    serverVersion: 'PostgreSQL 16.4',
    connectedAt: '2026-01-01T00:00:00Z',
    ...over,
  }
}

/** Resets every mock and installs harmless defaults. */
export function resetBindings() {
  for (const fn of [
    ...Object.values(AppService),
    ...Object.values(ConnectionService),
    ...Object.values(SchemaService),
    ...Object.values(BrowseService),
  ])
    fn.mockReset()
  AppService.Version.mockResolvedValue('0.0.1')
  AppService.Platform.mockResolvedValue('linux')
  ConnectionService.Drivers.mockResolvedValue(DRIVERS)
  ConnectionService.List.mockResolvedValue([])
  SchemaService.Current.mockResolvedValue({ database: 'shop', schema: 'public' })
  SchemaService.ListDatabases.mockResolvedValue(['shop'])
  SchemaService.ListSchemas.mockResolvedValue(['public'])
  SchemaService.ListTables.mockResolvedValue([])
  BrowseService.CancelFetch.mockResolvedValue(true)
}
