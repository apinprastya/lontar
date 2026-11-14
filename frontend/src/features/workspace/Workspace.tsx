import { useEffect, useMemo, useRef, useState } from 'react'
import ConfirmDialog from '../../components/ConfirmDialog'
import { matchesShortcut } from '../../lib/mod'
import ExportDialog from '../export/ExportDialog'
import ImportWizard from '../import/ImportWizard'
import CommandPalette, { type PaletteConnection, type PaletteMode, type PaletteTable } from '../palette/CommandPalette'
import HostKeyDialog from '../connections/HostKeyDialog'
import { connectionSummary } from '../connections/types'
import { commandAvailable, runCommand } from '../palette/commands'
import { resultEditView } from '../../stores/queryTabs'
import { editorApi, type EditorApi } from '../editor/api'
import QueryTab from '../editor/QueryTab'
import { Group, Panel, Separator } from 'react-resizable-panels'
import { usePanelLayout } from '../../lib/panelLayout'
import { useConnections } from '../../stores/connections'
import { WorkspaceContext, createWorkspaceStore, useActiveTab, useWorkspace, type WorkspaceStore } from '../../stores/workspace'
import { connectionColor } from '../connections/types'
import RowDetail from '../grid/RowDetail'
import Sidebar from '../sidebar/Sidebar'
import {
  ExportIcon,
  FilterIcon,
  ImportIcon,
  NewQueryIcon,
  PanelRightIcon,
  RefreshIcon,
  RowPlusIcon,
  SearchIcon,
  ShieldIcon,
  UnplugIcon,
} from '../../components/icons'
import ToolButton from '../../components/ToolButton'
import { shortcutLabel } from '../../lib/mod'
import { workspaceApi, type WorkspaceApi } from './api'
import { handleShortcut, isTextTarget } from './shortcuts'
import { editApi, type EditApi } from '../edits/api'
import { PreviewDialog, UnsavedDialog } from '../edits/dialogs'
import { safeModeLabel } from '../edits/types'
import { canEdit } from '../../stores/workspace'
import { displayRows, pendingCount } from '../../stores/pending'
import { detailEditing, type Editable } from '../grid/editing'
import StatusBar from './StatusBar'
import TabBar from './TabBar'
import TableTab from './TableTab'

export interface WorkspaceProps {
  platform?: string
  /** Backend adapters; tests may inject fakes. */
  api?: WorkspaceApi
  editor?: EditorApi
  edits?: EditApi
}

/** Safe mode badge next to the connection name. */
/** Panel ids of the workspace Group, with and without the row detail (each keeps its layout). */
const WITH_DETAIL = ['sidebar', 'main', 'detail']
const WITHOUT_DETAIL = ['sidebar', 'main']

export function SafeModeBadge({ mode }: { mode: string | undefined }) {
  if (!mode || mode === 'off') return null
  return (
    <span className={`safe-mode-badge safe-${mode}`} data-testid="safe-mode" title={mode === 'readonly' ? 'Read-only: the database rejects writes' : 'Writes need confirmation'}>
      <ShieldIcon className="icon" /> {safeModeLabel(mode as 'confirm' | 'readonly')}
    </span>
  )
}

/** Tables for quick open: the current schema's, then other schemas' (loaded when the palette opens). */
function usePaletteTables(store: WorkspaceStore, api: WorkspaceApi, open: boolean): { tables: PaletteTable[]; loading: boolean } {
  const schema = useWorkspace((s) => s.schema)
  const schemas = useWorkspace((s) => s.schemas)
  const current = useWorkspace((s) => s.tables)
  const [other, setOther] = useState<PaletteTable[]>([])
  const [loading, setLoading] = useState(false)
  useEffect(() => {
    if (!open) return
    const rest = schemas.filter((x) => x !== schema).slice(0, 20)
    if (rest.length === 0) return
    let live = true
    setLoading(true)
    const sid = store.getState().sessionId
    Promise.all(rest.map((x) => api.listTables(sid, x).catch(() => [])))
      .then((lists) => {
        if (live) setOther(lists.flat().map((t) => ({ schema: t.schema, name: t.name, type: t.type, current: false })))
      })
      .finally(() => live && setLoading(false))
    return () => {
      live = false
    }
  }, [open, schema, schemas, store, api])
  const tables = useMemo(
    () => [...current.map((t) => ({ schema: t.schema, name: t.name, type: t.type, current: true })), ...other.filter((t) => t.schema !== schema)],
    [current, other, schema],
  )
  return { tables, loading }
}

