import Welcome from '../features/connections/Welcome'
import Workspace from '../features/workspace/Workspace'
import { modLabel } from '../lib/mod'
import { useConnections } from '../stores/connections'
import { useAppInfo } from './useAppInfo'

const platformLabels: Record<string, string> = {
  darwin: 'macOS',
  linux: 'Linux',
  windows: 'Windows',
}

/** Human readable name for a Go runtime.GOOS value. */
export function platformLabel(goos: string): string {
  return platformLabels[goos] ?? goos
}

/**
 * Application shell: sidebar, main area and status bar. The main area shows
 * the connection list until a session is open, then the workspace.
 */
export default function App() {
  const info = useAppInfo()
  const platform = info.status === 'ready' ? info.platform : undefined
  const session = useConnections((s) => s.session)

  if (session) {
    // The workspace brings its own sidebar, toolbar and status bar.
    return (
      <div className="app-shell app-shell-workspace" data-platform={platform}>
        <Workspace platform={platform} />
      </div>
    )
  }

  return (
    <div className="app-shell" data-platform={platform}>
      <aside className="sidebar" aria-label="Sidebar">
        <div className="sidebar-header">dbedit</div>
        <p className="sidebar-empty">Not connected</p>
      </aside>

      <main className="workspace">
        <Welcome platform={platform} />
      </main>

      <footer className="statusbar" role="status">
        {info.status === 'loading' && <span>Loading…</span>}
        {info.status === 'ready' && (
          <>
            <span data-testid="app-version">v{info.version}</span>
            <span data-testid="app-platform">{platformLabel(info.platform)}</span>
            <span className="statusbar-hint">Shortcuts use {modLabel(info.platform)}</span>
          </>
        )}
        {info.status === 'error' && <span className="statusbar-error">Backend unavailable: {info.error}</span>}
      </footer>
    </div>
  )
}
