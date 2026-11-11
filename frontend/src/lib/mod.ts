/**
 * Platform-aware keyboard modifier helpers. Shortcuts use Cmd on macOS and
 * Ctrl on Windows/Linux. The platform comes from the Go AppService.Platform()
 * binding (runtime.GOOS); while it is unknown we fall back to the browser's
 * navigator.platform.
 */

export interface ModifierEvent {
  key: string
  metaKey: boolean
  ctrlKey: boolean
  shiftKey: boolean
  altKey: boolean
}

/** Whether the platform (a Go GOOS value) is macOS. */
export function isMac(platform?: string): boolean {
  if (platform) return platform === 'darwin'
  return typeof navigator !== 'undefined' && /mac|iphone|ipad/i.test(navigator.platform)
}

/** Display name of the primary modifier: "⌘" on macOS, "Ctrl" elsewhere. */
export function modLabel(platform?: string): string {
  return isMac(platform) ? '⌘' : 'Ctrl'
}

/** Whether exactly the platform's primary modifier is held. */
export function isModPressed(e: Pick<ModifierEvent, 'metaKey' | 'ctrlKey'>, platform?: string): boolean {
  return isMac(platform) ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey
}

export interface ShortcutOptions {
  shift?: boolean
}

/** Whether e is Mod(+Shift)+key, ignoring case. Alt must not be held. */
export function matchesShortcut(e: ModifierEvent, platform: string | undefined, key: string, opts: ShortcutOptions = {}): boolean {
  return (
    isModPressed(e, platform) &&
    !e.altKey &&
    e.shiftKey === Boolean(opts.shift) &&
    e.key.toLowerCase() === key.toLowerCase()
  )
}

/** Human readable shortcut: "⇧⌘N" on macOS, "Ctrl+Shift+N" elsewhere. */
export function shortcutLabel(platform: string | undefined, key: string, opts: ShortcutOptions = {}): string {
  const k = key.length === 1 ? key.toUpperCase() : key
  if (isMac(platform)) return `${opts.shift ? '⇧' : ''}⌘${k}`
  return `Ctrl+${opts.shift ? 'Shift+' : ''}${k}`
}
