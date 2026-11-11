import { useWorkspace } from '../../stores/workspace'
import { shortcutLabel } from '../../lib/mod'

/** Table tabs: italic = preview (replaced by the next single-click). */
export default function TabBar({ platform }: { platform?: string }) {
  const tabs = useWorkspace((s) => s.tabs)
  const activeTabId = useWorkspace((s) => s.activeTabId)
  const activateTab = useWorkspace((s) => s.activateTab)
  const pinTab = useWorkspace((s) => s.pinTab)
  const closeTab = useWorkspace((s) => s.closeTab)
  if (tabs.length === 0) return null
  return (
    <div className="tab-bar" role="tablist" aria-label="Open tables">
      {tabs.map((t, i) => (
        <div
          key={t.id}
          role="tab"
          tabIndex={0}
          aria-selected={t.id === activeTabId}
          className={`tab${t.pinned ? '' : ' tab-preview'}`}
          title={`${t.schema}.${t.table}${i < 9 ? ` (${shortcutLabel(platform, String(i + 1))})` : ''}`}
          onClick={() => activateTab(t.id)}
          onDoubleClick={() => pinTab(t.id)}
          onAuxClick={(e) => {
            if (e.button === 1) closeTab(t.id)
          }}
        >
          <span className="tab-title">{t.table}</span>
          <button
            type="button"
            className="tab-close"
            aria-label={`Close ${t.table}`}
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
