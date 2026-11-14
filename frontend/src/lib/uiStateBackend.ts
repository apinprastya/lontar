import { UIStateService } from '../../bindings/lontar/internal/services'
import type { UIStateBackend } from './uiState'

/** The UI state stored by the Go UIStateService (state.json). */
export const uiStateBackend: UIStateBackend = {
  all: () => UIStateService.All(),
  set: (key, value) => UIStateService.Set(key, value),
}
