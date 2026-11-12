import { FormEvent, KeyboardEvent, useEffect, useId, useRef, useState } from 'react'
import { errorMessage } from '../../lib/errors'
import { hostKeyInfo, type HostKeyInfo } from '../../lib/hostkey'
import { SAFE_MODES, type SafeMode } from '../edits/types'
import { matchesShortcut, shortcutLabel } from '../../lib/mod'
import HostKeyDialog from './HostKeyDialog'
import { dialogApi, type FileFilter } from './dialogApi'
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
  sslModes,
  type SSHAuthMethod,
  type SSLMode,
  type Secrets,
} from './types'

export interface ConnectionFormProps {
  /** Connection to edit; undefined creates a new one. */
  initial?: Connection
  drivers: DriverInfo[]
  platform?: string
  onSave(c: Connection, secrets: Secrets): Promise<Connection>
  onTest(c: Connection, secrets: Secrets): Promise<string>
  /** Called after Save when the user chose Connect. */
  onConnect(c: Connection): Promise<unknown> | void
  onClose(): void
  /** Trusts an unknown SSH host key (Test asks first). */
  onTrustHostKey?(info: HostKeyInfo): Promise<void>
  /** Native file picker; resolves "" when cancelled. */
  pickFile?(title: string, filters: FileFilter[]): Promise<string>
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
  safeMode: SafeMode
  sshEnabled: boolean
  sshHost: string
  sshPort: string
  sshUser: string
  sshAuth: SSHAuthMethod
  sshKeyPath: string
  sslMode: SSLMode
  sslCA: string
  sslCert: string
  sslKey: string
}

type Errors = Partial<Record<keyof Draft, string>>

type FormTab = 'general' | 'ssh' | 'ssl'

/** A secret field: what was typed and whether it was touched. */
interface SecretState {
  value: string
  touched: boolean
}

const untouched: SecretState = { value: '', touched: false }

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
    safeMode: c?.safeMode ?? 'off',
    sshEnabled: c?.ssh?.enabled ?? false,
    sshHost: c?.ssh?.host ?? '',
    sshPort: String(c?.ssh?.port || 22),
    sshUser: c?.ssh?.user ?? '',
    sshAuth: c?.ssh?.authMethod ?? 'password',
    sshKeyPath: c?.ssh?.keyPath ?? '',
    sslMode: c?.ssl?.mode ?? '',
    sslCA: c?.ssl?.caFile ?? '',
    sslCert: c?.ssl?.certFile ?? '',
    sslKey: c?.ssl?.keyFile ?? '',
  }
}

const validPort = (p: string) => /^\d+$/.test(p.trim()) && Number(p) >= 1 && Number(p) <= 65535

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
    if (d.sshEnabled) {
      if (!d.sshHost.trim()) errors.sshHost = 'SSH host is required'
      if (!validPort(d.sshPort)) errors.sshPort = 'Port must be between 1 and 65535'
      if (!d.sshUser.trim()) errors.sshUser = 'SSH user is required'
      if (d.sshAuth === 'key' && !d.sshKeyPath.trim()) errors.sshKeyPath = 'Private key file is required'
    }
    if (Boolean(d.sslCert.trim()) !== Boolean(d.sslKey.trim())) errors.sslKey = 'Give both the client certificate and its key'
  }
  return errors
}

/** The tab that holds a field (to show the first error). */
function tabOf(field: keyof Draft): FormTab {
  if (field.startsWith('ssh')) return 'ssh'
  if (field.startsWith('ssl')) return 'ssl'
  return 'general'
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
    safeMode: d.safeMode,
    ssh: file
      ? null
      : {
          enabled: d.sshEnabled,
          host: d.sshHost.trim(),
          port: validPort(d.sshPort) ? Number(d.sshPort) : 22,
          user: d.sshUser.trim(),
          authMethod: d.sshAuth,
          keyPath: d.sshKeyPath.trim(),
        },
    ssl: file ? null : { mode: d.sslMode, caFile: d.sslCA.trim(), certFile: d.sslCert.trim(), keyFile: d.sslKey.trim() },
  }
}

