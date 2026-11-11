//go:build linux

package store

// fallbackEnabled: on Linux a Secret Service (gnome-keyring, KWallet) is not
// guaranteed to run, so secrets fall back to the encrypted file store when
// the keychain fails. See NewSecretStore for the security trade-off.
const fallbackEnabled = true
