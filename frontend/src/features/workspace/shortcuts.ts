import { isModPressed, type ModifierEvent } from '../../lib/mod'
import type { WorkspaceState } from '../../stores/workspace'

/**
 * Handles a workspace keyboard shortcut; returns true when the event was
 * consumed (the caller then prevents the default action, e.g. reload on
 * Mod+R).
 *
 *   Mod+W close tab · Mod+1..9 switch tab · Mod+R refresh ·
 *   Mod+F filter bar · Mod+I row detail
 */
export function handleShortcut(e: ModifierEvent, platform: string | undefined, s: WorkspaceState): boolean {
  if (!isModPressed(e, platform) || e.altKey || e.shiftKey) return false
  const key = e.key.toLowerCase()
  const tab = s.activeTab()
  if (/^[1-9]$/.test(key)) {
    s.activateIndex(Number(key) - 1)
    return true
  }
  switch (key) {
    case 'w':
      if (tab) s.closeTab(tab.id)
      return true
    case 'r':
      void s.refresh()
      return true
    case 'f':
      if (!tab) return false
      s.toggleFilterBar(tab.id)
      return true
    case 'i':
      s.toggleDetail()
      return true
  }
  return false
}
