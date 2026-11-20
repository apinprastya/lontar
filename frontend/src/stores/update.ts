import { create } from 'zustand'
import { updateApi, type UpdateInfo, type UpdateProgress } from '../features/update/api'
import { errorMessage } from '../lib/errors'

/**
 * Where the update dialog is: idle (nothing asked yet), checking, checked
 * (info set), installing (progress set), installed (restart to use it).
 */
export type UpdatePhase = 'idle' | 'checking' | 'checked' | 'installing' | 'installed'

/**
 * The small notice the automatic update shows while the dialog is closed:
 * an update was installed (restart to use it), or one is available that
 * could not be installed here (see the dialog for why).
 */
export type UpdateNotice = 'installed' | 'available' | null

export interface UpdateState {
  open: boolean
  phase: UpdatePhase
  info: UpdateInfo | null
  progress: UpdateProgress | null
  error: string | null
  notice: UpdateNotice
  setOpen(open: boolean): void
  /** Opens the dialog and checks GitHub for the latest release. */
  check(): Promise<void>
  install(): Promise<void>
  cancel(): Promise<void>
  restart(): Promise<void>
  openReleasePage(): Promise<void>
  /**
   * The scheduled check: silent unless a newer release exists. One that can
   * be installed here is downloaded and installed in the background, then
   * the notice offers a restart; otherwise the notice says it is available.
   * Does nothing while the dialog is open or an update is in progress.
   */
  autoUpdate(): Promise<void>
  /** Opens the dialog for the notice's update and hides the notice. */
  showNotice(): void
  /** Hides the notice; an available release is not announced again this session. */
  dismissNotice(): void
}

/** The running install was cancelled by the user (its error is not shown). */
let cancelled = false

/** The version whose "available" notice was dismissed (not shown again). */
let dismissed: string | null = null

/** Forgets the dismissed version (tests). */
export function resetDismissed() {
  dismissed = null
}

const initial = { phase: 'idle' as UpdatePhase, info: null, progress: null, error: null, notice: null as UpdateNotice }

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
      set({ open: true, notice: null })
      return
    }
    set({ open: true, phase: 'checking', error: null, info: null, notice: null })
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

  async autoUpdate() {
    const busy = () => get().open || get().phase !== 'idle'
    if (busy()) return
    let info: UpdateInfo | null
    try {
      info = await updateApi.autoCheck()
    } catch {
      return // offline or rate limited: try again next time
    }
    if (!info?.available || busy()) return
    if (!info.canInstall) {
      if (dismissed !== info.latest) set({ phase: 'checked', info, notice: 'available' })
      return
    }
    set({ phase: 'checked', info, error: null })
    await get().install()
    if (get().open) return // the dialog shows the outcome
    if (get().phase === 'installed') set({ notice: 'installed' })
    else if (get().phase === 'checked' && get().error && dismissed !== info.latest) set({ notice: 'available' })
    else set({ ...initial }) // cancelled
  },

  showNotice() {
    set({ open: true, notice: null })
  },

  dismissNotice() {
    const { notice, info } = get()
    if (notice === 'available') {
      dismissed = info?.latest ?? null
      set({ ...initial })
    } else set({ notice: null })
  },
}))
