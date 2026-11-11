import { useMemo } from 'react'
import { canEdit, readOnlyReason, useWorkspace, type TabState } from '../../stores/workspace'
import { displayRows, stagedValue, type CellChange, type DisplayRow } from '../../stores/pending'
import FilterBar from '../filters/FilterBar'
import DataGrid, { type GridEditing } from '../grid/DataGrid'
import { initialText } from '../edits/editors'
import type { EditValue } from '../edits/types'
import { LockIcon } from '../../components/icons'

/** Cell changes for TSV pasted at (row, col), clipped to the grid. */
export function pasteChanges(tab: Pick<TabState, 'columns'>, rows: DisplayRow[], row: number, col: number, values: readonly (readonly string[])[]): CellChange[] {
  const out: CellChange[] = []
  values.forEach((line, dr) => {
    const d = rows[row + dr]
    if (!d || d.state === 'deleted') return
    line.forEach((text, dc) => {
      const c = col + dc
      const column = tab.columns[c]
      if (!column || d.locked[c]) return
      out.push({ ref: d.ref, column: column.name, value: { kind: 'value', text } })
    })
  })
  return out
}

/** Content of a table tab: filter bar, grid, inline errors. */
export default function TableTab({ tab, platform }: { tab: TabState; platform?: string }) {
  const s = useWorkspace((st) => st)
  const safeMode = useWorkspace((st) => st.safeMode)
  const kind = useWorkspace((st) => st.kind)
  const filtered = tab.appliedFilters.length > 0 || tab.appliedRawWhere !== ''
  const editable = canEdit(tab, safeMode)
  const reason = tab.columns.length > 0 ? readOnlyReason(tab, safeMode) : null
  const rows = useMemo(() => displayRows(tab.columns, tab.rows, tab.pending), [tab.columns, tab.rows, tab.pending])
  const cells = useMemo(() => rows.map((r) => r.cells), [rows])

  const editing = useMemo<GridEditing | undefined>(() => {
    if (!editable) return undefined
    const set = (row: number, col: number, value: EditValue) => {
      const d = rows[row]
      const column = tab.columns[col]
      if (d && column) s.stageCells(tab.id, [{ ref: d.ref, column: column.name, value }])
    }
    return {
      engine: kind,
      rowStates: rows,
      failed: tab.failed,
      editText: (row, col) => {
        const d = rows[row]
        const column = tab.columns[col]
        if (!d || !column) return ''
        const orig = d.ref.type === 'row' ? tab.rows[d.ref.index]?.[col] : undefined
        return initialText(stagedValue(tab.pending, d, column.name), orig)
      },
      onCommitCell: set,
      onSetCells: (items, value) =>
        s.stageCells(
          tab.id,
          items.flatMap(([col, row]) => {
            const d = rows[row]
            const column = tab.columns[col]
            return d && column && !d.locked[col] && d.state !== 'deleted' ? [{ ref: d.ref, column: column.name, value }] : []
          }),
        ),
      onDeleteRows: (rs) => s.markDeleted(tab.id, rs.flatMap((r) => (rows[r] ? [rows[r].ref] : []))),
      onDuplicateRow: (row) => rows[row] && s.duplicateRow(tab.id, rows[row].ref),
      onInsertRow: () => s.addRow(tab.id),
      onUndo: () => s.undoEdit(tab.id),
      onRedo: () => s.redoEdit(tab.id),
      onCommit: () => void s.requestCommit(tab.id),
      onPaste: (row, col, values) => s.stageCells(tab.id, pasteChanges(tab, rows, row, col, values)),
    }
  }, [editable, rows, tab, kind, s])

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
    </div>
  )
}
