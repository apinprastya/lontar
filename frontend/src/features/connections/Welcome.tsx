import { useEffect, useRef, useState } from 'react'
import ConfirmDialog from '../../components/ConfirmDialog'
import { matchesShortcut, shortcutLabel } from '../../lib/mod'
import { useConnections } from '../../stores/connections'
import ConnectionForm from './ConnectionForm'
import ConnectionList from './ConnectionList'
import HostKeyDialog from './HostKeyDialog'
import type { Connection } from './types'

type Dialog = { type: 'form'; connection?: Connection } | { type: 'delete'; connection: Connection } | null

/**
 * Welcome screen: saved connections with search, create/edit/duplicate/
 * delete, and connect (double-click or Enter).
 */
export default function Welcome({ platform }: { platform?: string }) {
  const { connections, drivers, loading, error, connectingId, load, save, remove, duplicate, test, connect, clearError } =
    useConnections()
  const hostKey = useConnections((s) => s.hostKey)
  const acceptHostKey = useConnections((s) => s.acceptHostKey)
  const dismissHostKey = useConnections((s) => s.dismissHostKey)
  const trustHostKey = useConnections((s) => s.trustHostKey)
  const [query, setQuery] = useState('')
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [dialog, setDialog] = useState<Dialog>(null)
  const searchRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    void load()
  }, [load])

  const selected = connections.find((c) => c.id === selectedId)

  function askDelete(id: string) {
    const c = connections.find((x) => x.id === id)
    if (c) setDialog({ type: 'delete', connection: c })
  }

  async function doDuplicate(id: string) {
    const copy = await duplicate(id)
    if (copy) setSelectedId(copy.id)
  }

  // Global shortcuts while no dialog is open.
  useEffect(() => {
    if (dialog) return
    function onKey(e: KeyboardEvent) {
      if (matchesShortcut(e, platform, 'n')) {
        e.preventDefault()
        setDialog({ type: 'form' })
      } else if (matchesShortcut(e, platform, 'f')) {
        e.preventDefault()
        searchRef.current?.focus()
      } else if (matchesShortcut(e, platform, 'e') && selected) {
        e.preventDefault()
        setDialog({ type: 'form', connection: selected })
      } else if (matchesShortcut(e, platform, 'd') && selected) {
        e.preventDefault()
        void doDuplicate(selected.id)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  })

  return (
    <section className="welcome-screen" aria-label="Connections">
      <header className="toolbar" role="toolbar" aria-label="Connection actions">
        <h1>Connections</h1>
        <span className="spacer" />
        <button type="button" className="primary" onClick={() => setDialog({ type: 'form' })} title={shortcutLabel(platform, 'n')}>
          New
        </button>
        <button
          type="button"
          disabled={!selected}
          onClick={() => selected && setDialog({ type: 'form', connection: selected })}
          title={shortcutLabel(platform, 'e')}
        >
          Edit
        </button>
        <button
          type="button"
          disabled={!selected}
          onClick={() => selected && void doDuplicate(selected.id)}
          title={shortcutLabel(platform, 'd')}
        >
          Duplicate
        </button>
        <button type="button" disabled={!selected} onClick={() => selected && askDelete(selected.id)}>
          Delete
        </button>
      </header>

      {error && (
        <div className="banner error" role="alert">
          <span>{error}</span>
          <button type="button" className="link-button" onClick={clearError} aria-label="Dismiss error">
            ×
          </button>
        </div>
      )}

      {loading && connections.length === 0 ? (
        <p className="list-empty">Loading…</p>
      ) : (
        <ConnectionList
          connections={connections}
          query={query}
          onQueryChange={setQuery}
          selectedId={selectedId}
          onSelect={setSelectedId}
          onConnect={(id) => void connect(id)}
          onDelete={askDelete}
          connectingId={connectingId}
          searchRef={searchRef}
          searchHint={shortcutLabel(platform, 'f')}
        />
      )}

      {dialog?.type === 'form' && (
        <ConnectionForm
          initial={dialog.connection}
          drivers={drivers}
          platform={platform}
          onSave={async (c, pw) => {
            const saved = await save(c, pw)
            setSelectedId(saved.id)
            return saved
          }}
          onTest={test}
          onConnect={(c) => connect(c.id)}
          onClose={() => setDialog(null)}
          onTrustHostKey={trustHostKey}
        />
      )}

      {hostKey && <HostKeyDialog info={hostKey.info} busy={hostKey.busy} onAccept={() => void acceptHostKey()} onCancel={dismissHostKey} />}

      {dialog?.type === 'delete' && (
        <ConfirmDialog
          title="Delete connection?"
          message={`“${dialog.connection.name}” and its saved password will be removed. This cannot be undone.`}
          confirmLabel="Delete"
          danger
          onCancel={() => setDialog(null)}
          onConfirm={() => {
            const id = dialog.connection.id
            setDialog(null)
            if (selectedId === id) setSelectedId(null)
            void remove(id)
          }}
        />
      )}
    </section>
  )
}
