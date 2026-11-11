import type { Cell, ResultColumn } from '../workspace/types'
import { binaryLabel, cellText, isNull } from './cells'
import { KeyIcon } from '../../components/icons'

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
}

/** Read-only vertical view of one row: column → full value. */
export default function RowDetail({ columns, row, rowNumber, onClose }: RowDetailProps) {
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
            return (
              <div key={c.name} className="row-detail-item">
                <dt>
                  {c.isPK && <KeyIcon className="icon pk-icon" title="Primary key" />}
                  <span className="row-detail-name">{c.name}</span>
                  <span className="row-detail-type">{c.type}</span>
                </dt>
                <dd className={isNull(cell) ? 'null-value' : cell.k === 'num' ? 'num-value' : undefined}>
                  {detailText(cell)}
                  {cell.trunc && cell.k !== 'bytes' && <span className="muted"> (truncated)</span>}
                </dd>
              </div>
            )
          })}
        </dl>
      )}
    </aside>
  )
}
