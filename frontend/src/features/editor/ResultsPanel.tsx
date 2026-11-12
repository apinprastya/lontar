import { useEffect, useMemo, useState } from 'react'
import { useWorkspace } from '../../stores/workspace'
import { MESSAGES, resultEditView, resultSets, type QueryTabState, type ResultEditView } from '../../stores/queryTabs'
import DataGrid from '../grid/DataGrid'
import { gridEditing } from '../grid/editing'
import { shortcutLabel } from '../../lib/mod'
import { LockIcon } from '../../components/icons'
import Messages from './Messages'

/** Whether the active result can be edited, and why not. */
export function EditBadge({ ev }: { ev: ResultEditView }) {
  const e = ev.edit
  if (!e || e.loading) return null
  const src = e.source
  if (ev.editable && src) {
    const name = src.schema && src.schema !== 'main' ? `${src.schema}.${src.table}` : src.table
    return (
      <span className="result-edit-badge editable" data-testid="result-editable" title={`Edits to this result are saved to ${name}`}>
        Editing {name}
      </span>
    )
  }
  const reason = e.error ?? (src && !src.editable ? src.reason : 'This connection is in read-only safe mode.')
  return (
    <span className="result-edit-badge" data-testid="result-readonly" title={reason} aria-label={`Read-only: ${reason}`}>
      <LockIcon className="icon" /> Read-only
    </span>
  )
}

const fmt = (n: number) => n.toLocaleString('en-US')

/** Results of the last run: one tab per result set plus Messages. */
export default function ResultsPanel({ tab, platform }: { tab: QueryTabState; platform?: string }) {
  const s = useWorkspace((st) => st)
  const safeMode = useWorkspace((st) => st.safeMode)
  const kind = useWorkspace((st) => st.kind)
  const sets = useMemo(() => resultSets(tab.result), [tab.result])
  const { result, activeResult, sorts, edit } = tab
  const ev = useMemo(() => resultEditView({ result, activeResult, sorts, edit }, safeMode), [result, activeResult, sorts, edit, safeMode])
  const view = ev?.view ?? null
  const [widths, setWidths] = useState<Record<string, Record<string, number>>>({})
  const errors = tab.result?.statements.filter((st) => st.status === 'error').length ?? 0
  const loadSource = s.loadResultSource
  const setId = view?.set.id
  // Look up (once per result set) whether it maps onto one table.
  useEffect(() => {
    if (setId) void loadSource(tab.id)
  }, [setId, tab.id, loadSource])

  const editing = useMemo(() => {
    const e = ev?.edit
    if (!ev || !e || !ev.editable) return undefined
    return gridEditing({ id: tab.id, columns: ev.columns, rows: ev.view.set.rows, pending: e.pending, failed: e.failed }, ev.rows, kind, s)
  }, [ev, tab.id, kind, s])

  if (!tab.result) {
    return (
      <div className="results results-empty">
        {tab.error && (
          <div className="banner error" role="alert">
            <span className="selectable">{tab.error}</span>
          </div>
        )}
        <p className="muted">
          {tab.running
            ? 'Running…'
            : `Run the statement under the cursor with ${shortcutLabel(platform, 'Enter')}, or everything with ${shortcutLabel(platform, 'Enter', { shift: true })}.`}
        </p>
      </div>
    )
  }

  return (
    <div className="results">
      <div className="result-tabs" role="tablist" aria-label="Results">
        {sets.map((rs, i) => (
          <button key={rs.id} type="button" role="tab" aria-selected={tab.activeResult === rs.id} onClick={() => s.setActiveResult(tab.id, rs.id)}>
            Result {i + 1}
            <span className="muted"> {fmt(rs.rows.length)}{rs.truncated ? '+' : ''}</span>
          </button>
        ))}
        <button type="button" role="tab" aria-selected={tab.activeResult === MESSAGES} onClick={() => s.setActiveResult(tab.id, MESSAGES)}>
          Messages
          {errors > 0 && <span className="result-tab-errors"> {errors} ✗</span>}
        </button>
        {tab.running && <span className="muted result-running">Running…</span>}
        {ev && <span className="spacer" />}
        {ev && <EditBadge ev={ev} />}
      </div>
      {tab.error && (
        <div className="banner error results-error" role="alert">
          <span className="selectable">{tab.error}</span>
        </div>
      )}
      {ev?.edit?.commitError && (
        <div className="banner error results-error" role="alert" data-testid="commit-error">
          <span className="selectable">
            <strong>Commit failed.</strong> {ev.edit.commitError} Your changes are still staged{ev.edit.failed ? '; the failing row is highlighted' : ''}.
          </span>
        </div>
      )}
      {view && ev ? (
        <>
          {view.set.truncated && (
            <div className="banner truncated-banner" role="status">
              <span>
                Showing first {fmt(view.set.rows.length)} rows
                {view.set.canLoadMore ? ' — ' : '. Run the query again with a higher row limit to see more.'}
              </span>
              {view.set.canLoadMore && (
                <button type="button" disabled={tab.loadingMore || tab.running} onClick={() => void s.loadMore(tab.id, view.set.id)}>
                  {tab.loadingMore ? 'Loading…' : `Load ${fmt(tab.maxRows)} more`}
                </button>
              )}
            </div>
          )}
          <div className="grid-area">
            {view.set.columns.length > 0 && (
              <DataGrid
                columns={ev.columns}
                rows={ev.cells}
                offset={0}
                sort={view.sort}
                columnWidths={widths[view.set.id] ?? {}}
                platform={platform}
                onHeaderClick={(col, additive) => s.sortResult(tab.id, view.set.id, col, additive)}
                onColumnResize={(col, w) => setWidths((cur) => ({ ...cur, [view.set.id]: { ...cur[view.set.id], [col]: w } }))}
                onSelectRow={(row) => s.selectResultRow(tab.id, row)}
                onToggleDetail={s.toggleDetail}
                editing={editing}
                onExportSelection={(spec) => s.requestExport({ type: 'selection', columns: ev.columns, rows: ev.cells, spec, title: tab.title })}
              />
            )}
            {ev.rows.length === 0 && <p className="grid-empty muted">No rows.</p>}
          </div>
        </>
      ) : (
        <Messages result={tab.result} onShow={(i) => s.showStatement(tab.id, i)} />
      )}
    </div>
  )
}
