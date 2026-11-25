package services

import (
	"sync/atomic"
	"time"

	"lontar/internal/conn"
	"lontar/internal/settings"
)

// Events about idle sessions, sent to the frontend with a SessionEvent.
const (
	// EventSessionSuspended: the session's connection was closed for
	// idleness; it reconnects on its next use.
	EventSessionSuspended = "session:suspended"
	// EventSessionResumed: a suspended session reconnected.
	EventSessionResumed = "session:resumed"
)

// IdleSweepInterval is how often idle sessions are looked for.
const IdleSweepInterval = 30 * time.Second

// SessionEvent is the data of EventSessionSuspended and EventSessionResumed.
type SessionEvent struct {
	SessionID string `json:"sessionId"`
}

// SessionSubscriber delivers session events (*conn.Manager).
type SessionSubscriber interface {
	Subscribe(fn func(conn.Event))
}

// ForwardSessionEvents sends the suspended and resumed session events of
// sub to the frontend through em.
func ForwardSessionEvents(sub SessionSubscriber, em Emitter) {
	sub.Subscribe(func(ev conn.Event) {
		switch ev.Kind {
		case conn.SessionSuspended:
			em.Emit(EventSessionSuspended, SessionEvent{SessionID: ev.SessionID})
		case conn.SessionResumed:
			em.Emit(EventSessionResumed, SessionEvent{SessionID: ev.SessionID})
		}
	})
}

// IdleTimeout holds the idle disconnect timeout from the settings. It is
// safe for concurrent use; the zero value is off.
type IdleTimeout struct {
	minutes atomic.Int64
}

// Apply takes the timeout from v (SettingsService.OnSave).
func (t *IdleTimeout) Apply(v settings.Settings) {
	t.minutes.Store(int64(v.IdleDisconnectMinutes))
}

// Get returns the timeout (0: off).
func (t *IdleTimeout) Get() time.Duration {
	return time.Duration(t.minutes.Load()) * time.Minute
}
