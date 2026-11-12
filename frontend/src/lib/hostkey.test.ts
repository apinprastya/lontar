import { describe, expect, it } from 'vitest'
import { hostKeyInfo } from './hostkey'

describe('hostKeyInfo', () => {
  it('reads the structured cause of a rejected call', () => {
    const cause = { kind: 'hostKeyUnknown', host: 'h', fingerprintSHA256: 'SHA256:x' }
    expect(hostKeyInfo({ cause })).toEqual(cause)
    expect(hostKeyInfo({ cause: JSON.stringify({ ...cause, kind: 'hostKeyChanged' }) })?.kind).toBe('hostKeyChanged')
    expect(hostKeyInfo({ cause: '{bad' })).toBeNull()
    expect(hostKeyInfo({ cause: { kind: 'other' } })).toBeNull()
    expect(hostKeyInfo({ cause: 7 })).toBeNull()
    expect(hostKeyInfo(new Error('plain'))).toBeNull()
    expect(hostKeyInfo(null)).toBeNull()
    expect(hostKeyInfo('x')).toBeNull()
  })
})
