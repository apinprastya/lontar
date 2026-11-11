//go:build !linux

package store

// fallbackEnabled: macOS Keychain and Windows Credential Manager are always
// available, so keychain errors are reported instead of silently using the
// weaker file store.
const fallbackEnabled = false
