import { useEffect, useState } from 'react'
import { AppService } from '../../bindings/lontar/internal/services'

export type AppInfo =
  | { status: 'loading' }
  | { status: 'ready'; version: string; platform: string }
  | { status: 'error'; error: string }

/** Loads static application metadata from the Go AppService binding. */
export function useAppInfo(): AppInfo {
  const [info, setInfo] = useState<AppInfo>({ status: 'loading' })

  useEffect(() => {
    let active = true
    Promise.all([AppService.Version(), AppService.Platform()])
      .then(([version, platform]) => {
        if (active) setInfo({ status: 'ready', version, platform })
      })
      .catch((err: unknown) => {
        if (active) setInfo({ status: 'error', error: err instanceof Error ? err.message : String(err) })
      })
    return () => {
      active = false
    }
  }, [])

  return info
}
