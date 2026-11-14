import { useDefaultLayout } from 'react-resizable-panels'
import { getUIState, setUIState } from './uiState'

/**
 * The UI state as react-resizable-panels storage. The library parses stored
 * layouts without a guard, so anything that is not a JSON object of numbers
 * (a hand-edited state.json) reads as missing.
 */
export const layoutStorage = {
  getItem(key: string): string | null {
    const raw = getUIState(key)
    if (raw === null) return null
    try {
      const v: unknown = JSON.parse(raw)
      const ok = typeof v === 'object' && v !== null && !Array.isArray(v) && Object.values(v).every((n) => typeof n === 'number')
      return ok ? raw : null
    } catch {
      return null
    }
  },
  setItem(key: string, value: string) {
    setUIState(key, value)
  },
}

/**
 * Saves and restores a resizable panel Group's layout in the UI state
 * (state.json). Spread the result onto the Group. Pass panelIds (a stable
 * array) for a Group whose panels are rendered conditionally, so each
 * combination keeps its own layout. Only layouts the user dragged are saved,
 * not ones caused by resizing the window.
 */
export function usePanelLayout(id: string, panelIds?: string[]) {
  const { defaultLayout, onLayoutChanged } = useDefaultLayout({
    id: `layout.${id}`,
    panelIds,
    storage: layoutStorage,
    onlySaveAfterUserInteractions: true,
  })
  return { defaultLayout, onLayoutChanged }
}
