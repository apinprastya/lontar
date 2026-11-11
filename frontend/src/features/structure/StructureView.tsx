import { useState } from 'react'
import { KeyIcon, LinkIcon } from '../../components/icons'
import { copyText } from '../../lib/clipboard'
import { errorMessage } from '../../lib/errors'
import { useWorkspace, type TabState } from '../../stores/workspace'
import { formatEstimate } from '../sidebar/tableList'
import DdlView from './DdlView'
import { displayName } from './fk'
import { columnExtra, formatBytes, type ForeignKey, type StructureSection, type TableDetail } from './types'

const SECTIONS: { id: StructureSection; label: string; count?: (d: TableDetail) => number | null }[] = [
  { id: 'columns', label: 'Columns', count: (d) => d.columns.length },
  { id: 'indexes', label: 'Indexes', count: (d) => d.indexes.length },
  { id: 'fks', label: 'Foreign keys', count: (d) => d.foreignKeys.length },
  { id: 'refs', label: 'Referenced by', count: (d) => d.referencedBy.length },
  { id: 'checks', label: 'Checks', count: (d) => (d.checksSupported ? d.checks.length : null) },
  { id: 'triggers', label: 'Triggers', count: (d) => d.triggers.length },
  { id: 'ddl', label: 'DDL' },
]

function Empty({ children }: { children: React.ReactNode }) {
  return <p className="muted structure-empty">{children}</p>
}

/** Link that opens a table's tab (foreign key targets and sources). */
function TableLink({ schema, table }: { schema: string; table: string }) {
  const current = useWorkspace((s) => s.schema)
  const open = useWorkspace((s) => s.openTableView)
  const name = displayName(schema, table, current)
  return (
    <button type="button" className="link-button" title={`Open ${schema}.${table}`} onClick={() => open(schema, table, 'data')}>
      {name}
    </button>
  )
}

