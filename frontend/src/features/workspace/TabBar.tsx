import { useState } from 'react'
import { useWorkspace, type Tab } from '../../stores/workspace'
import { shortcutLabel } from '../../lib/mod'
import { displayName } from '../structure/fk'

/** Tab title; tables of another schema (e.g. opened by a foreign key) are qualified. */
function tabTitle(t: Tab, schema: string): string {
  return t.kind === 'query' ? t.title : displayName(t.schema, t.table, schema)
}

function tabTooltip(t: Tab): string {
  return t.kind === 'query' ? `${t.title} — double-click to rename` : `${t.schema}.${t.table}`
}

/**
 * Table and SQL tabs. Table tabs in italics are previews (replaced by the
 * next single-click; double-click keeps them). Double-clicking a query tab
 * renames it.
 */
export default function TabBar({ platform }: { platform?: string }) {
  const tabs = useWorkspace((s) => s.tabs)
  const activeTabId = useWorkspace((s) => s.activeTabId)
  const activateTab = useWorkspace((s) => s.activateTab)
  const pinTab = useWorkspace((s) => s.pinTab)
  const closeTab = useWorkspace((s) => s.closeTab)
  const renameTab = useWorkspace((s) => s.renameTab)
  const schema = useWorkspace((s) => s.schema)
  const [editing, setEditing] = useState<string | null>(null)
  const [draft, setDraft] = useState('')
  if (tabs.length === 0) return null

  const commit = () => {
    if (editing) renameTab(editing, draft)
    setEditing(null)
  }

  return (
    <div className="tab-bar" role="tablist" aria-label="Open tabs">
      {tabs.map((t, i) => (
        <div
          key={t.id}
          role="tab"
          tabIndex={0}
          aria-selected={t.id === activeTabId}
          className={`tab tab-${t.kind}${t.pinned ? '' : ' tab-preview'}`}
          title={`${tabTooltip(t)}${i < 9 ? ` (${shortcutLabel(platform, String(i + 1))})` : ''}`}
          onClick={() => activateTab(t.id)}
          onDoubleClick={() => {
            if (t.kind === 'table') pinTab(t.id)
            else {
              setDraft(t.title)
              setEditing(t.id)
            }
          }}
          onAuxClick={(e) => {
            if (e.button === 1) closeTab(t.id)
          }}
        >
          {t.kind === 'query' && <span className="tab-sql-icon" aria-hidden="true">SQL</span>}
          {editing === t.id ? (
            <input
              className="tab-rename"
              aria-label="Tab name"
              value={draft}
              autoFocus
              onChange={(e) => setDraft(e.target.value)}
              onBlur={commit}
              onClick={(e) => e.stopPropagation()}
              onKeyDown={(e) => {
                e.stopPropagation()
                if (e.key === 'Enter') commit()
                if (e.key === 'Escape') setEditing(null)
              }}
            />
          ) : (
            <span className="tab-title">{tabTitle(t, schema)}</span>
          )}
          {t.kind === 'query' && t.running && <span className="tab-running" aria-label="running" />}
          <button
            type="button"
            className="tab-close"
            aria-label={`Close ${tabTitle(t, schema)}`}
            onClick={(e) => {
              e.stopPropagation()
              closeTab(t.id)
            }}
          >
            ×
          </button>
        </div>
      ))}
    </div>
  )
}
