import { useCallback, useMemo, useState } from 'react'
import { copyText } from '../../lib/clipboard'
import { getUIJSON, isStringArray, setUIJSON } from '../../lib/uiState'
import { CaretIcon, ChevronIcon, MatViewIcon, RefreshIcon, RowPlusIcon, TableIcon, ViewIcon } from '../../components/icons'
import { useWorkspace } from '../../stores/workspace'
import { KIND_LABELS, type Kind } from '../connections/types'
import { EngineBadge } from '../../components/EngineLogo'
import ObjectIcon from '../objects/ObjectIcon'
import { OBJECT_KIND_LABELS, objectKey, type DbObject } from '../objects/types'
import type { TableInfo } from '../workspace/types'
import { OBJECT_GROUPS, formatEstimate, groupObjects, groupTables } from './tableList'

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

/** Open context menu of a sidebar table or object. */
type MenuState = { t: TableInfo; x: number; y: number } | { o: DbObject; x: number; y: number }

/** A context menu at x, y; the first item takes the focus. */
function ContextMenu({ label, x, y, items, onClose }: { label: string; x: number; y: number; items: [string, () => void][]; onClose(): void }) {
  const act = (fn: () => void) => () => {
    fn()
    onClose()
  }
  return (
    <div className="context-menu-backdrop" onMouseDown={onClose} onContextMenu={(e) => (e.preventDefault(), onClose())}>
      <ul
        className="context-menu"
        role="menu"
        aria-label={label}
        style={{ left: x, top: y }}
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => e.key === 'Escape' && onClose()}
      >
        {items.map(([text, fn], i) => (
          <li role="none" key={text}>
            <button type="button" role="menuitem" autoFocus={i === 0} onClick={act(fn)}>
              <span>{text}</span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}

/** Right-click menu of a table: open data / structure, copy name / DDL. */
export function TableMenu({ menu, onClose }: { menu: { t: TableInfo; x: number; y: number }; onClose(): void }) {
  const openTableView = useWorkspace((s) => s.openTableView)
  const copyDDL = useWorkspace((s) => s.copyDDL)
  const editTableStructure = useWorkspace((s) => s.editTableStructure)
  const requestDropTable = useWorkspace((s) => s.requestDropTable)
  const writable = useWorkspace((s) => s.safeMode !== 'readonly')
  const { t } = menu
  const editStructure = () => void editTableStructure(t.schema, t.name)
  return (
    <ContextMenu
      label={`${t.name} actions`}
      x={menu.x}
      y={menu.y}
      onClose={onClose}
      items={[
        ['Open data', () => openTableView(t.schema, t.name, 'data')],
        ['Open structure', () => openTableView(t.schema, t.name, 'structure')],
        ['Copy name', () => void copyText(t.name).catch(() => {})],
        ['Copy DDL', () => void copyDDL(t.schema, t.name)],
        ...(writable && t.type === 'table'
          ? ([
              ['Edit structure', editStructure],
              ['Drop table…', () => void requestDropTable(t.schema, t.name)],
            ] as [string, () => void][])
          : []),
      ]}
    />
  )
}

/** Right-click menu of an object: copy name / DDL, DDL in a new query tab. */
export function ObjectMenu({ menu, onClose }: { menu: { o: DbObject; x: number; y: number }; onClose(): void }) {
  const copyObjectDDL = useWorkspace((s) => s.copyObjectDDL)
  const objectDDLToQuery = useWorkspace((s) => s.objectDDLToQuery)
  const { o } = menu
  return (
    <ContextMenu
      label={`${o.name} actions`}
      x={menu.x}
      y={menu.y}
      onClose={onClose}
      items={[
        ['Copy name', () => void copyText(o.name).catch(() => {})],
        ['Copy DDL', () => void copyObjectDDL(o)],
        ['Open in new query tab', () => void objectDDLToQuery(o)],
      ]}
    />
  )
}

/** UI state key of the collapsed sidebar groups (ids, all connections). */
export const COLLAPSED_GROUPS_KEY = 'sidebar.collapsedGroups'

/** The collapsed sidebar groups, saved in the UI state. */
function useCollapsedGroups(): [Set<string>, (id: string) => void] {
  const [collapsed, setCollapsed] = useState(() => new Set(getUIJSON(COLLAPSED_GROUPS_KEY, [], isStringArray)))
  const toggle = useCallback((id: string) => {
    setCollapsed((cur) => {
      const next = new Set(cur)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      setUIJSON(COLLAPSED_GROUPS_KEY, [...next])
      return next
    })
  }, [])
  return [collapsed, toggle]
}

/** A collapsible group of the list: heading with count, then the items. */
function SidebarGroup({ label, count, collapsed, onToggle, children }: { label: string; count: number; collapsed: boolean; onToggle(): void; children: React.ReactNode }) {
  return (
    <section aria-label={label}>
      <h3 className="table-group">
        <button type="button" className="table-group-toggle" aria-expanded={!collapsed} onClick={onToggle}>
          <CaretIcon className="icon caret" />
          {label} <span className="muted">{count}</span>
        </button>
      </h3>
      {!collapsed && <ul>{children}</ul>}
    </section>
  )
}

function ObjectItem({ o, active, onMenu }: { o: DbObject; active: boolean; onMenu(m: MenuState): void }) {
  const openObject = useWorkspace((s) => s.openObject)
  const routine = o.kind === 'function' || o.kind === 'procedure' || o.kind === 'aggregate'
  const extra = routine ? `(${o.signature})` : o.kind === 'trigger' ? o.table : o.kind === 'enum' || o.kind === 'composite' || o.kind === 'domain' ? o.kind : ''
  return (
    <li>
      <button
        type="button"
        className="table-item object-item"
        aria-current={active ? 'true' : undefined}
        title={`${o.schema}.${o.name} (${OBJECT_KIND_LABELS[o.kind]})${o.detail ? ` — ${o.detail}` : ''}`}
        onClick={() => openObject(o)}
        onContextMenu={(e) => {
          e.preventDefault()
          onMenu({ o, x: e.clientX, y: e.clientY })
        }}
      >
        <ObjectIcon kind={o.kind} />
        <span className="table-name">
          {o.name}
          {extra && !routine && ' '}
          {extra && <span className="object-extra">{extra}</span>}
        </span>
      </button>
    </li>
  )
}

function TableItem({ t, active, onMenu }: { t: TableInfo; active: boolean; onMenu(m: MenuState): void }) {
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
  const objectGroups = useMemo(() => groupObjects(s.objects, s.search), [s.objects, s.search])
  const objectCount = OBJECT_GROUPS.reduce((n, g) => n + objectGroups[g.id].length, 0)
  const [menu, setMenu] = useState<MenuState | null>(null)
  const [collapsed, toggleGroup] = useCollapsedGroups()
  // MySQL: database == schema, so only the database switcher is shown.
  const showDatabases = kind !== 'sqlite'
  const showSchemas = kind === 'postgres' || (kind === 'sqlite' && s.schemas.length > 1)
  const isActive = (t: TableInfo) => active?.kind === 'table' && active.schema === t.schema && active.table === t.name
  const activeObject = active?.kind === 'object' ? objectKey(active.object) : null

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
        {s.safeMode !== 'readonly' && s.schema && (
          <button type="button" className="icon-button ws-conn-refresh" aria-label="New table" title="New table" onClick={() => void s.newTable()}>
            <RowPlusIcon />
          </button>
        )}
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
        placeholder="Search tables and objects"
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
            <SidebarGroup
              key={g}
              label={g === 'tables' ? 'Tables' : 'Views'}
              count={groups[g].length}
              collapsed={collapsed.has(g)}
              onToggle={() => toggleGroup(g)}
            >
              {groups[g].map((t) => (
                <TableItem key={t.name} t={t} active={isActive(t)} onMenu={setMenu} />
              ))}
            </SidebarGroup>
          ) : null,
        )}
        {OBJECT_GROUPS.map((g) =>
          objectGroups[g.id].length > 0 ? (
            <SidebarGroup key={g.id} label={g.label} count={objectGroups[g.id].length} collapsed={collapsed.has(g.id)} onToggle={() => toggleGroup(g.id)}>
              {objectGroups[g.id].map((o) => (
                <ObjectItem key={objectKey(o)} o={o} active={activeObject === objectKey(o)} onMenu={setMenu} />
              ))}
            </SidebarGroup>
          ) : null,
        )}
        {!s.tablesLoading && s.tables.length + s.objects.length > 0 && groups.tables.length + groups.views.length + objectCount === 0 && (
          <p className="muted">Nothing matches “{s.search}”.</p>
        )}
        {!s.tablesLoading && !s.sidebarError && s.schema && s.tables.length === 0 && <p className="muted">No tables.</p>}
      </div>
      {menu && ('t' in menu ? <TableMenu menu={menu} onClose={() => setMenu(null)} /> : <ObjectMenu menu={menu} onClose={() => setMenu(null)} />)}
    </aside>
  )
}
