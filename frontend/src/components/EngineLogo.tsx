/**
 * Database engine logos. Path data from Simple Icons (CC0-1.0,
 * https://simpleicons.org); the logos themselves are trademarks of their
 * owners and are used only to name the engine a connection uses.
 */
import { siMariadb, siMysql, siPostgresql, siSqlite } from 'simple-icons'
import { KIND_BADGES, KIND_LABELS, type Kind } from '../features/connections/types'

const ICONS: Record<Kind, { path: string; hex: string }> = {
  postgres: siPostgresql,
  mysql: siMysql,
  mariadb: siMariadb,
  sqlite: siSqlite,
}

/** Brand colour of an engine (the badge background). */
export function engineColor(kind: Kind): string {
  const i = ICONS[kind]
  return i ? `#${i.hex}` : '#6b6b76'
}

/** The engine's logo in currentColor (null for an unknown engine). */
export function EngineLogo({ kind, className, title }: { kind: Kind; className?: string; title?: string }) {
  const i = ICONS[kind]
  if (!i) return null
  return (
    <svg className={className ?? 'engine-logo'} viewBox="0 0 24 24" role={title ? 'img' : undefined} aria-hidden={title ? undefined : true} aria-label={title}>
      <path d={i.path} fill="currentColor" />
    </svg>
  )
}

/**
 * A rounded tile in the engine's colour with its logo in white; unknown
 * engines fall back to the short text badge.
 */
export function EngineBadge({ kind, className }: { kind: Kind; className?: string }) {
  const label = KIND_LABELS[kind] ?? kind
  return (
    <span className={`kind-badge kind-${kind}${className ? ` ${className}` : ''}`} title={label} data-testid="engine-badge" data-kind={kind}>
      {ICONS[kind] ? <EngineLogo kind={kind} className="engine-logo" /> : (KIND_BADGES[kind] ?? '?')}
      <span className="sr-only">{label}</span>
    </span>
  )
}
