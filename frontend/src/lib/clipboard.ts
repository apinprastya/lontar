import { Clipboard } from '@wailsio/runtime'

/**
 * Copies text to the system clipboard. Uses the Wails runtime (works in
 * every webview without clipboard permissions) and falls back to the
 * browser API (e.g. when running the frontend alone in a browser).
 */
export async function copyText(text: string): Promise<void> {
  try {
    await Clipboard.SetText(text)
  } catch {
    await navigator.clipboard.writeText(text)
  }
}
