// Command updatesign makes and checks the ed25519 signatures that Lontar's
// in-app updater requires on a release's SHA256SUMS (see internal/update).
//
//	updatesign keygen
//	    print a new key pair: the public key (paste into
//	    internal/update.PublicKey) and the private key (store as the
//	    UPDATE_SIGNING_KEY repository secret); both base64
//	updatesign sign FILE
//	    sign FILE with the base64 private key in $UPDATE_SIGNING_KEY and
//	    print the base64 signature (written to SHA256SUMS.sig in CI)
//	updatesign verify PUBKEY FILE SIGFILE
//	    check SIGFILE against FILE with the base64 public key
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	if err := run(os.Args[1:], os.Getenv, os.Stdout, rand.Reader); err != nil {
		fmt.Fprintln(os.Stderr, "updatesign:", err)
		os.Exit(1)
	}
}

const usage = "usage: updatesign keygen | sign FILE | verify PUBKEY FILE SIGFILE"

func decode(s string, size int, what string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != size {
		return nil, fmt.Errorf("invalid %s", what)
	}
	return b, nil
}

func run(args []string, getenv func(string) string, out io.Writer, rnd io.Reader) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	enc := base64.StdEncoding.EncodeToString
	switch args[0] {
	case "keygen":
		if len(args) != 1 {
			return errors.New(usage)
		}
		pub, priv, err := ed25519.GenerateKey(rnd)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "public key (internal/update.PublicKey):\n%s\nprivate key (secret UPDATE_SIGNING_KEY):\n%s\n", enc(pub), enc(priv))
		return err
	case "sign":
		if len(args) != 2 {
			return errors.New(usage)
		}
		key, err := decode(getenv("UPDATE_SIGNING_KEY"), ed25519.PrivateKeySize, "UPDATE_SIGNING_KEY")
		if err != nil {
			return err
		}
		data, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, enc(ed25519.Sign(ed25519.PrivateKey(key), data)))
		return err
	case "verify":
		if len(args) != 4 {
			return errors.New(usage)
		}
		pub, err := decode(args[1], ed25519.PublicKeySize, "public key")
		if err != nil {
			return err
		}
		data, err := os.ReadFile(args[2])
		if err != nil {
			return err
		}
		sigText, err := os.ReadFile(args[3])
		if err != nil {
			return err
		}
		sig, err := decode(string(sigText), ed25519.SignatureSize, "signature")
		if err != nil {
			return err
		}
		if !ed25519.Verify(pub, data, sig) {
			return errors.New("signature does not match")
		}
		_, err = fmt.Fprintln(out, "OK")
		return err
	}
	return errors.New(usage)
}
