import { UpdateService } from '../../../bindings/lontar/internal/services'
import { onEvent } from '../../lib/events'

/** Result of a check for updates (Go services.UpdateInfo). */
export interface UpdateInfo {
  current: string
  latest: string
  available: boolean
  name: string
  notes: string
  url: string
  publishedAt: string
  /** Install can replace this installation; otherwise reason says why not. */
  canInstall: boolean
  reason: string
}

/** Download progress in bytes (Go services.UpdateProgress). */
export interface UpdateProgress {
  done: number
  total: number
}

export const PROGRESS_EVENT = 'update:progress'

/** Typed wrapper around the generated UpdateService bindings. */
export const updateApi = {
  async check(): Promise<UpdateInfo> {
    const info = await UpdateService.Check()
    if (!info) throw new Error('no update information')
    return info as unknown as UpdateInfo
  },
  install: () => UpdateService.Install() as Promise<void>,
  cancel: () => UpdateService.Cancel() as Promise<boolean>,
  restart: () => UpdateService.Restart() as Promise<void>,
  openReleasePage: () => UpdateService.OpenReleasePage() as Promise<void>,
  onProgress: (cb: (p: UpdateProgress) => void) => onEvent<UpdateProgress>(PROGRESS_EVENT, cb),
}
