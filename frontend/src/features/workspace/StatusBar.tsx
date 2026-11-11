import type { Tab, TabState } from '../../stores/workspace'
import { activeResultView, type QueryTabState } from '../../stores/queryTabs'
import { formatDuration, rowCountText } from '../editor/format'

const fmt = (n: number) => n.toLocaleString('en-US')

/**
 * Row range of a tab's page: "1–300 of ~12,345" (estimate from the table
 * list until the exact count arrives), "1–300 of 12,345", "1–300" (unknown
 * total) or "No rows".
 */
export function rowRangeText(tab: Pick<TabState, 'offset' | 'rows' | 'totalCount' | 'estimatedRows' | 'appliedFilters' | 'appliedRawWhere' | 'hasMore'>): string {
  const n = tab.rows.length
  if (n === 0) return tab.offset > 0 ? `No rows after ${fmt(tab.offset)}` : 'No rows'
  const range = `${fmt(tab.offset + 1)}–${fmt(tab.offset + n)}`
  if (tab.totalCount !== null) return `${range} of ${fmt(tab.totalCount)}`
  const filtered = tab.appliedFilters.length > 0 || tab.appliedRawWhere !== ''
  if (!filtered && tab.estimatedRows !== null && tab.estimatedRows >= tab.offset + n) return `${range} of ~${fmt(tab.estimatedRows)}`
  return tab.hasMore ? `${range} of many` : range
}

/** Staged edits of the active table tab. */
export interface PendingStatus {
  count: number
  committing: boolean
  previewLoading: boolean
  /** Label of the commit shortcut, e.g. "Ctrl+S". */
  commitHint: string
  onPreview(): void
  onCommit(): void
  onDiscard(): void
}

export interface StatusBarProps {
  tab: Tab | null
  serverVersion: string
  onPrev(): void
  onNext(): void
  pending?: PendingStatus | null
}

export function pendingText(n: number): string {
  return `${n.toLocaleString('en-US')} pending change${n === 1 ? '' : 's'}`
}

/** Bottom bar: row range, query time, paging and pending-change actions. */
export default function StatusBar({ tab, serverVersion, onPrev, onNext, pending }: StatusBarProps) {
  return (
    <footer className="statusbar ws-statusbar" role="status">
      {pending && pending.count > 0 && (
        <span className="pending-actions" data-testid="pending">
          <span className="pending-count">{pendingText(pending.count)}</span>
          <button type="button" onClick={pending.onDiscard} disabled={pending.committing}>
            Discard
          </button>
          <button type="button" onClick={pending.onPreview} disabled={pending.committing || pending.previewLoading}>
            Preview SQL
          </button>
          <button type="button" className="primary" onClick={pending.onCommit} disabled={pending.committing} title={`Commit (${pending.commitHint})`}>
            {pending.committing ? 'Committing…' : 'Commit'}
          </button>
        </span>
      )}
      {tab?.kind === 'query' && <QueryStatus tab={tab} />}
      {tab?.kind === 'table' && (
        <>
          <span data-testid="row-range">{rowRangeText(tab)}</span>
          {tab.counting && <span className="muted">counting…</span>}
          {tab.elapsedMs !== null && <span data-testid="query-time">{fmt(tab.elapsedMs)} ms</span>}
          <span className="pager">
            <button type="button" aria-label="Previous page" disabled={tab.loading || tab.offset === 0} onClick={onPrev}>
              ‹
            </button>
            <button type="button" aria-label="Next page" disabled={tab.loading || !tab.hasMore} onClick={onNext}>
              ›
            </button>
          </span>
        </>
      )}
      <span className="statusbar-hint" data-testid="statusbar-session">
        {serverVersion}
      </span>
    </footer>
  )
}

/** Status of an SQL tab: rows of the active result and the run's total time. */
export function QueryStatus({ tab }: { tab: QueryTabState }) {
  if (!tab.result) return tab.running ? <span>Running…</span> : null
  const view = activeResultView(tab)
  const n = tab.result.statements.length
  return (
    <>
      <span data-testid="row-range">
        {view ? rowCountText(view.set.rows.length, view.set.truncated) : `${n} statement${n === 1 ? '' : 's'}`}
      </span>
      <span data-testid="query-time">{formatDuration(tab.result.durationMs)}</span>
      {tab.running && <span className="muted">running…</span>}
    </>
  )
}
