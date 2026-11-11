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
 * Application shell: sidebar, main area and status bar. Real content
 * (connections, table browser, SQL editor) arrives in later milestones.
 */
export default function App() {
  const info = useAppInfo()
  const platform = info.status === 'ready' ? info.platform : undefined

  return (
    <div className="app-shell" data-platform={platform}>
      <aside className="sidebar" aria-label="Sidebar">
        <div className="sidebar-header">dbedit</div>
        <p className="sidebar-empty">No connections</p>
      </aside>

      <main className="workspace">
        <section className="welcome">
          <h1>dbedit</h1>
          <p>A fast, lightweight client for PostgreSQL, MySQL/MariaDB and SQLite.</p>
        </section>
      </main>

      <footer className="statusbar" role="status">
        {info.status === 'loading' && <span>Loading…</span>}
        {info.status === 'ready' && (
          <>
            <span data-testid="app-version">v{info.version}</span>
            <span data-testid="app-platform">{platformLabel(info.platform)}</span>
          </>
        )}
        {info.status === 'error' && (
          <span className="statusbar-error">Backend unavailable: {info.error}</span>
        )}
      </footer>
    </div>
  )
}
