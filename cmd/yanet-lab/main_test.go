package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestValidSessionName(t *testing.T) {
	for _, name := range []string{"default", "experiment-1", "lab.v2"} {
		if !validSessionName(name) {
			t.Errorf("validSessionName(%q) = false", name)
		}
	}
	for _, name := range []string{"", ".", "..", "../escape", "/tmp/lab", "two words"} {
		if validSessionName(name) {
			t.Errorf("validSessionName(%q) = true", name)
		}
	}
	// Names that would push supervisor.sock past sun_path on darwin.
	longName := strings.Repeat("a", maxSessionNameLen+1)
	if validSessionName(longName) {
		t.Errorf("validSessionName(%q) accepted, want rejected for sun_path", longName)
	}
}

// fakeFileInfo lets a test inject a synthetic os.FileInfo for any path
// the helper would normally inspect with runtimeStat. Production code
// never constructs one: it is purely a seam for owner and mode drift
// scenarios that cannot be reproduced by chmod/chown in a non-root
// process.
type fakeFileInfo struct {
	name string
	mode os.FileMode
	sys  any
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.mode&os.ModeDir != 0 }
func (f fakeFileInfo) Sys() any           { return f.sys }

// seamOverrides bundles the per-test seam overrides. A zero-value field
// leaves the corresponding production value in place.
type seamOverrides struct {
	Base   string
	Stat   func(string) (os.FileInfo, error)
	Keygen func() (string, error)
}

// withSeams overrides the package-level seams (provisioningBase,
// runtimeStat, lookupKeygen) for the duration of the test and restores
// them on cleanup. A zero-value entry in overrides leaves the
// corresponding seam at its current production value.
func withSeams(t *testing.T, o seamOverrides) {
	t.Helper()
	origBase, origStat, origFinder := provisioningBase, runtimeStat, lookupKeygen
	if o.Base != "" {
		provisioningBase = func() string { return o.Base }
	}
	if o.Stat != nil {
		runtimeStat = o.Stat
	}
	if o.Keygen != nil {
		lookupKeygen = o.Keygen
	}
	t.Cleanup(func() {
		provisioningBase, runtimeStat, lookupKeygen = origBase, origStat, origFinder
	})
}

func TestRootDigest(t *testing.T) {
	// First 12 hex chars of sha256 are an AC contract: the runtime
	// directory namespace is keyed by this prefix.
	const root = "/Users/moonug/projects/yanet/yanet2"
	got := rootDigest(root)
	want := "97985db1afa4"
	if got != want {
		t.Errorf("rootDigest(%q) = %q, want %q", root, got, want)
	}
	if len(got) != 12 {
		t.Errorf("rootDigest length = %d, want 12", len(got))
	}
	// Cross-check against a hand-computed sha256 prefix.
	sum := sha256.Sum256([]byte(root))
	want2 := hex.EncodeToString(sum[:])[:12]
	if got != want2 {
		t.Errorf("rootDigest(%q) = %q, want sha256 prefix %q", root, got, want2)
	}
}

