//go:build windows

package sshtunnel

import (
	"errors"
	"testing"
)

// The CI runner has no OpenSSH agent service running.
func TestDialAgentWindows(t *testing.T) {
	c, err := dialAgent()
	if err == nil {
		c.Close()
		t.Skip("an OpenSSH agent is running")
	}
	if !errors.Is(err, ErrNoAgent) {
		t.Fatalf("err = %v", err)
	}
}
