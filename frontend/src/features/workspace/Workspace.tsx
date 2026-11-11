import { useEffect, useMemo } from 'react'
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

function WorkspaceView({ platform, store, editor }: { platform?: string; store: WorkspaceStore; editor: EditorApi }) {
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

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      // Keys the SQL editor (or another widget) already handled.
      if (e.defaultPrevented) return
      if (handleShortcut(e, platform, store.getState(), isTextTarget(e.target))) e.preventDefault()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [platform, store])

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
          <Sidebar name={session.name} kind={kind} color={color} badge={<SafeModeBadge mode={safeMode} />} />
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
      <WorkspaceView platform={platform} store={store} editor={editor} />
    </WorkspaceContext.Provider>
  )
}
