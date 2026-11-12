import { useEffect, useMemo, useRef, useState } from 'react'
import ConfirmDialog from '../../components/ConfirmDialog'
import { matchesShortcut } from '../../lib/mod'
import ExportDialog from '../export/ExportDialog'
import ImportWizard from '../import/ImportWizard'
import CommandPalette, { type PaletteMode, type PaletteTable } from '../palette/CommandPalette'
import { commandAvailable, runCommand } from '../palette/commands'
import { activeResultView } from '../../stores/queryTabs'
import { editorApi, type EditorApi } from '../editor/api'
import QueryTab from '../editor/QueryTab'
import { Group, Panel, Separator } from 'react-resizable-panels'
import { useConnections } from '../../stores/connections'
import { WorkspaceContext, createWorkspaceStore, useActiveTab, useWorkspace, type WorkspaceStore } from '../../stores/workspace'
import { connectionColor } from '../connections/types'
import RowDetail from '../grid/RowDetail'
import Sidebar from '../sidebar/Sidebar'
import { RefreshIcon, ShieldIcon } from '../../components/icons'
import { shortcutLabel } from '../../lib/mod'
import { workspaceApi, type WorkspaceApi } from './api'
import { handleShortcut, isTextTarget } from './shortcuts'
import { editApi, type EditApi } from '../edits/api'
import { PreviewDialog, UnsavedDialog } from '../edits/dialogs'
import { initialText } from '../edits/editors'
import { safeModeLabel, type EditValue } from '../edits/types'
import type { Kind } from '../connections/types'
import { canEdit, type TabState } from '../../stores/workspace'
import { displayRows, pendingCount, stagedValue } from '../../stores/pending'
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
export function SafeModeBadge({ mode }: { mode: string | undefined }) {
  if (!mode || mode === 'off') return null
  return (
    <span className={`safe-mode-badge safe-${mode}`} data-testid="safe-mode" title={mode === 'readonly' ? 'Read-only: the database rejects writes' : 'Writes need confirmation'}>
      <ShieldIcon className="icon" /> {safeModeLabel(mode as 'confirm' | 'readonly')}
    </span>
  )
}

