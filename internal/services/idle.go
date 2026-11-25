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

// Timeouts holds the connection timeouts from the settings: the idle
// disconnect and the connect timeout. It is safe for concurrent use; the
// zero value has the idle disconnect off and the connect timeout unset
// (callers then use their default).
type Timeouts struct {
	idleMinutes    atomic.Int64
	connectSeconds atomic.Int64
}

// Apply takes the timeouts from v (at start and SettingsService.OnSave).
func (t *Timeouts) Apply(v settings.Settings) {
	t.idleMinutes.Store(int64(v.IdleDisconnectMinutes))
	t.connectSeconds.Store(int64(v.ConnectTimeoutSeconds))
}

// Idle returns the idle disconnect timeout (0: off).
func (t *Timeouts) Idle() time.Duration {
	return time.Duration(t.idleMinutes.Load()) * time.Minute
}

// Connect returns the connect timeout (0: unset, use the default).
func (t *Timeouts) Connect() time.Duration {
	return time.Duration(t.connectSeconds.Load()) * time.Second
}

// LoadTimeouts returns Timeouts applied from the stored settings (the
// defaults when they cannot be read).
func LoadTimeouts(st *settings.Store) *Timeouts {
	t := &Timeouts{}
	v, err := st.Load()
	if err != nil {
		v = settings.Defaults()
	}
	t.Apply(v)
	return t
}
