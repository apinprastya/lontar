import { FormEvent, KeyboardEvent, useEffect, useId, useRef, useState } from 'react'
import { errorMessage } from '../../lib/errors'
import { matchesShortcut, shortcutLabel } from '../../lib/mod'
import {
  Connection,
  DriverInfo,
  EXTRA_COLORS,
  KIND_LABELS,
  Kind,
  TAGS,
  Tag,
  connectionColor,
  isFileBased,
} from './types'

export interface ConnectionFormProps {
  /** Connection to edit; undefined creates a new one. */
  initial?: Connection
  drivers: DriverInfo[]
  platform?: string
  onSave(c: Connection, password: string | null): Promise<Connection>
  onTest(c: Connection, password: string | null): Promise<string>
  /** Called after Save when the user chose Connect. */
  onConnect(c: Connection): Promise<unknown> | void
  onClose(): void
}

interface Draft {
  name: string
  kind: Kind
  host: string
  port: string
  user: string
  database: string
  filePath: string
  readOnly: boolean
  createIfMissing: boolean
  tag: Tag
  color: string
}

type Errors = Partial<Record<keyof Draft, string>>

type TestState = { status: 'idle' } | { status: 'running' } | { status: 'ok'; version: string } | { status: 'error'; message: string }

const FALLBACK_PORTS: Record<Kind, number> = { postgres: 5432, mysql: 3306, mariadb: 3306, sqlite: 0 }

function defaultPort(drivers: DriverInfo[], kind: Kind): number {
  return drivers.find((d) => d.kind === kind)?.defaultPort ?? FALLBACK_PORTS[kind]
}

function toDraft(c: Connection | undefined, drivers: DriverInfo[]): Draft {
  const kind = c?.kind ?? 'postgres'
  const port = c?.port || defaultPort(drivers, kind)
  return {
    name: c?.name ?? '',
    kind,
    host: c?.host ?? (c ? '' : 'localhost'),
    port: isFileBased(kind) ? '' : String(port),
    user: c?.user ?? '',
    database: c?.database ?? '',
    filePath: c?.filePath ?? '',
    readOnly: c?.readOnly ?? false,
    createIfMissing: c?.createIfMissing ?? false,
    tag: c?.tag ?? 'none',
    color: c?.color ?? '',
  }
}

/** Validates a draft; returns field errors (empty when valid). */
export function validateDraft(d: Draft): Errors {
  const errors: Errors = {}
  if (!d.name.trim()) errors.name = 'Name is required'
  if (isFileBased(d.kind)) {
    if (!d.filePath.trim()) errors.filePath = 'Database file is required'
  } else {
    if (!d.host.trim()) errors.host = 'Host is required'
    const port = Number(d.port)
    if (!/^\d+$/.test(d.port.trim()) || port < 1 || port > 65535) errors.port = 'Port must be between 1 and 65535'
  }
  return errors
}

/** JSON form of Go's zero time.Time; "" fails to unmarshal. */
export const GO_ZERO_TIME = '0001-01-01T00:00:00Z'

function toConnection(d: Draft, initial: Connection | undefined): Connection {
  const file = isFileBased(d.kind)
  return {
    // Keep fields the form does not edit (options, ssh, ssl, timestamps).
    // New connections use Go's zero time: the backend decodes these into
    // time.Time, which rejects "" (the Store sets the real values on save).
    ...(initial ?? { id: '', hasPassword: false, createdAt: GO_ZERO_TIME, updatedAt: GO_ZERO_TIME }),
    name: d.name.trim(),
    kind: d.kind,
    host: file ? '' : d.host.trim(),
    port: file ? 0 : Number(d.port),
    user: file ? '' : d.user.trim(),
    database: file ? '' : d.database.trim(),
    filePath: file ? d.filePath.trim() : '',
    readOnly: file && d.readOnly,
    createIfMissing: file && d.createIfMissing,
    tag: d.tag,
    color: d.color,
  }
}

