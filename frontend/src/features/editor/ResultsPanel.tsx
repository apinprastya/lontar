import { useMemo, useState } from 'react'
import { useWorkspace } from '../../stores/workspace'
import { MESSAGES, activeResultView, resultSets, type QueryTabState } from '../../stores/queryTabs'
import DataGrid from '../grid/DataGrid'
import { shortcutLabel } from '../../lib/mod'
import Messages from './Messages'

const fmt = (n: number) => n.toLocaleString('en-US')

/** Results of the last run: one tab per result set plus Messages. */
export default function ResultsPanel({ tab, platform }: { tab: QueryTabState; platform?: string }) {
  const s = useWorkspace((st) => st)
  const sets = useMemo(() => resultSets(tab.result), [tab.result])
  const { result, activeResult, sorts } = tab
  const view = useMemo(() => activeResultView({ result, activeResult, sorts }), [result, activeResult, sorts])
  const [widths, setWidths] = useState<Record<string, Record<string, number>>>({})
  const errors = tab.result?.statements.filter((st) => st.status === 'error').length ?? 0

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
      </div>
      {tab.error && (
        <div className="banner error results-error" role="alert">
          <span className="selectable">{tab.error}</span>
        </div>
      )}
      {view ? (
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
                columns={view.set.columns}
                rows={view.rows}
                offset={0}
                sort={view.sort}
                columnWidths={widths[view.set.id] ?? {}}
                platform={platform}
                onHeaderClick={(col, additive) => s.sortResult(tab.id, view.set.id, col, additive)}
                onColumnResize={(col, w) => setWidths((cur) => ({ ...cur, [view.set.id]: { ...cur[view.set.id], [col]: w } }))}
                onSelectRow={(row) => s.selectResultRow(tab.id, row)}
                onToggleDetail={s.toggleDetail}
              />
            )}
            {view.rows.length === 0 && <p className="grid-empty muted">No rows.</p>}
          </div>
        </>
      ) : (
        <Messages result={tab.result} onShow={(i) => s.showStatement(tab.id, i)} />
      )}
    </div>
  )
}
