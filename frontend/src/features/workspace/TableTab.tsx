import { useMemo } from 'react'
import { canEdit, readOnlyReason, useWorkspace, type TabState } from '../../stores/workspace'
import { displayRows } from '../../stores/pending'
import FilterBar from '../filters/FilterBar'
import DataGrid, { type GridEditing, type GridLinks } from '../grid/DataGrid'
import StructureView from '../structure/StructureView'
import { fkByColumn, referenceFilters } from '../structure/fk'
import { shortcutLabel } from '../../lib/mod'
import { gridEditing } from '../grid/editing'
import { LockIcon } from '../../components/icons'

/** Data / Structure switch at the bottom of a table tab. */
export function ViewToggle({ tab, platform }: { tab: TabState; platform?: string }) {
  const setTabView = useWorkspace((st) => st.setTabView)
  const hint = shortcutLabel(platform, 'E')
  return (
    <div className="view-toggle" role="group" aria-label="View">
      {(['data', 'structure'] as const).map((v) => (
        <button key={v} type="button" aria-pressed={tab.view === v} title={`Switch view (${hint})`} onClick={() => setTabView(tab.id, v)}>
          {v === 'data' ? 'Data' : 'Structure'}
        </button>
      ))}
      <span className="hint muted">{hint}</span>
    </div>
  )
}

/** Content of a table tab: filter bar, grid, inline errors; or the structure. */
export default function TableTab({ tab, platform }: { tab: TabState; platform?: string }) {
  const s = useWorkspace((st) => st)
  const safeMode = useWorkspace((st) => st.safeMode)
  const kind = useWorkspace((st) => st.kind)
  const filtered = tab.appliedFilters.length > 0 || tab.appliedRawWhere !== ''
  const editable = canEdit(tab, safeMode)
  const reason = tab.columns.length > 0 ? readOnlyReason(tab, safeMode) : null
  const rows = useMemo(() => displayRows(tab.columns, tab.rows, tab.pending), [tab.columns, tab.rows, tab.pending])
  const cells = useMemo(() => rows.map((r) => r.cells), [rows])

  const editing = useMemo<GridEditing | undefined>(
    () => (editable ? gridEditing({ id: tab.id, columns: tab.columns, rows: tab.rows, pending: tab.pending, failed: tab.failed }, rows, kind, s) : undefined),
    [editable, rows, tab, kind, s],
  )

  const fks = tab.structure?.foreignKeys
  const links = useMemo<GridLinks | undefined>(() => {
    if (!fks || fks.length === 0) return undefined
    const byCol = fkByColumn(fks, tab.columns)
    const fkAt = (col: number) => byCol[col] ?? null
    return {
      fkColumns: byCol.map((fk) => fk !== null),
      canFollow: (row, col) => {
        const fk = fkAt(col)
        return fk !== null && referenceFilters(fk, tab.columns, cells[row]) !== null
      },
      onFollow: (row, col) => {
        const fk = fkAt(col)
        if (fk && cells[row]) s.followReference(tab.id, fk, cells[row])
      },
    }
  }, [fks, tab.columns, tab.id, cells, s])

  if (tab.view === 'structure') {
    return (
      <div className="table-tab" role="tabpanel" aria-label={tab.table}>
        <StructureView tab={tab} />
        <ViewToggle tab={tab} platform={platform} />
      </div>
    )
  }

  return (
    <div className="table-tab" role="tabpanel" aria-label={tab.table}>
      {tab.filterOpen && (
        <FilterBar
          columns={tab.columns}
          rows={tab.draftFilters}
          rawMode={tab.rawMode}
          rawWhere={tab.rawWhere}
          active={filtered}
          onChange={(r) => s.setDraftFilters(tab.id, r)}
          onRawModeChange={(raw) => s.setRawMode(tab.id, raw)}
          onRawWhereChange={(raw) => s.setRawWhere(tab.id, raw)}
          onApply={() => s.applyFilters(tab.id)}
          onClear={() => s.clearFilters(tab.id)}
        />
      )}
      {reason && (
        <div className="banner readonly-banner" role="note" data-testid="readonly-banner">
          <LockIcon className="icon" /> <span>{reason}</span>
        </div>
      )}
      {tab.commitError && (
        <div className="banner error tab-error" role="alert" data-testid="commit-error">
          <span className="selectable">
            <strong>Commit failed.</strong> {tab.commitError} Your changes are still staged{tab.failed ? '; the failing row is highlighted' : ''}.
          </span>
        </div>
      )}
      {tab.error && (
        <div className="banner error tab-error" role="alert">
          <span className="selectable">{tab.error}</span>
          <button type="button" onClick={() => void s.loadTab(tab.id)}>
            Retry
          </button>
        </div>
      )}
      <div className="grid-area">
        {tab.columns.length > 0 && (
          <DataGrid
            columns={tab.columns}
            rows={cells}
            offset={tab.offset}
            sort={tab.sort}
            columnWidths={tab.columnWidths}
            platform={platform}
            onHeaderClick={(col, additive) => s.toggleSort(tab.id, col, additive)}
            onColumnResize={(col, w) => s.setColumnWidth(tab.id, col, w)}
            onSelectRow={(row) => s.selectRow(tab.id, row)}
            onToggleDetail={s.toggleDetail}
            editing={editing}
            links={links}
            onExportSelection={(spec) => s.requestExport({ type: 'selection', columns: tab.columns, rows: cells, spec, title: tab.table })}
          />
        )}
        {tab.loading && (
          <div className="grid-loading" role="progressbar" aria-label="Loading rows">
            <span>Loading…</span>
            <button type="button" onClick={() => s.cancelLoad(tab.id)}>
              Cancel
            </button>
          </div>
        )}
        {!tab.loading && !tab.error && tab.columns.length > 0 && cells.length === 0 && (
          <p className="grid-empty muted">{filtered ? 'No rows match the filter.' : 'This table is empty.'}</p>
        )}
      </div>
      <ViewToggle tab={tab} platform={platform} />
    </div>
  )
}
