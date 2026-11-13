// Package update checks GitHub Releases for a newer stable Lontar and
// installs it in place.
//
// Trust: every release carries SHA256SUMS and SHA256SUMS.sig, an ed25519
// signature of SHA256SUMS made in CI with a private key whose public half is
// compiled in (PublicKey). An asset is installed only when the signature
// verifies and the downloaded file's SHA-256 matches its SHA256SUMS line.
// Without a compiled-in key, nothing is installed (the user is pointed at the
// release page instead).
//
// What can be replaced depends on how Lontar was installed (see Detect):
// the AppImage file, the Windows lontar.exe (installer or portable zip), or
// the macOS Lontar.app bundle. Distribution packages (.deb, .rpm) belong to
// the package manager and are only reported, never replaced.
package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PublicKey is the base64 ed25519 public key that release checksums are
// signed with. Generate the pair with `go -C tools run ./updatesign keygen`
// (see README "Releasing"); the private key is the UPDATE_SIGNING_KEY
// secret. Empty disables installing (checking still works).
var PublicKey = ""

// Repo is the GitHub repository releases are published to.
const Repo = "apinprastya/lontar"

// Names of the signed checksum files attached to every release.
const (
	SumsName = "SHA256SUMS"
	SigName  = "SHA256SUMS.sig"
)

// Errors meant for the UI.
var (
	ErrNoRelease   = errors.New("no release has been checked yet")
	ErrNotNewer    = errors.New("this version is already up to date")
	ErrUnsupported = errors.New("this installation cannot update itself; download the new version from the release page")
	ErrNoKey       = errors.New("this build has no update signing key; download the new version from the release page")
	ErrSignature   = errors.New("the release checksums are not signed by Lontar's update key")
	ErrChecksum    = errors.New("the downloaded file does not match the signed checksum")
	ErrTooLarge    = errors.New("the download is larger than allowed")
)

// Asset is a file attached to a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release is a published GitHub release.
type Release struct {
	Tag         string    `json:"tag_name"`
	Name        string    `json:"name"`
	Notes       string    `json:"body"`
	URL         string    `json:"html_url"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
}

// Version is the release version without the leading "v".
func (r Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

// Asset returns the asset with the given name.
func (r Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

var versionRE = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:[-+](.*))?$`)

// parseVersion returns the numeric X.Y.Z core and whether a suffix follows.
func parseVersion(v string) (core [3]int, suffix string, ok bool) {
	m := versionRE.FindStringSubmatch(v)
	if m == nil {
		return core, "", false
	}
	for i := range 3 {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return core, "", false
		}
		core[i] = n
	}
	return core, m[4], true
}

// Newer reports whether latest is a newer release than current. Versions
// are CalVer YY.MM.MICRO (compared numerically). A pre-release of the same
// core is older than the final; a git-describe build after a tag
// (26.11.1-4-gabc) has the tag's core, so the next release is newer than
// it. An unparsable current version (a dev build) is older than any release.
func Newer(latest, current string) bool {
	l, lsuf, ok := parseVersion(latest)
	if !ok || lsuf != "" {
		return false // only final releases are offered
	}
	c, csuf, ok := parseVersion(current)
	if !ok {
		return true
	}
	for i := range 3 {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	// Same core: the final is newer than its pre-release (26.11.2-rc.1),
	// not newer than a later git-describe build (26.11.2-3-gabc).
	return csuf != "" && !isDescribe(csuf)
}

var describeRE = regexp.MustCompile(`^\d+-g[0-9a-f]+(-dirty)?$`)

func isDescribe(s string) bool { return describeRE.MatchString(s) }

// IsDev reports whether v is an unstamped development version.
func IsDev(v string) bool {
	_, _, ok := parseVersion(v)
	return !ok || strings.HasPrefix(v, "0.0.0")
}

// parseKey decodes a base64 ed25519 public key.
func parseKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid update public key")
	}
	return ed25519.PublicKey(b), nil
}

// VerifySums checks sig (base64 ed25519 signature, surrounding whitespace
// ignored) over sums with the base64 public key.
func VerifySums(pubKey string, sums, sig []byte) error {
	key, err := parseKey(pubKey)
	if err != nil {
		return err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize || !ed25519.Verify(key, sums, raw) {
		return ErrSignature
	}
	return nil
}

// SumFor returns the hex SHA-256 listed for name in a sha256sum file
// ("<hex>  <name>" or "<hex> *<name>").
func SumFor(sums []byte, name string) (string, bool) {
	for line := range strings.SplitSeq(string(sums), "\n") {
		hash, file, ok := strings.Cut(strings.TrimRight(line, "\r"), " ")
		if !ok || len(hash) != 64 {
			continue
		}
		file = strings.TrimPrefix(strings.TrimLeft(file, " "), "*")
		if file == name {
			return strings.ToLower(hash), true
		}
	}
	return "", false
}
