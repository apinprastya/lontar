import { useEffect, useId, useRef, useState, type FormEvent } from 'react'
import type { ParamType, ParamValue, ParamValues } from './types'

const NUMBER_RE = /^\s*[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?\s*$/

/** Whether text is a number Go binds (an integer or a float). */
export function isNumberText(text: string): boolean {
  return NUMBER_RE.test(text)
}

const EMPTY: ParamValue = { value: '', null: false, type: 'text' }

/**
 * Asks for the values of a run's :name placeholders. Values are bound as
 * parameters by Go, never written into the SQL; initial holds the values of
 * the last run.
 */
export function ParamsDialog({
  names,
  initial,
  explain = false,
  onSubmit,
  onCancel,
}: {
  names: string[]
  initial: ParamValues
  /** The values are for Explain (only changes the button label). */
  explain?: boolean
  onSubmit(values: ParamValues): void
  onCancel(): void
}) {
  const uid = useId()
  const first = useRef<HTMLInputElement>(null)
  const [values, setValues] = useState<ParamValues>(() => Object.fromEntries(names.map((n) => [n, initial[n] ?? EMPTY])))
  const [checked, setChecked] = useState(false)
  useEffect(() => {
    first.current?.focus()
    first.current?.select()
  }, [])

  const errorOf = (v: ParamValue) => (!v.null && v.type === 'number' && !isNumberText(v.value) ? 'Not a number' : null)
  const update = (name: string, p: Partial<ParamValue>) => setValues((vs) => ({ ...vs, [name]: { ...vs[name], ...p } }))

  const submit = (e: FormEvent) => {
    e.preventDefault()
    setChecked(true)
    if (names.some((n) => errorOf(values[n]))) return
    onSubmit(values)
  }

  return (
    <div className="dialog-backdrop">
      <form
        className="dialog params-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={`${uid}-t`}
        onSubmit={submit}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault()
            onCancel()
          }
        }}
      >
        <h2 id={`${uid}-t`}>Query parameters</h2>
        <p className="muted">Values are sent as bound parameters, never pasted into the SQL.</p>
        <div className="params-grid">
          {names.map((name, i) => {
            const v = values[name]
            const err = checked ? errorOf(v) : null
            const id = `${uid}-${i}`
            return (
              <div key={name} className="params-row">
                <label htmlFor={id} className="params-name">
                  :{name}
                </label>
                <div className="field">
                  <input
                    id={id}
                    ref={i === 0 ? first : undefined}
                    value={v.null ? '' : v.value}
                    placeholder={v.null ? 'NULL' : ''}
                    disabled={v.null}
                    spellCheck={false}
                    autoComplete="off"
                    aria-invalid={err ? true : undefined}
                    aria-describedby={err ? `${id}-err` : undefined}
                    onChange={(e) => update(name, { value: e.target.value })}
                  />
                  {err && (
                    <span id={`${id}-err`} className="field-error">
                      {err}
                    </span>
                  )}
                </div>
                <select aria-label={`Type of :${name}`} value={v.type} onChange={(e) => update(name, { type: e.target.value as ParamType })}>
                  <option value="text">Text</option>
                  <option value="number">Number</option>
                </select>
                <label className="params-null">
                  <input type="checkbox" aria-label={`:${name} is NULL`} checked={v.null} onChange={(e) => update(name, { null: e.target.checked })} />
                  NULL
                </label>
              </div>
            )
          })}
        </div>
        <div className="dialog-actions">
          <span className="spacer" />
          <button type="button" onClick={onCancel}>
            Cancel
          </button>
          <button type="submit" className="primary">
            {explain ? 'Explain' : 'Run'}
          </button>
        </div>
      </form>
    </div>
  )
}