/** Number of staged changes in all tabs of a workspace store. */
export function stagedChanges(store: WorkspaceStore | undefined): number {
  if (!store) return 0
  return store.getState().tabs.reduce((n, t) => {
    if (t.kind === 'table') return n + pendingCount(t.pending)
    return n + (t.edit ? pendingCount(t.edit.pending) : 0)
  }, 0)
}

function WorkspaceView({
  platform,
  store,
  editor,
  api,
  storeOf,
}: {
  platform?: string
  store: WorkspaceStore
  editor: EditorApi
  api: WorkspaceApi
  /** The workspace store of another open session. */
  storeOf(sessionId: string): WorkspaceStore | undefined
}) {
  const session = useConnections((s) => s.session)
  const connection = useConnections((s) => s.connections.find((c) => c.id === s.session?.connectionId))
  const disconnect = useConnections((s) => s.disconnect)
  const connect = useConnections((s) => s.connect)
  const connections = useConnections((s) => s.connections)
  const connectingId = useConnections((s) => s.connectingId)
  const connError = useConnections((s) => s.error)
  const clearConnError = useConnections((s) => s.clearError)
  const hostKey = useConnections((s) => s.hostKey)
  const openSessions = useConnections((s) => s.sessions)
  const [closeSessionAsk, setCloseSession] = useState<{ id: string; changes: number } | null>(null)
  const acceptHostKey = useConnections((s) => s.acceptHostKey)
  const dismissHostKey = useConnections((s) => s.dismissHostKey)
  const tab = useActiveTab()
  const showDetail = useWorkspace((s) => s.showDetail)
  const toggleDetail = useWorkspace((s) => s.toggleDetail)
  const layout = usePanelLayout('workspace', showDetail ? WITH_DETAIL : WITHOUT_DETAIL)
  const refresh = useWorkspace((s) => s.refresh)
  const kind = useWorkspace((s) => s.kind)
  const safeMode = useWorkspace((s) => s.safeMode)
  const guard = useWorkspace((s) => s.guard)
  const tabs = useWorkspace((s) => s.tabs)
  const exportRequest = useWorkspace((s) => s.exportRequest)
  const importRequest = useWorkspace((s) => s.importRequest)
  const closeConfirm = useWorkspace((s) => s.closeConfirm)
  const [palette, setPalette] = useState<PaletteMode | null>(null)
  const paletteTables = usePaletteTables(store, api, palette === 'tables')
  // Overlays (palette, dialogs) own the keyboard while open.
  const overlay = Boolean(palette || exportRequest || importRequest || closeConfirm || hostKey || closeSessionAsk)
  const overlayRef = useRef(overlay)
  overlayRef.current = overlay
  const openSwitcher = () => {
    clearConnError()
    setPalette('connections')
  }
  const openSwitcherRef = useRef(openSwitcher)
  openSwitcherRef.current = openSwitcher

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      // Keys the SQL editor (or another widget) already handled.
      if (e.defaultPrevented) return
      // Mod+P / Mod+Shift+P: the palette (and never the webview's print dialog).
      if (matchesShortcut(e, platform, 'p', { shift: e.shiftKey })) {
        e.preventDefault()
        setPalette(e.shiftKey ? 'commands' : 'tables')
        return
      }
      if (matchesShortcut(e, platform, 'k')) {
        e.preventDefault()
        openSwitcherRef.current()
        return
      }
      if (overlayRef.current) return
      if (handleShortcut(e, platform, store.getState(), isTextTarget(e.target))) e.preventDefault()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [platform, store])

  /**
   * Switches to another saved connection. Staged edits are committed or
   * discarded first; a failed connect keeps this session (and shows why).
   */
  /**
   * Switches to another saved connection: its open session, or a new one.
   * This session stays connected with its tabs; a failed connect keeps it
   * active (and shows why).
   */
  const switchTo = async (id: string) => {
    const ok = await connect(id)
    // Success, or a host key to confirm: the palette makes way.
    if (!ok) setPalette(useConnections.getState().hostKey ? null : 'connections')
  }
  /** Disconnects a background session, asking first if it has staged edits. */
  const closeSession = (sessionId: string) => {
    const n = stagedChanges(storeOf(sessionId))
    if (n > 0) {
      setPalette(null)
      setCloseSession({ id: sessionId, changes: n })
    } else void disconnect(sessionId)
  }
  const paletteConnections: PaletteConnection[] = connections.map((c) => ({
    id: c.id,
    name: c.name,
    kind: c.kind,
    summary: connectionSummary(c),
    color: connectionColor(c),
    current: c.id === session?.connectionId,
    sessionId: openSessions.find((x) => x.connectionId === c.id)?.id ?? null,
  }))

  const commandContext = {
    platform,
    store,
    openPalette: (m: PaletteMode) => (m === 'connections' ? openSwitcher() : setPalette(m)),
    disconnect: () => store.getState().guarded(store.getState().tabs.map((t) => t.id), () => void disconnect()),
  }

  const q = tab?.kind === 'query' ? tab : null
  const [qResult, qActive, qSorts, qEdit] = [q?.result ?? null, q?.activeResult ?? '', q?.sorts ?? null, q?.edit ?? null]
  const queryView = useMemo(
    () => (qSorts ? resultEditView({ result: qResult, activeResult: qActive, sorts: qSorts, edit: qEdit }, safeMode) : null),
    [qResult, qActive, qSorts, qEdit, safeMode],
  )

  if (!session) return null
  const color = connection ? connectionColor(connection) : ''
  const tableTab = tab?.kind === 'table' ? tab : null
  // What the active tab edits: a table tab, or an editable query result.
  const resultEdit = queryView?.editable ? queryView.edit : null
  const editState = tableTab ?? resultEdit
  const editable = tableTab ? canEdit(tableTab, safeMode) : Boolean(resultEdit)
  const editableRows: Editable | null = tableTab
    ? { id: tableTab.id, columns: tableTab.columns, rows: tableTab.rows, pending: tableTab.pending, failed: tableTab.failed }
    : resultEdit && tab && queryView
      ? { id: tab.id, columns: queryView.columns, rows: queryView.view.set.rows, pending: resultEdit.pending, failed: resultEdit.failed }
      : null
  const guardCount = guard
    ? guard.tabIds.reduce((n, id) => {
        const t = tabs.find((x) => x.id === id)
        if (t?.kind === 'table') return n + pendingCount(t.pending)
        return n + (t?.kind === 'query' && t.edit ? pendingCount(t.edit.pending) : 0)
      }, 0)
    : 0
  const tabIds = () => store.getState().tabs.map((t) => t.id)
  const detailColumns = tab?.kind === 'table' ? tab.columns : (queryView?.columns ?? [])
  // The detail shows staged values (and staged inserts).
  const detailStates = tab?.kind === 'table' ? displayRows(tab.columns, tab.rows, tab.pending) : (queryView?.rows ?? [])
  const detailState = tab && tab.selectedRow !== null ? detailStates[tab.selectedRow] : undefined
  const detailRow = detailState?.cells ?? null
  const rowOffset = tab?.kind === 'table' ? tab.offset : 0

  return (
    <section className="ws" aria-label="Workspace" style={color ? { borderTopColor: color } : undefined}>
      <Group orientation="horizontal" className="ws-panels" {...layout}>
        <Panel id="sidebar" defaultSize={240} minSize={160} maxSize={480}>
          <Sidebar
            onSwitchConnection={openSwitcher}
            openCount={Math.max(openSessions.length, 1)}
            switchHint={shortcutLabel(platform, 'K')}
            name={session.name}
            kind={kind}
            color={color}
            badge={
              <>
                <SafeModeBadge mode={safeMode} />
                {session.tunneled && (
                  <span className="ssh-badge" title="Connected through an SSH tunnel" data-testid="ssh-badge">
                    SSH
                  </span>
                )}
              </>
            }
          />
        </Panel>
        <Separator className="resize-handle" />
        <Panel id="main" minSize={300}>
          <div className="ws-main">
            {connError && palette !== 'connections' && (
              <div className="banner error ws-banner" role="alert" data-testid="connection-error">
                <span className="selectable">{connError}</span>
                <button type="button" className="icon-button" aria-label="Dismiss" onClick={clearConnError}>
                  ×
                </button>
              </div>
            )}
            <div className="toolbar ws-toolbar">
              <ToolButton label="Refresh" title={`Refresh (${shortcutLabel(platform, 'R')})`} icon={<RefreshIcon />} onClick={() => void refresh()} />
              <span className="toolbar-sep" aria-hidden />
              <ToolButton
                label="New query"
                title={`New SQL query (${shortcutLabel(platform, 'T')})`}
                icon={<NewQueryIcon />}
                onClick={() => store.getState().newQueryTab()}
              />
              <ToolButton label="Open…" title={`Open a table or view (${shortcutLabel(platform, 'P')})`} icon={<SearchIcon />} onClick={() => setPalette('tables')} />
              <span className="toolbar-sep" aria-hidden />
              {tab?.kind === 'table' && (
                <ToolButton
                  label="Filter"
                  title={`Filter (${shortcutLabel(platform, 'F')})`}
                  icon={<FilterIcon />}
                  aria-pressed={tab.filterOpen}
                  onClick={() => store.getState().toggleFilterBar(tab.id)}
                />
              )}
              <ToolButton
                label="Row detail"
                title={`Row detail (${shortcutLabel(platform, 'I')} or Space)`}
                icon={<PanelRightIcon />}
                aria-pressed={showDetail}
                onClick={toggleDetail}
              />
              {tab && editable && (tableTab?.view ?? 'data') === 'data' && (
                <ToolButton
                  label="Insert row"
                  title={`Insert row (${shortcutLabel(platform, 'N')})`}
                  icon={<RowPlusIcon />}
                  onClick={() => store.getState().addRow(tab.id)}
                />
              )}
              {commandAvailable('exportData', store.getState()) && (
                <ToolButton label="Export…" title="Export rows to CSV, JSON or SQL" icon={<ExportIcon />} onClick={() => runCommand('exportData', commandContext)} />
              )}
              {tableTab && commandAvailable('importCsv', store.getState()) && (
                <ToolButton label="Import…" title="Import a CSV file into this table" icon={<ImportIcon />} onClick={() => runCommand('importCsv', commandContext)} />
              )}
              <span className="spacer" />
              <ToolButton
                className="danger-tool"
                label="Disconnect"
                title="Disconnect this connection"
                icon={<UnplugIcon />}
                onClick={() => store.getState().guarded(tabIds(), () => void disconnect())}
              />
            </div>
            <TabBar platform={platform} />
            {tab?.kind === 'table' && <TableTab key={tab.id} tab={tab} platform={platform} />}
            {tab?.kind === 'query' && <QueryTab key={tab.id} tab={tab} platform={platform} api={editor} />}
            {!tab && (
              <div className="ws-empty muted">
                <p>Select a table in the sidebar to browse its rows.</p>
                <p className="hint">Click to preview · double-click to keep it open</p>
                <p className="hint">{shortcutLabel(platform, 'T')} opens a new SQL query</p>
              </div>
            )}
          </div>
        </Panel>
        {showDetail && (
          <>
            <Separator className="resize-handle" />
            <Panel id="detail" defaultSize={300} minSize={200}>
              <RowDetail
                columns={detailColumns}
                row={detailRow}
                rowNumber={tab && tab.selectedRow !== null ? rowOffset + tab.selectedRow + 1 : null}
                onClose={toggleDetail}
                editing={editableRows && editable ? detailEditing(editableRows, detailState, kind, store.getState()) : null}
              />
            </Panel>
          </>
        )}
      </Group>
      <StatusBar
        tab={tab}
        serverVersion={session.serverVersion}
        onPrev={() => tab?.kind === 'table' && store.getState().prevPage(tab.id)}
        onNext={() => tab?.kind === 'table' && store.getState().nextPage(tab.id)}
        pending={
          tab && editState
            ? {
                count: pendingCount(editState.pending),
                committing: editState.committing,
                previewLoading: editState.previewLoading,
                commitHint: shortcutLabel(platform, 'S'),
                onPreview: () => void store.getState().openPreview(tab.id),
                onCommit: () => void store.getState().requestCommit(tab.id),
                onDiscard: () => store.getState().discardChanges(tab.id),
              }
            : null
        }
      />
      {tab && editState?.preview && (
        <PreviewDialog
          statements={editState.preview}
          safeMode={safeMode}
          committing={editState.committing}
          onCommit={() => void store.getState().commit(tab.id)}
          onClose={() => store.getState().closePreview(tab.id)}
        />
      )}
      {closeSessionAsk && (
        <ConfirmDialog
          title="Disconnect with unsaved changes?"
          message={`That connection has ${closeSessionAsk.changes} staged change${closeSessionAsk.changes === 1 ? '' : 's'} that were not committed. Disconnecting discards them.`}
          confirmLabel="Discard and disconnect"
          danger
          onConfirm={() => {
            void disconnect(closeSessionAsk.id)
            setCloseSession(null)
          }}
          onCancel={() => setCloseSession(null)}
        />
      )}
      {hostKey && <HostKeyDialog info={hostKey.info} busy={hostKey.busy} onAccept={() => void acceptHostKey()} onCancel={dismissHostKey} />}
      {guard && <UnsavedDialog count={guardCount} busy={guard.busy} onChoose={(c) => void store.getState().resolveGuard(c)} />}
      {palette && (
        <CommandPalette
          mode={palette}
          platform={platform}
          tables={paletteTables.tables}
          loading={paletteTables.loading}
          disabled={(id) => !commandAvailable(id, store.getState())}
          onModeChange={setPalette}
          onClose={() => setPalette(null)}
          connections={paletteConnections}
          connectingId={connectingId}
          connectError={connError}
          onConnect={(id) => void switchTo(id)}
          onDisconnect={closeSession}
          onOpenTable={(t, view) => {
            setPalette(null)
            store.getState().openTableView(t.schema, t.name, view)
          }}
          onCommand={(id) => {
            setPalette(null)
            runCommand(id, commandContext)
          }}
        />
      )}
      {exportRequest && <ExportDialog sessionId={session.id} source={exportRequest} onClose={() => store.getState().requestExport(null)} />}
      {importRequest && (
        <ImportWizard
          sessionId={session.id}
          schema={importRequest.schema}
          table={importRequest.table}
          safeMode={safeMode}
          onClose={() => store.getState().requestImport(null)}
          onImported={() => void store.getState().refresh()}
        />
      )}
      {closeConfirm && (
        <ConfirmDialog
          title="Close query tab?"
          message="This tab has SQL that was changed since it last ran. Closing it discards the text."
          confirmLabel="Close tab"
          danger
          onConfirm={() => store.getState().resolveCloseConfirm(true)}
          onCancel={() => store.getState().resolveCloseConfirm(false)}
        />
      )}
    </section>
  )
}

