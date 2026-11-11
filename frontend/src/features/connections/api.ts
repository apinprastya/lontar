import { ConnectionService } from '../../../bindings/dbedit/internal/services'
import type { Connection, DriverInfo, SessionInfo } from './types'

// The generated bindings type kind/tag as TS enums; the JSON is identical,
// so the casts below only bridge the static types.
type BindingConnection = Parameters<typeof ConnectionService.Save>[0]

/** Typed wrapper around the generated ConnectionService bindings. */
export const connectionApi = {
  async drivers(): Promise<DriverInfo[]> {
    return ((await ConnectionService.Drivers()) ?? []) as unknown as DriverInfo[]
  },
  async list(): Promise<Connection[]> {
    return ((await ConnectionService.List()) ?? []) as unknown as Connection[]
  },
  async save(c: Connection, password: string | null): Promise<Connection> {
    return (await ConnectionService.Save(c as unknown as BindingConnection, password)) as unknown as Connection
  },
  async remove(id: string): Promise<void> {
    await ConnectionService.Delete(id)
  },
  async duplicate(id: string): Promise<Connection> {
    return (await ConnectionService.Duplicate(id)) as unknown as Connection
  },
  async test(c: Connection, password: string | null): Promise<string> {
    return ConnectionService.Test(c as unknown as BindingConnection, password)
  },
  async connect(id: string): Promise<SessionInfo> {
    return (await ConnectionService.Connect(id)) as unknown as SessionInfo
  },
  async disconnect(sessionId: string): Promise<void> {
    await ConnectionService.Disconnect(sessionId)
  },
}
