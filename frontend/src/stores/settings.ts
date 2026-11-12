import { create } from 'zustand'
import { settingsApi } from '../features/settings/api'
import { DEFAULT_SETTINGS, type ResolvedTheme, type Settings } from '../features/settings/types'
import { errorMessage } from '../lib/errors'

export interface SettingsState {
  settings: Settings
  loaded: boolean
  /** Why settings.json could not be read or written (defaults are used). */
  error: string | null
  /** The OS prefers a dark colour scheme (prefers-color-scheme). */
  systemDark: boolean
  /** The settings dialog is open. */
  open: boolean
  load(): Promise<void>
  /** Saves (the backend clamps values); false on failure (see error). */
  save(s: Settings): Promise<boolean>
  setSystemDark(dark: boolean): void
  setOpen(open: boolean): void
}

const DARK_QUERY = '(prefers-color-scheme: dark)'

function prefersDark(): boolean {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function' ? window.matchMedia(DARK_QUERY).matches : true
}

export const useSettings = create<SettingsState>()((set) => ({
  settings: DEFAULT_SETTINGS,
  loaded: false,
  error: null,
  systemDark: prefersDark(),
  open: false,

  async load() {
    try {
      const { settings, error } = await settingsApi.get()
      set({ settings, loaded: true, error: error || null })
    } catch (err) {
      set({ loaded: true, error: errorMessage(err) })
    }
  },

  async save(s) {
    try {
      const saved = await settingsApi.save(s)
      set({ settings: saved, error: null })
      return true
    } catch (err) {
      set({ error: errorMessage(err) })
      return false
    }
  },

  setSystemDark(systemDark) {
    set({ systemDark })
  },

  setOpen(open) {
    set({ open })
  },
}))

/** The theme in effect: the setting, or the OS preference for "system". */
export function resolveTheme(theme: Settings['theme'], systemDark: boolean): ResolvedTheme {
  if (theme === 'system') return systemDark ? 'dark' : 'light'
  return theme
}

/** Hook: the theme in effect (re-renders when the setting or the OS preference changes). */
export function useResolvedTheme(): ResolvedTheme {
  return useSettings((s) => resolveTheme(s.settings.theme, s.systemDark))
}

/**
 * Applies the theme and editor font size to the document: data-theme on
 * <html> switches the CSS custom properties; --editor-font-size feeds
 * CodeMirror.
 */
export function applyAppearance(theme: ResolvedTheme, fontSize: number, root: HTMLElement = document.documentElement) {
  root.dataset.theme = theme
  root.style.colorScheme = theme
  root.style.setProperty('--editor-font-size', `${fontSize}px`)
}

/** Follows the OS colour scheme live; returns the unsubscribe function. */
export function watchSystemTheme(): () => void {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return () => {}
  const mq = window.matchMedia(DARK_QUERY)
  const onChange = (e: MediaQueryListEvent) => useSettings.getState().setSystemDark(e.matches)
  useSettings.getState().setSystemDark(mq.matches)
  mq.addEventListener('change', onChange)
  return () => mq.removeEventListener('change', onChange)
}

/**
 * Applies the appearance now and on every settings change. The store
 * subscription runs synchronously when the state changes, before React
 * re-renders, so components that read the CSS variables while rendering
 * (the Glide grid theme) already see the new theme. Returns the
 * unsubscribe function.
 */
export function bindAppearance(root: HTMLElement = document.documentElement): () => void {
  const apply = (s: SettingsState) => applyAppearance(resolveTheme(s.settings.theme, s.systemDark), s.settings.editorFontSize, root)
  apply(useSettings.getState())
  return useSettings.subscribe(apply)
}
