import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { shortcutLabel } from '../../lib/mod'
import { useSettings } from '../../stores/settings'
import { useUpdate } from '../../stores/update'
import McpSetupSection from './McpSetup'
import { DEFAULT_SETTINGS, LIMITS, type Settings, type ThemeSetting } from './types'

export interface SettingsDialogProps {
  platform?: string
  /** Application version, shown in the Updates section. */
  version?: string
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

export type SettingsSection = 'appearance' | 'editor' | 'data' | 'connections' | 'ai' | 'updates'

export const SECTIONS: { id: SettingsSection; label: string; description: string }[] = [
  { id: 'appearance', label: 'Appearance', description: 'Theme and text size.' },
  { id: 'editor', label: 'SQL editor', description: 'Running queries and formatting SQL.' },
  { id: 'data', label: 'Data & export', description: 'Table paging and CSV export defaults.' },
  { id: 'connections', label: 'Connections', description: 'Closing connections that are not in use.' },
  { id: 'ai', label: 'AI assistants', description: 'Connect Claude to Lontar over MCP.' },
  { id: 'updates', label: 'Updates', description: 'Check GitHub Releases for a newer version.' },
]

/** Idle disconnect choices, in minutes (0 = never). */
export const IDLE_CHOICES = [0, 5, 15, 30, 60, 120, 240]

export function idleLabel(min: number): string {
  if (min === 0) return 'Never'
  if (min % 60 === 0) return min === 60 ? '1 hour' : `${min / 60} hours`
  return `${min} minutes`
}

// The section shown when the dialog opens again in this run.
let lastSection: SettingsSection = 'appearance'

const clamp = (v: number, [lo, hi]: readonly [number, number]) => (Number.isFinite(v) ? Math.min(hi, Math.max(lo, Math.round(v))) : lo)

/** The settings dialog (Mod+,): sections on the left, one shown at a time. Changes apply when saved. */
export default function SettingsDialog({ platform, version }: SettingsDialogProps) {
  const uid = useId()
  const stored = useSettings((s) => s.settings)
  const error = useSettings((s) => s.error)
  const save = useSettings((s) => s.save)
  const setOpen = useSettings((s) => s.setOpen)
  const [draft, setDraft] = useState<Settings>(stored)
  const [busy, setBusy] = useState(false)
  const [section, setSectionState] = useState<SettingsSection>(lastSection)
  const tabRefs = useRef<Partial<Record<SettingsSection, HTMLButtonElement | null>>>({})

  useEffect(() => {
    tabRefs.current[lastSection]?.focus()
  }, [])

  const setSection = (s: SettingsSection) => {
    lastSection = s
    setSectionState(s)
  }
  const set = <K extends keyof Settings>(k: K, v: Settings[K]) => setDraft((d) => ({ ...d, [k]: v }))
  const setCsv = <K extends keyof Settings['csvExport']>(k: K, v: Settings['csvExport'][K]) =>
    setDraft((d) => ({ ...d, csvExport: { ...d.csvExport, [k]: v } }))
  const fmt = draft.sqlFormat ?? DEFAULT_SETTINGS.sqlFormat
  const setFmt = <K extends keyof Settings['sqlFormat']>(k: K, v: Settings['sqlFormat'][K]) =>
    setDraft((d) => ({ ...d, sqlFormat: { ...(d.sqlFormat ?? DEFAULT_SETTINGS.sqlFormat), [k]: v } }))
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

  // Arrow keys, Home and End move between sections (vertical tab list).
  const onTabKey = (e: KeyboardEvent<HTMLDivElement>) => {
    const i = SECTIONS.findIndex((s) => s.id === section)
    const next = { ArrowDown: i + 1, ArrowUp: i - 1, Home: 0, End: SECTIONS.length - 1 }[e.key]
    if (next === undefined) return
    e.preventDefault()
    const target = SECTIONS[(next + SECTIONS.length) % SECTIONS.length].id
    setSection(target)
    tabRefs.current[target]?.focus()
  }

  const id = (name: string) => `${uid}-${name}`
  const current = SECTIONS.find((s) => s.id === section)!

  let body: ReactNode
  switch (section) {
    case 'appearance':
      body = (
        <div className="form-grid">
          <span id={id('theme')}>Theme</span>
          <div className="segmented" role="radiogroup" aria-labelledby={id('theme')}>
            {THEMES.map((t) => (
              <label key={t.value}>
                <input type="radio" name={id('theme')} checked={draft.theme === t.value} onChange={() => set('theme', t.value)} />
                {t.label}
              </label>
            ))}
          </div>

          <label htmlFor={id('font')}>Editor font size</label>
          <input id={id('font')} type="number" min={LIMITS.fontSize[0]} max={LIMITS.fontSize[1]} value={draft.editorFontSize} onChange={(e) => set('editorFontSize', Number(e.target.value))} />
        </div>
      )
      break
    case 'editor':
      body = (
        <div className="form-grid">
          <label htmlFor={id('maxrows')}>Max rows</label>
          <input id={id('maxrows')} type="number" min={LIMITS.maxRows[0]} max={LIMITS.maxRows[1]} value={draft.editorMaxRows} onChange={(e) => set('editorMaxRows', Number(e.target.value))} />

          <span />
          <label className="checks">
            <input type="checkbox" checked={draft.confirmCloseDirtyQuery} onChange={(e) => set('confirmCloseDirtyQuery', e.target.checked)} /> Ask before closing
            a query tab with unrun changes
          </label>

          <h4 className="form-section">Format SQL</h4>
          <label htmlFor={id('kwcase')}>Keyword case</label>
          <select id={id('kwcase')} value={fmt.keywordCase} onChange={(e) => setFmt('keywordCase', e.target.value as Settings['sqlFormat']['keywordCase'])}>
            <option value="upper">UPPER</option>
            <option value="lower">lower</option>
            <option value="preserve">Keep as typed</option>
          </select>

          <label htmlFor={id('indent')}>Indent</label>
          <select id={id('indent')} value={fmt.indent} onChange={(e) => setFmt('indent', Number(e.target.value))}>
            <option value={2}>2 spaces</option>
            <option value={4}>4 spaces</option>
          </select>
        </div>
      )
      break
    case 'data':
      body = (
        <div className="form-grid">
          <label htmlFor={id('page')}>Rows per page</label>
          <input id={id('page')} type="number" min={LIMITS.pageSize[0]} max={LIMITS.pageSize[1]} value={draft.gridPageSize} onChange={(e) => set('gridPageSize', Number(e.target.value))} />

          <h4 className="form-section">CSV export defaults</h4>
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
      )
      break
    case 'connections': {
      const idle = draft.idleDisconnectMinutes ?? 0
      const choices = IDLE_CHOICES.includes(idle) ? IDLE_CHOICES : [...IDLE_CHOICES, idle].sort((a, b) => a - b)
      body = (
        <div className="form-grid">
          <label htmlFor={id('idle')}>Disconnect when idle for</label>
          <select id={id('idle')} aria-describedby={id('idle-hint')} value={idle} onChange={(e) => set('idleDisconnectMinutes', Number(e.target.value))}>
            {choices.map((m) => (
              <option key={m} value={m}>
                {idleLabel(m)}
              </option>
            ))}
          </select>
          <span />
          <p className="hint" id={id('idle-hint')}>
            Closes the database connection and SSH tunnel of an unused session. Its tabs stay open and it reconnects on the next action. A
            running query, an open transaction or a truncated result keeps the connection open; temporary tables and session settings are lost.
          </p>
        </div>
      )
      break
    }
    case 'ai':
      body = (
        <div className="form-grid">
          <McpSetupSection />
        </div>
      )
      break
    case 'updates':
      body = (
        <div className="settings-updates">
          {version && (
            <p>
              Installed version <strong className="mono" data-testid="settings-version">v{version}</strong>
            </p>
          )}
          <label className="checks">
            <input type="checkbox" checked={draft.autoUpdate ?? true} onChange={(e) => set('autoUpdate', e.target.checked)} /> Install updates
            automatically
          </label>
          <div>
            <button
              type="button"
              onClick={() => {
                close()
                void useUpdate.getState().check()
              }}
            >
              Check for updates…
            </button>
          </div>
        </div>
      )
      break
  }

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
        <div className="settings-body">
          <div className="settings-nav" role="tablist" aria-orientation="vertical" aria-label="Settings sections" onKeyDown={onTabKey}>
            {SECTIONS.map((s) => (
              <button
                key={s.id}
                ref={(el) => {
                  tabRefs.current[s.id] = el
                }}
                id={id(`tab-${s.id}`)}
                type="button"
                role="tab"
                aria-selected={section === s.id}
                aria-controls={id('panel')}
                tabIndex={section === s.id ? 0 : -1}
                className={section === s.id ? 'active' : undefined}
                onClick={() => setSection(s.id)}
              >
                {s.label}
              </button>
            ))}
          </div>
          <div className="settings-panel" role="tabpanel" id={id('panel')} aria-labelledby={id(`tab-${section}`)}>
            <h3>{current.label}</h3>
            <p className="hint settings-description">{current.description}</p>
            {body}
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
