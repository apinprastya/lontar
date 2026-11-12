/**
 * One-time move of localStorage keys written under the app's former name,
 * dbedit (`dbedit.queryDrafts.<id>` and any other `dbedit.*` key), to the
 * `lontar.*` prefix. Runs once on start, before anything reads storage.
 *
 * A key is copied only when its `lontar.*` target is missing (newer data
 * wins), and the old key is removed after a successful copy. Every storage
 * call is guarded: storage may be unavailable or full, and drafts are a
 * convenience only.
 *
 * Limit: the desktop webview keeps its storage in a directory named after the
 * program (Linux WebKitGTK: $XDG_DATA_HOME/<program name>; Windows WebView2:
 * %AppData%\<exe name>; macOS WKWebView: per bundle identifier), so the
 * renamed desktop app starts with a fresh, empty storage and cannot see the
 * old keys. This migration only helps where the origin's storage is shared
 * across the rename, such as server mode in a browser.
 */
export const LEGACY_PREFIX = 'dbedit.'
export const PREFIX = 'lontar.'

/** Moves `dbedit.*` keys to `lontar.*`; returns how many were copied. */
export function migrateLegacyStorage(storage?: Storage): number {
  let moved = 0
  try {
    const s = storage ?? window.localStorage
    const keys: string[] = []
    for (let i = 0; i < s.length; i++) {
      const k = s.key(i)
      if (k !== null && k.startsWith(LEGACY_PREFIX)) keys.push(k)
    }
    for (const k of keys) {
      try {
        const target = PREFIX + k.slice(LEGACY_PREFIX.length)
        const value = s.getItem(k)
        if (value !== null && s.getItem(target) === null) {
          s.setItem(target, value)
          moved++
        }
        s.removeItem(k)
      } catch {
        // Copy failed (e.g. quota): keep the old key for the next start.
      }
    }
  } catch {
    // No storage at all.
  }
  return moved
}