func TestProvisionSession(t *testing.T) {
	const digest = "abc123def456"

	checkOwned := func(t *testing.T, path string, wantMode os.FileMode) {
		t.Helper()
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("Lstat(%s): %v", path, err)
		}
		if info.Mode().Perm() != wantMode {
			t.Errorf("Lstat(%s).Mode().Perm() = %#o, want %#o", path, info.Mode().Perm(), wantMode)
		}
		if got := ownerUID(info); got != os.Getuid() {
			t.Errorf("Lstat(%s) owner uid = %d, want %d", path, got, os.Getuid())
		}
	}

	t.Run("HAPPY_PATH", func(t *testing.T) {
		base := t.TempDir()
		withSeams(t, seamOverrides{Base: base})
		rt := sessionRuntimeAt(base, "default", digest)
		if _, err := provisionSessionAt(rt); err != nil {
			t.Fatalf("provisionSessionAt: %v", err)
		}
		checkOwned(t, rt.Socket, 0o600)
		checkOwned(t, rt.Lock, 0o600)
		checkOwned(t, rt.PrivateKey, 0o600)
		checkOwned(t, rt.PublicKey, 0o644)
		info, err := os.Lstat(rt.Dir)
		if err != nil {
			t.Fatalf("Lstat(%s): %v", rt.Dir, err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("Lstat(%s).Mode().Perm() = %#o, want %#o", rt.Dir, info.Mode().Perm(), 0o700)
		}
		if !info.IsDir() {
			t.Errorf("Lstat(%s).IsDir() = false, want true", rt.Dir)
		}
		pub, err := os.ReadFile(rt.PublicKey)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", rt.PublicKey, err)
		}
		if !strings.HasPrefix(string(pub), "ssh-ed25519 ") {
			t.Errorf("public key prefix = %q, want ssh-ed25519 prefix", strings.SplitN(string(pub), "\n", 2)[0])
		}
	})

	t.Run("HAPPY_PATH_REUSE", func(t *testing.T) {
		base := t.TempDir()
		withSeams(t, seamOverrides{Base: base})
		rt := sessionRuntimeAt(base, "default", digest)
		if _, err := provisionSessionAt(rt); err != nil {
			t.Fatalf("first provisionSessionAt: %v", err)
		}
		pubBefore, err := os.ReadFile(rt.PublicKey)
		if err != nil {
			t.Fatalf("ReadFile public key (before): %v", err)
		}
		if _, err := provisionSessionAt(rt); err != nil {
			t.Fatalf("second provisionSessionAt: %v", err)
		}
		pubAfter, err := os.ReadFile(rt.PublicKey)
		if err != nil {
			t.Fatalf("ReadFile public key (after): %v", err)
		}
		// Byte-for-byte equality is the contract. A regenerated ed25519
		// keypair would produce different bytes; mtime is not a reliable
		// signal on coarse-grained filesystems.
		if string(pubBefore) != string(pubAfter) {
			t.Fatalf("public key contents changed: before %q, after %q", string(pubBefore), string(pubAfter))
		}
	})

	t.Run("ERROR_OWNER", func(t *testing.T) {
		base := t.TempDir()
		rt := sessionRuntimeAt(base, "default", digest)
		if err := os.MkdirAll(rt.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(rt.Socket, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		// Lock exists per real Lstat but is reported owned by a different
		// uid via the runtimeStat seam.
		const foreignUID = 9999
		stat := func(path string) (os.FileInfo, error) {
			if filepath.Base(path) == "supervisor.lock" {
				return fakeFileInfo{
					name: "supervisor.lock",
					mode: 0o600,
					sys:  &syscall.Stat_t{Uid: foreignUID, Gid: foreignUID},
				}, nil
			}
			return os.Lstat(path)
		}
		withSeams(t, seamOverrides{Base: base, Stat: stat})
		_, err := provisionSessionAt(rt)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "expected owner uid") {
			t.Errorf("error %q does not mention expected owner", err.Error())
		}
		if !strings.Contains(err.Error(), "supervisor.lock") {
			t.Errorf("error %q does not name offending path", err.Error())
		}
	})

	t.Run("ERROR_SYMLINK_SOCKET", func(t *testing.T) {
		base := t.TempDir()
		withSeams(t, seamOverrides{Base: base})
		rt := sessionRuntimeAt(base, "default", digest)
		if err := os.MkdirAll(rt.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(base, "elsewhere"), rt.Socket); err != nil {
			t.Fatal(err)
		}
		_, err := provisionSessionAt(rt)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "symlink") {
			t.Errorf("error %q does not mention symlink", err.Error())
		}
		if !strings.Contains(err.Error(), rt.Socket) {
			t.Errorf("error %q does not name offending path", err.Error())
		}
	})

	t.Run("ERROR_SYMLINK_PRIVATE_KEY", func(t *testing.T) {
		base := t.TempDir()
		withSeams(t, seamOverrides{Base: base})
		rt := sessionRuntimeAt(base, "default", digest)
		if err := os.MkdirAll(rt.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(base, "elsewhere"), rt.PrivateKey); err != nil {
			t.Fatal(err)
		}
		_, err := provisionSessionAt(rt)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "symlink") {
			t.Errorf("error %q does not mention symlink", err.Error())
		}
		if !strings.Contains(err.Error(), rt.PrivateKey) {
			t.Errorf("error %q does not name offending path", err.Error())
		}
	})

	t.Run("ERROR_SYMLINK_PUBLIC_KEY", func(t *testing.T) {
		base := t.TempDir()
		withSeams(t, seamOverrides{Base: base})
		rt := sessionRuntimeAt(base, "default", digest)
		if err := os.MkdirAll(rt.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(base, "elsewhere"), rt.PublicKey); err != nil {
			t.Fatal(err)
		}
		_, err := provisionSessionAt(rt)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "symlink") {
			t.Errorf("error %q does not mention symlink", err.Error())
		}
		if !strings.Contains(err.Error(), rt.PublicKey) {
			t.Errorf("error %q does not name offending path", err.Error())
		}
	})

	t.Run("ERROR_MODE", func(t *testing.T) {
		base := t.TempDir()
		withSeams(t, seamOverrides{Base: base})
		rt := sessionRuntimeAt(base, "default", digest)
		if err := os.MkdirAll(rt.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(rt.Lock, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := provisionSessionAt(rt)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "wrong mode") {
			t.Errorf("error %q does not mention wrong mode", err.Error())
		}
	})

	t.Run("ERROR_TYPE", func(t *testing.T) {
		base := t.TempDir()
		withSeams(t, seamOverrides{Base: base})
		rt := sessionRuntimeAt(base, "default", digest)
		if err := os.MkdirAll(rt.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
		// Place a directory where the private key file should live so the
		// type check trips first.
		if err := os.Mkdir(rt.PrivateKey, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := provisionSessionAt(rt)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "wrong type") {
			t.Errorf("error %q does not mention wrong type", err.Error())
		}
	})

	t.Run("ERROR_NAME", func(t *testing.T) {
		base := t.TempDir()
		withSeams(t, seamOverrides{Base: base})
		_, err := provisionSession("../escape")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "invalid session name") {
			t.Errorf("error %q does not mention invalid session name", err.Error())
		}
	})

	t.Run("ERROR_KEYGEN", func(t *testing.T) {
		base := t.TempDir()
		withSeams(t, seamOverrides{
			Base: base,
			Keygen: func() (string, error) {
				return "", errors.New("ssh-keygen not in PATH")
			},
		})
		rt := sessionRuntimeAt(base, "default", digest)
		_, err := provisionSessionAt(rt)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "ssh-keygen not found") {
			t.Errorf("error %q does not mention ssh-keygen", err.Error())
		}
	})

	t.Run("REGENERATES_ON_MISSING_PUBLIC", func(t *testing.T) {
		// A previous ssh-keygen run that died after writing the private
		// key but before writing the public key leaves the runtime in a
		// partial state. ensureKeypair must detect the missing public
		// key and regenerate the pair rather than refusing forever.
		base := t.TempDir()
		withSeams(t, seamOverrides{Base: base})
		rt := sessionRuntimeAt(base, "default", digest)
		if err := os.MkdirAll(rt.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
		// Plant a private key without a matching public key.
		keygen, lookErr := lookupKeygen()
		if lookErr != nil {
			t.Skip("ssh-keygen not available; cannot plant partial state")
		}
		ctx, cancel := context.WithTimeout(context.Background(), sshKeygenTimeout)
		defer cancel()
		command := exec.CommandContext(ctx, keygen, "-t", "ed25519", "-N", "", "-f", rt.PrivateKey)
		if _, err := command.CombinedOutput(); err != nil {
			t.Fatalf("plant ssh-keygen: %v", err)
		}
		if err := os.Remove(rt.PublicKey); err != nil {
			t.Fatalf("remove planted public key: %v", err)
		}
		// Now run provision; it must regenerate the pair.
		if _, err := provisionSessionAt(rt); err != nil {
			t.Fatalf("provisionSessionAt: %v", err)
		}
		pub, err := os.ReadFile(rt.PublicKey)
		if err != nil {
			t.Fatalf("ReadFile public key: %v", err)
		}
		if !strings.HasPrefix(string(pub), "ssh-ed25519 ") {
			t.Errorf("regenerated public key prefix = %q, want ssh-ed25519 prefix", strings.SplitN(string(pub), "\n", 2)[0])
		}
	})
}

