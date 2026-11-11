/**
 * Frontend view of the Go connection models. The generated bindings use TS
 * enums for Kind/Tag; these string-literal types are structurally identical
 * on the wire and nicer to work with. api.ts converts at the boundary.
 */

import type { SafeMode } from '../edits/types'

export type Kind = 'postgres' | 'mysql' | 'mariadb' | 'sqlite'
export type Tag = 'none' | 'local' | 'dev' | 'staging' | 'prod'

export interface Connection {
  id: string
  name: string
  kind: Kind
  host?: string
  port?: number
  user?: string
  database?: string
  filePath?: string
  readOnly?: boolean
  createIfMissing?: boolean
  options?: Record<string, string | undefined> | null
  tag: Tag
  color?: string
  /** Guards against accidental writes; "off" when absent. */
  safeMode?: SafeMode
  ssh?: unknown
  ssl?: unknown
  /** Set by the backend; the password itself never reaches the frontend. */
  hasPassword: boolean
  createdAt: string
  updatedAt: string
}

export interface DriverInfo {
  kind: Kind
  displayName: string
  defaultPort: number
  fileBased: boolean
}

export interface SessionInfo {
  id: string
  connectionId: string
  name: string
  kind: Kind
  serverVersion: string
  connectedAt: string
  /** Safe mode of the session (fixed until reconnect). */
  safeMode?: SafeMode
}

export const TAGS: { value: Tag; label: string; color: string }[] = [
  { value: 'none', label: 'None', color: '' },
  { value: 'local', label: 'Local', color: '#3fb950' },
  { value: 'dev', label: 'Development', color: '#2f81f7' },
  { value: 'staging', label: 'Staging', color: '#d29922' },
  { value: 'prod', label: 'Production', color: '#f85149' },
]

/** Extra colour choices besides the tag colours. */
export const EXTRA_COLORS = ['#a371f7', '#39c5cf', '#db61a2', '#8b949e']

/** Colour used for a connection's strip: its own colour or its tag's. */
export function connectionColor(c: Pick<Connection, 'color' | 'tag'>): string {
  return c.color || TAGS.find((t) => t.value === c.tag)?.color || ''
}

export const KIND_LABELS: Record<Kind, string> = {
  postgres: 'PostgreSQL',
  mysql: 'MySQL',
  mariadb: 'MariaDB',
  sqlite: 'SQLite',
}

/** Short badge text for a kind. */
export const KIND_BADGES: Record<Kind, string> = {
  postgres: 'PG',
  mysql: 'MY',
  mariadb: 'MA',
  sqlite: 'SL',
}

export function isFileBased(kind: Kind): boolean {
  return kind === 'sqlite'
}

/** "host:port/db" for servers, the file path for SQLite. */
export function connectionSummary(c: Connection): string {
  if (isFileBased(c.kind)) return c.filePath ?? ''
  let s = c.host ?? ''
  if (c.port) s += `:${c.port}`
  if (c.database) s += `/${c.database}`
  return s
}
