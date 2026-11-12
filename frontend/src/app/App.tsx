import { useEffect } from 'react'
import Welcome from '../features/connections/Welcome'
import SettingsDialog from '../features/settings/SettingsDialog'
import Workspace from '../features/workspace/Workspace'
import { matchesShortcut, modLabel } from '../lib/mod'
import { useConnections } from '../stores/connections'
import { bindAppearance, useSettings, watchSystemTheme } from '../stores/settings'
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
/**
 * Loads the settings, follows the OS colour scheme, applies the theme and
 * editor font size, and handles the app-wide keys: Mod+, opens the
 * settings; Mod+P never reaches the webview's print dialog.
 */
export function useAppearance(platform: string | undefined, hasSession: boolean) {
  useEffect(() => {
    void useSettings.getState().load()
    const unbind = bindAppearance()
    const unwatch = watchSystemTheme()
    return () => {
      unbind()
      unwatch()
    }
  }, [])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (matchesShortcut(e, platform, ',')) {
        e.preventDefault()
        useSettings.getState().setOpen(true)
      } else if (!hasSession && matchesShortcut(e, platform, 'p', { shift: e.shiftKey })) {
        e.preventDefault() // no print dialog (the workspace uses Mod+P)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [platform, hasSession])
}

export default function App() {
  const info = useAppInfo()
  const platform = info.status === 'ready' ? info.platform : undefined
  const session = useConnections((s) => s.session)
  const settingsOpen = useSettings((s) => s.open)
  useAppearance(platform, Boolean(session))

  if (session) {
    // The workspace brings its own sidebar, toolbar and status bar.
    return (
      <div className="app-shell app-shell-workspace" data-platform={platform}>
        <Workspace platform={platform} />
        {settingsOpen && <SettingsDialog platform={platform} />}
      </div>
    )
  }

  return (
    <div className="app-shell" data-platform={platform}>
      <aside className="sidebar" aria-label="Sidebar">
        <div className="sidebar-header">Lontar</div>
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
        <span className="spacer" />
        <button type="button" className="statusbar-button" onClick={() => useSettings.getState().setOpen(true)}>
          Settings
        </button>
      </footer>
      {settingsOpen && <SettingsDialog platform={platform} />}
    </div>
  )
}
