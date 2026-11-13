package update

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Kind is how the running Lontar was installed.
type Kind string

// Installation kinds.
const (
	KindAppImage Kind = "appimage" // Linux AppImage: the file is replaced
	KindWindows  Kind = "windows"  // lontar.exe (installer or portable): the exe is swapped
	KindMacOS    Kind = "macos"    // Lontar.app: the bundle is replaced from the .dmg
	KindManual   Kind = "manual"   // anything else: only the release page is offered
)

// Installation describes the running installation.
type Installation struct {
	Kind Kind
	// Target is the file (AppImage, exe) or bundle (.app) that is replaced.
	Target string
	// Reason explains KindManual.
	Reason string
}

// Env is what Detect looks at.
type Env struct {
	GOOS, GOARCH string
	Executable   string
	Getenv       func(string) string
}

// Detect works out how Lontar was installed.
func Detect(env Env) Installation {
	getenv := env.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	manual := func(reason string) Installation { return Installation{Kind: KindManual, Reason: reason} }
	switch env.GOOS {
	case "linux":
		if p := getenv("APPIMAGE"); p != "" && env.GOARCH == "amd64" {
			return Installation{Kind: KindAppImage, Target: p}
		}
		if strings.HasPrefix(env.Executable, "/usr/") || strings.HasPrefix(env.Executable, "/opt/") {
			return manual("Lontar was installed with a system package (.deb or .rpm); update it with your package manager.")
		}
		return manual("Only the AppImage can update itself on Linux.")
	case "windows":
		if env.GOARCH == "amd64" && env.Executable != "" {
			return Installation{Kind: KindWindows, Target: env.Executable}
		}
		return manual("Updates are published for 64-bit Windows (amd64) only.")
	case "darwin":
		if i := strings.Index(env.Executable, ".app/Contents/MacOS/"); i > 0 {
			return Installation{Kind: KindMacOS, Target: env.Executable[:i+len(".app")]}
		}
		return manual("Only Lontar.app can update itself on macOS.")
	}
	return manual("Updates are not published for this platform.")
}

// AssetName is the release asset an installation of kind k installs.
func AssetName(k Kind, version string) string {
	switch k {
	case KindAppImage:
		return "lontar-" + version + "-linux-x86_64.AppImage"
	case KindWindows:
		return "lontar-" + version + "-windows-amd64-portable.zip"
	case KindMacOS:
		return "lontar-" + version + "-macos-universal.dmg"
	}
	return ""
}

// RestartCommand is the command that starts the installed version.
func (in Installation) RestartCommand() (string, []string) {
	if in.Kind == KindMacOS {
		return "open", []string{"-n", in.Target}
	}
	return in.Target, nil
}

// Cleanup removes what an earlier update left behind (the previous exe on
// Windows, which cannot be deleted while it runs). Errors are ignored.
func (in Installation) Cleanup() {
	if in.Kind == KindWindows || in.Kind == KindMacOS {
		_ = os.RemoveAll(in.Target + ".old")
		_ = os.RemoveAll(in.Target + ".new")
	}
}

// Runner runs an external command (hdiutil, ditto on macOS).
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecRunner runs commands with os/exec.
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Updater finds, downloads, verifies and installs releases.
type Updater struct {
	Client    *http.Client
	APIURL    string // latest stable release (GitHub excludes drafts and pre-releases)
	PublicKey string
	Install   Installation
	Run       Runner
	TempDir   string // "" uses the OS temp directory
	MaxSize   int64  // largest asset accepted
}

// LatestURL is GitHub's "latest release" endpoint for Repo.
const LatestURL = "https://api.github.com/repos/" + Repo + "/releases/latest"

// DefaultMaxSize bounds an asset download.
const DefaultMaxSize = 512 << 20

func (u *Updater) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return http.DefaultClient
}

func (u *Updater) get(ctx context.Context, url, accept string) (*http.Response, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("refusing non-HTTPS URL %q", url)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Lontar-updater")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := u.client().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return resp, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp, nil
}

// Latest returns the latest stable release.
func (u *Updater) Latest(ctx context.Context) (Release, error) {
	url := u.APIURL
	if url == "" {
		url = LatestURL
	}
	resp, err := u.get(ctx, url, "application/vnd.github+json")
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return Release{}, errors.New("no Lontar release has been published yet")
		}
		return Release{}, fmt.Errorf("check for updates: %w", err)
	}
	defer resp.Body.Close()
	var r Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return Release{}, fmt.Errorf("check for updates: %w", err)
	}
	if r.Draft || r.Prerelease || r.Tag == "" {
		return Release{}, errors.New("check for updates: the latest release is not a stable release")
	}
	return r, nil
}

// fetchSmall downloads a small asset into memory.
func (u *Updater) fetchSmall(ctx context.Context, r Release, name string, limit int64) ([]byte, error) {
	a, ok := r.Asset(name)
	if !ok {
		return nil, fmt.Errorf("release %s has no %s", r.Tag, name)
	}
	resp, err := u.get(ctx, a.URL, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, ErrTooLarge
	}
	return b, nil
}

