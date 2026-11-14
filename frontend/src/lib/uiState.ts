/**
 * Persistent UI state: panel layouts, table column widths and panel toggles
 * that should survive a restart. The values live in the backend's
 * state.json (UIStateService), not in the webview's localStorage, which
 * some platforms reset.
 *
 * Reads are synchronous from an in-memory cache filled once by
 * `initUIState` before the app renders. Writes update the cache at once and
 * reach the backend debounced per key (column resizing fires on every
 * pointer move). Before `initUIState` runs (unit tests) writes stay in
 * memory.
 */

/** The backend the values are stored in. */
export interface UIStateBackend {
  all(): Promise<Record<string, string | undefined> | null>
  set(key: string, value: string): Promise<void>
}

/** Delay before a changed value is written to the backend. */
export const SAVE_DELAY = 400

const cache = new Map<string, string>()
const timers = new Map<string, ReturnType<typeof setTimeout>>()
let backend: UIStateBackend | null = null

/**
 * Loads every stored value and connects the backend for later writes.
 * Failures leave the cache empty (defaults are used); never throws.
 */
export async function initUIState(b: UIStateBackend): Promise<void> {
  try {
    const all = (await b.all()) ?? {}
    for (const [k, v] of Object.entries(all)) {
      if (typeof v === 'string' && !cache.has(k)) cache.set(k, v)
    }
  } catch (err) {
    console.warn('load UI state:', err)
  }
  backend = b
}

/** The stored value of key, or null. */
export function getUIState(key: string): string | null {
  return cache.get(key) ?? null
}

/** Stores value under key (null or '' removes it). */
export function setUIState(key: string, value: string | null): void {
  const v = value ?? ''
  if ((cache.get(key) ?? '') === v) return
  if (v === '') cache.delete(key)
  else cache.set(key, v)
  if (!backend) return
  clearTimeout(timers.get(key))
  timers.set(
    key,
    setTimeout(() => write(key), SAVE_DELAY),
  )
}

function write(key: string): Promise<void> {
  timers.delete(key)
  if (!backend) return Promise.resolve()
  return backend.set(key, cache.get(key) ?? '').catch((err: unknown) => console.warn('save UI state:', err))
}

/** Writes every pending value now (on quit). */
export function flushUIState(): Promise<void> {
  const keys = [...timers.keys()]
  for (const k of keys) clearTimeout(timers.get(k))
  return Promise.all(keys.map(write)).then(() => undefined)
}

/** The JSON value of key, or fallback when it is missing, invalid or rejected by `valid`. */
export function getUIJSON<T>(key: string, fallback: T, valid: (v: unknown) => v is T): T {
  const raw = getUIState(key)
  if (raw === null) return fallback
  try {
    const v: unknown = JSON.parse(raw)
    return valid(v) ? v : fallback
  } catch {
    return fallback
  }
}

/** Stores v as JSON under key (undefined removes it). */
export function setUIJSON(key: string, v: unknown): void {
  setUIState(key, v === undefined ? null : JSON.stringify(v))
}

export function isBoolean(v: unknown): v is boolean {
  return typeof v === 'boolean'
}

/** A map of column name to a positive width in pixels. */
export function isWidthMap(v: unknown): v is Record<string, number> {
  return (
    typeof v === 'object' &&
    v !== null &&
    !Array.isArray(v) &&
    Object.values(v).every((w) => typeof w === 'number' && Number.isFinite(w) && w > 0)
  )
}

/** Clears the cache and disconnects the backend (tests only). */
export function resetUIState(): void {
  for (const t of timers.values()) clearTimeout(t)
  timers.clear()
  cache.clear()
  backend = null
}
