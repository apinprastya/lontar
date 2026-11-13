import { create } from 'zustand'
import { updateApi, type UpdateInfo, type UpdateProgress } from '../features/update/api'
import { errorMessage } from '../lib/errors'

/**
 * Where the update dialog is: idle (nothing asked yet), checking, checked
 * (info set), installing (progress set), installed (restart to use it).
 */
export type UpdatePhase = 'idle' | 'checking' | 'checked' | 'installing' | 'installed'

export interface UpdateState {
  open: boolean
  phase: UpdatePhase
  info: UpdateInfo | null
  progress: UpdateProgress | null
  error: string | null
  setOpen(open: boolean): void
  /** Opens the dialog and checks GitHub for the latest release. */
  check(): Promise<void>
  install(): Promise<void>
  cancel(): Promise<void>
  restart(): Promise<void>
  openReleasePage(): Promise<void>
}

/** The running install was cancelled by the user (its error is not shown). */
let cancelled = false

const initial = { phase: 'idle' as UpdatePhase, info: null, progress: null, error: null }

export const useUpdate = create<UpdateState>()((set, get) => ({
  open: false,
  ...initial,

  setOpen(open) {
    // An install keeps running in the background; its state is kept.
    if (!open && get().phase !== 'installing' && get().phase !== 'installed') set({ open, ...initial })
    else set({ open })
  },

  async check() {
    if (get().phase === 'installing' || get().phase === 'installed') {
      set({ open: true })
      return
    }
    set({ open: true, phase: 'checking', error: null, info: null })
    try {
      const info = await updateApi.check()
      set({ phase: 'checked', info })
    } catch (err) {
      set({ phase: 'idle', error: errorMessage(err) })
    }
  },

  async install() {
    if (get().phase !== 'checked' || !get().info?.canInstall) return
    cancelled = false
    set({ phase: 'installing', error: null, progress: null })
    const off = updateApi.onProgress((p) => set({ progress: p }))
    try {
      await updateApi.install()
      set({ phase: 'installed' })
    } catch (err) {
      set({ phase: 'checked', error: cancelled ? null : errorMessage(err), progress: null })
    } finally {
      off()
    }
  },

  async cancel() {
    if (get().phase !== 'installing') return
    cancelled = true
    await updateApi.cancel()
  },

  async restart() {
    try {
      await updateApi.restart()
    } catch (err) {
      set({ error: errorMessage(err) })
    }
  },

  async openReleasePage() {
    try {
      await updateApi.openReleasePage()
    } catch (err) {
      set({ error: errorMessage(err) })
    }
  },
}))
