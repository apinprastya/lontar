//go:build darwin

package spotlight

import (
	"errors"
	"os/exec"

	"golang.org/x/sys/unix"
)

const supported = true

// systemAttrs uses the bundle directory's extended attributes.
type systemAttrs struct{}

func (systemAttrs) Get(path, name string) ([]byte, error) {
	n, err := unix.Getxattr(path, name, nil)
	if errors.Is(err, unix.ENOATTR) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	buf := make([]byte, n)
	n, err = unix.Getxattr(path, name, buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func (systemAttrs) Set(path, name string, value []byte) error {
	return unix.Setxattr(path, name, value, 0)
}

// reindex asks Spotlight to import the bundle again.
func reindex(bundle string) error {
	return exec.Command("/usr/bin/mdimport", bundle).Run()
}
