//go:build !windows

package sshtunnel

import (
	"net"
	"os"
)

// dialAgent connects to the SSH agent at $SSH_AUTH_SOCK.
func dialAgent() (net.Conn, error) {
	return dialAgentSocket(os.Getenv("SSH_AUTH_SOCK"))
}

func dialAgentSocket(sock string) (net.Conn, error) {
	if sock == "" {
		return nil, ErrNoAgent
	}
	return net.Dial("unix", sock)
}
