import { useEffect } from 'react'
import { useUpdate } from '../../stores/update'

/** The first automatic check runs this long after startup (not while the app starts). */
export const AUTO_UPDATE_DELAY_MS = 10_000
/** Later checks run this often while Lontar stays open. */
export const AUTO_UPDATE_INTERVAL_MS = 6 * 60 * 60 * 1000

/**
 * Runs the automatic update check shortly after start and every few hours
 * while enabled (the autoUpdate setting, once settings are loaded).
 */
export function useAutoUpdate(enabled: boolean) {
  useEffect(() => {
    if (!enabled) return
    const run = () => void useUpdate.getState().autoUpdate()
    const first = setTimeout(run, AUTO_UPDATE_DELAY_MS)
    const every = setInterval(run, AUTO_UPDATE_INTERVAL_MS)
    return () => {
      clearTimeout(first)
      clearInterval(every)
    }
  }, [enabled])
}
