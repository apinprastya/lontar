package update

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"26.11.2", "26.11.1", true},
		{"v26.12.1", "26.11.9", true},
		{"27.1.1", "26.12.3", true},
		{"26.11.1", "26.11.1", false},
		{"26.11.1", "26.11.2", false},
		{"26.11.2", "26.11.2-rc.1", true},
		{"26.11.2", "26.11.2-3-gabc1234", false},
		{"26.11.2", "26.11.2-3-gabc1234-dirty", false},
		{"26.11.3", "26.11.2-3-gabc1234", true},
		{"26.11.2-rc.1", "26.11.1", false}, // pre-releases are never offered
		{"garbage", "26.11.1", false},
		{"26.11.1", "0.0.0-dev", true},
		{"26.11.1", "0.0.0-dev+abc", true},
		{"26.11.1", "dev", true},
	}
	for _, c := range cases {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestIsDev(t *testing.T) {
	for v, want := range map[string]bool{"0.0.0-dev": true, "0.0.0-dev+abc": true, "x": true, "26.11.1": false, "26.11.1-rc.1": false} {
		if IsDev(v) != want {
			t.Errorf("IsDev(%q) != %v", v, want)
		}
	}
}

func TestSumFor(t *testing.T) {
	h := strings.Repeat("ab", 32)
	sums := []byte("garbage\n" + h + "  a.zip\r\n" + strings.Repeat("CD", 32) + " *b.dmg\nshort  c\n")
	if got, ok := SumFor(sums, "a.zip"); !ok || got != h {
		t.Fatalf("a.zip = %q %v", got, ok)
	}
	if got, ok := SumFor(sums, "b.dmg"); !ok || got != strings.Repeat("cd", 32) {
		t.Fatalf("b.dmg = %q %v", got, ok)
	}
	if _, ok := SumFor(sums, "c"); ok {
		t.Fatal("short hash accepted")
	}
	if _, ok := SumFor(sums, "missing"); ok {
		t.Fatal("missing found")
	}
}

func newKey(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pub), priv
}

func sign(priv ed25519.PrivateKey, b []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, b)) + "\n")
}

func TestVerifySums(t *testing.T) {
	pub, priv := newKey(t)
	sums := []byte("data")
	if err := VerifySums(pub, sums, sign(priv, sums)); err != nil {
		t.Fatal(err)
	}
	if err := VerifySums(pub, []byte("other"), sign(priv, sums)); !errors.Is(err, ErrSignature) {
		t.Fatalf("tampered: %v", err)
	}
	if err := VerifySums(pub, sums, []byte("!!")); !errors.Is(err, ErrSignature) {
		t.Fatalf("bad sig: %v", err)
	}
	if err := VerifySums("short", sums, sign(priv, sums)); err == nil || errors.Is(err, ErrSignature) {
		t.Fatalf("bad key: %v", err)
	}
}

func TestDetect(t *testing.T) {
	env := func(k, v string) func(string) string {
		return func(n string) string {
			if n == k {
				return v
			}
			return ""
		}
	}
	cases := []struct {
		env    Env
		kind   Kind
		target string
	}{
		{Env{GOOS: "linux", GOARCH: "amd64", Getenv: env("APPIMAGE", "/home/u/Lontar.AppImage")}, KindAppImage, "/home/u/Lontar.AppImage"},
		{Env{GOOS: "linux", GOARCH: "arm64", Getenv: env("APPIMAGE", "/x")}, KindManual, ""},
		{Env{GOOS: "linux", GOARCH: "amd64", Executable: "/usr/bin/lontar"}, KindManual, ""},
		{Env{GOOS: "linux", GOARCH: "amd64", Executable: "/home/u/lontar"}, KindManual, ""},
		{Env{GOOS: "windows", GOARCH: "amd64", Executable: `C:\x\lontar.exe`}, KindWindows, `C:\x\lontar.exe`},
		{Env{GOOS: "windows", GOARCH: "arm64", Executable: `C:\x\lontar.exe`}, KindManual, ""},
		{Env{GOOS: "darwin", Executable: "/Applications/Lontar.app/Contents/MacOS/lontar"}, KindMacOS, "/Applications/Lontar.app"},
		{Env{GOOS: "darwin", Executable: "/tmp/lontar"}, KindManual, ""},
		{Env{GOOS: "freebsd"}, KindManual, ""},
	}
	for _, c := range cases {
		got := Detect(c.env)
		if got.Kind != c.kind || got.Target != c.target {
			t.Errorf("Detect(%+v) = %+v", c.env, got)
		}
		if got.Kind == KindManual && got.Reason == "" {
			t.Errorf("Detect(%+v): manual without reason", c.env)
		}
	}
}

