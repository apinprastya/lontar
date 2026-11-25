package sshtunnel

import (
	"testing"
	"time"
)

func TestDialerTimeout(t *testing.T) {
	if got := New(Options{}).timeout(); got != DefaultTimeout {
		t.Fatalf("default = %v", got)
	}
	v := time.Duration(0)
	d := New(Options{Timeout: 7 * time.Second, TimeoutFunc: func() time.Duration { return v }})
	if got := d.timeout(); got != 7*time.Second {
		t.Fatalf("unset func = %v", got)
	}
	v = 42 * time.Second
	if got := d.timeout(); got != 42*time.Second {
		t.Fatalf("func = %v", got)
	}
}
