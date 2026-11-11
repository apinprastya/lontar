import { useEffect, useMemo } from 'react'
import { Group, Panel, Separator } from 'react-resizable-panels'
import { useConnections } from '../../stores/connections'
import { WorkspaceContext, createWorkspaceStore, useActiveTab, useWorkspace, type WorkspaceStore } from '../../stores/workspace'
import { connectionColor } from '../connections/types'
import RowDetail from '../grid/RowDetail'
import Sidebar from '../sidebar/Sidebar'
import { RefreshIcon } from '../../components/icons'
import { shortcutLabel } from '../../lib/mod'
import { workspaceApi, type WorkspaceApi } from './api'
import { handleShortcut } from './shortcuts'
import StatusBar from './StatusBar'
import TabBar from './TabBar'
import TableTab from './TableTab'

export interface WorkspaceProps {
  platform?: string
  /** Backend adapter; tests may inject a fake. */
  api?: WorkspaceApi
}

function WorkspaceView({ platform, store }: { platform?: string; store: WorkspaceStore }) {
  const session = useConnections((s) => s.session)
  const connection = useConnections((s) => s.connections.find((c) => c.id === s.session?.connectionId))
  const disconnect = useConnections((s) => s.disconnect)
  const tab = useActiveTab()
  const showDetail = useWorkspace((s) => s.showDetail)
  const toggleDetail = useWorkspace((s) => s.toggleDetail)
  const refresh = useWorkspace((s) => s.refresh)
  const kind = useWorkspace((s) => s.kind)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (handleShortcut(e, platform, store.getState())) e.preventDefault()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [platform, store])

  if (!session) return null
  const color = connection ? connectionColor(connection) : ''
  const detailRow = tab && tab.selectedRow !== null ? (tab.rows[tab.selectedRow] ?? null) : null

  return (
    <section className="ws" aria-label="Workspace" style={color ? { borderTopColor: color } : undefined}>
      <Group orientation="horizontal" className="ws-panels">
        <Panel id="sidebar" defaultSize={240} minSize={160} maxSize={480}>
          <Sidebar name={session.name} kind={kind} color={color} />
        </Panel>
        <Separator className="resize-handle" />
        <Panel id="main" minSize={300}>
          <div className="ws-main">
            <div className="toolbar ws-toolbar">
              <button type="button" onClick={() => void refresh()} title={`Refresh (${shortcutLabel(platform, 'R')})`}>
                <RefreshIcon /> Refresh
              </button>
              {tab && (
                <button
                  type="button"
                  aria-pressed={tab.filterOpen}
                  onClick={() => store.getState().toggleFilterBar(tab.id)}
                  title={`Filter (${shortcutLabel(platform, 'F')})`}
                >
                  Filter
                </button>
              )}
              <button type="button" aria-pressed={showDetail} onClick={toggleDetail} title={`Row detail (${shortcutLabel(platform, 'I')} or Space)`}>
                Row detail
              </button>
              <span className="spacer" />
              <button type="button" onClick={() => void disconnect()}>
                Disconnect
              </button>
            </div>
            <TabBar platform={platform} />
            {tab ? (
              <TableTab key={tab.id} tab={tab} platform={platform} />
            ) : (
              <div className="ws-empty muted">
                <p>Select a table in the sidebar to browse its rows.</p>
                <p className="hint">Click to preview · double-click to keep it open</p>
              </div>
            )}
          </div>
        </Panel>
        {showDetail && (
          <>
            <Separator className="resize-handle" />
            <Panel id="detail" defaultSize={300} minSize={200}>
              <RowDetail
                columns={tab?.columns ?? []}
                row={detailRow}
                rowNumber={tab && tab.selectedRow !== null ? tab.offset + tab.selectedRow + 1 : null}
                onClose={toggleDetail}
              />
            </Panel>
          </>
        )}
      </Group>
      <StatusBar
        tab={tab}
        serverVersion={session.serverVersion}
        onPrev={() => tab && store.getState().prevPage(tab.id)}
        onNext={() => tab && store.getState().nextPage(tab.id)}
      />
    </section>
  )
}

/**
 * Workspace for the open session: sidebar | table tabs | optional row
 * detail, with a status bar. Each session gets its own workspace store.
 */
export default function Workspace({ platform, api = workspaceApi }: WorkspaceProps) {
  const session = useConnections((s) => s.session)
  const sessionId = session?.id
  const kind = session?.kind
  const store = useMemo(() => (sessionId && kind ? createWorkspaceStore({ id: sessionId, kind }, api) : null), [sessionId, kind, api])

  useEffect(() => {
    if (!store) return
    void store.getState().init()
    return () => store.getState().dispose()
  }, [store])

  if (!store) return null
  return (
    <WorkspaceContext.Provider value={store}>
      <WorkspaceView platform={platform} store={store} />
    </WorkspaceContext.Provider>
  )
}
