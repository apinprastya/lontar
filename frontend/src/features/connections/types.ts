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
  ssh?: SSHConfig | null
  ssl?: SSLConfig | null
  /** Set by the backend; the password itself never reaches the frontend. */
  hasPassword: boolean
  /** Saved SSH password / key passphrase (flags only, never the values). */
  hasSshPassword?: boolean
  hasSshPassphrase?: boolean
  createdAt: string
  updatedAt: string
}

export type SSHAuthMethod = 'password' | 'key' | 'agent'

/** SSH tunnel settings (secrets are passed separately, see Secrets). */
export interface SSHConfig {
  enabled: boolean
  host?: string
  port?: number
  user?: string
  authMethod?: SSHAuthMethod
  keyPath?: string
}

export type SSLMode = '' | 'disable' | 'preferred' | 'require' | 'verify-ca' | 'verify-full'

/** TLS settings; an empty mode leaves TLS to the driver options. */
export interface SSLConfig {
  mode?: SSLMode
  caFile?: string
  certFile?: string
  keyFile?: string
}

/** Secrets sent with Save/Test: null keeps the stored value, '' clears it. */
export interface Secrets {
  password: string | null
  sshPassword: string | null
  sshPassphrase: string | null
}

export const NO_SECRETS: Secrets = { password: null, sshPassword: null, sshPassphrase: null }

/** SSL modes offered per kind ('' = driver options / default). */
export function sslModes(kind: Kind): { value: SSLMode; label: string }[] {
  if (kind === 'sqlite') return []
  const modes: { value: SSLMode; label: string }[] = [
    { value: '', label: 'Default (driver options)' },
    { value: 'disable', label: 'Disable' },
  ]
  if (kind !== 'postgres') modes.push({ value: 'preferred', label: 'Preferred (TLS if offered, not verified)' })
  modes.push(
    { value: 'require', label: 'Require (encrypted, not verified)' },
    { value: 'verify-ca', label: 'Verify CA (chain, no host name)' },
    { value: 'verify-full', label: 'Verify full (chain and host name)' },
  )
  return modes
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
  /** Connected through an SSH tunnel. */
  tunneled?: boolean
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