// Download fetches this installation's asset of r into a temporary file and
// verifies it against the signed SHA256SUMS. The caller removes the file.
// progress (may be nil) receives bytes done and the expected total.
func (u *Updater) Download(ctx context.Context, r Release, progress func(done, total int64)) (string, error) {
	if u.Install.Kind == KindManual {
		return "", ErrUnsupported
	}
	if strings.TrimSpace(u.PublicKey) == "" {
		return "", ErrNoKey
	}
	name := AssetName(u.Install.Kind, r.Version())
	asset, ok := r.Asset(name)
	if !ok {
		return "", fmt.Errorf("release %s has no %s", r.Tag, name)
	}
	sums, err := u.fetchSmall(ctx, r, SumsName, 1<<16)
	if err != nil {
		return "", err
	}
	sig, err := u.fetchSmall(ctx, r, SigName, 1<<10)
	if err != nil {
		return "", err
	}
	if err := VerifySums(u.PublicKey, sums, sig); err != nil {
		return "", err
	}
	want, ok := SumFor(sums, name)
	if !ok {
		return "", fmt.Errorf("%s does not list %s", SumsName, name)
	}
	max := u.MaxSize
	if max <= 0 {
		max = DefaultMaxSize
	}
	if asset.Size > max {
		return "", ErrTooLarge
	}

	resp, err := u.get(ctx, asset.URL, "")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	f, err := os.CreateTemp(u.TempDir, "lontar-update-*")
	if err != nil {
		return "", err
	}
	ok = false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	h := sha256.New()
	w := &progressWriter{w: io.MultiWriter(f, h), total: asset.Size, fn: progress}
	n, err := io.Copy(w, io.LimitReader(resp.Body, max+1))
	if err != nil {
		return "", err
	}
	if n > max {
		return "", ErrTooLarge
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return "", ErrChecksum
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	ok = true
	return f.Name(), nil
}

type progressWriter struct {
	w     io.Writer
	done  int64
	total int64
	fn    func(done, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	if p.fn != nil {
		p.fn(p.done, p.total)
	}
	return n, err
}

// Apply installs a verified download (from Download) over the installation.
func (u *Updater) Apply(ctx context.Context, file string) error {
	switch u.Install.Kind {
	case KindAppImage:
		return applyAppImage(file, u.Install.Target)
	case KindWindows:
		return applyWindows(file, u.Install.Target)
	case KindMacOS:
		run := u.Run
		if run == nil {
			run = ExecRunner
		}
		return applyMacOS(ctx, run, file, u.Install.Target)
	}
	return ErrUnsupported
}

// permissionHint wraps permission errors with a hint for the UI.
func permissionHint(err error) error {
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("no permission to replace the installed Lontar (%w); download the new version from the release page", err)
	}
	return err
}

// copyTo copies src into a new temporary file next to target and returns it.
func copyTo(src io.Reader, target string, mode os.FileMode) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(target), ".lontar-update-*")
	if err != nil {
		return "", permissionHint(err)
	}
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// applyAppImage replaces the AppImage file (the running one keeps its open
// inode, so this is safe while it runs).
func applyAppImage(file, target string) error {
	src, err := os.Open(file)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp, err := copyTo(src, target, 0o755)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return permissionHint(err)
	}
	return nil
}

// swap moves next into target's place, keeping the old one as target.old
// (Windows can rename, not delete, a running exe). It rolls back on failure.
func swap(next, target string) error {
	old := target + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(target, old); err != nil {
		return permissionHint(err)
	}
	if err := os.Rename(next, target); err != nil {
		_ = os.Rename(old, target)
		return permissionHint(err)
	}
	return nil
}

// applyWindows extracts lontar.exe from the portable zip and swaps it in.
func applyWindows(file, target string) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return fmt.Errorf("open update archive: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if !strings.EqualFold(filepath.Base(f.Name), "lontar.exe") {
			continue
		}
		if f.UncompressedSize64 > DefaultMaxSize {
			return ErrTooLarge
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		tmp, err := copyTo(io.LimitReader(rc, DefaultMaxSize), target, 0o755)
		rc.Close()
		if err != nil {
			return err
		}
		if err := swap(tmp, target); err != nil {
			os.Remove(tmp)
			return err
		}
		return nil
	}
	return errors.New("the update archive has no lontar.exe")
}

// applyMacOS mounts the .dmg, copies its .app next to the installed bundle
// and swaps the bundles.
func applyMacOS(ctx context.Context, run Runner, file, bundle string) error {
	mnt, err := os.MkdirTemp("", "lontar-dmg-*")
	if err != nil {
		return err
	}
	defer os.Remove(mnt)
	if _, err := run(ctx, "hdiutil", "attach", "-nobrowse", "-readonly", "-noautoopen", "-mountpoint", mnt, file); err != nil {
		return fmt.Errorf("mount update: %w", err)
	}
	defer func() { _, _ = run(context.WithoutCancel(ctx), "hdiutil", "detach", mnt, "-force") }()

	entries, err := os.ReadDir(mnt)
	if err != nil {
		return err
	}
	var app string
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".app") {
			app = filepath.Join(mnt, e.Name())
			break
		}
	}
	if app == "" {
		return errors.New("the update disk image has no .app")
	}
	next := bundle + ".new"
	_ = os.RemoveAll(next)
	if out, err := run(ctx, "ditto", app, next); err != nil {
		_ = os.RemoveAll(next)
		if bytes.Contains(out, []byte("Permission denied")) {
			return permissionHint(os.ErrPermission)
		}
		return fmt.Errorf("copy update: %w", err)
	}
	if err := swap(next, bundle); err != nil {
		_ = os.RemoveAll(next)
		return err
	}
	_ = os.RemoveAll(bundle + ".old")
	return nil
}
