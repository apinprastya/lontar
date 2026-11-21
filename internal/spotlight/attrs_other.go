//go:build !darwin

package spotlight

import "errors"

// Spotlight is macOS only; Ensure does nothing elsewhere.
const supported = false

type systemAttrs struct{}

func (systemAttrs) Get(string, string) ([]byte, error) { return nil, errors.ErrUnsupported }

func (systemAttrs) Set(string, string, []byte) error { return errors.ErrUnsupported }

func reindex(string) error { return nil }
