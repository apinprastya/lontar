import { create } from 'zustand'
import { connectionApi } from '../features/connections/api'
import type { Connection, DriverInfo, Secrets, SessionInfo } from '../features/connections/types'
import { errorMessage } from '../lib/errors'
import { hostKeyInfo, type HostKeyInfo } from '../lib/hostkey'

/** An SSH host key waiting for the user's decision (connect retries after Accept). */
export interface HostKeyPrompt {
  info: HostKeyInfo
  connectionId: string
  busy: boolean
}

export interface ConnectionsState {
  connections: Connection[]
  drivers: DriverInfo[]
  loading: boolean
  /** Last error from a list-level action (load, delete, connect, ...). */
  error: string | null
  /** The active session; null shows the welcome screen. */
  session: SessionInfo | null
  /** Every open session (the active one included), in the order opened. */
  sessions: SessionInfo[]
  /** Connection currently being connected to. */
  connectingId: string | null

  load(): Promise<void>
  /** Saves and returns the connection; throws so forms can show the error. */
  save(c: Connection, secrets: Secrets): Promise<Connection>
  remove(id: string): Promise<void>
  duplicate(id: string): Promise<Connection | null>
  /** Returns the server version; throws on failure. */
  test(c: Connection, secrets: Secrets): Promise<string>
  /**
   * Makes a connection active: its open session if there is one, else a
   * new session. Other open sessions stay connected in the background.
   */
  connect(id: string): Promise<boolean>
  /** Makes an open session the active one. */
  activate(sessionId: string): void
  /** Unknown SSH host key met by connect (shown as a dialog). */
  hostKey: HostKeyPrompt | null
  /** Trusts the prompted key (Lontar's own known_hosts) and connects again. */
  acceptHostKey(): Promise<boolean>
  dismissHostKey(): void
  /** Trusts a host key without connecting (the form's Test). */
  trustHostKey(info: HostKeyInfo): Promise<void>
  /**
   * Closes a session (default: the active one). Closing the active session
   * activates the most recently opened remaining one, if any.
   */
  disconnect(sessionId?: string): Promise<void>
  clearError(): void
}

export const initialConnectionsState = {
  connections: [] as Connection[],
  drivers: [] as DriverInfo[],
  loading: false,
  error: null as string | null,
  session: null as SessionInfo | null,
  sessions: [] as SessionInfo[],
  connectingId: null as string | null,
  hostKey: null as HostKeyPrompt | null,
}

function sortByName(list: Connection[]): Connection[] {
  return [...list].sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }))
}

export const useConnections = create<ConnectionsState>()((set, get) => ({
  ...initialConnectionsState,

  async load() {
    set({ loading: true, error: null })
    try {
      const [drivers, connections] = await Promise.all([connectionApi.drivers(), connectionApi.list()])
      set({ drivers, connections: sortByName(connections), loading: false })
    } catch (err) {
      set({ loading: false, error: errorMessage(err) })
    }
  },

  async save(c, secrets) {
    const saved = await connectionApi.save(c, secrets)
    const others = get().connections.filter((x) => x.id !== saved.id)
    set({ connections: sortByName([...others, saved]) })
    return saved
  },

  async remove(id) {
    try {
      await connectionApi.remove(id)
      set({ connections: get().connections.filter((c) => c.id !== id), error: null })
    } catch (err) {
      set({ error: errorMessage(err) })
      // The backend may have removed the entry despite the error (e.g. the
      // keychain entry could not be deleted); resync.
      await get().load()
      set({ error: errorMessage(err) })
    }
  },

  async duplicate(id) {
    try {
      const copy = await connectionApi.duplicate(id)
      set({ connections: sortByName([...get().connections, copy]), error: null })
      return copy
    } catch (err) {
      set({ error: errorMessage(err) })
      return null
    }
  },

  test(c, secrets) {
    return connectionApi.test(c, secrets)
  },

  async connect(id) {
    // Already open: just bring it to the front.
    const open = get().sessions.find((x) => x.connectionId === id)
    if (open) {
      set({ session: open, error: null })
      return true
    }
    set({ connectingId: id, error: null })
    try {
      const session = await connectionApi.connect(id)
      set((s) => ({ session, sessions: [...s.sessions.filter((x) => x.id !== session.id), session], connectingId: null, hostKey: null }))
      return true
    } catch (err) {
      const info = hostKeyInfo(err)
      if (info?.kind === 'hostKeyUnknown') {
        set({ connectingId: null, hostKey: { info, connectionId: id, busy: false } })
      } else {
        set({ connectingId: null, hostKey: null, error: errorMessage(err) })
      }
      return false
    }
  },

  activate(sessionId) {
    const s = get().sessions.find((x) => x.id === sessionId)
    if (s) set({ session: s })
  },

  async acceptHostKey() {
    const prompt = get().hostKey
    if (!prompt) return false
    set({ hostKey: { ...prompt, busy: true } })
    try {
      await connectionApi.acceptHostKey(prompt.info.host, prompt.info.fingerprintSHA256)
    } catch (err) {
      set({ hostKey: null, error: errorMessage(err) })
      return false
    }
    set({ hostKey: null })
    return get().connect(prompt.connectionId)
  },

  dismissHostKey() {
    set({ hostKey: null })
  },

  async trustHostKey(info) {
    await connectionApi.acceptHostKey(info.host, info.fingerprintSHA256)
  },

  async disconnect(sessionId) {
    const id = sessionId ?? get().session?.id
    if (!id) return
    set((s) => {
      const sessions = s.sessions.filter((x) => x.id !== id)
      const session = s.session?.id === id ? (sessions[sessions.length - 1] ?? null) : s.session
      return { sessions, session }
    })
    try {
      await connectionApi.disconnect(id)
    } catch (err) {
      set({ error: errorMessage(err) })
    }
  },

  clearError() {
    set({ error: null })
  },
}))
