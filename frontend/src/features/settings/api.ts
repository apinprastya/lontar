import { SettingsService } from '../../../bindings/lontar/internal/services'
import type { Settings } from './types'

type BindingSettings = Parameters<typeof SettingsService.Save>[0]

/** Typed wrapper around the generated SettingsService bindings. */
export const settingsApi = {
  async get(): Promise<{ settings: Settings; error: string }> {
    const s = await SettingsService.Get()
    return { settings: s.settings as unknown as Settings, error: s.error }
  },
  async save(s: Settings): Promise<Settings> {
    return (await SettingsService.Save(s as unknown as BindingSettings)) as unknown as Settings
  },
}