func TestAssetNameAndRestart(t *testing.T) {
	if AssetName(KindAppImage, "1.2.3") != "lontar-1.2.3-linux-x86_64.AppImage" ||
		AssetName(KindWindows, "1.2.3") != "lontar-1.2.3-windows-amd64-portable.zip" ||
		AssetName(KindMacOS, "1.2.3") != "lontar-1.2.3-macos-universal.dmg" ||
		AssetName(KindManual, "1.2.3") != "" {
		t.Fatal("asset names")
	}
	name, args := Installation{Kind: KindMacOS, Target: "/A/L.app"}.RestartCommand()
	if name != "open" || len(args) != 2 || args[1] != "/A/L.app" {
		t.Fatal(name, args)
	}
	name, args = Installation{Kind: KindWindows, Target: "x.exe"}.RestartCommand()
	if name != "x.exe" || args != nil {
		t.Fatal(name, args)
	}
}

func TestCleanup(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "lontar.exe")
	for _, p := range []string{target + ".old", target + ".new"} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	Installation{Kind: KindWindows, Target: target}.Cleanup()
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Fatal(".old not removed")
	}
	if _, err := os.Stat(target + ".new"); !os.IsNotExist(err) {
		t.Fatal(".new not removed")
	}
}

// fakeRelease serves a release over TLS with the given asset contents.
type fakeRelease struct {
	srv     *httptest.Server
	files   map[string][]byte
	release Release
	status  int // for /latest
}

func newFakeRelease(t *testing.T, version string, files map[string][]byte) *fakeRelease {
	t.Helper()
	f := &fakeRelease{files: files, status: http.StatusOK}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("no User-Agent")
		}
		if r.URL.Path == "/latest" {
			if f.status != http.StatusOK {
				w.WriteHeader(f.status)
				return
			}
			_ = json.NewEncoder(w).Encode(f.release)
			return
		}
		b, ok := f.files[strings.TrimPrefix(r.URL.Path, "/dl/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(f.srv.Close)
	f.release = Release{Tag: "v" + version, Name: "Lontar " + version, URL: "https://github.com/x/releases/v" + version}
	for name, b := range files {
		f.release.Assets = append(f.release.Assets, Asset{Name: name, URL: f.srv.URL + "/dl/" + name, Size: int64(len(b))})
	}
	return f
}

func (f *fakeRelease) updater(pub string, inst Installation, tmp string) *Updater {
	return &Updater{Client: f.srv.Client(), APIURL: f.srv.URL + "/latest", PublicKey: pub, Install: inst, TempDir: tmp}
}

// signedFiles returns assets plus a signed SHA256SUMS over them.
func signedFiles(priv ed25519.PrivateKey, assets map[string][]byte) map[string][]byte {
	var sums bytes.Buffer
	out := map[string][]byte{}
	for name, b := range assets {
		h := sha256.Sum256(b)
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(h[:]), name)
		out[name] = b
	}
	out[SumsName] = sums.Bytes()
	out[SigName] = sign(priv, sums.Bytes())
	return out
}

func TestLatest(t *testing.T) {
	f := newFakeRelease(t, "26.11.2", nil)
	u := f.updater("", Installation{}, "")
	r, err := u.Latest(context.Background())
	if err != nil || r.Version() != "26.11.2" {
		t.Fatalf("Latest = %+v, %v", r, err)
	}
	f.release.Prerelease = true
	if _, err := u.Latest(context.Background()); err == nil {
		t.Fatal("pre-release accepted")
	}
	f.status = http.StatusNotFound
	if _, err := u.Latest(context.Background()); err == nil || !strings.Contains(err.Error(), "no Lontar release") {
		t.Fatalf("404: %v", err)
	}
	f.status = http.StatusInternalServerError
	if _, err := u.Latest(context.Background()); err == nil {
		t.Fatal("500 accepted")
	}
	if _, err := (&Updater{APIURL: "http://insecure"}).Latest(context.Background()); err == nil || !strings.Contains(err.Error(), "non-HTTPS") {
		t.Fatalf("http: %v", err)
	}
}