// TestProvisionSessionEndToEnd drives the full path layout
// (provisioningBase + projectRoot + rootDigest + name) and asserts the
// literal AC contract: /tmp/yanet2-lab-<uid>/<root-digest>/<name>/...
func TestProvisionSessionEndToEnd(t *testing.T) {
	// Use a temp base to keep the test hermetic and assert the layout
	// independent of the real /tmp tree.
	base := t.TempDir()
	withSeams(t, seamOverrides{Base: base})
	// Plant a synthetic project root: a real directory with a go.mod.
	// Resolve symlinks (macOS /var -> /private/var) so the projectRoot
	// canonical check passes.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	prevStart := walkStart
	t.Cleanup(func() { walkStart = prevStart })
	walkStart = func() (string, error) { return root, nil }
	resetProjectRootCache()

	rt, err := provisionSession("default")
	if err != nil {
		t.Fatalf("provisionSession: %v", err)
	}
	wantDir := filepath.Join(base, rootDigest(root), "default")
	if rt.Dir != wantDir {
		t.Errorf("rt.Dir = %q, want %q", rt.Dir, wantDir)
	}
	if rt.Socket != filepath.Join(wantDir, "supervisor.sock") {
		t.Errorf("rt.Socket = %q, want %q", rt.Socket, filepath.Join(wantDir, "supervisor.sock"))
	}
	if _, err := os.Stat(rt.Dir); err != nil {
		t.Errorf("runtime dir not on disk after provisionSession: %v", err)
	}
}

func TestSessionNameSunPathCap(t *testing.T) {
	long := strings.Repeat("a", maxSessionNameLen+1)
	if validSessionName(long) {
		t.Errorf("validSessionName(%d-char) = true, want false", len(long))
	}
}

func TestProjectRootSymlinkTamper(t *testing.T) {
	realDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(realDir, "go.mod"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "lab")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	prevStart := walkStart
	t.Cleanup(func() { walkStart = prevStart })
	walkStart = func() (string, error) { return link, nil }
	resetProjectRootCache()
	if _, err := projectRoot(); err == nil {
		t.Fatal("expected error, got nil")
	} else if !strings.Contains(err.Error(), "project root is not canonical") {
		t.Errorf("error %q does not mention canonical", err.Error())
	}
}

func TestProjectRootNoGoMod(t *testing.T) {
	prevStart := walkStart
	t.Cleanup(func() { walkStart = prevStart })
	walkStart = func() (string, error) { return t.TempDir(), nil }
	resetProjectRootCache()
	if _, err := projectRoot(); err == nil {
		t.Fatal("expected error, got nil")
	} else if !strings.Contains(err.Error(), "not inside the YANET2 repository") {
		t.Errorf("error %q does not mention YANET2", err.Error())
	}
}
