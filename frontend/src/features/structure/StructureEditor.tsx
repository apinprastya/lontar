import { useCallback, useState } from 'react'
import { KeyIcon } from '../../components/icons'
import { useWorkspace, type TabState } from '../../stores/workspace'
import {
  ENUM_RE,
  addCheck,
  addColumn,
  addFK,
  addIndex,
  changedFields,
  columnName,
  columnState,
  itemState,
  liveColumns,
  renameIndex,
  toggleDropCheck,
  toggleDropColumn,
  toggleDropFK,
  toggleDropIndex,
  toggleDropTrigger,
  togglePK,
  updateCheck,
  updateColumn,
  type ColumnDraft,
  type DefaultMode,
  type StructureDraft,
} from '../../stores/structureEdits'
import { structureDirty } from '../../stores/structureTabs'
import type { Caps } from './editTypes'
import { ForeignKeyDialog, IndexDialog, StructurePreviewDialog } from './editDialogs'
import type { StructureSection } from './types'

type EditSection = 'columns' | 'indexes' | 'fks' | 'checks' | 'triggers'

const SECTIONS: { id: EditSection; label: string }[] = [
  { id: 'columns', label: 'Columns' },
  { id: 'indexes', label: 'Indexes' },
  { id: 'fks', label: 'Foreign keys' },
  { id: 'checks', label: 'Checks' },
  { id: 'triggers', label: 'Triggers' },
]

const DEFAULT_LABELS: Record<DefaultMode, string> = {
  none: 'No default',
  null: 'NULL',
  value: 'Value',
  expression: 'Expression',
  generatedStored: 'Generated (stored)',
  generatedVirtual: 'Generated (virtual)',
}

/** The default modes a column can choose. */
function defaultModes(c: ColumnDraft, caps: Caps, create: boolean): DefaultMode[] {
  const modes: DefaultMode[] = ['none', 'null', 'value', 'expression']
  if (c.original) {
    if (c.defaultMode === 'generatedStored' || c.defaultMode === 'generatedVirtual') return [c.defaultMode]
    return modes
  }
  // A new table takes any generated column its engine has; an existing one what ALTER TABLE can add.
  if (create || caps.generatedStored) modes.push('generatedStored')
  if (create ? caps.flavor !== 'postgres' : caps.generatedVirtual) modes.push('generatedVirtual')
  return modes
}

/** Trigger templates for the SQL editor (a trigger body is code). */
export function triggerTemplate(caps: Caps | null, schema: string, table: string): string {
  const t = table || 'my_table'
  switch (caps?.flavor) {
    case 'mysql':
    case 'mariadb':
      return `DELIMITER //\nCREATE TRIGGER ${t}_before_update\nBEFORE UPDATE ON ${t}\nFOR EACH ROW\nBEGIN\n  -- SET NEW.updated_at = NOW();\nEND //\nDELIMITER ;\n`
    case 'sqlite':
      return `CREATE TRIGGER ${t}_after_update\nAFTER UPDATE ON ${t}\nBEGIN\n  -- UPDATE ${t} SET updated_at = CURRENT_TIMESTAMP WHERE rowid = NEW.rowid;\n  SELECT 1;\nEND;\n`
  }
  return `CREATE TRIGGER ${t}_before_update\nBEFORE UPDATE ON ${schema}.${t}\nFOR EACH ROW\nEXECUTE FUNCTION my_trigger_function();\n`
}

interface SectionProps {
  draft: StructureDraft
  caps: Caps
  create: boolean
  failureKey: string | null
  failureField: string
  stage(fn: (d: StructureDraft) => StructureDraft): void
}

function rowClass(state: string, failed: boolean): string {
  return `struct-row struct-row-${state}${failed ? ' struct-row-failed' : ''}`
}

