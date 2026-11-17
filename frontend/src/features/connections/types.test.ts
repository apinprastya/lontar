import { describe, expect, it } from 'vitest'
import { makeConnection } from '../../test/mockBindings'
import { connectionColor, connectionSummary, isFileBased, sshBadgeTitle, sshChain } from './types'

describe('connection helpers', () => {
  it('summarises server and file connections', () => {
    expect(connectionSummary(makeConnection())).toBe('db.example.com:5432/shop')
    expect(connectionSummary(makeConnection({ port: 0, database: '' }))).toBe('db.example.com')
    expect(connectionSummary(makeConnection({ host: undefined, port: undefined, database: undefined }))).toBe('')
    expect(connectionSummary(makeConnection({ kind: 'sqlite', filePath: '/tmp/a.db' }))).toBe('/tmp/a.db')
    expect(connectionSummary(makeConnection({ kind: 'sqlite', filePath: undefined }))).toBe('')
  })

  it('picks the explicit colour, then the tag colour', () => {
    expect(connectionColor({ color: '#123456', tag: 'prod' })).toBe('#123456')
    expect(connectionColor({ tag: 'prod' })).toBe('#f85149')
    expect(connectionColor({ tag: 'none' })).toBe('')
  })

  it('knows file-based kinds', () => {
    expect(isFileBased('sqlite')).toBe(true)
    expect(isFileBased('mysql')).toBe(false)
  })

  it('describes the SSH chain', () => {
    const ssh = { enabled: true, host: 'gw', jumps: [{ host: 'b1' }, { host: 'b2' }] }
    expect(sshChain(ssh)).toBe('you → b1 → b2 → gw')
    expect(sshChain({ enabled: true, host: 'gw' })).toBe('you → gw')
    expect(sshChain({ enabled: true, jumps: [{}] })).toBe('you →  → ')
    expect(sshChain(null)).toBe('')
    expect(sshBadgeTitle(ssh)).toBe('Connected through SSH: you → b1 → b2 → gw')
    expect(sshBadgeTitle({ enabled: true, host: 'gw', jumps: [] })).toBe('Connected through an SSH tunnel')
    expect(sshBadgeTitle(undefined)).toBe('Connected through an SSH tunnel')
  })
})