func TestLatestBadJSON(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("{")) }))
	defer srv.Close()
	u := &Updater{Client: srv.Client(), APIURL: srv.URL}
	if _, err := u.Latest(context.Background()); err == nil {
		t.Fatal("bad JSON accepted")
	}
}

func TestDownloadAndApplyAppImage(t *testing.T) {
	pub, priv := newKey(t)
	payload := []byte("new appimage")
	name := AssetName(KindAppImage, "26.11.2")
	f := newFakeRelease(t, "26.11.2", signedFiles(priv, map[string][]byte{name: payload}))

	dir := t.TempDir()
	target := filepath.Join(dir, "Lontar.AppImage")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	u := f.updater(pub, Installation{Kind: KindAppImage, Target: target}, t.TempDir())
	var last int64
	file, err := u.Download(context.Background(), f.release, func(done, total int64) {
		last = done
		if total != int64(len(payload)) {
			t.Errorf("total = %d", total)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if last != int64(len(payload)) {
		t.Fatalf("progress ended at %d", last)
	}
	if err := u.Apply(context.Background(), file); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != string(payload) {
		t.Fatalf("target = %q", got)
	}
	// Windows has no POSIX exec bit; Stat never reports it there.
	if fi, _ := os.Stat(target); runtime.GOOS != "windows" && fi.Mode().Perm()&0o100 == 0 {
		t.Fatal("not executable")
	}
}

func TestDownloadRejects(t *testing.T) {
	pub, priv := newKey(t)
	otherPub, _ := newKey(t)
	name := AssetName(KindAppImage, "26.11.2")
	inst := Installation{Kind: KindAppImage, Target: "/nowhere"}
	ctx := context.Background()

	t.Run("manual", func(t *testing.T) {
		f := newFakeRelease(t, "26.11.2", nil)
		if _, err := f.updater(pub, Installation{Kind: KindManual}, "").Download(ctx, f.release, nil); !errors.Is(err, ErrUnsupported) {
			t.Fatal(err)
		}
	})
	t.Run("no key", func(t *testing.T) {
		f := newFakeRelease(t, "26.11.2", nil)
		if _, err := f.updater(" ", inst, "").Download(ctx, f.release, nil); !errors.Is(err, ErrNoKey) {
			t.Fatal(err)
		}
	})
	t.Run("no asset", func(t *testing.T) {
		f := newFakeRelease(t, "26.11.2", signedFiles(priv, map[string][]byte{"other": nil}))
		if _, err := f.updater(pub, inst, "").Download(ctx, f.release, nil); err == nil || !strings.Contains(err.Error(), "has no") {
			t.Fatal(err)
		}
	})
	t.Run("no sums", func(t *testing.T) {
		f := newFakeRelease(t, "26.11.2", map[string][]byte{name: []byte("x")})
		if _, err := f.updater(pub, inst, "").Download(ctx, f.release, nil); err == nil || !strings.Contains(err.Error(), SumsName) {
			t.Fatal(err)
		}
	})
	t.Run("no sig", func(t *testing.T) {
		files := signedFiles(priv, map[string][]byte{name: []byte("x")})
		delete(files, SigName)
		f := newFakeRelease(t, "26.11.2", files)
		if _, err := f.updater(pub, inst, "").Download(ctx, f.release, nil); err == nil || !strings.Contains(err.Error(), SigName) {
			t.Fatal(err)
		}
	})
	t.Run("wrong key", func(t *testing.T) {
		f := newFakeRelease(t, "26.11.2", signedFiles(priv, map[string][]byte{name: []byte("x")}))
		if _, err := f.updater(otherPub, inst, "").Download(ctx, f.release, nil); !errors.Is(err, ErrSignature) {
			t.Fatal(err)
		}
	})
	t.Run("not listed", func(t *testing.T) {
		files := signedFiles(priv, map[string][]byte{"other": nil})
		files[name] = []byte("x")
		f := newFakeRelease(t, "26.11.2", files)
		if _, err := f.updater(pub, inst, "").Download(ctx, f.release, nil); err == nil || !strings.Contains(err.Error(), "does not list") {
			t.Fatal(err)
		}
	})
	t.Run("tampered", func(t *testing.T) {
		files := signedFiles(priv, map[string][]byte{name: []byte("good")})
		files[name] = []byte("evil")
		f := newFakeRelease(t, "26.11.2", files)
		tmp := t.TempDir()
		if _, err := f.updater(pub, inst, tmp).Download(ctx, f.release, nil); !errors.Is(err, ErrChecksum) {
			t.Fatal(err)
		}
		if left, _ := os.ReadDir(tmp); len(left) != 0 {
			t.Fatalf("temp file left: %v", left)
		}
	})
	t.Run("too large", func(t *testing.T) {
		f := newFakeRelease(t, "26.11.2", signedFiles(priv, map[string][]byte{name: []byte("12345")}))
		u := f.updater(pub, inst, t.TempDir())
		u.MaxSize = 4
		if _, err := u.Download(ctx, f.release, nil); !errors.Is(err, ErrTooLarge) {
			t.Fatal(err)
		}
		// The listed size lies: the stream is still capped.
		for i := range f.release.Assets {
			f.release.Assets[i].Size = 1
		}
		if _, err := u.Download(ctx, f.release, nil); !errors.Is(err, ErrTooLarge) {
			t.Fatal(err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		f := newFakeRelease(t, "26.11.2", signedFiles(priv, map[string][]byte{name: []byte("x")}))
		c, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := f.updater(pub, inst, "").Download(c, f.release, nil); err == nil {
			t.Fatal("cancelled download succeeded")
		}
	})
}

func zipWith(t *testing.T, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "u.zip")
	out, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(out)
	w, _ := zw.Create(name)
	_, _ = w.Write(content)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	out.Close()
	return p
}

func TestApplyWindows(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "lontar.exe")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	u := &Updater{Install: Installation{Kind: KindWindows, Target: target}}
	if err := u.Apply(context.Background(), zipWith(t, "Lontar.EXE", []byte("new"))); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != "new" {
		t.Fatalf("target = %q", b)
	}
	if b, _ := os.ReadFile(target + ".old"); string(b) != "old" {
		t.Fatalf("old = %q", b)
	}

	if err := u.Apply(context.Background(), zipWith(t, "readme.txt", nil)); err == nil || !strings.Contains(err.Error(), "no lontar.exe") {
		t.Fatal(err)
	}
	notZip := filepath.Join(dir, "x.zip")
	_ = os.WriteFile(notZip, []byte("nope"), 0o644)
	if err := u.Apply(context.Background(), notZip); err == nil {
		t.Fatal("bad zip accepted")
	}
	// Missing target: the swap fails and nothing is left behind.
	u.Install.Target = filepath.Join(dir, "missing", "lontar.exe")
	if err := u.Apply(context.Background(), zipWith(t, "lontar.exe", nil)); err == nil {
		t.Fatal("missing target accepted")
	}
}

func TestApplyAppImageErrors(t *testing.T) {
	u := &Updater{Install: Installation{Kind: KindAppImage, Target: filepath.Join(t.TempDir(), "missing-dir", "x")}}
	src := filepath.Join(t.TempDir(), "src")
	_ = os.WriteFile(src, []byte("x"), 0o644)
	if err := u.Apply(context.Background(), src); err == nil {
		t.Fatal("missing dir accepted")
	}
	u.Install.Target = filepath.Join(t.TempDir(), "x")
	if err := u.Apply(context.Background(), filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("missing source accepted")
	}
	if err := (&Updater{Install: Installation{Kind: KindManual}}).Apply(context.Background(), src); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestPermissionHint(t *testing.T) {
	err := permissionHint(os.ErrPermission)
	if !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), "release page") {
		t.Fatal(err)
	}
	plain := errors.New("x")
	if permissionHint(plain) != plain {
		t.Fatal("non-permission error wrapped")
	}
}

// fakeMac simulates hdiutil (attach creates an .app in the mount point) and
// ditto (copies a directory).
type fakeMac struct {
	calls    []string
	app      string // "" mounts an empty image
	failOn   string
	dittoOut string
}

func (m *fakeMac) run(_ context.Context, name string, args ...string) ([]byte, error) {
	m.calls = append(m.calls, name+" "+args[0])
	if m.failOn == name+" "+args[0] {
		return []byte(m.dittoOut), errors.New("failed")
	}
	switch {
	case name == "hdiutil" && args[0] == "attach":
		mnt := args[len(args)-2]
		if m.app != "" {
			if err := os.MkdirAll(filepath.Join(mnt, m.app, "Contents"), 0o755); err != nil {
				return nil, err
			}
			return nil, os.WriteFile(filepath.Join(mnt, m.app, "Contents", "v"), []byte("new"), 0o644)
		}
	case name == "hdiutil" && args[0] == "detach":
		entries, _ := os.ReadDir(args[1])
		for _, e := range entries {
			_ = os.RemoveAll(filepath.Join(args[1], e.Name()))
		}
	case name == "ditto":
		return nil, os.CopyFS(args[1], os.DirFS(args[0]))
	}
	return nil, nil
}

func TestApplyMacOS(t *testing.T) {
	newBundle := func(t *testing.T) string {
		b := filepath.Join(t.TempDir(), "Lontar.app")
		_ = os.MkdirAll(filepath.Join(b, "Contents"), 0o755)
		_ = os.WriteFile(filepath.Join(b, "Contents", "v"), []byte("old"), 0o644)
		return b
	}
	ctx := context.Background()

	t.Run("ok", func(t *testing.T) {
		b := newBundle(t)
		m := &fakeMac{app: "Lontar.app"}
		u := &Updater{Install: Installation{Kind: KindMacOS, Target: b}, Run: m.run}
		if err := u.Apply(ctx, "x.dmg"); err != nil {
			t.Fatal(err)
		}
		if v, _ := os.ReadFile(filepath.Join(b, "Contents", "v")); string(v) != "new" {
			t.Fatalf("bundle = %q", v)
		}
		if _, err := os.Stat(b + ".old"); !os.IsNotExist(err) {
			t.Fatal(".old left")
		}
		if m.calls[len(m.calls)-1] != "hdiutil detach" {
			t.Fatalf("not detached: %v", m.calls)
		}
	})
	t.Run("mount fails", func(t *testing.T) {
		m := &fakeMac{failOn: "hdiutil attach"}
		err := (&Updater{Install: Installation{Kind: KindMacOS, Target: newBundle(t)}, Run: m.run}).Apply(ctx, "x.dmg")
		if err == nil || !strings.Contains(err.Error(), "mount") {
			t.Fatal(err)
		}
	})
	t.Run("no app", func(t *testing.T) {
		m := &fakeMac{}
		err := (&Updater{Install: Installation{Kind: KindMacOS, Target: newBundle(t)}, Run: m.run}).Apply(ctx, "x.dmg")
		if err == nil || !strings.Contains(err.Error(), "no .app") {
			t.Fatal(err)
		}
	})
	t.Run("copy fails", func(t *testing.T) {
		m := &fakeMac{app: "Lontar.app", dittoOut: "Permission denied"}
		run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if name == "ditto" {
				return []byte(m.dittoOut), errors.New("failed")
			}
			return m.run(ctx, name, args...)
		}
		b := newBundle(t)
		err := (&Updater{Install: Installation{Kind: KindMacOS, Target: b}, Run: run}).Apply(ctx, "x.dmg")
		if !errors.Is(err, os.ErrPermission) {
			t.Fatal(err)
		}
		m.dittoOut = "other"
		err = (&Updater{Install: Installation{Kind: KindMacOS, Target: b}, Run: run}).Apply(ctx, "x.dmg")
		if err == nil || !strings.Contains(err.Error(), "copy update") {
			t.Fatal(err)
		}
		if v, _ := os.ReadFile(filepath.Join(b, "Contents", "v")); string(v) != "old" {
			t.Fatal("bundle changed")
		}
	})
	t.Run("swap fails", func(t *testing.T) {
		m := &fakeMac{app: "Lontar.app"}
		missing := filepath.Join(t.TempDir(), "Lontar.app") // nothing to move aside
		err := (&Updater{Install: Installation{Kind: KindMacOS, Target: missing}, Run: m.run}).Apply(ctx, "x.dmg")
		if err == nil {
			t.Fatal("swap of missing bundle succeeded")
		}
		if _, serr := os.Stat(missing + ".new"); !os.IsNotExist(serr) {
			t.Fatal(".new left")
		}
	})
}

func TestExecRunner(t *testing.T) {
	if _, err := ExecRunner(context.Background(), "go", "version"); err != nil {
		t.Fatal(err)
	}
	if _, err := ExecRunner(context.Background(), "go", "no-such-command"); err == nil {
		t.Fatal("failing command succeeded")
	}
}