/**
 * Tag change: choosing Production suggests confirm safe mode (a UI default
 * only; the user can change it), unless a stricter mode is already set.
 */
export function safeModeForTag(tag: Tag, current: SafeMode): SafeMode {
  return tag === 'prod' && current === 'off' ? 'confirm' : current
}

const KEY_FILTERS: FileFilter[] = [{ displayName: 'All files', pattern: '*' }]
const CERT_FILTERS: FileFilter[] = [
  { displayName: 'Certificates and keys (*.pem, *.crt, *.key)', pattern: '*.pem;*.crt;*.cer;*.key' },
  { displayName: 'All files', pattern: '*' },
]

export default function ConnectionForm({
  initial,
  drivers,
  platform,
  onSave,
  onTest,
  onConnect,
  onClose,
  onTrustHostKey,
  pickFile = dialogApi.openFile,
}: ConnectionFormProps) {
  const uid = useId()
  const [draft, setDraft] = useState<Draft>(() => toDraft(initial, drivers))
  const [password, setPassword] = useState('')
  const [passwordTouched, setPasswordTouched] = useState(false)
  const [errors, setErrors] = useState<Errors>({})
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [test, setTest] = useState<TestState>({ status: 'idle' })
  const [tab, setTab] = useState<FormTab>('general')
  const [sshPassword, setSshPassword] = useState<SecretState>(untouched)
  const [sshPassphrase, setSshPassphrase] = useState<SecretState>(untouched)
  const [hostKey, setHostKey] = useState<{ info: HostKeyInfo; busy: boolean } | null>(null)
  const [pickError, setPickError] = useState<string | null>(null)
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
    setDraft((d) => {
      const modes = sslModes(kind).map((m) => m.value)
      return { ...d, kind, port: isFileBased(kind) ? '' : String(defaultPort(drivers, kind)), sslMode: modes.includes(d.sslMode) ? d.sslMode : '' }
    })
    setErrors({})
    setTest({ status: 'idle' })
    if (isFileBased(kind)) setTab('general')
  }

  /** null = keep the stored secret; '' = clear it. */
  function secrets(): Secrets {
    return {
      password: passwordTouched ? password : null,
      sshPassword: sshPassword.touched ? sshPassword.value : null,
      sshPassphrase: sshPassphrase.touched ? sshPassphrase.value : null,
    }
  }

  function check(): Connection | null {
    const errs = validateDraft(draft)
    setErrors(errs)
    const first = Object.keys(errs)[0] as keyof Draft | undefined
    if (first) {
      setTab(tabOf(first))
      return null
    }
    return toConnection(draft, savedRef.current)
  }

  async function runTest() {
    const c = check()
    if (!c) return
    setTest({ status: 'running' })
    try {
      setTest({ status: 'ok', version: await onTest(c, secrets()) })
    } catch (err) {
      const info = hostKeyInfo(err)
      if (info) {
        setTest({ status: 'error', message: info.message })
        setHostKey({ info, busy: false })
        return
      }
      setTest({ status: 'error', message: errorMessage(err) })
    }
  }

  async function trustHostKey() {
    if (!hostKey || !onTrustHostKey) return
    setHostKey({ ...hostKey, busy: true })
    try {
      await onTrustHostKey(hostKey.info)
    } catch (err) {
      setHostKey(null)
      setTest({ status: 'error', message: errorMessage(err) })
      return
    }
    setHostKey(null)
    await runTest()
  }

  async function browse(key: 'sshKeyPath' | 'sslCA' | 'sslCert' | 'sslKey', title: string, filters: FileFilter[]) {
    setPickError(null)
    try {
      const path = await pickFile(title, filters)
      if (path) update(key, path)
    } catch (err) {
      setPickError(errorMessage(err))
    }
  }

  async function save(): Promise<Connection | null> {
    const c = check()
    if (!c) return null
    setBusy(true)
    setSubmitError(null)
    try {
      const saved = await onSave(c, secrets())
      savedRef.current = saved
      setPassword('')
      setPasswordTouched(false)
      setSshPassword(untouched)
      setSshPassphrase(untouched)
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
  const tabErr = (t: FormTab) => (Object.keys(errors) as (keyof Draft)[]).some((k) => errors[k] && tabOf(k) === t)
  const secretField = (
    id: string,
    label: string,
    state: SecretState,
    setState: (s: SecretState) => void,
    saved: boolean,
  ) => (
    <>
      <label htmlFor={`${uid}-${id}`}>{label}</label>
      <div className="field password-field">
        <input
          id={`${uid}-${id}`}
          type="password"
          autoComplete="new-password"
          placeholder={saved && !state.touched ? '•••••••• saved' : ''}
          value={state.value}
          onChange={(e) => {
            setState({ value: e.target.value, touched: true })
            setTest({ status: 'idle' })
          }}
        />
        {saved && !(state.touched && state.value === '') && (
          <button type="button" className="link-button" aria-label={`Clear saved ${label.toLowerCase()}`} onClick={() => setState({ value: '', touched: true })}>
            Clear saved
          </button>
        )}
        {saved && state.touched && state.value === '' && <span className="hint">Saved {label.toLowerCase()} will be removed</span>}
      </div>
    </>
  )
  const pathField = (key: 'sshKeyPath' | 'sslCA' | 'sslCert' | 'sslKey', label: string, title: string, filters: FileFilter[], placeholder: string) => (
    <>
      <label htmlFor={field(key)}>{label}</label>
      <div className="field">
        <div className="path-field">
          <input {...aria(key)} className="mono" placeholder={placeholder} value={draft[key]} onChange={(e) => update(key, e.target.value)} />
          <button type="button" onClick={() => void browse(key, title, filters)} aria-label={`Browse for ${label.toLowerCase()}`}>
            Browse…
          </button>
        </div>
        {err(key)}
      </div>
    </>
  )

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

        {!file && (
          <div className="form-tabs" role="tablist" aria-label="Connection settings">
            {(
              [
                ['general', 'General'],
                ['ssh', 'SSH'],
                ['ssl', 'SSL'],
              ] as [FormTab, string][]
            ).map(([t, label]) => (
              <button
                key={t}
                type="button"
                role="tab"
                aria-selected={tab === t}
                className={`${tab === t ? 'active' : ''}${tabErr(t) ? ' has-error' : ''}`}
                onClick={() => setTab(t)}
              >
                {label}
                {t === 'ssh' && draft.sshEnabled && <span className="tab-dot" aria-label="enabled" />}
                {t === 'ssl' && draft.sslMode && draft.sslMode !== 'disable' && <span className="tab-dot" aria-label="enabled" />}
              </button>
            ))}
          </div>
        )}

        {tab === 'ssh' && !file && (
          <div className="form-grid" role="tabpanel" aria-label="SSH">
            <span />
            <label className="checks">
              <input type="checkbox" checked={draft.sshEnabled} onChange={(e) => update('sshEnabled', e.target.checked)} /> Connect through an SSH
              tunnel
            </label>

            <label htmlFor={field('sshHost')}>SSH host</label>
            <div className="field host-port">
              <div className="field">
                <input {...aria('sshHost')} placeholder="bastion.example.com" value={draft.sshHost} disabled={!draft.sshEnabled} onChange={(e) => update('sshHost', e.target.value)} />
                {err('sshHost')}
              </div>
              <label htmlFor={field('sshPort')} className="inline-label">
                SSH port
              </label>
              <div className="field port">
                <input {...aria('sshPort')} inputMode="numeric" value={draft.sshPort} disabled={!draft.sshEnabled} onChange={(e) => update('sshPort', e.target.value)} />
                {err('sshPort')}
              </div>
            </div>

            <label htmlFor={field('sshUser')}>SSH user</label>
            <div className="field">
              <input {...aria('sshUser')} autoComplete="off" value={draft.sshUser} disabled={!draft.sshEnabled} onChange={(e) => update('sshUser', e.target.value)} />
              {err('sshUser')}
            </div>

            <label htmlFor={field('sshAuth')}>Authentication</label>
            <select id={field('sshAuth')} value={draft.sshAuth} disabled={!draft.sshEnabled} onChange={(e) => update('sshAuth', e.target.value as SSHAuthMethod)}>
              <option value="password">Password</option>
              <option value="key">Private key</option>
              <option value="agent">SSH agent</option>
            </select>

            {draft.sshAuth === 'password' && secretField('ssh-password', 'SSH password', sshPassword, setSshPassword, Boolean(savedRef.current?.hasSshPassword))}
            {draft.sshAuth === 'key' && (
              <>
                {pathField('sshKeyPath', 'Private key', 'Choose an SSH private key', KEY_FILTERS, '~/.ssh/id_ed25519')}
                {secretField('ssh-passphrase', 'Passphrase', sshPassphrase, setSshPassphrase, Boolean(savedRef.current?.hasSshPassphrase))}
              </>
            )}
            {draft.sshAuth === 'agent' && (
              <>
                <span />
                <span className="hint">Uses the running SSH agent ({platform === 'windows' ? 'OpenSSH agent service' : '$SSH_AUTH_SOCK'}).</span>
              </>
            )}
            <span />
            <span className="hint">
              The database host and port are reached from the SSH server. Unknown host keys are shown for you to confirm; jump hosts are not
              supported.
            </span>
          </div>
        )}

        {tab === 'ssl' && !file && (
          <div className="form-grid" role="tabpanel" aria-label="SSL">
            <label htmlFor={field('sslMode')}>SSL mode</label>
            <div className="field">
              <select id={field('sslMode')} value={draft.sslMode} onChange={(e) => update('sslMode', e.target.value as SSLMode)}>
                {sslModes(draft.kind).map((m) => (
                  <option key={m.value || 'default'} value={m.value}>
                    {m.label}
                  </option>
                ))}
              </select>
              <span className="hint">
                {draft.sslMode === ''
                  ? `Uses the ${draft.kind === 'postgres' ? 'sslmode' : 'tls'} driver option if set (otherwise the driver default).`
                  : 'Overrides any sslmode / tls driver option.'}
              </span>
            </div>
            {pathField('sslCA', 'CA certificate', 'Choose the CA certificate', CERT_FILTERS, '/path/to/ca.pem')}
            {pathField('sslCert', 'Client certificate', 'Choose the client certificate', CERT_FILTERS, 'optional')}
            {pathField('sslKey', 'Client key', 'Choose the client key', CERT_FILTERS, 'optional')}
          </div>
        )}
        {pickError && (
          <p className="hint" role="status">
            {pickError}
          </p>
        )}

        <div className="form-grid" hidden={tab !== 'general'}>
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
          <select
            id={field('tag')}
            value={draft.tag}
            onChange={(e) => {
              const tag = e.target.value as Tag
              update('tag', tag)
              update('safeMode', safeModeForTag(tag, draft.safeMode))
            }}
          >
            {TAGS.map((t) => (
              <option key={t.value} value={t.value}>
                {t.label}
              </option>
            ))}
          </select>

          <label htmlFor={field('safeMode')}>Safe mode</label>
          <div className="field">
            <select id={field('safeMode')} value={draft.safeMode} onChange={(e) => update('safeMode', e.target.value as SafeMode)}>
              {SAFE_MODES.map((m) => (
                <option key={m.value} value={m.value}>
                  {m.label}
                </option>
              ))}
            </select>
            <span className="hint">{SAFE_MODES.find((m) => m.value === draft.safeMode)?.hint}</span>
          </div>

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
      {hostKey && <HostKeyDialog info={hostKey.info} busy={hostKey.busy} onAccept={() => void trustHostKey()} onCancel={() => setHostKey(null)} />}
    </div>
  )
}
