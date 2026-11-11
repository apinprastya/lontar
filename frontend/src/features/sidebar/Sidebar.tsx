import { useMemo } from 'react'
import { MatViewIcon, RefreshIcon, TableIcon, ViewIcon } from '../../components/icons'
import { useWorkspace } from '../../stores/workspace'
import { KIND_LABELS, type Kind } from '../connections/types'
import type { TableInfo } from '../workspace/types'
import { formatEstimate, groupTables } from './tableList'

export interface SidebarProps {
  name: string
  kind: Kind
  color: string
  /** Shown next to the name (safe mode). */
  badge?: React.ReactNode
}

function TableItem({ t, active }: { t: TableInfo; active: boolean }) {
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
export default function Sidebar({ name, kind, color, badge }: SidebarProps) {
  const s = useWorkspace((st) => st)
  const active = s.tabs.find((t) => t.id === s.activeTabId)
  const groups = useMemo(() => groupTables(s.tables, s.search), [s.tables, s.search])
  // MySQL: database == schema, so only the database switcher is shown.
  const showDatabases = kind !== 'sqlite'
  const showSchemas = kind === 'postgres' || (kind === 'sqlite' && s.schemas.length > 1)
  const isActive = (t: TableInfo) => active?.kind === 'table' && active.schema === t.schema && active.table === t.name

  return (
    <aside className="ws-sidebar" aria-label="Sidebar">
      <div className="ws-conn">
        <span className="ws-conn-strip" style={color ? { background: color } : undefined} data-testid="conn-strip" />
        <div className="ws-conn-text">
          <strong>{name}</strong>
          <span className="muted">
            {KIND_LABELS[kind] ?? kind} {badge}
          </span>
        </div>
        <button type="button" className="icon-button" aria-label="Refresh table list" title="Refresh" onClick={() => void s.refreshTables()}>
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
                  <TableItem key={t.name} t={t} active={isActive(t)} />
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
    </aside>
  )
}
