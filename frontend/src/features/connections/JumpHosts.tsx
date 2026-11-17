import { useId } from 'react'
import { MAX_JUMPS, type HopSecrets, type SSHAuthMethod, type SSHHop } from './types'

/** A secret input: what was typed and whether it was touched. */
export interface SecretState {
  value: string
  touched: boolean
}

export const untouchedSecret: SecretState = { value: '', touched: false }

/** One jump host as edited in the form. */
export interface HopDraft {
  /** Local React key (stable across reorders). */
  key: string
  /** Backend hop id ('' until saved); it carries the hop's saved secrets. */
  id: string
  host: string
  port: string
  user: string
  auth: SSHAuthMethod
  keyPath: string
  hasPassword: boolean
  hasPassphrase: boolean
  password: SecretState
  passphrase: SecretState
}

export type HopField = 'host' | 'port' | 'user' | 'keyPath'
export type HopErrors = Partial<Record<HopField, string>>

let nextKey = 0
const newKey = () => `hop-${++nextKey}`

export function emptyHop(): HopDraft {
  return {
    key: newKey(),
    id: '',
    host: '',
    port: '22',
    user: '',
    auth: 'password',
    keyPath: '',
    hasPassword: false,
    hasPassphrase: false,
    password: untouchedSecret,
    passphrase: untouchedSecret,
  }
}

export function toHopDrafts(hops: SSHHop[] | null | undefined): HopDraft[] {
  return (hops ?? []).map((h) => ({
    ...emptyHop(),
    id: h.id ?? '',
    host: h.host ?? '',
    port: String(h.port || 22),
    user: h.user ?? '',
    auth: h.authMethod ?? 'password',
    keyPath: h.keyPath ?? '',
    hasPassword: Boolean(h.hasPassword),
    hasPassphrase: Boolean(h.hasPassphrase),
  }))
}

const validPort = (p: string) => /^\d+$/.test(p.trim()) && Number(p) >= 1 && Number(p) <= 65535

export function toHops(drafts: HopDraft[]): SSHHop[] {
  return drafts.map((h) => ({
    id: h.id,
    host: h.host.trim(),
    port: validPort(h.port) ? Number(h.port) : 22,
    user: h.user.trim(),
    authMethod: h.auth,
    keyPath: h.keyPath.trim(),
  }))
}

/** Secrets by position, matching toHops (null = keep the saved one). */
export function hopSecrets(drafts: HopDraft[]): HopSecrets[] {
  return drafts.map((h) => ({
    password: h.password.touched ? h.password.value : null,
    passphrase: h.passphrase.touched ? h.passphrase.value : null,
  }))
}

/**
 * Takes the ids and saved-secret flags from the saved hops (same order) and
 * forgets what was typed, so a second Save keeps the hops' secrets.
 */
export function afterSave(drafts: HopDraft[], saved: SSHHop[] | null | undefined): HopDraft[] {
  return drafts.map((h, i) => {
    const s = saved?.[i]
    return {
      ...h,
      id: s?.id ?? h.id,
      hasPassword: Boolean(s?.hasPassword),
      hasPassphrase: Boolean(s?.hasPassphrase),
      password: untouchedSecret,
      passphrase: untouchedSecret,
    }
  })
}

/** Field errors per hop (by index); empty when all are valid. */
export function validateHops(drafts: HopDraft[]): Record<number, HopErrors> {
  const out: Record<number, HopErrors> = {}
  drafts.forEach((h, i) => {
    const e: HopErrors = {}
    if (!h.host.trim()) e.host = 'Host is required'
    if (!validPort(h.port)) e.port = 'Port must be between 1 and 65535'
    if (!h.user.trim()) e.user = 'User is required'
    if (h.auth === 'key' && !h.keyPath.trim()) e.keyPath = 'Private key file is required'
    if (Object.keys(e).length) out[i] = e
  })
  return out
}

export interface JumpHostsProps {
  hops: HopDraft[]
  errors: Record<number, HopErrors>
  disabled: boolean
  onChange(hops: HopDraft[]): void
  /** Opens a file picker for hop i's private key. */
  onBrowseKey(index: number): void
}

/**
 * The jump host list of the SSH tab: hop 1 is dialled first from this
 * machine, the SSH host is reached through the last one.
 */
