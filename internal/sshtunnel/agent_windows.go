//go:build windows

package sshtunnel

import (
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

// AgentPipe is the named pipe of the Windows OpenSSH agent service.
const AgentPipe = `\\.\pipe\openssh-ssh-agent`

// dialAgent connects to the OpenSSH agent's named pipe (Pageant is not
// supported).
func dialAgent() (net.Conn, error) {
	timeout := 5 * time.Second
	c, err := winio.DialPipe(AgentPipe, &timeout)
	if err != nil {
		return nil, ErrNoAgent
	}
	return c, nil
}
