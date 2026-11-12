import { useEffect, useId, useRef, useState } from 'react'
import { shortcutLabel } from '../../lib/mod'
import { useSettings } from '../../stores/settings'
import { DEFAULT_SETTINGS, LIMITS, type Settings, type ThemeSetting } from './types'

export interface SettingsDialogProps {
  platform?: string
}

const THEMES: { value: ThemeSetting; label: string }[] = [
  { value: 'system', label: 'System' },
  { value: 'dark', label: 'Dark' },
  { value: 'light', label: 'Light' },
]

export const DELIMITERS: { value: string; label: string }[] = [
  { value: ',', label: 'Comma (,)' },
  { value: ';', label: 'Semicolon (;)' },
  { value: '\t', label: 'Tab' },
  { value: '|', label: 'Pipe (|)' },
]

const clamp = (v: number, [lo, hi]: readonly [number, number]) => (Number.isFinite(v) ? Math.min(hi, Math.max(lo, Math.round(v))) : lo)

/** The settings dialog (Mod+,). Changes apply when saved. */
export default function SettingsDialog({ platform }: SettingsDialogProps) {
  const uid = useId()
  const stored = useSettings((s) => s.settings)
  const error = useSettings((s) => s.error)
  const save = useSettings((s) => s.save)
  const setOpen = useSettings((s) => s.setOpen)
  const [draft, setDraft] = useState<Settings>(stored)
  const [busy, setBusy] = useState(false)
  const firstRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    firstRef.current?.focus()
  }, [])

  const set = <K extends keyof Settings>(k: K, v: Settings[K]) => setDraft((d) => ({ ...d, [k]: v }))
  const setCsv = <K extends keyof Settings['csvExport']>(k: K, v: Settings['csvExport'][K]) =>
    setDraft((d) => ({ ...d, csvExport: { ...d.csvExport, [k]: v } }))
  const close = () => setOpen(false)

  async function submit() {
    setBusy(true)
    const ok = await save({
      ...draft,
      editorFontSize: clamp(draft.editorFontSize, LIMITS.fontSize),
      gridPageSize: clamp(draft.gridPageSize, LIMITS.pageSize),
      editorMaxRows: clamp(draft.editorMaxRows, LIMITS.maxRows),
    })
    setBusy(false)
    if (ok) close()
  }

  const id = (name: string) => `${uid}-${name}`
  return (
    <div className="dialog-backdrop">
      <form
        className="dialog settings-dialog"
        noValidate
        role="dialog"
        aria-modal="true"
        aria-labelledby={id('title')}
        onSubmit={(e) => {
          e.preventDefault()
          void submit()
        }}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault()
            close()
          }
        }}
      >
        <h2 id={id('title')}>Settings</h2>
        <div className="form-grid">
          <span id={id('theme')}>Theme</span>
          <div className="segmented" role="radiogroup" aria-labelledby={id('theme')}>
            {THEMES.map((t, i) => (
              <label key={t.value}>
                <input ref={i === 0 ? firstRef : undefined} type="radio" name={id('theme')} checked={draft.theme === t.value} onChange={() => set('theme', t.value)} />
                {t.label}
              </label>
            ))}
          </div>

          <label htmlFor={id('font')}>Editor font size</label>
          <input id={id('font')} type="number" min={LIMITS.fontSize[0]} max={LIMITS.fontSize[1]} value={draft.editorFontSize} onChange={(e) => set('editorFontSize', Number(e.target.value))} />

          <label htmlFor={id('page')}>Rows per table page</label>
          <input id={id('page')} type="number" min={LIMITS.pageSize[0]} max={LIMITS.pageSize[1]} value={draft.gridPageSize} onChange={(e) => set('gridPageSize', Number(e.target.value))} />

          <label htmlFor={id('maxrows')}>Editor max rows</label>
          <input id={id('maxrows')} type="number" min={LIMITS.maxRows[0]} max={LIMITS.maxRows[1]} value={draft.editorMaxRows} onChange={(e) => set('editorMaxRows', Number(e.target.value))} />

          <span />
          <label className="checks">
            <input type="checkbox" checked={draft.confirmCloseDirtyQuery} onChange={(e) => set('confirmCloseDirtyQuery', e.target.checked)} /> Ask before closing
            a query tab with unrun changes
          </label>

          <h3 className="form-section">CSV export defaults</h3>
          <label htmlFor={id('delim')}>Delimiter</label>
          <select id={id('delim')} value={draft.csvExport.delimiter} onChange={(e) => setCsv('delimiter', e.target.value)}>
            {DELIMITERS.map((d) => (
              <option key={d.label} value={d.value}>
                {d.label}
              </option>
            ))}
          </select>

          <label htmlFor={id('null')}>NULL as</label>
          <div className="field inline-fields">
            <select id={id('null')} value={draft.csvExport.null} onChange={(e) => setCsv('null', e.target.value)}>
              <option value="empty">Empty field</option>
              <option value="\N">\N</option>
              <option value="custom">Custom text</option>
            </select>
            {draft.csvExport.null === 'custom' && (
              <input aria-label="NULL text" value={draft.csvExport.nullText} onChange={(e) => setCsv('nullText', e.target.value)} />
            )}
          </div>

          <span />
          <div className="checks">
            <label>
              <input type="checkbox" checked={draft.csvExport.header} onChange={(e) => setCsv('header', e.target.checked)} /> Header row
            </label>
            <label>
              <input type="checkbox" checked={draft.csvExport.crlf} onChange={(e) => setCsv('crlf', e.target.checked)} /> CRLF line endings
            </label>
            <label>
              <input type="checkbox" checked={draft.csvExport.bom} onChange={(e) => setCsv('bom', e.target.checked)} /> UTF-8 BOM (Excel)
            </label>
          </div>
        </div>
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <div className="dialog-actions">
          <button type="button" onClick={() => setDraft({ ...DEFAULT_SETTINGS })}>
            Restore defaults
          </button>
          <span className="spacer" />
          <span className="hint">{shortcutLabel(platform, ',')}</span>
          <button type="button" onClick={close}>
            Cancel
          </button>
          <button type="submit" className="primary" disabled={busy}>
            Save
          </button>
        </div>
      </form>
    </div>
  )
}
