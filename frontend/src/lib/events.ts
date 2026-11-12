import { Events } from '@wailsio/runtime'

/**
 * Subscribes to a backend event (Go app.Event.Emit) and returns the
 * unsubscribe function. The callback receives the event's data.
 */
export function onEvent<T>(name: string, cb: (data: T) => void): () => void {
  return Events.On(name, (ev: { data: unknown }) => cb(ev.data as T))
}
