package services

import (
	"context"
	"sync"
	"testing"
	"time"

	"lontar/internal/query"
	"lontar/internal/settings"
)

// nameEmitter records event names and data.
type nameEmitter struct {
	mu     sync.Mutex
	names  []string
	events []SessionEvent
}

func (r *nameEmitter) Emit(name string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.names = append(r.names, name)
	if ev, ok := data.(SessionEvent); ok {
		r.events = append(r.events, ev)
	}
}

func TestIdleSuspendWithEditorsAndEvents(t *testing.T) {
	ctx := context.Background()
	svc, m, sid, _ := newEditorService(t)
	em := &nameEmitter{}
	ForwardSessionEvents(m, em)

	eid, err := svc.OpenEditor(sid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Execute(ctx, eid, "r1", "BEGIN", query.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if got := m.SuspendIdle(time.Millisecond, EditorIdleHold(svc)); got != nil {
		t.Fatalf("editor in a transaction: suspended %v", got)
	}
	if _, err := svc.Execute(ctx, eid, "r2", "COMMIT", query.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	// The idle editor's pinned connection does not keep the session.
	if got := m.SuspendIdle(time.Millisecond, EditorIdleHold(svc)); len(got) != 1 || got[0] != sid {
		t.Fatalf("suspended %v", got)
	}
	if s, _ := m.Suspended(sid); !s {
		t.Fatal("not suspended")
	}

	// The next run reconnects and reports the reset.
	res, err := svc.Execute(ctx, eid, "r3", "SELECT count(*) FROM t", query.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.SessionReset || res.Statements[0].Status != query.StatusOK {
		t.Fatalf("after resume = %+v", res)
	}
	em.mu.Lock()
	defer em.mu.Unlock()
	if len(em.names) != 2 || em.names[0] != EventSessionSuspended || em.names[1] != EventSessionResumed ||
		em.events[0].SessionID != sid || em.events[1].SessionID != sid {
		t.Fatalf("events = %v %v", em.names, em.events)
	}
}

func TestForwardSessionEventsIgnoresOthers(t *testing.T) {
	m, sid := openSQLiteSession(t)
	em := &nameEmitter{}
	ForwardSessionEvents(m, em)
	if err := m.Disconnect(sid); err != nil {
		t.Fatal(err)
	}
	if len(em.names) != 0 {
		t.Fatalf("events = %v", em.names)
	}
}

func TestIdleTimeout(t *testing.T) {
	var it IdleTimeout
	if it.Get() != 0 {
		t.Fatal("zero value should be off")
	}
	it.Apply(settings.Settings{IdleDisconnectMinutes: 15})
	if it.Get() != 15*time.Minute {
		t.Fatalf("Get = %v", it.Get())
	}
}

func TestSettingsServiceOnSave(t *testing.T) {
	svc := NewSettingsService(settings.New(t.TempDir()))
	var got []settings.Settings
	svc.OnSave = func(v settings.Settings) { got = append(got, v) }
	v := settings.Defaults()
	v.IdleDisconnectMinutes = 30
	if _, err := svc.Save(v); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].IdleDisconnectMinutes != 30 {
		t.Fatalf("OnSave got %+v", got)
	}
}
