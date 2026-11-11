import { useEffect, useRef, useState } from 'react'
import ConfirmDialog from '../../components/ConfirmDialog'
import { errorMessage } from '../../lib/errors'
import type { EditorApi } from './api'
import { firstLine, formatDuration, historyTime } from './format'
import type { HistoryEntry } from './types'

/** How long a click waits for a second click (double-click opens a tab). */
export const CLICK_DELAY = 250

export interface HistoryPanelProps {
  api: Pick<EditorApi, 'historyList' | 'historyDelete' | 'historyClear'>
  connectionId: string
  /** Changes after every run, so the list reloads. */
  refreshKey?: unknown
  onInsert(sql: string): void
  onOpen(sql: string): void
  onClose(): void
}

/** Query history of the connection: click inserts, double-click opens a new tab. */
export default function HistoryPanel({ api, connectionId, refreshKey, onInsert, onOpen, onClose }: HistoryPanelProps) {
  const [search, setSearch] = useState('')
  const [entries, setEntries] = useState<HistoryEntry[]>([])
  const [error, setError] = useState<string | null>(null)
  const [version, setVersion] = useState(0)
  const [confirmClear, setConfirmClear] = useState(false)
  const clickTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    let alive = true
    const timer = setTimeout(() => {
      api
        .historyList(connectionId, search, 200, 0)
        .then((list) => {
          if (!alive) return
          setEntries(list)
          setError(null)
        })
        .catch((err) => alive && setError(errorMessage(err)))
    }, 120)
    return () => {
      alive = false
      clearTimeout(timer)
    }
  }, [api, connectionId, search, version, refreshKey])

  useEffect(() => () => {
    if (clickTimer.current) clearTimeout(clickTimer.current)
  }, [])

  const click = (sql: string) => {
    if (clickTimer.current) clearTimeout(clickTimer.current)
    clickTimer.current = setTimeout(() => {
      clickTimer.current = null
      onInsert(sql)
    }, CLICK_DELAY)
  }
  const doubleClick = (sql: string) => {
    if (clickTimer.current) clearTimeout(clickTimer.current)
    clickTimer.current = null
    onOpen(sql)
  }
  const remove = async (id: number) => {
    try {
      await api.historyDelete(id)
      setEntries((list) => list.filter((e) => e.id !== id))
    } catch (err) {
      setError(errorMessage(err))
    }
  }
  const clear = async () => {
    setConfirmClear(false)
    try {
      await api.historyClear(connectionId)
      setVersion((v) => v + 1)
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <aside className="history-panel" aria-label="Query history">
      <div className="panel-header">
        <span>History</span>
        <span className="history-actions">
          <button type="button" disabled={entries.length === 0} onClick={() => setConfirmClear(true)}>
            Clear
          </button>
          <button type="button" className="icon-button" aria-label="Close history" onClick={onClose}>
            ×
          </button>
        </span>
      </div>
      <input
        type="search"
        className="history-search"
        placeholder="Search history"
        aria-label="Search history"
        value={search}
        onChange={(e) => setSearch(e.target.value)}
      />
      {error && (
        <div className="banner error history-error" role="alert">
          {error}
        </div>
      )}
      <ul className="history-list">
        {entries.length === 0 && <li className="muted history-empty">{search ? 'No matching queries.' : 'No queries yet.'}</li>}
        {entries.map((e) => (
          <li key={e.id} className={`history-item${e.isError ? ' history-error-item' : ''}`}>
            <button
              type="button"
              className="history-entry"
              title={`${e.sql}\n\nClick: insert into the editor · Double-click: open in a new tab`}
              onClick={() => click(e.sql)}
              onDoubleClick={() => doubleClick(e.sql)}
            >
              <span className="history-sql mono">{firstLine(e.sql, 200)}</span>
              <span className="history-meta">
                {e.isError && (
                  <span className="history-err" title={e.error} aria-label="failed">
                    ✗
                  </span>
                )}
                <span>{historyTime(e.startedAt)}</span>
                <span>{formatDuration(e.durationMs)}</span>
                {e.database && <span>{e.database}</span>}
              </span>
            </button>
            <button type="button" className="icon-button history-delete" aria-label="Delete from history" onClick={() => void remove(e.id)}>
              ×
            </button>
          </li>
        ))}
      </ul>
      {confirmClear && (
        <ConfirmDialog
          title="Clear history"
          message="Remove every query of this connection from the history?"
          confirmLabel="Clear"
          danger
          onConfirm={() => void clear()}
          onCancel={() => setConfirmClear(false)}
        />
      )}
    </aside>
  )
}