/** Row detail editing for the selected row of an editable table tab. */
function detailEditing(tab: TabState, kind: Kind, store: WorkspaceStore) {
  if (tab.selectedRow === null) return null
  const rows = displayRows(tab.columns, tab.rows, tab.pending)
  const d = rows[tab.selectedRow]
  if (!d) return null
  return {
    engine: kind,
    state: d,
    editText: (col: number) => {
      const column = tab.columns[col]
      const orig = d.ref.type === 'row' ? tab.rows[d.ref.index]?.[col] : undefined
      return column ? initialText(stagedValue(tab.pending, d, column.name), orig) : ''
    },
    onCommit: (col: number, value: EditValue) => {
      const column = tab.columns[col]
      if (column) store.getState().stageCells(tab.id, [{ ref: d.ref, column: column.name, value }])
    },
  }
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

function WorkspaceView({ platform, store, editor, api }: { platform?: string; store: WorkspaceStore; editor: EditorApi; api: WorkspaceApi }) {
  const session = useConnections((s) => s.session)
  const connection = useConnections((s) => s.connections.find((c) => c.id === s.session?.connectionId))
  const disconnect = useConnections((s) => s.disconnect)
  const tab = useActiveTab()
  const showDetail = useWorkspace((s) => s.showDetail)
  const toggleDetail = useWorkspace((s) => s.toggleDetail)
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
  const overlay = Boolean(palette || exportRequest || importRequest || closeConfirm)
  const overlayRef = useRef(overlay)
  overlayRef.current = overlay

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
      if (overlayRef.current) return
      if (handleShortcut(e, platform, store.getState(), isTextTarget(e.target))) e.preventDefault()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [platform, store])

  const commandContext = {
    platform,
    store,
    openPalette: (m: PaletteMode) => setPalette(m),
    disconnect: () => store.getState().guarded(store.getState().tabs.filter((t) => t.kind === 'table').map((t) => t.id), () => void disconnect()),
  }

  const q = tab?.kind === 'query' ? tab : null
  const [qResult, qActive, qSorts] = [q?.result ?? null, q?.activeResult ?? '', q?.sorts ?? null]
  const queryView = useMemo(
    () => (qSorts ? activeResultView({ result: qResult, activeResult: qActive, sorts: qSorts }) : null),
    [qResult, qActive, qSorts],
  )

  if (!session) return null
  const color = connection ? connectionColor(connection) : ''
  const tableTab = tab?.kind === 'table' ? tab : null
  const editable = tableTab ? canEdit(tableTab, safeMode) : false
  const guardCount = guard
    ? guard.tabIds.reduce((n, id) => {
        const t = tabs.find((x) => x.id === id)
        return n + (t?.kind === 'table' ? pendingCount(t.pending) : 0)
      }, 0)
    : 0
  const tableIds = () => store.getState().tabs.filter((t) => t.kind === 'table').map((t) => t.id)
  const detailColumns = tab?.kind === 'table' ? tab.columns : (queryView?.set.columns ?? [])
  // Table tabs show staged values (and staged inserts) in the detail.
  const detailRows = tab?.kind === 'table' ? displayRows(tab.columns, tab.rows, tab.pending).map((r) => r.cells) : (queryView?.rows ?? [])
  const detailRow = tab && tab.selectedRow !== null ? (detailRows[tab.selectedRow] ?? null) : null
  const rowOffset = tab?.kind === 'table' ? tab.offset : 0

  return (
    <section className="ws" aria-label="Workspace" style={color ? { borderTopColor: color } : undefined}>
      <Group orientation="horizontal" className="ws-panels">
        <Panel id="sidebar" defaultSize={240} minSize={160} maxSize={480}>
          <Sidebar
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
            <div className="toolbar ws-toolbar">
              <button type="button" onClick={() => void refresh()} title={`Refresh (${shortcutLabel(platform, 'R')})`}>
                <RefreshIcon /> Refresh
              </button>
              <button type="button" onClick={() => store.getState().newQueryTab()} title={`New SQL query (${shortcutLabel(platform, 'T')})`}>
                New query
              </button>
              {tab?.kind === 'table' && (
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
              {tableTab && editable && (
                <button type="button" onClick={() => store.getState().addRow(tableTab.id)} title={`Insert row (${shortcutLabel(platform, 'N')})`}>
                  Insert row
                </button>
              )}
              {commandAvailable('exportData', store.getState()) && (
                <button type="button" onClick={() => runCommand('exportData', commandContext)} title="Export rows to CSV, JSON or SQL">
                  Export…
                </button>
              )}
              {tableTab && commandAvailable('importCsv', store.getState()) && (
                <button type="button" onClick={() => runCommand('importCsv', commandContext)} title="Import a CSV file into this table">
                  Import…
                </button>
              )}
              <button type="button" onClick={() => setPalette('tables')} title={`Open a table (${shortcutLabel(platform, 'P')})`}>
                Open…
              </button>
              <span className="spacer" />
              <button type="button" onClick={() => store.getState().guarded(tableIds(), () => void disconnect())}>
                Disconnect
              </button>
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
                editing={tableTab && editable ? detailEditing(tableTab, kind, store) : null}
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
          tableTab
            ? {
                count: pendingCount(tableTab.pending),
                committing: tableTab.committing,
                previewLoading: tableTab.previewLoading,
                commitHint: shortcutLabel(platform, 'S'),
                onPreview: () => void store.getState().openPreview(tableTab.id),
                onCommit: () => void store.getState().requestCommit(tableTab.id),
                onDiscard: () => store.getState().discardChanges(tableTab.id),
              }
            : null
        }
      />
      {tableTab?.preview && (
        <PreviewDialog
          statements={tableTab.preview}
          safeMode={safeMode}
          committing={tableTab.committing}
          onCommit={() => void store.getState().commit(tableTab.id)}
          onClose={() => store.getState().closePreview(tableTab.id)}
        />
      )}
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
  const sessionId = session?.id
  const kind = session?.kind
  const connectionId = session?.connectionId
  const safeMode = session?.safeMode
  const store = useMemo(
    () => (sessionId && kind ? createWorkspaceStore({ id: sessionId, kind, connectionId, safeMode }, api, editor, edits) : null),
    [sessionId, kind, connectionId, safeMode, api, editor, edits],
  )

  useEffect(() => {
    if (!store) return
    void store.getState().init()
    return () => store.getState().dispose()
  }, [store])

  if (!store) return null
  return (
    <WorkspaceContext.Provider value={store}>
      <WorkspaceView platform={platform} store={store} editor={editor} api={api} />
    </WorkspaceContext.Provider>
  )
}
