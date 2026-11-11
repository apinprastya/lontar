import Welcome from '../features/connections/Welcome'
import { KIND_LABELS, connectionColor } from '../features/connections/types'
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
  const connection = useConnections((s) => s.connections.find((c) => c.id === s.session?.connectionId))
  const color = connection ? connectionColor(connection) : ''

  return (
    <div className="app-shell" data-platform={platform}>
      <aside className="sidebar" aria-label="Sidebar">
        <div className="sidebar-header">dbedit</div>
        {session ? (
          <div className="sidebar-session">
            <span className="color-dot" style={color ? { background: color } : undefined} />
            <span>
              <strong>{session.name}</strong>
              <br />
              <span className="muted">{KIND_LABELS[session.kind] ?? session.kind}</span>
            </span>
          </div>
        ) : (
          <p className="sidebar-empty">Not connected</p>
        )}
      </aside>

      <main className="workspace">{session ? <Workspace /> : <Welcome platform={platform} />}</main>

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
        {session && <span data-testid="statusbar-session">{session.serverVersion}</span>}
      </footer>
    </div>
  )
}
