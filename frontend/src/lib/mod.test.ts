import { afterEach, describe, expect, it, vi } from 'vitest'
import { isMac, isModPressed, matchesShortcut, modLabel, shortcutLabel } from './mod'

const ev = (over: Partial<KeyboardEvent> = {}) => ({
  key: 'n',
  metaKey: false,
  ctrlKey: false,
  shiftKey: false,
  altKey: false,
  ...over,
})

describe('mod helper', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('detects macOS from the Go platform', () => {
    expect(isMac('darwin')).toBe(true)
    expect(isMac('linux')).toBe(false)
    expect(isMac('windows')).toBe(false)
  })

  it('falls back to navigator.platform when the platform is unknown', () => {
    vi.stubGlobal('navigator', { platform: 'MacIntel' })
    expect(isMac(undefined)).toBe(true)
    vi.stubGlobal('navigator', { platform: 'Win32' })
    expect(isMac(undefined)).toBe(false)
    vi.stubGlobal('navigator', undefined)
    expect(isMac(undefined)).toBe(false)
  })

  it('labels the modifier', () => {
    expect(modLabel('darwin')).toBe('⌘')
    expect(modLabel('windows')).toBe('Ctrl')
  })

  it('uses Cmd on macOS and Ctrl elsewhere', () => {
    expect(isModPressed(ev({ metaKey: true }), 'darwin')).toBe(true)
    expect(isModPressed(ev({ ctrlKey: true }), 'darwin')).toBe(false)
    expect(isModPressed(ev({ ctrlKey: true }), 'linux')).toBe(true)
    expect(isModPressed(ev({ metaKey: true }), 'windows')).toBe(false)
    expect(isModPressed(ev({ ctrlKey: true, metaKey: true }), 'linux')).toBe(false)
  })

  it('matches shortcuts', () => {
    expect(matchesShortcut(ev({ ctrlKey: true, key: 'N' }), 'linux', 'n')).toBe(true)
    expect(matchesShortcut(ev({ metaKey: true }), 'darwin', 'n')).toBe(true)
    expect(matchesShortcut(ev({ ctrlKey: true, shiftKey: true }), 'linux', 'n')).toBe(false)
    expect(matchesShortcut(ev({ ctrlKey: true, shiftKey: true }), 'linux', 'n', { shift: true })).toBe(true)
    expect(matchesShortcut(ev({ ctrlKey: true, altKey: true }), 'linux', 'n')).toBe(false)
    expect(matchesShortcut(ev({ ctrlKey: true, key: 'x' }), 'linux', 'n')).toBe(false)
  })

  it('formats shortcut labels', () => {
    expect(shortcutLabel('darwin', 'n')).toBe('⌘N')
    expect(shortcutLabel('darwin', 'n', { shift: true })).toBe('⇧⌘N')
    expect(shortcutLabel('linux', 'n')).toBe('Ctrl+N')
    expect(shortcutLabel('windows', 'p', { shift: true })).toBe('Ctrl+Shift+P')
    expect(shortcutLabel('linux', 'Enter')).toBe('Ctrl+Enter')
  })
})