function Columns({ d }: { d: TableDetail }) {
  const fkCols = new Set(d.foreignKeys.flatMap((fk) => fk.columns))
  return (
    <table className="structure-table" aria-label="Columns">
      <thead>
        <tr>
          <th>#</th>
          <th>Name</th>
          <th>Type</th>
          <th>Nullable</th>
          <th>Default</th>
          <th>Extra</th>
          <th>Collation</th>
          <th>Comment</th>
        </tr>
      </thead>
      <tbody>
        {d.columns.map((c) => (
          <tr key={c.name}>
            <td className="muted">{c.ordinal}</td>
            <td className="mono">
              {c.isPrimaryKey && <KeyIcon className="icon key-icon" title={`Primary key (${c.pkPosition})`} />}
              {fkCols.has(c.name) && <LinkIcon className="icon fk-icon" title="Foreign key" />} {c.name}
            </td>
            <td className="mono">{c.dataType}</td>
            <td>{c.nullable ? 'YES' : 'NO'}</td>
            <td className="mono">{c.default ?? <span className="muted">—</span>}</td>
            <td>{columnExtra(c)}</td>
            <td>{c.collation}</td>
            <td>{c.comment}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function Indexes({ d }: { d: TableDetail }) {
  if (d.indexes.length === 0) return <Empty>No indexes.</Empty>
  return (
    <table className="structure-table" aria-label="Indexes">
      <thead>
        <tr>
          <th>Name</th>
          <th>Columns</th>
          <th>Unique</th>
          <th>Method</th>
          <th>Definition</th>
        </tr>
      </thead>
      <tbody>
        {d.indexes.map((ix) => (
          <tr key={ix.name}>
            <td className="mono">
              {ix.primary && <KeyIcon className="icon key-icon" title="Primary key" />} {ix.name}
            </td>
            <td className="mono">
              {ix.columns.join(', ')}
              {ix.predicate && <span className="muted"> WHERE {ix.predicate}</span>}
            </td>
            <td>{ix.unique ? 'YES' : 'NO'}</td>
            <td>{ix.method}</td>
            <td className="mono selectable">{ix.definition}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function Keys({ list, incoming }: { list: ForeignKey[]; incoming: boolean }) {
  if (list.length === 0) return <Empty>{incoming ? 'No foreign keys point at this table.' : 'No foreign keys.'}</Empty>
  return (
    <table className="structure-table" aria-label={incoming ? 'Referenced by' : 'Foreign keys'}>
      <thead>
        <tr>
          <th>Name</th>
          {incoming ? <th>Table</th> : null}
          <th>Columns</th>
          {incoming ? null : <th>References</th>}
          <th>On update</th>
          <th>On delete</th>
        </tr>
      </thead>
      <tbody>
        {list.map((fk) => (
          <tr key={`${fk.schema}.${fk.table}.${fk.name}.${fk.columns.join(',')}`}>
            <td className="mono">{fk.name || <span className="muted">—</span>}</td>
            {incoming && (
              <td className="mono">
                <TableLink schema={fk.schema} table={fk.table} />
              </td>
            )}
            <td className="mono">
              {fk.columns.join(', ')}
              {incoming && <span className="muted"> → {fk.refColumns.join(', ')}</span>}
            </td>
            {!incoming && (
              <td className="mono">
                <TableLink schema={fk.refSchema} table={fk.refTable} /> ({fk.refColumns.join(', ')})
              </td>
            )}
            <td>{fk.onUpdate}</td>
            <td>{fk.onDelete}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function Checks({ d }: { d: TableDetail }) {
  if (!d.checksSupported) return <Empty>This database does not report check constraints. They are part of the DDL.</Empty>
  if (d.checks.length === 0) return <Empty>No check constraints.</Empty>
  return (
    <table className="structure-table" aria-label="Checks">
      <thead>
        <tr>
          <th>Name</th>
          <th>Expression</th>
        </tr>
      </thead>
      <tbody>
        {d.checks.map((c) => (
          <tr key={c.name}>
            <td className="mono">{c.name}</td>
            <td className="mono selectable">{c.expression}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function Triggers({ d }: { d: TableDetail }) {
  if (d.triggers.length === 0) return <Empty>No triggers.</Empty>
  return (
    <table className="structure-table" aria-label="Triggers">
      <thead>
        <tr>
          <th>Name</th>
          <th>Timing</th>
          <th>Event</th>
          <th>Definition</th>
        </tr>
      </thead>
      <tbody>
        {d.triggers.map((t) => (
          <tr key={t.name}>
            <td className="mono">{t.name}</td>
            <td>{t.timing}</td>
            <td>{t.event}</td>
            <td className="mono selectable pre">{t.definition}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function Ddl({ tab }: { tab: TabState }) {
  const s = useWorkspace((st) => st)
  const [copied, setCopied] = useState<string | null>(null)
  if (tab.ddlError)
    return (
      <div className="banner error" role="alert">
        <span className="selectable">{tab.ddlError}</span>
        <button type="button" onClick={() => void s.loadDDL(tab.id)}>
          Retry
        </button>
      </div>
    )
  if (tab.ddl === null) return <Empty>Loading DDL…</Empty>
  const ddl = tab.ddl
  const copy = async () => {
    try {
      await copyText(ddl)
      setCopied('Copied')
    } catch (err) {
      setCopied(`Copy failed: ${errorMessage(err)}`)
    }
  }
  return (
    <div className="ddl-section">
      <div className="toolbar ddl-toolbar">
        <button type="button" onClick={() => void copy()}>
          Copy
        </button>
        <button type="button" onClick={() => s.newQueryTab(ddl)}>
          Open in new query tab
        </button>
        {copied && (
          <span className="muted" role="status">
            {copied}
          </span>
        )}
      </div>
      <DdlView text={ddl} kind={s.kind} />
    </div>
  )
}

/** Read-only structure of a table tab: metadata header and sections. */
export default function StructureView({ tab }: { tab: TabState }) {
  const s = useWorkspace((st) => st)
  const d = tab.structure
  if (tab.structureError && !d) {
    return (
      <div className="structure-view">
        <div className="banner error tab-error" role="alert">
          <span className="selectable">{tab.structureError}</span>
          <button type="button" onClick={() => void s.loadStructure(tab.id)}>
            Retry
          </button>
        </div>
      </div>
    )
  }
  if (!d) return <div className="structure-view muted structure-loading">Loading structure…</div>
  const meta = [
    d.type,
    d.estimatedRows !== null ? `~${formatEstimate(d.estimatedRows)} rows` : '',
    d.sizeBytes !== null ? formatBytes(d.sizeBytes) : '',
    d.engine,
    d.collation,
  ].filter(Boolean)
  return (
    <div className="structure-view" data-testid="structure-view">
      <header className="structure-header">
        <strong className="mono">{displayName(d.schema, d.name, s.schema)}</strong>
        <span className="muted">{meta.join(' · ')}</span>
        {tab.structureLoading && <span className="muted">Refreshing…</span>}
        {d.comment && <p className="structure-comment">{d.comment}</p>}
      </header>
      <div className="structure-sections" role="tablist" aria-label="Structure sections">
        {SECTIONS.map((sec) => {
          const n = sec.count?.(d) ?? null
          return (
            <button
              key={sec.id}
              type="button"
              role="tab"
              aria-selected={tab.section === sec.id}
              className="structure-section-tab"
              onClick={() => s.setStructureSection(tab.id, sec.id)}
            >
              {sec.label}
              {n !== null && <span className="count">{n}</span>}
            </button>
          )
        })}
      </div>
      <div className="structure-body" role="tabpanel">
        {tab.section === 'columns' && <Columns d={d} />}
        {tab.section === 'indexes' && <Indexes d={d} />}
        {tab.section === 'fks' && <Keys list={d.foreignKeys} incoming={false} />}
        {tab.section === 'refs' && <Keys list={d.referencedBy} incoming />}
        {tab.section === 'checks' && <Checks d={d} />}
        {tab.section === 'triggers' && <Triggers d={d} />}
        {tab.section === 'ddl' && <Ddl tab={tab} />}
      </div>
    </div>
  )
}