export default function JumpHosts({ hops, errors, disabled, onChange, onBrowseKey }: JumpHostsProps) {
  const uid = useId()
  const set = (i: number, patch: Partial<HopDraft>) => onChange(hops.map((h, j) => (j === i ? { ...h, ...patch } : h)))
  const move = (i: number, by: -1 | 1) => {
    const next = [...hops]
    const [h] = next.splice(i, 1)
    next.splice(i + by, 0, h)
    onChange(next)
  }

  return (
    <div className="jump-hosts">
      {hops.map((h, i) => {
        const n = i + 1
        const name = `Jump host ${n}`
        const id = (f: string) => `${uid}-${h.key}-${f}`
        const e = errors[i] ?? {}
        const aria = (f: HopField) => ({
          id: id(f),
          'aria-invalid': e[f] ? true : undefined,
          'aria-describedby': e[f] ? `${id(f)}-error` : undefined,
        })
        const err = (f: HopField) =>
          e[f] ? (
            <span className="field-error" id={`${id(f)}-error`} role="alert">
              {e[f]}
            </span>
          ) : null
        const secret = (what: 'password' | 'passphrase', label: string, saved: boolean) => {
          const state = h[what]
          const setSecret = (v: SecretState) => set(i, what === 'password' ? { password: v } : { passphrase: v })
          const full = `${name} ${label.toLowerCase()}`
          return (
            <>
              <label htmlFor={id(what)}>{label}</label>
              <div className="field password-field">
                <input
                  id={id(what)}
                  aria-label={full}
                  type="password"
                  autoComplete="new-password"
                  disabled={disabled}
                  placeholder={saved && !state.touched ? '•••••••• saved' : ''}
                  value={state.value}
                  onChange={(ev) => setSecret({ value: ev.target.value, touched: true })}
                />
                {saved && !(state.touched && state.value === '') && (
                  <button type="button" className="link-button" aria-label={`Clear saved ${full.toLowerCase()}`} onClick={() => setSecret({ value: '', touched: true })}>
                    Clear saved
                  </button>
                )}
                {saved && state.touched && state.value === '' && <span className="hint">Saved {label.toLowerCase()} will be removed</span>}
              </div>
            </>
          )
        }
        return (
          <fieldset key={h.key} className="jump-hop" aria-label={name}>
            <legend>
              {name}
              {i === 0 && <span className="hint"> · dialled first from this machine</span>}
            </legend>
            <div className="jump-hop-actions">
              <button type="button" aria-label={`Move ${name.toLowerCase()} up`} title="Move up" disabled={disabled || i === 0} onClick={() => move(i, -1)}>
                ↑
              </button>
              <button
                type="button"
                aria-label={`Move ${name.toLowerCase()} down`}
                title="Move down"
                disabled={disabled || i === hops.length - 1}
                onClick={() => move(i, 1)}
              >
                ↓
              </button>
              <button type="button" aria-label={`Remove ${name.toLowerCase()}`} disabled={disabled} onClick={() => onChange(hops.filter((_, j) => j !== i))}>
                Remove
              </button>
            </div>
            <div className="form-grid jump-hop-grid">
              <label htmlFor={id('host')}>Host</label>
              <div className="field host-port">
                <div className="field">
                  <input {...aria('host')} aria-label={`${name} host`} placeholder="bastion.example.com" value={h.host} disabled={disabled} onChange={(ev) => set(i, { host: ev.target.value })} />
                  {err('host')}
                </div>
                <label htmlFor={id('port')} className="inline-label">
                  Port
                </label>
                <div className="field port">
                  <input {...aria('port')} aria-label={`${name} port`} inputMode="numeric" value={h.port} disabled={disabled} onChange={(ev) => set(i, { port: ev.target.value })} />
                  {err('port')}
                </div>
              </div>

              <label htmlFor={id('user')}>User</label>
              <div className="field">
                <input {...aria('user')} aria-label={`${name} user`} autoComplete="off" value={h.user} disabled={disabled} onChange={(ev) => set(i, { user: ev.target.value })} />
                {err('user')}
              </div>

              <label htmlFor={id('auth')}>Authentication</label>
              <select
                id={id('auth')}
                aria-label={`${name} authentication`}
                value={h.auth}
                disabled={disabled}
                onChange={(ev) => set(i, { auth: ev.target.value as SSHAuthMethod })}
              >
                <option value="password">Password</option>
                <option value="key">Private key</option>
                <option value="agent">SSH agent</option>
              </select>

              {h.auth === 'password' && secret('password', 'Password', h.hasPassword)}
              {h.auth === 'key' && (
                <>
                  <label htmlFor={id('keyPath')}>Private key</label>
                  <div className="field">
                    <div className="path-field">
                      <input
                        {...aria('keyPath')}
                        aria-label={`${name} private key`}
                        className="mono"
                        placeholder="~/.ssh/id_ed25519"
                        value={h.keyPath}
                        disabled={disabled}
                        onChange={(ev) => set(i, { keyPath: ev.target.value })}
                      />
                      <button type="button" disabled={disabled} onClick={() => onBrowseKey(i)} aria-label={`Browse for ${name.toLowerCase()} private key`}>
                        Browse…
                      </button>
                    </div>
                    {err('keyPath')}
                  </div>
                  {secret('passphrase', 'Passphrase', h.hasPassphrase)}
                </>
              )}
            </div>
          </fieldset>
        )
      })}
      <div className="jump-hosts-footer">
        <button type="button" disabled={disabled || hops.length >= MAX_JUMPS} onClick={() => onChange([...hops, emptyHop()])}>
          Add jump host
        </button>
        <span className="hint">
          {hops.length >= MAX_JUMPS
            ? `At most ${MAX_JUMPS} jump hosts.`
            : 'Jump hosts are dialled in order before the SSH host above; host keys are checked on every hop.'}
        </span>
      </div>
    </div>
  )
}