function DropButton({ dropped, isNew, label, disabled, title, onClick }: { dropped: boolean; isNew: boolean; label: string; disabled?: boolean; title?: string; onClick(): void }) {
  const text = isNew ? 'Remove' : dropped ? 'Restore' : 'Drop'
  return (
    <button type="button" className="struct-drop" aria-label={`${text} ${label}`} disabled={disabled} title={title} onClick={onClick}>
      {text}
    </button>
  )
}

function ColumnsSection({ draft, caps, create, failureKey, failureField, stage }: SectionProps) {
  const pkEditable = create || caps.primaryKey
  return (
    <div className="struct-section">
      <table className="structure-table struct-edit-table" aria-label="Columns">
        <thead>
          <tr>
            <th title="Primary key">Key</th>
            <th>Name</th>
            <th>Type</th>
            <th>Nullable</th>
            <th>Default</th>
            <th title="Auto-increment">Auto</th>
            {caps.comments && <th>Comment</th>}
            {caps.collation && <th>Collation</th>}
            <th />
          </tr>
        </thead>
        <tbody>
          {draft.columns.map((c, i) => {
            const state = columnState(c, caps)
            const existing = c.original !== null
            const alter = !existing || caps.alterColumn
            const failed = failureKey === c.key
            const changed = new Set(changedFields(c, caps))
            const label = c.name || `column ${i + 1}`
            const pkPos = draft.pk.indexOf(c.key)
            const enumType = ENUM_RE.test(c.type.trim())
            const generated = c.defaultMode === 'generatedStored' || c.defaultMode === 'generatedVirtual'
            const inv = (f: string) => (failed && failureField === f ? true : undefined)
            const set = (p: Partial<ColumnDraft>) => stage((d) => updateColumn(d, c.key, p))
            return (
              <tr key={c.key} className={rowClass(state, failed)} data-testid={`column-${c.key}`}>
                <td>
                  <button
                    type="button"
                    className={`struct-pk${pkPos >= 0 ? ' on' : ''}`}
                    aria-pressed={pkPos >= 0}
                    aria-label={`Primary key: ${label}`}
                    title={pkEditable ? (pkPos >= 0 ? `Key position ${pkPos + 1}: click to remove` : 'Add to the primary key') : caps.reason}
                    disabled={!pkEditable || c.dropped}
                    onClick={() => stage((d) => togglePK(d, c.key))}
                  >
                    <KeyIcon className="icon" />
                    {pkPos >= 0 && draft.pk.length > 1 && <span className="count">{pkPos + 1}</span>}
                  </button>
                </td>
                <td>
                  <input
                    aria-label={`Name of ${label}`}
                    className={changed.has('name') ? 'changed' : ''}
                    value={c.name}
                    disabled={c.dropped || (existing && !caps.renameColumn)}
                    aria-invalid={inv('name')}
                    spellCheck={false}
                    onChange={(e) => set({ name: e.target.value })}
                  />
                </td>
                <td>
                  <input
                    aria-label={`Type of ${label}`}
                    className={changed.has('type') ? 'changed' : ''}
                    list="struct-types"
                    value={c.type}
                    disabled={c.dropped || !alter}
                    title={!alter ? caps.reason : undefined}
                    aria-invalid={inv('type')}
                    spellCheck={false}
                    onChange={(e) => set({ type: e.target.value })}
                  />
                  {enumType && (
                    <input
                      aria-label={`Values of ${label}`}
                      className="struct-sub"
                      value={c.enumValues.join(', ')}
                      placeholder="a, b, c"
                      disabled={c.dropped || !alter}
                      onChange={(e) => set({ enumValues: e.target.value.split(',').map((v) => v.trim()).filter(Boolean) })}
                    />
                  )}
                  {caps.usingExpr && existing && changed.has('type') && (
                    <input
                      aria-label={`USING for ${label}`}
                      className="struct-sub"
                      value={c.using}
                      placeholder={`USING ${c.original!.name}::${c.type || 'type'}`}
                      aria-invalid={inv('using')}
                      spellCheck={false}
                      onChange={(e) => set({ using: e.target.value })}
                    />
                  )}
                </td>
                <td>
                  <input
                    type="checkbox"
                    aria-label={`${label} is nullable`}
                    checked={c.nullable}
                    disabled={c.dropped || !alter}
                    onChange={(e) => set({ nullable: e.target.checked })}
                  />
                </td>
                <td className="struct-default">
                  <select
                    aria-label={`Default of ${label}`}
                    value={c.defaultMode}
                    disabled={c.dropped || !alter || (existing && generated)}
                    aria-invalid={inv('default') ?? inv('generated')}
                    onChange={(e) => set({ defaultMode: e.target.value as DefaultMode })}
                  >
                    {defaultModes(c, caps, create).map((m) => (
                      <option key={m} value={m}>
                        {DEFAULT_LABELS[m]}
                      </option>
                    ))}
                  </select>
                  {(c.defaultMode === 'value' || c.defaultMode === 'expression' || generated) && (
                    <input
                      aria-label={`${generated ? 'Generation expression' : 'Default value'} of ${label}`}
                      className={changed.has('default') ? 'changed' : ''}
                      value={c.defaultText}
                      placeholder={c.defaultMode === 'value' ? 'text, quoted for you' : 'now()'}
                      disabled={c.dropped || !alter || (existing && generated)}
                      aria-invalid={inv('default') ?? inv('generated')}
                      spellCheck={false}
                      onChange={(e) => set({ defaultText: e.target.value })}
                    />
                  )}
                </td>
                <td>
                  <input
                    type="checkbox"
                    aria-label={`${label} auto-increments`}
                    checked={c.autoIncrement}
                    disabled={c.dropped || !alter}
                    aria-invalid={inv('autoIncrement')}
                    onChange={(e) => set({ autoIncrement: e.target.checked })}
                  />
                </td>
                {caps.comments && (
                  <td>
                    <input aria-label={`Comment of ${label}`} value={c.comment} disabled={c.dropped} aria-invalid={inv('comment')} onChange={(e) => set({ comment: e.target.value })} />
                  </td>
                )}
                {caps.collation && (
                  <td>
                    <input aria-label={`Collation of ${label}`} value={c.collation} disabled={c.dropped} aria-invalid={inv('collation')} spellCheck={false} onChange={(e) => set({ collation: e.target.value })} />
                  </td>
                )}
                <td>
                  <DropButton
                    dropped={c.dropped}
                    isNew={!existing}
                    label={label}
                    disabled={existing && !caps.dropColumn}
                    title={existing && !caps.dropColumn ? caps.reason : undefined}
                    onClick={() => stage((d) => toggleDropColumn(d, c.key))}
                  />
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
      <datalist id="struct-types">
        {caps.types.map((t) => (
          <option key={t} value={t} />
        ))}
      </datalist>
      <div className="struct-actions">
        <button type="button" onClick={() => stage((d) => addColumn(d, { type: caps.types[0] ?? '' }))}>
          Add column
        </button>
        {failureKey === 'pk' && (
          <span className="field-error" role="note">
            The primary key change failed.
          </span>
        )}
      </div>
    </div>
  )
}

function IndexesSection({ draft, caps, create, failureKey, stage, table }: SectionProps & { table: string }) {
  const [adding, setAdding] = useState(false)
  return (
    <div className="struct-section">
      {draft.indexes.length === 0 ? (
        <p className="muted structure-empty">No indexes{create ? ' yet' : ''}.</p>
      ) : (
        <table className="structure-table struct-edit-table" aria-label="Indexes">
          <thead>
            <tr>
              <th>Name</th>
              <th>Columns</th>
              <th>Unique</th>
              <th>Method</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {draft.indexes.map((x) => {
              const state = itemState(x, x.original?.name)
              const cols = x.original
                ? x.original.columns.join(', ')
                : x.parts.map((p) => (p.column ? columnName(draft, p.column) + (p.length ? `(${p.length})` : '') : `(${p.expr})`) + (p.desc ? ' DESC' : '')).join(', ')
              return (
                <tr key={x.key} className={rowClass(state, failureKey === x.key)}>
                  <td>
                    {x.original && caps.renameIndex ? (
                      <input aria-label={`Name of index ${x.original.name}`} value={x.name} disabled={x.dropped} spellCheck={false} onChange={(e) => stage((d) => renameIndex(d, x.key, e.target.value))} />
                    ) : (
                      <span className="mono">{x.name}</span>
                    )}
                  </td>
                  <td className="mono">
                    {cols}
                    {(x.original?.predicate || x.predicate) && <span className="muted"> WHERE {x.original?.predicate || x.predicate}</span>}
                  </td>
                  <td>{x.unique ? 'YES' : 'NO'}</td>
                  <td>{x.method}</td>
                  <td>
                    <DropButton dropped={x.dropped} isNew={!x.original} label={`index ${x.name}`} onClick={() => stage((d) => toggleDropIndex(d, x.key))} />
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
      <div className="struct-actions">
        <button type="button" onClick={() => setAdding(true)} disabled={liveColumns(draft).length === 0}>
          Add index
        </button>
      </div>
      {adding && (
        <IndexDialog
          caps={caps}
          columns={liveColumns(draft)}
          table={table}
          onCancel={() => setAdding(false)}
          onAdd={(x) => {
            stage((d) => addIndex(d, x))
            setAdding(false)
          }}
        />
      )}
    </div>
  )
}

function ForeignKeysSection({ draft, caps, create, failureKey, stage, schema, table }: SectionProps & { schema: string; table: string }) {
  const [adding, setAdding] = useState(false)
  const tables = useWorkspace((s) => s.tables)
  const tableColumns = useWorkspace((s) => s.tableColumns)
  const loadColumns = useCallback((t: string) => tableColumns(schema, t), [tableColumns, schema])
  const allowed = create || caps.foreignKeys
  return (
    <div className="struct-section">
      {draft.fks.length === 0 ? (
        <p className="muted structure-empty">No foreign keys.</p>
      ) : (
        <table className="structure-table struct-edit-table" aria-label="Foreign keys">
          <thead>
            <tr>
              <th>Name</th>
              <th>Columns</th>
              <th>References</th>
              <th>On update</th>
              <th>On delete</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {draft.fks.map((f) => (
              <tr key={f.key} className={rowClass(itemState(f), failureKey === f.key)}>
                <td className="mono">{f.name || <span className="muted">—</span>}</td>
                <td className="mono">{f.columns.map((k) => columnName(draft, k)).join(', ')}</td>
                <td className="mono">
                  {f.refTable} ({f.refColumns.join(', ')})
                </td>
                <td>{f.onUpdate}</td>
                <td>{f.onDelete}</td>
                <td>
                  <DropButton
                    dropped={f.dropped}
                    isNew={!f.original}
                    label={`foreign key ${f.name || f.refTable}`}
                    disabled={!allowed}
                    title={!allowed ? caps.reason : undefined}
                    onClick={() => stage((d) => toggleDropFK(d, f.key))}
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="struct-actions">
        <button type="button" onClick={() => setAdding(true)} disabled={!allowed || liveColumns(draft).length === 0} title={!allowed ? caps.reason : undefined}>
          Add foreign key
        </button>
      </div>
      {adding && (
        <ForeignKeyDialog
          columns={liveColumns(draft)}
          schema={schema}
          table={draft.name || table}
          tables={tables.filter((t) => t.type === 'table').map((t) => t.name)}
          loadColumns={loadColumns}
          onCancel={() => setAdding(false)}
          onAdd={(x) => {
            stage((d) => addFK(d, x))
            setAdding(false)
          }}
        />
      )}
    </div>
  )
}

function ChecksSection({ draft, caps, create, failureKey, failureField, stage }: SectionProps) {
  const allowed = caps.checks || (create && caps.flavor === 'sqlite')
  return (
    <div className="struct-section">
      {draft.checks.length === 0 ? (
        <p className="muted structure-empty">No check constraints.</p>
      ) : (
        <table className="structure-table struct-edit-table" aria-label="Checks">
          <thead>
            <tr>
              <th>Name</th>
              <th>Expression</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {draft.checks.map((c, i) => {
              const failed = failureKey === c.key
              return (
                <tr key={c.key} className={rowClass(itemState(c), failed)}>
                  <td>
                    {c.original ? (
                      <span className="mono">{c.name}</span>
                    ) : (
                      <input aria-label={`Name of check ${i + 1}`} value={c.name} placeholder="Optional" spellCheck={false} onChange={(e) => stage((d) => updateCheck(d, c.key, { name: e.target.value }))} />
                    )}
                  </td>
                  <td>
                    {c.original ? (
                      <span className="mono selectable">{c.expression}</span>
                    ) : (
                      <input
                        aria-label={`Expression of check ${i + 1}`}
                        className="struct-wide"
                        value={c.expression}
                        placeholder="price > 0"
                        aria-invalid={failed && failureField === 'expression' ? true : undefined}
                        spellCheck={false}
                        onChange={(e) => stage((d) => updateCheck(d, c.key, { expression: e.target.value }))}
                      />
                    )}
                  </td>
                  <td>
                    <DropButton dropped={c.dropped} isNew={!c.original} label={`check ${c.name || i + 1}`} disabled={!allowed} onClick={() => stage((d) => toggleDropCheck(d, c.key))} />
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
      <div className="struct-actions">
        <button type="button" disabled={!allowed} title={!allowed ? caps.reason || 'Check constraints are not supported by this server.' : undefined} onClick={() => stage((d) => addCheck(d, { name: '', expression: '' }))}>
          Add check
        </button>
      </div>
    </div>
  )
}

function TriggersSection({ draft, caps, failureKey, stage, schema, table }: SectionProps & { schema: string; table: string }) {
  const newQueryTab = useWorkspace((s) => s.newQueryTab)
  return (
    <div className="struct-section">
      {draft.triggers.length === 0 ? (
        <p className="muted structure-empty">No triggers.</p>
      ) : (
        <table className="structure-table struct-edit-table" aria-label="Triggers">
          <thead>
            <tr>
              <th>Name</th>
              <th>Timing</th>
              <th>Event</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {draft.triggers.map((t) => (
              <tr key={t.original.name} className={rowClass(t.dropped ? 'dropped' : 'same', failureKey === t.original.name)}>
                <td className="mono">{t.original.name}</td>
                <td>{t.original.timing}</td>
                <td>{t.original.event}</td>
                <td className="struct-row-actions">
                  <button type="button" aria-label={`Edit trigger ${t.original.name} in a query tab`} onClick={() => newQueryTab(t.original.definition)}>
                    Edit as SQL
                  </button>
                  <DropButton dropped={t.dropped} isNew={false} label={`trigger ${t.original.name}`} onClick={() => stage((d) => toggleDropTrigger(d, t.original.name))} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="struct-actions">
        <button type="button" onClick={() => newQueryTab(triggerTemplate(caps, schema, table))}>
          New trigger…
        </button>
        <span className="muted">A trigger body is code: it opens in a query tab.</span>
      </div>
    </div>
  )
}

/** Staged structure editing of a table tab (or a new table). */
export default function StructureEditor({ tab }: { tab: TabState }) {
  const s = useWorkspace((st) => st)
  const caps = s.structureCaps
  const e = tab.structureEdit
  const [section, setSection] = useState<EditSection>(() => (['columns', 'indexes', 'fks', 'checks', 'triggers'] as StructureSection[]).includes(tab.section) ? (tab.section as EditSection) : 'columns')
  if (!e || !caps) return null
  const d = e.draft
  const create = e.base === null
  const stage = (fn: (x: StructureDraft) => StructureDraft) => s.stageStructure(tab.id, fn)
  const dirty = structureDirty(tab, caps)
  const failureField = e.failure?.field ?? ''
  const props: SectionProps = { draft: d, caps, create, failureKey: e.failureKey, failureField, stage }
  const sections = create ? SECTIONS.filter((x) => x.id !== 'triggers') : SECTIONS
  const tableFailed = e.failureKey === 'table'

  return (
    <div className="structure-view struct-editor" data-testid="structure-editor">
      <header className="structure-header struct-edit-header">
        <label className="struct-field">
          <span className="muted">Table</span>
          <input
            aria-label="Table name"
            value={d.name}
            placeholder="new_table"
            autoFocus={create}
            disabled={!create && !caps.renameTable}
            aria-invalid={tableFailed && failureField === 'name' ? true : undefined}
            spellCheck={false}
            onChange={(ev) => stage((x) => ({ ...x, name: ev.target.value }))}
          />
        </label>
        {caps.comments && (
          <label className="struct-field struct-grow">
            <span className="muted">Comment</span>
            <input aria-label="Table comment" value={d.comment} onChange={(ev) => stage((x) => ({ ...x, comment: ev.target.value }))} />
          </label>
        )}
        <span className="spacer" />
        <button type="button" onClick={() => s.undoStructure(tab.id)} disabled={e.undo.length === 0}>
          Undo
        </button>
        <button type="button" onClick={() => s.redoStructure(tab.id)} disabled={e.redo.length === 0}>
          Redo
        </button>
        <button type="button" onClick={() => s.cancelStructureEdit(tab.id)} disabled={e.applying}>
          {create ? 'Discard table' : dirty ? 'Discard changes' : 'Done'}
        </button>
        <button type="button" className="primary" onClick={() => void s.previewStructure(tab.id)} disabled={!dirty || e.previewLoading || e.applying} title="Review the SQL, then apply (Mod+S)">
          {e.previewLoading ? 'Checking…' : create ? 'Create table…' : 'Apply changes…'}
        </button>
      </header>
      {caps.reason && !create && (
        <p className="banner note struct-note" role="note">
          {caps.reason}
        </p>
      )}
      {e.failure && (
        <div className="banner error tab-error" role="alert" data-testid="structure-failure">
          <span className="selectable">
            {e.failure.message}
            {e.failure.sql && (
              <>
                {' '}
                <code className="mono">{e.failure.sql}</code>
              </>
            )}
          </span>
          {(e.failure.kind === 'structure_changed' || e.failure.applied > 0) && (
            <button
              type="button"
              onClick={() => {
                s.cancelStructureEdit(tab.id)
                void s.loadStructure(tab.id)
              }}
            >
              Reload structure
            </button>
          )}
        </div>
      )}
      <div className="structure-sections" role="tablist" aria-label="Structure sections">
        {sections.map((sec) => (
          <button key={sec.id} type="button" role="tab" aria-selected={section === sec.id} className="structure-section-tab" onClick={() => setSection(sec.id)}>
            {sec.label}
          </button>
        ))}
      </div>
      <div className="structure-body" role="tabpanel">
        {section === 'columns' && <ColumnsSection {...props} />}
        {section === 'indexes' && <IndexesSection {...props} table={d.name || tab.table} />}
        {section === 'fks' && <ForeignKeysSection {...props} schema={tab.schema} table={create ? d.name : tab.table} />}
        {section === 'checks' && <ChecksSection {...props} />}
        {section === 'triggers' && <TriggersSection {...props} schema={tab.schema} table={tab.table} />}
      </div>
      {e.preview && (
        <StructurePreviewDialog
          statements={e.preview}
          transactional={caps.transactionalDdl}
          applying={e.applying}
          onApply={() => void s.applyStructure(tab.id)}
          onClose={() => s.closeStructurePreview(tab.id)}
        />
      )}
    </div>
  )
}
