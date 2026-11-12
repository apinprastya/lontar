/** An SSH host key the backend refused (sshtunnel.HostKeyUnknownError / HostKeyChangedError). */
export interface HostKeyInfo {
  kind: 'hostKeyUnknown' | 'hostKeyChanged'
  host: string
  address: string
  keyType: string
  fingerprintSHA256: string
  /** Changed keys: the recorded entries ("file:line"). */
  known?: string[]
  message: string
}

/** The structured host key error carried by a rejected backend call, if any. */
export function hostKeyInfo(err: unknown): HostKeyInfo | null {
  if (typeof err !== 'object' || err === null) return null
  let cause = (err as { cause?: unknown }).cause
  if (typeof cause === 'string') {
    try {
      cause = JSON.parse(cause)
    } catch {
      return null
    }
  }
  if (typeof cause !== 'object' || cause === null) return null
  const kind = (cause as { kind?: unknown }).kind
  if (kind !== 'hostKeyUnknown' && kind !== 'hostKeyChanged') return null
  return cause as HostKeyInfo
}
