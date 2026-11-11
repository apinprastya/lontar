import { create } from 'zustand'
import { connectionApi } from '../features/connections/api'
import type { Connection, DriverInfo, SessionInfo } from '../features/connections/types'
import { errorMessage } from '../lib/errors'

export interface ConnectionsState {
  connections: Connection[]
  drivers: DriverInfo[]
  loading: boolean
  /** Last error from a list-level action (load, delete, connect, ...). */
  error: string | null
  /** The active session; null shows the welcome screen. */
  session: SessionInfo | null
  /** Connection currently being connected to. */
  connectingId: string | null

  load(): Promise<void>
  /** Saves and returns the connection; throws so forms can show the error. */
  save(c: Connection, password: string | null): Promise<Connection>
  remove(id: string): Promise<void>
  duplicate(id: string): Promise<Connection | null>
  /** Returns the server version; throws on failure. */
  test(c: Connection, password: string | null): Promise<string>
  connect(id: string): Promise<boolean>
  disconnect(): Promise<void>
  clearError(): void
}

export const initialConnectionsState = {
  connections: [] as Connection[],
  drivers: [] as DriverInfo[],
  loading: false,
  error: null as string | null,
  session: null as SessionInfo | null,
  connectingId: null as string | null,
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

  async save(c, password) {
    const saved = await connectionApi.save(c, password)
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

  test(c, password) {
    return connectionApi.test(c, password)
  },

  async connect(id) {
    set({ connectingId: id, error: null })
    try {
      const session = await connectionApi.connect(id)
      set({ session, connectingId: null })
      return true
    } catch (err) {
      set({ connectingId: null, error: errorMessage(err) })
      return false
    }
  },

  async disconnect() {
    const s = get().session
    if (!s) return
    set({ session: null })
    try {
      await connectionApi.disconnect(s.id)
    } catch (err) {
      set({ error: errorMessage(err) })
    }
  },

  clearError() {
    set({ error: null })
  },
}))
