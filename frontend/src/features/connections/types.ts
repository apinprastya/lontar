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
  /** What AI assistants may do through `lontar mcp`; "off" when absent. */
  aiAccess?: AIAccess
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

/** What an AI assistant connected through Lontar's MCP server may do. */
export type AIAccess = 'off' | 'read' | 'write'

export const AI_ACCESS: { value: AIAccess; label: string; hint: string }[] = [
  { value: 'off', label: 'Off', hint: 'Not visible to AI assistants' },
  {
    value: 'read',
    label: 'Read',
    hint: 'Claude can read the schema and run read-only queries. Query results are sent to the AI provider.',
  },
  {
    value: 'write',
    label: 'Read and write',
    hint: 'Claude can also change data (INSERT, UPDATE, DELETE), each change approved by you first. Query results are sent to the AI provider.',
  },
]

/**
 * The access the MCP server grants (store.Connection.EffectiveAIAccess):
 * write is capped at read when the connection itself is read-only.
 */
export function effectiveAIAccess(c: Pick<Connection, 'aiAccess' | 'safeMode' | 'kind' | 'readOnly'>): AIAccess {
  const a = c.aiAccess ?? 'off'
  if (!AI_ACCESS.some((x) => x.value === a)) return 'off'
  if (a === 'write' && (c.safeMode === 'readonly' || (isFileBased(c.kind) && c.readOnly))) return 'read'
  return a
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
  /**
   * Jump hosts in dialling order: jumps[0] is reached from this machine and
   * the last one reaches `host`. At most MAX_JUMPS.
   */
  jumps?: SSHHop[] | null
}

/** Most jump hosts a connection may go through (store.MaxSSHJumps). */
export const MAX_JUMPS = 3

/** One jump host. Its secrets stay with its id when hops are reordered. */
export interface SSHHop {
  /** Assigned by the backend; '' for a hop that was never saved. */
  id?: string
  host?: string
  port?: number
  user?: string
  authMethod?: SSHAuthMethod
  keyPath?: string
  /** Saved secrets (flags only, set by the backend). */
  hasPassword?: boolean
  hasPassphrase?: boolean
}

/** SSH badge tooltip: the chain of hops when the connection has jump hosts. */
export function sshBadgeTitle(ssh: SSHConfig | null | undefined): string {
  return ssh?.jumps?.length ? `Connected through SSH: ${sshChain(ssh)}` : 'Connected through an SSH tunnel'
}

/** "you → bastion1 → bastion2 → db-gateway" for an SSH tunnel. */
export function sshChain(ssh: SSHConfig | null | undefined): string {
  if (!ssh) return ''
  return ['you', ...(ssh.jumps ?? []).map((h) => h.host ?? ''), ssh.host ?? ''].join(' → ')
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
  /** Jump host secrets in the order of ssh.jumps (sent only with jump hosts). */
  jumps?: HopSecrets[]
}

/** Secrets of one jump host (null keeps, '' clears). */
export interface HopSecrets {
  password: string | null
  passphrase: string | null
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
