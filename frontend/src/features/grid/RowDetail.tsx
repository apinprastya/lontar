import { useState } from 'react'
import type { Cell, ResultColumn } from '../workspace/types'
import { binaryLabel, cellText, isNull } from './cells'
import { KeyIcon, LockIcon } from '../../components/icons'
import type { Kind } from '../connections/types'
import { CellEditor } from '../edits/editors'
import type { EditValue } from '../edits/types'
import type { DisplayRow } from '../../stores/pending'

/** Full text of a value for the detail panel (JSON pretty-printed). */
export function detailText(cell: Cell): string {
  if (cell.k === 'json' && typeof cell.v === 'string') {
    try {
      return JSON.stringify(JSON.parse(cell.v), null, 2)
    } catch {
      return cell.v
    }
  }
  if (cell.k === 'bytes') {
    const hex = String(cell.v ?? '')
    return hex ? `${binaryLabel(cell)}\n0x${hex}${cell.trunc ? '…' : ''}` : binaryLabel(cell)
  }
  return cellText(cell)
}

export interface RowDetailProps {
  columns: ResultColumn[]
  row: Cell[] | null
  /** 1-based row number shown in the title. */
  rowNumber: number | null
  onClose(): void
  /** Editable tables: the selected row's staged state and how to stage values. */
  editing?: RowDetailEditing | null
}

export interface RowDetailEditing {
  engine: Kind
  state: DisplayRow
  editText(col: number): string
  onCommit(col: number, value: EditValue): void
}

/** Vertical view of one row: column → full value; editable for editable tables. */
export default function RowDetail({ columns, row, rowNumber, onClose, editing }: RowDetailProps) {
  const [editingCol, setEditingCol] = useState<number | null>(null)
  const state = editing?.state
  return (
    <aside className="row-detail" aria-label="Row detail">
      <div className="panel-header">
        <span>{rowNumber ? `Row ${rowNumber.toLocaleString('en-US')}` : 'Row detail'}</span>
        <button type="button" className="icon-button" aria-label="Close row detail" onClick={onClose}>
          ×
        </button>
      </div>
      {!row ? (
        <p className="muted row-detail-empty">Select a row to see its values.</p>
      ) : (
        <dl className="row-detail-list">
          {columns.map((c, i) => {
            const cell = row[i] ?? { k: 'null' }
            const canEditCol = Boolean(editing && state && state.state !== 'deleted' && !state.locked[i])
            const modified = Boolean(state?.modified[i])
            return (
              <div key={`${i}:${c.name}`} className={`row-detail-item${modified ? ' modified' : ''}`}>
                <dt>
                  {c.isPK && <KeyIcon className="icon pk-icon" title="Primary key" />}
                  <span className="row-detail-name">{c.name}</span>
                  <span className="row-detail-type">{c.type}</span>
                  {editing && state?.locked[i] && <LockIcon className="icon" title="Truncated value: cannot be edited" />}
                  {canEditCol && editingCol !== i && (
                    <button type="button" className="link-button row-detail-edit" aria-label={`Edit ${c.name}`} onClick={() => setEditingCol(i)}>
                      Edit
                    </button>
                  )}
                </dt>
                {editing && editingCol === i && canEditCol ? (
                  <dd>
                    <CellEditor
                      column={c}
                      engine={editing.engine}
                      text={editing.editText(i)}
                      allowDefault={editing.engine !== 'sqlite' || state?.state === 'inserted'}
                      onCommit={(v) => {
                        editing.onCommit(i, v)
                        setEditingCol(null)
                      }}
                      onCancel={() => setEditingCol(null)}
                    />
                  </dd>
                ) : (
                  <dd className={isNull(cell) ? 'null-value' : cell.k === 'num' ? 'num-value' : undefined}>
                    {detailText(cell)}
                    {cell.trunc && cell.k !== 'bytes' && <span className="muted"> (truncated)</span>}
                  </dd>
                )}
              </div>
            )
          })}
        </dl>
      )}
    </aside>
  )
}
