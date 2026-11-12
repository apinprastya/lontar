import { ConnectionService } from '../../../bindings/lontar/internal/services'
import type { Connection, DriverInfo, Secrets, SessionInfo } from './types'

// The generated bindings type kind/tag as TS enums; the JSON is identical,
// so the casts below only bridge the static types.
type BindingConnection = Parameters<typeof ConnectionService.Save>[0]
type BindingSecrets = Parameters<typeof ConnectionService.Save>[1]

/** Typed wrapper around the generated ConnectionService bindings. */
export const connectionApi = {
  async drivers(): Promise<DriverInfo[]> {
    return ((await ConnectionService.Drivers()) ?? []) as unknown as DriverInfo[]
  },
  async list(): Promise<Connection[]> {
    return ((await ConnectionService.List()) ?? []) as unknown as Connection[]
  },
  async save(c: Connection, secrets: Secrets): Promise<Connection> {
    return (await ConnectionService.Save(c as unknown as BindingConnection, secrets as unknown as BindingSecrets)) as unknown as Connection
  },
  async remove(id: string): Promise<void> {
    await ConnectionService.Delete(id)
  },
  async duplicate(id: string): Promise<Connection> {
    return (await ConnectionService.Duplicate(id)) as unknown as Connection
  },
  async test(c: Connection, secrets: Secrets): Promise<string> {
    return ConnectionService.Test(c as unknown as BindingConnection, secrets as unknown as BindingSecrets)
  },
  async acceptHostKey(host: string, fingerprint: string): Promise<void> {
    await ConnectionService.AcceptHostKey(host, fingerprint)
  },
  async connect(id: string): Promise<SessionInfo> {
    return (await ConnectionService.Connect(id)) as unknown as SessionInfo
  },
  async disconnect(sessionId: string): Promise<void> {
    await ConnectionService.Disconnect(sessionId)
  },
}