/**
 * Workspace for the open session: sidebar | table tabs | optional row
 * detail, with a status bar. Each session gets its own workspace store.
 */
export default function Workspace({ platform, api = workspaceApi, editor = editorApi, edits = editApi }: WorkspaceProps) {
  const session = useConnections((s) => s.session)
  // One store per open session: switching keeps each session's tabs,
  // results and staged edits. A store is disposed when its session closes.
  const stores = useRef(new Map<string, WorkspaceStore>())
  const inited = useRef(new WeakSet<WorkspaceStore>())
  let store: WorkspaceStore | null = null
  if (session) {
    store = stores.current.get(session.id) ?? null
    if (!store) {
      const { id, kind, connectionId, safeMode } = session
      store = createWorkspaceStore({ id, kind, connectionId, safeMode }, api, editor, edits)
      stores.current.set(id, store)
    }
  }

  useEffect(() => {
    if (store && !inited.current.has(store)) {
      inited.current.add(store)
      void store.getState().init()
    }
  }, [store])

  useEffect(
    () =>
      useConnections.subscribe((s) => {
        const open = new Set(s.sessions.map((x) => x.id))
        if (s.session) open.add(s.session.id)
        for (const [id, st] of stores.current) {
          if (open.has(id)) continue
          st.getState().dispose()
          stores.current.delete(id)
        }
      }),
    [],
  )

  if (!store || !session) return null
  return (
    <WorkspaceContext.Provider value={store}>
      <WorkspaceView key={session.id} platform={platform} store={store} editor={editor} api={api} storeOf={(id) => stores.current.get(id)} />
    </WorkspaceContext.Provider>
  )
}
