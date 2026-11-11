import { useWorkspace, type TabState } from '../../stores/workspace'
import FilterBar from '../filters/FilterBar'
import DataGrid from '../grid/DataGrid'

/** Content of a table tab: filter bar, grid, inline errors. */
export default function TableTab({ tab, platform }: { tab: TabState; platform?: string }) {
  const s = useWorkspace((st) => st)
  const filtered = tab.appliedFilters.length > 0 || tab.appliedRawWhere !== ''
  return (
    <div className="table-tab" role="tabpanel" aria-label={tab.table}>
      {tab.filterOpen && (
        <FilterBar
          columns={tab.columns}
          rows={tab.draftFilters}
          rawMode={tab.rawMode}
          rawWhere={tab.rawWhere}
          active={filtered}
          onChange={(rows) => s.setDraftFilters(tab.id, rows)}
          onRawModeChange={(raw) => s.setRawMode(tab.id, raw)}
          onRawWhereChange={(raw) => s.setRawWhere(tab.id, raw)}
          onApply={() => s.applyFilters(tab.id)}
          onClear={() => s.clearFilters(tab.id)}
        />
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
            rows={tab.rows}
            offset={tab.offset}
            sort={tab.sort}
            columnWidths={tab.columnWidths}
            platform={platform}
            onHeaderClick={(col, additive) => s.toggleSort(tab.id, col, additive)}
            onColumnResize={(col, w) => s.setColumnWidth(tab.id, col, w)}
            onSelectRow={(row) => s.selectRow(tab.id, row)}
            onToggleDetail={s.toggleDetail}
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
        {!tab.loading && !tab.error && tab.columns.length > 0 && tab.rows.length === 0 && (
          <p className="grid-empty muted">{filtered ? 'No rows match the filter.' : 'This table is empty.'}</p>
        )}
      </div>
    </div>
  )
}