export default function ConnectionForm({ initial, drivers, platform, onSave, onTest, onConnect, onClose }: ConnectionFormProps) {
  const uid = useId()
  const [draft, setDraft] = useState<Draft>(() => toDraft(initial, drivers))
  const [password, setPassword] = useState('')
  const [passwordTouched, setPasswordTouched] = useState(false)
  const [errors, setErrors] = useState<Errors>({})
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [test, setTest] = useState<TestState>({ status: 'idle' })
  // The connection once saved (so a second Save updates instead of creating).
  const savedRef = useRef<Connection | undefined>(initial)
  const nameRef = useRef<HTMLInputElement>(null)
  const kinds: Kind[] = drivers.length ? drivers.map((d) => d.kind) : (Object.keys(KIND_LABELS) as Kind[])

  useEffect(() => {
    nameRef.current?.focus()
  }, [])

  const hasSavedPassword = Boolean(savedRef.current?.hasPassword)
  const file = isFileBased(draft.kind)
  const editing = Boolean(initial)

  function update<K extends keyof Draft>(key: K, value: Draft[K]) {
    setDraft((d) => ({ ...d, [key]: value }))
    setErrors((e) => ({ ...e, [key]: undefined }))
    setTest({ status: 'idle' })
  }

  function changeKind(kind: Kind) {
    setDraft((d) => ({ ...d, kind, port: isFileBased(kind) ? '' : String(defaultPort(drivers, kind)) }))
    setErrors({})
    setTest({ status: 'idle' })
  }

  /** null = keep the stored password; '' = clear it. */
  function passwordArg(): string | null {
    return passwordTouched ? password : null
  }

  function check(): Connection | null {
    const errs = validateDraft(draft)
    setErrors(errs)
    if (Object.keys(errs).length) return null
    return toConnection(draft, savedRef.current)
  }

  async function runTest() {
    const c = check()
    if (!c) return
    setTest({ status: 'running' })
    try {
      setTest({ status: 'ok', version: await onTest(c, passwordArg()) })
    } catch (err) {
      setTest({ status: 'error', message: errorMessage(err) })
    }
  }

  async function save(): Promise<Connection | null> {
    const c = check()
    if (!c) return null
    setBusy(true)
    setSubmitError(null)
    try {
      const saved = await onSave(c, passwordArg())
      savedRef.current = saved
      setPassword('')
      setPasswordTouched(false)
      return saved
    } catch (err) {
      setSubmitError(errorMessage(err))
      return null
    } finally {
      setBusy(false)
    }
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (await save()) onClose()
  }

  async function handleConnect() {
    const saved = await save()
    if (saved) {
      onClose()
      await onConnect(saved)
    }
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault()
      onClose()
    } else if (matchesShortcut(e, platform, 'Enter')) {
      e.preventDefault()
      void handleConnect()
    }
  }

  const field = (name: keyof Draft) => `${uid}-${name}`
  const err = (name: keyof Draft) =>
    errors[name] ? (
      <span className="field-error" id={`${field(name)}-error`} role="alert">
        {errors[name]}
      </span>
    ) : null
  const aria = (name: keyof Draft) => ({
    id: field(name),
    'aria-invalid': errors[name] ? true : undefined,
    'aria-describedby': errors[name] ? `${field(name)}-error` : undefined,
  })
  const accent = connectionColor(draft)

  return (
    <div className="dialog-backdrop">
      <form
        className="dialog connection-form"
        role="dialog"
        aria-modal="true"
        aria-labelledby={`${uid}-title`}
        onSubmit={handleSubmit}
        onKeyDown={handleKeyDown}
        noValidate
        style={accent ? { borderTopColor: accent } : undefined}
      >
        <h2 id={`${uid}-title`}>{editing ? 'Edit connection' : 'New connection'}</h2>

        <div className="form-grid">
          <label htmlFor={field('kind')}>Type</label>
          <select id={field('kind')} value={draft.kind} onChange={(e) => changeKind(e.target.value as Kind)}>
            {kinds.map((k) => (
              <option key={k} value={k}>
                {drivers.find((d) => d.kind === k)?.displayName ?? KIND_LABELS[k]}
              </option>
            ))}
          </select>

          <label htmlFor={field('name')}>Name</label>
          <div className="field">
            <input ref={nameRef} {...aria('name')} value={draft.name} onChange={(e) => update('name', e.target.value)} />
            {err('name')}
          </div>

          {file ? (
            <>
              <label htmlFor={field('filePath')}>Database file</label>
              <div className="field">
                <input
                  {...aria('filePath')}
                  className="mono"
                  placeholder="/path/to/database.sqlite"
                  value={draft.filePath}
                  onChange={(e) => update('filePath', e.target.value)}
                />
                {err('filePath')}
              </div>

              <span />
              <div className="checks">
                <label>
                  <input type="checkbox" checked={draft.readOnly} onChange={(e) => update('readOnly', e.target.checked)} /> Read-only
                </label>
                <label>
                  <input
                    type="checkbox"
                    checked={draft.createIfMissing}
                    disabled={draft.readOnly}
                    onChange={(e) => update('createIfMissing', e.target.checked)}
                  />{' '}
                  Create file if missing
                </label>
              </div>
            </>
          ) : (
            <>
              <label htmlFor={field('host')}>Host</label>
              <div className="field host-port">
                <div className="field">
                  <input {...aria('host')} value={draft.host} onChange={(e) => update('host', e.target.value)} />
                  {err('host')}
                </div>
                <label htmlFor={field('port')} className="inline-label">
                  Port
                </label>
                <div className="field port">
                  <input
                    {...aria('port')}
                    inputMode="numeric"
                    value={draft.port}
                    onChange={(e) => update('port', e.target.value)}
                  />
                  {err('port')}
                </div>
              </div>

              <label htmlFor={field('user')}>User</label>
              <input id={field('user')} autoComplete="off" value={draft.user} onChange={(e) => update('user', e.target.value)} />

              <label htmlFor={`${uid}-password`}>Password</label>
              <div className="field password-field">
                <input
                  id={`${uid}-password`}
                  type="password"
                  autoComplete="new-password"
                  placeholder={hasSavedPassword && !passwordTouched ? '•••••••• saved' : ''}
                  value={password}
                  onChange={(e) => {
                    setPassword(e.target.value)
                    setPasswordTouched(true)
                    setTest({ status: 'idle' })
                  }}
                />
                {hasSavedPassword && !(passwordTouched && password === '') && (
                  <button
                    type="button"
                    className="link-button"
                    onClick={() => {
                      setPassword('')
                      setPasswordTouched(true)
                    }}
                  >
                    Clear saved
                  </button>
                )}
                {hasSavedPassword && passwordTouched && password === '' && (
                  <span className="hint">Saved password will be removed</span>
                )}
              </div>

              <label htmlFor={field('database')}>Database</label>
              <input id={field('database')} value={draft.database} onChange={(e) => update('database', e.target.value)} />
            </>
          )}

          <label htmlFor={field('tag')}>Tag</label>
          <select id={field('tag')} value={draft.tag} onChange={(e) => update('tag', e.target.value as Tag)}>
            {TAGS.map((t) => (
              <option key={t.value} value={t.value}>
                {t.label}
              </option>
            ))}
          </select>

          <span id={`${uid}-color-label`}>Colour</span>
          <div className="swatches" role="radiogroup" aria-labelledby={`${uid}-color-label`}>
            <button
              type="button"
              role="radio"
              aria-checked={draft.color === ''}
              aria-label="Tag colour"
              className="swatch swatch-auto"
              onClick={() => update('color', '')}
            />
            {[...TAGS.filter((t) => t.color).map((t) => t.color), ...EXTRA_COLORS].map((c) => (
              <button
                key={c}
                type="button"
                role="radio"
                aria-checked={draft.color === c}
                aria-label={c}
                className="swatch"
                style={{ background: c }}
                onClick={() => update('color', c)}
              />
            ))}
          </div>
        </div>

        <div className="test-result" aria-live="polite">
          {test.status === 'running' && <span className="muted">Testing…</span>}
          {test.status === 'ok' && <span className="ok">Connection OK · {test.version}</span>}
          {test.status === 'error' && <span className="error">{test.message}</span>}
        </div>
        {submitError && (
          <p className="error" role="alert">
            {submitError}
          </p>
        )}

        <div className="dialog-actions">
          <button type="button" onClick={runTest} disabled={busy || test.status === 'running'}>
            Test
          </button>
          <span className="spacer" />
          <button type="button" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" disabled={busy}>
            Save
          </button>
          <button
            type="button"
            className="primary"
            onClick={handleConnect}
            disabled={busy}
            title={shortcutLabel(platform, 'Enter')}
          >
            Connect
          </button>
        </div>
      </form>
    </div>
  )
}
