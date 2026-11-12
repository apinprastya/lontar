import { useMemo, useState } from 'react'
import { copyText } from '../../lib/clipboard'
import { ChevronIcon, MatViewIcon, RefreshIcon, TableIcon, ViewIcon } from '../../components/icons'
import { useWorkspace } from '../../stores/workspace'
import { KIND_LABELS, type Kind } from '../connections/types'
import { EngineBadge } from '../../components/EngineLogo'
import type { TableInfo } from '../workspace/types'
import { formatEstimate, groupTables } from './tableList'

export interface SidebarProps {
  name: string
  kind: Kind
  color: string
  /** Shown next to the name (safe mode). */
  badge?: React.ReactNode
  /** Opens the connection switcher (the header is then a button). */
  onSwitchConnection?(): void
  /** Shortcut label of the switcher, e.g. "Ctrl+K". */
  switchHint?: string
  /** Number of open connections (shown when more than one). */
  openCount?: number
}

/** Open context menu of a sidebar table. */
interface TableMenuState {
  t: TableInfo
  x: number
  y: number
}

/** Right-click menu of a table: open data / structure, copy name / DDL. */
export function TableMenu({ menu, onClose }: { menu: TableMenuState; onClose(): void }) {
  const openTableView = useWorkspace((s) => s.openTableView)
  const copyDDL = useWorkspace((s) => s.copyDDL)
  const act = (fn: () => void) => () => {
    fn()
    onClose()
  }
  const { t } = menu
  return (
    <div className="context-menu-backdrop" onMouseDown={onClose} onContextMenu={(e) => (e.preventDefault(), onClose())}>
      <ul
        className="context-menu"
        role="menu"
        aria-label={`${t.name} actions`}
        style={{ left: menu.x, top: menu.y }}
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => e.key === 'Escape' && onClose()}
      >
        {(
          [
            ['Open data', () => openTableView(t.schema, t.name, 'data')],
            ['Open structure', () => openTableView(t.schema, t.name, 'structure')],
            ['Copy name', () => void copyText(t.name).catch(() => {})],
            ['Copy DDL', () => void copyDDL(t.schema, t.name)],
          ] as const
        ).map(([label, fn]) => (
          <li role="none" key={label}>
            <button type="button" role="menuitem" autoFocus={label === 'Open data'} onClick={act(fn)}>
              <span>{label}</span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}

function TableItem({ t, active, onMenu }: { t: TableInfo; active: boolean; onMenu(m: TableMenuState): void }) {
  const openTable = useWorkspace((s) => s.openTable)
  const Icon = t.type === 'table' ? TableIcon : t.type === 'view' ? ViewIcon : MatViewIcon
  return (
    <li>
      <button
        type="button"
        className="table-item"
        aria-current={active ? 'true' : undefined}
        title={`${t.schema}.${t.name} (${t.type}) — double-click to keep the tab open`}
        onClick={() => openTable(t, false)}
        onDoubleClick={() => openTable(t, true)}
        onContextMenu={(e) => {
          e.preventDefault()
          onMenu({ t, x: e.clientX, y: e.clientY })
        }}
      >
        <Icon className={`icon table-icon table-icon-${t.type.replace(' ', '-')}`} />
        <span className="table-name">{t.name}</span>
        <span className="table-estimate">{formatEstimate(t.estimatedRows)}</span>
      </button>
    </li>
  )
}

/**
 * Workspace sidebar: connection header, database/schema switchers, search
 * and the grouped table/view list.
 */
export default function Sidebar({ name, kind, color, badge, onSwitchConnection, switchHint, openCount = 1 }: SidebarProps) {
  const s = useWorkspace((st) => st)
  const active = s.tabs.find((t) => t.id === s.activeTabId)
  const groups = useMemo(() => groupTables(s.tables, s.search), [s.tables, s.search])
  const [menu, setMenu] = useState<TableMenuState | null>(null)
  // MySQL: database == schema, so only the database switcher is shown.
  const showDatabases = kind !== 'sqlite'
  const showSchemas = kind === 'postgres' || (kind === 'sqlite' && s.schemas.length > 1)
  const isActive = (t: TableInfo) => active?.kind === 'table' && active.schema === t.schema && active.table === t.name

  return (
    <aside className="ws-sidebar" aria-label="Sidebar">
      <div className={`ws-conn${onSwitchConnection ? ' switchable' : ''}`}>
        {(() => {
          const body = (
            <>
              <span className="ws-conn-strip" style={color ? { background: color } : undefined} data-testid="conn-strip" />
              <EngineBadge kind={kind} className="ws-conn-kind" />
              <span className="ws-conn-text">
                <span className="ws-conn-name">
                  <strong>{name}</strong>
                  {openCount > 1 && (
                    <span className="ws-conn-count" title={`${openCount} connections open`} data-testid="open-count">
                      {openCount}
                    </span>
                  )}
                </span>
                <span className="muted">
                  {KIND_LABELS[kind] ?? kind} {badge}
                </span>
              </span>
            </>
          )
          // The whole card opens the connection switcher.
          return onSwitchConnection ? (
            <button
              type="button"
              className="ws-conn-card"
              onClick={onSwitchConnection}
              title={`Switch connection${switchHint ? ` (${switchHint})` : ''}`}
              aria-label={`${name}: switch connection`}
            >
              {body}
              <ChevronIcon className="icon ws-conn-chevron" />
            </button>
          ) : (
            <div className="ws-conn-card">{body}</div>
          )
        })()}
        <button type="button" className="icon-button ws-conn-refresh" aria-label="Refresh table list" title="Refresh" onClick={() => void s.refreshTables()}>
          <RefreshIcon />
        </button>
      </div>

      {showDatabases && (
        <label className="ws-switcher">
          <span>Database</span>
          <select aria-label="Database" value={s.database} onChange={(e) => void s.selectDatabase(e.target.value)}>
            {!s.databases.includes(s.database) && <option value={s.database}>{s.database || '— choose —'}</option>}
            {s.databases.map((d) => (
              <option key={d} value={d}>
                {d}
              </option>
            ))}
          </select>
        </label>
      )}
      {showSchemas && (
        <label className="ws-switcher">
          <span>Schema</span>
          <select aria-label="Schema" value={s.schema} onChange={(e) => void s.selectSchema(e.target.value)}>
            {s.schemas.map((d) => (
              <option key={d} value={d}>
                {d}
              </option>
            ))}
          </select>
        </label>
      )}
      <label className="ws-system">
        <input type="checkbox" checked={s.showSystem} onChange={(e) => void s.setShowSystem(e.target.checked)} /> Show system{' '}
        {kind === 'postgres' ? 'schemas' : kind === 'sqlite' ? 'schemas' : 'databases'}
      </label>

      <input
        className="search ws-search"
        type="search"
        placeholder="Search tables"
        aria-label="Search tables"
        value={s.search}
        onChange={(e) => s.setSearch(e.target.value)}
      />

      {s.sidebarError && (
        <p className="error sidebar-error" role="alert">
          {s.sidebarError}
        </p>
      )}

      <div className="table-list">
        {s.tablesLoading && s.tables.length === 0 && <p className="muted">Loading…</p>}
        {!s.tablesLoading && !s.sidebarError && kind !== 'sqlite' && !s.schema && (
          <p className="muted">Choose a database to see its tables.</p>
        )}
        {(['tables', 'views'] as const).map((g) =>
          groups[g].length > 0 ? (
            <section key={g} aria-label={g === 'tables' ? 'Tables' : 'Views'}>
              <h3 className="table-group">
                {g === 'tables' ? 'Tables' : 'Views'} <span className="muted">{groups[g].length}</span>
              </h3>
              <ul>
                {groups[g].map((t) => (
                  <TableItem key={t.name} t={t} active={isActive(t)} onMenu={setMenu} />
                ))}
              </ul>
            </section>
          ) : null,
        )}
        {!s.tablesLoading && s.tables.length > 0 && groups.tables.length + groups.views.length === 0 && (
          <p className="muted">No tables match “{s.search}”.</p>
        )}
        {!s.tablesLoading && !s.sidebarError && s.schema && s.tables.length === 0 && <p className="muted">No tables.</p>}
      </div>
      {menu && <TableMenu menu={menu} onClose={() => setMenu(null)} />}
    </aside>
  )
}
