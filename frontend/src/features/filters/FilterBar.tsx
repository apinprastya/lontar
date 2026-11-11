import type { KeyboardEvent } from 'react'
import type { FilterRow } from '../../stores/workspace'
import { newFilterRow } from '../../stores/workspace'
import { FILTER_OPS, opTakesValue, type FilterOp, type ResultColumn } from '../workspace/types'

export interface FilterBarProps {
  columns: ResultColumn[]
  rows: FilterRow[]
  rawMode: boolean
  rawWhere: string
  /** Whether the applied filter differs from none (enables Clear). */
  active: boolean
  onChange(rows: FilterRow[]): void
  onRawModeChange(raw: boolean): void
  onRawWhereChange(raw: string): void
  onApply(): void
  onClear(): void
}

/**
 * Filter rows (column, operator, value) combined with AND, or a raw SQL
 * WHERE condition typed by the user. Enter applies.
 */
export default function FilterBar(props: FilterBarProps) {
  const { columns, rows, rawMode, rawWhere, active, onChange, onRawModeChange, onRawWhereChange, onApply, onClear } = props

  const update = (id: number, patch: Partial<FilterRow>) => onChange(rows.map((r) => (r.id === id ? { ...r, ...patch } : r)))
  const remove = (id: number) => onChange(rows.filter((r) => r.id !== id))
  const add = () => onChange([...rows, newFilterRow(columns[0]?.name ?? '')])
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      onApply()
    }
  }

  return (
    <div className="filter-bar" role="search" aria-label="Filters" onKeyDown={onKeyDown}>
      {rawMode ? (
        <div className="filter-row">
          <span className="filter-where mono">WHERE</span>
          <input
            className="filter-raw mono"
            aria-label="Raw SQL WHERE condition"
            placeholder="e.g. price > 10 AND name LIKE 'a%'"
            value={rawWhere}
            onChange={(e) => onRawWhereChange(e.target.value)}
            autoFocus
          />
        </div>
      ) : (
        rows.map((r, i) => (
          <div className="filter-row" key={r.id} data-testid="filter-row">
            <select aria-label={`Column ${i + 1}`} value={r.column} onChange={(e) => update(r.id, { column: e.target.value })}>
              {columns.map((c) => (
                <option key={c.name} value={c.name}>
                  {c.name}
                </option>
              ))}
            </select>
            <select aria-label={`Operator ${i + 1}`} value={r.op} onChange={(e) => update(r.id, { op: e.target.value as FilterOp })}>
              {FILTER_OPS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
            {opTakesValue(r.op) && (
              <input
                aria-label={`Value ${i + 1}`}
                placeholder={r.op === 'in' || r.op === 'notIn' ? 'a, b, c' : r.op === 'between' ? 'from' : 'value'}
                value={r.value}
                onChange={(e) => update(r.id, { value: e.target.value })}
                autoFocus={i === rows.length - 1}
              />
            )}
            {r.op === 'between' && (
              <>
                <span className="muted">and</span>
                <input aria-label={`Second value ${i + 1}`} placeholder="to" value={r.value2} onChange={(e) => update(r.id, { value2: e.target.value })} />
              </>
            )}
            <button type="button" className="icon-button" aria-label={`Remove filter ${i + 1}`} onClick={() => remove(r.id)}>
              −
            </button>
            {i === rows.length - 1 && (
              <button type="button" className="icon-button" aria-label="Add filter" onClick={add}>
                +
              </button>
            )}
          </div>
        ))
      )}
      <div className="filter-actions">
        {!rawMode && rows.length === 0 && (
          <button type="button" onClick={add}>
            Add filter
          </button>
        )}
        <label className="filter-raw-toggle">
          <input type="checkbox" checked={rawMode} onChange={(e) => onRawModeChange(e.target.checked)} /> Raw SQL
        </label>
        <span className="spacer" />
        <button type="button" onClick={onClear} disabled={!active}>
          Clear
        </button>
        <button type="button" className="primary" onClick={onApply}>
          Apply
        </button>
      </div>
    </div>
  )
}
