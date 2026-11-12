package sshtunnel

import (
	"io"
	"net"
)

// tunnelAddr is the remote address reported by tunnelled connections: the
// "host:port" that was dialled, so that pgx's cancel request (which dials
// RemoteAddr()) goes through the tunnel to the same server.
type tunnelAddr string

func (a tunnelAddr) Network() string { return "tcp" }
func (a tunnelAddr) String() string  { return string(a) }

// pipeConn is the database side of a bridged channel.
type pipeConn struct {
	net.Conn
	remote tunnelAddr
}

func (c *pipeConn) RemoteAddr() net.Addr { return c.remote }

// bridge connects ch (an SSH channel, which has no deadline support) to
// one end of a net.Pipe, whose other end is returned. Deadlines then work
// as for TCP, which pgx relies on to interrupt cancelled queries. Closing
// either side closes the other.
func bridge(ch net.Conn, addr string) net.Conn {
	local, inner := net.Pipe()
	go func() {
		_, _ = io.Copy(ch, inner)
		_ = ch.Close()
		_ = inner.Close()
	}()
	go func() {
		_, _ = io.Copy(inner, ch)
		_ = inner.Close()
		_ = ch.Close()
	}()
	return &pipeConn{Conn: local, remote: tunnelAddr(addr)}
}
