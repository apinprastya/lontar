import { useConnections } from '../../stores/connections'
import { KIND_LABELS, connectionColor } from '../connections/types'

/**
 * Placeholder workspace for an open session (M1). The table browser and SQL
 * editor replace this in later milestones.
 */
export default function Workspace() {
  const session = useConnections((s) => s.session)
  const connection = useConnections((s) => s.connections.find((c) => c.id === s.session?.connectionId))
  const disconnect = useConnections((s) => s.disconnect)
  if (!session) return null
  const color = connection ? connectionColor(connection) : ''

  return (
    <section className="workspace-placeholder" aria-label="Workspace" style={color ? { borderTopColor: color } : undefined}>
      <h1>{session.name}</h1>
      <dl>
        <dt>Type</dt>
        <dd>{KIND_LABELS[session.kind] ?? session.kind}</dd>
        <dt>Server</dt>
        <dd data-testid="server-version">{session.serverVersion}</dd>
      </dl>
      <p className="muted">Table browsing and the SQL editor arrive in the next milestones.</p>
      <button type="button" onClick={() => void disconnect()}>
        Disconnect
      </button>
    </section>
  )
}
