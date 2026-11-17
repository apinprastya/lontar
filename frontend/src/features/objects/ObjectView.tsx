import { useWorkspace, type ObjectTabState } from '../../stores/workspace'
import DdlSection from '../structure/DdlSection'
import { displayName } from '../structure/fk'
import ObjectIcon from './ObjectIcon'
import { OBJECT_KIND_LABELS, objectLabel } from './types'

/**
 * Read-only tab of a function, procedure, aggregate, sequence, type,
 * trigger or event: its details as a key/value list and its DDL. A
 * trigger links to its table.
 */
export default function ObjectView({ tab }: { tab: ObjectTabState }) {
  const schema = useWorkspace((s) => s.schema)
  const openTableView = useWorkspace((s) => s.openTableView)
  const loadObjectDDL = useWorkspace((s) => s.loadObjectDDL)
  const o = tab.object
  return (
    <div className="structure-view object-view" data-testid="object-view">
      <header className="structure-header">
        <ObjectIcon kind={o.kind} />
        <strong className="mono">{displayName(o.schema, objectLabel(o), schema)}</strong>
        <span className="muted">{[OBJECT_KIND_LABELS[o.kind], o.detail].filter(Boolean).join(' · ')}</span>
        {tab.ddlLoading && tab.ddl !== null && <span className="muted">Refreshing…</span>}
      </header>
      <div className="structure-body">
        {o.properties.length > 0 && (
          <dl className="object-props" aria-label="Details">
            {o.properties.map((p) => (
              <div key={p.key} className="object-prop">
                <dt>{p.key}</dt>
                <dd className="selectable pre">
                  {p.key === 'Table' && o.table ? (
                    <button
                      type="button"
                      className="link-button"
                      title={`Open ${o.schema}.${o.table}`}
                      onClick={() => openTableView(o.schema, o.table, 'structure')}
                    >
                      {o.table}
                    </button>
                  ) : (
                    p.value
                  )}
                </dd>
              </div>
            ))}
          </dl>
        )}
        <h3 className="object-ddl-title">DDL</h3>
        <DdlSection ddl={tab.ddl} error={tab.ddlError} onRetry={() => void loadObjectDDL(tab.id)} copyLabel="Copy DDL" />
      </div>
    </div>
  )
}
