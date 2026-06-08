package paths

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// withEnv sets an env var for the duration of a test and restores it afterward.
// t.Setenv already does this, but t.Setenv forbids parallel tests and panics if
// the var was set by the parent process; this helper additionally lets us model
// an *unset* var, which several cases below depend on.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	prev, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

// TestResolveHonorsDataDirEnv verifies the highest-priority resolution branch:
// MYKEEP_DATA_DIR is used verbatim, the directory is created, and the layout is
// reported as portable.
func TestResolveHonorsDataDirEnv(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "explicit", "data")

	t.Setenv("MYKEEP_DATA_DIR", dataDir)

	layout, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if layout.DataDir != dataDir {
		t.Errorf("DataDir = %q, want %q", layout.DataDir, dataDir)
	}
	if !layout.Portable {
		t.Errorf("Portable = false, want true when MYKEEP_DATA_DIR is set")
	}

	// Resolve must have created the directory (MkdirAll with 0o700).
	info, err := os.Stat(dataDir)
	if err != nil {
		t.Fatalf("stat data dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", dataDir)
	}
}

// TestResolveDataDirEnvCreatesNestedDirs confirms intermediate parents are made.
func TestResolveDataDirEnvCreatesNestedDirs(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "a", "b", "c", "data")
	t.Setenv("MYKEEP_DATA_DIR", dataDir)

	layout, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := os.Stat(layout.DataDir); err != nil {
		t.Fatalf("nested data dir not created: %v", err)
	}
}

// TestResolveDataDirEnvUnwritableParent exercises the error path of the env
// branch: MkdirAll fails when a parent component is a non-writable directory, so
// Resolve must surface the error rather than silently falling back.
func TestResolveDataDirEnvUnwritableParent(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root bypasses directory permission checks")
	}
	root := t.TempDir()
	ro := filepath.Join(root, "ro")
	if err := os.Mkdir(ro, 0o500); err != nil {
		t.Fatalf("mkdir ro: %v", err)
	}
	// Restore perms so t.TempDir cleanup can remove the tree.
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })

	// Attempt to create a child under the read-only directory.
	dataDir := filepath.Join(ro, "child", "data")
	t.Setenv("MYKEEP_DATA_DIR", dataDir)

	if _, err := Resolve(); err == nil {
		t.Fatalf("Resolve succeeded, want error for unwritable parent %q", ro)
	}
}

// TestResolveFallsBackToHostConfigDir drives the host-fallback branch. We unset
// MYKEEP_DATA_DIR so resolution proceeds to binaryDir(). Under `go test` the
// test binary lives in os.TempDir(), so binaryDir() reports ok=false and Resolve
// must land on os.UserConfigDir()/mykeep/data with Portable=false. We redirect
// the host config dir into a TempDir via XDG_CONFIG_HOME to avoid touching the
// real one.
func TestResolveFallsBackToHostConfigDir(t *testing.T) {
	unsetEnv(t, "MYKEEP_DATA_DIR")
	unsetEnv(t, "MYKEEP_DEV")

	cfgRoot := t.TempDir()
	// os.UserConfigDir honors XDG_CONFIG_HOME on Linux.
	t.Setenv("XDG_CONFIG_HOME", cfgRoot)

	layout, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// The test binary is in TempDir, so we expect the non-portable host fallback.
	if exe, _ := os.Executable(); strings.HasPrefix(exe, os.TempDir()) {
		want := filepath.Join(cfgRoot, "mykeep", "mykeep_kb")
		if layout.DataDir != want {
			t.Errorf("DataDir = %q, want host fallback %q", layout.DataDir, want)
		}
		if layout.Portable {
			t.Errorf("Portable = true, want false for host fallback")
		}
		if _, err := os.Stat(layout.DataDir); err != nil {
			t.Errorf("host fallback dir not created: %v", err)
		}
	} else {
		// Defensive: if some environment runs the test binary off TempDir, we
		// can't assert the fallback, but Resolve must still succeed.
		t.Logf("test binary not under TempDir (%q); skipping fallback assertion", layout.DataDir)
	}
}

// TestConfigAndDBPaths checks the path-joining helpers against the known
// filenames.
func TestConfigAndDBPaths(t *testing.T) {
	dataDir := t.TempDir()
	l := Layout{DataDir: dataDir}

	if got, want := l.ConfigPath(), filepath.Join(dataDir, configName); got != want {
		t.Errorf("ConfigPath() = %q, want %q", got, want)
	}
	if got, want := l.DBPath(), filepath.Join(dataDir, dbName); got != want {
		t.Errorf("DBPath() = %q, want %q", got, want)
	}

	// Sanity: the constants match the documented on-disk names.
	if filepath.Base(l.ConfigPath()) != "mykeep.config.json" {
		t.Errorf("config base = %q, want mykeep.config.json", filepath.Base(l.ConfigPath()))
	}
	if filepath.Base(l.DBPath()) != "mykeep.db.enc" {
		t.Errorf("db base = %q, want mykeep.db.enc", filepath.Base(l.DBPath()))
	}
}

// TestIsFirstLaunch covers both states: config absent (first launch) and config
// present (returning drive).
func TestIsFirstLaunch(t *testing.T) {
	dataDir := t.TempDir()
	l := Layout{DataDir: dataDir}

	if !l.IsFirstLaunch() {
		t.Fatalf("IsFirstLaunch() = false on empty data dir, want true")
	}

	if err := os.WriteFile(l.ConfigPath(), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if l.IsFirstLaunch() {
		t.Fatalf("IsFirstLaunch() = true after config written, want false")
	}
}

// TestIsFirstLaunchPermissionError documents a subtle property: IsFirstLaunch
// uses errors.Is(err, os.ErrNotExist), so a stat error that is NOT
// "does not exist" (e.g. a permission error reaching the file) reports false —
// i.e. it does not treat an unreadable directory as a fresh drive.
func TestIsFirstLaunchPermissionError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root bypasses directory permission checks")
	}
	parent := t.TempDir()
	dataDir := filepath.Join(parent, "locked")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Remove search permission on the data dir so Stat of a child fails with
	// EACCES rather than ENOENT.
	if err := os.Chmod(dataDir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dataDir, 0o700) })

	l := Layout{DataDir: dataDir}
	if l.IsFirstLaunch() {
		t.Errorf("IsFirstLaunch() = true on permission error, want false (not ErrNotExist)")
	}
}

// TestWritable exercises the writable probe helper directly.
func TestWritable(t *testing.T) {
	t.Run("creates and probes a fresh dir", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "fresh", "data")
		if !writable(dir) {
			t.Fatalf("writable(%q) = false, want true", dir)
		}
		// The probe file must be cleaned up.
		if _, err := os.Stat(filepath.Join(dir, ".mykeep-write-probe")); !os.IsNotExist(err) {
			t.Errorf("write-probe left behind: err=%v", err)
		}
	})

	t.Run("read-only parent", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("running as root bypasses directory permission checks")
		}
		root := t.TempDir()
		ro := filepath.Join(root, "ro")
		if err := os.Mkdir(ro, 0o500); err != nil {
			t.Fatalf("mkdir ro: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })

		// MkdirAll of a child under a 0o500 dir fails -> not writable.
		if writable(filepath.Join(ro, "child")) {
			t.Errorf("writable under read-only parent = true, want false")
		}
	})

	t.Run("existing read-only dir blocks probe write", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("running as root bypasses directory permission checks")
		}
		dir := filepath.Join(t.TempDir(), "exists")
		if err := os.Mkdir(dir, 0o500); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

		// The dir already exists (MkdirAll is a no-op) but creating the probe
		// file fails because the dir is read-only.
		if writable(dir) {
			t.Errorf("writable(read-only existing dir) = true, want false")
		}
	})
}

// TestBinaryDirTempExeFallback verifies that under `go test` — where the test
// binary is written into os.TempDir() — binaryDir reports ok=false so Resolve
// does not treat the temp executable as living on the USB stick.
func TestBinaryDirTempExeFallback(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable unavailable: %v", err)
	}
	if !strings.HasPrefix(exe, os.TempDir()) {
		t.Skipf("test binary %q is not under TempDir; cannot assert temp-exe path", exe)
	}
	unsetEnv(t, "MYKEEP_DEV")

	if dir, ok := binaryDir(); ok {
		t.Errorf("binaryDir() = (%q, true) for a temp-dir exe, want ok=false", dir)
	}
}

// TestBinaryDirDevOverride verifies MYKEEP_DEV forces binaryDir to report
// not-on-stick regardless of the executable's location.
func TestBinaryDirDevOverride(t *testing.T) {
	t.Setenv("MYKEEP_DEV", "1")
	if dir, ok := binaryDir(); ok {
		t.Errorf("binaryDir() = (%q, true) with MYKEEP_DEV set, want ok=false", dir)
	}
}

// TestResolveDevOverrideUsesHostFallback ties the MYKEEP_DEV override to the
// full Resolve flow: with no MYKEEP_DATA_DIR and MYKEEP_DEV set, binaryDir is
// skipped and Resolve must use the non-portable host config dir.
func TestResolveDevOverrideUsesHostFallback(t *testing.T) {
	unsetEnv(t, "MYKEEP_DATA_DIR")
	t.Setenv("MYKEEP_DEV", "1")

	cfgRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgRoot)

	layout, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := filepath.Join(cfgRoot, "mykeep", "mykeep_kb")
	if layout.DataDir != want {
		t.Errorf("DataDir = %q, want %q", layout.DataDir, want)
	}
	if layout.Portable {
		t.Errorf("Portable = true, want false for host fallback")
	}
}

// helperSrc is a minimal program that calls Resolve() and prints the result. It
// is compiled into a synthetic mykeep/bin/<os>-<arch>/ layout so we can observe
// binaryDir()'s real walk-up behavior from a genuine on-disk executable, which
// is impossible to do in-process because binaryDir reads os.Executable().
const helperSrc = `package main

import (
	"fmt"

	"mykeep.ai/internal/paths"
)

func main() {
	l, err := paths.Resolve()
	if err != nil {
		fmt.Printf("ERR %v\n", err)
		return
	}
	fmt.Printf("DATADIR %s\n", l.DataDir)
	fmt.Printf("PORTABLE %v\n", l.Portable)
}
`

// buildHelper compiles helperSrc into outPath. The source is staged inside the
// module tree (under a temp dir within the repo) so the mykeep.ai import
// resolves against the local module.
func buildHelper(t *testing.T, outPath string) {
	t.Helper()

	// Stage source inside the module so the local import resolves.
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs repo root: %v", err)
	}
	stage, err := os.MkdirTemp(repoRoot, "pathshelper-")
	if err != nil {
		t.Fatalf("mkdtemp in repo: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stage) })

	srcFile := filepath.Join(stage, "main.go")
	if err := os.WriteFile(srcFile, []byte(helperSrc), 0o600); err != nil {
		t.Fatalf("write helper src: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o700); err != nil {
		t.Fatalf("mkdir helper out: %v", err)
	}

	cmd := exec.Command("go", "build", "-o", outPath, srcFile)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build helper: %v\n%s", err, out)
	}
}

// runHelper runs the compiled helper with the given env and returns parsed
// (dataDir, portable).
func runHelper(t *testing.T, exePath string, env []string) (string, string) {
	t.Helper()
	cmd := exec.Command(exePath)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run helper: %v\n%s", err, out)
	}
	var dataDir, portable string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		switch {
		case strings.HasPrefix(line, "DATADIR "):
			dataDir = strings.TrimPrefix(line, "DATADIR ")
		case strings.HasPrefix(line, "PORTABLE "):
			portable = strings.TrimPrefix(line, "PORTABLE ")
		case strings.HasPrefix(line, "ERR "):
			t.Fatalf("helper Resolve error: %s", strings.TrimPrefix(line, "ERR "))
		}
	}
	if dataDir == "" {
		t.Fatalf("helper produced no DATADIR; output:\n%s", out)
	}
	return dataDir, portable
}

// scratchOutsideTemp creates a scratch directory that is NOT under os.TempDir(),
// because binaryDir() treats any exe under TempDir as a non-portable go-run
// binary. We place it under $HOME so the walk-up branch is reachable.
func scratchOutsideTemp(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	if strings.HasPrefix(home, os.TempDir()) {
		t.Skipf("home %q is under TempDir; cannot stage a non-temp exe", home)
	}
	dir, err := os.MkdirTemp(home, "mykeep-pathtest-")
	if err != nil {
		t.Skipf("cannot create scratch under home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// baseEnv returns the current environment with MYKEEP_* vars stripped so the
// helper starts from a clean resolution state.
func baseEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "MYKEEP_DATA_DIR=") || strings.HasPrefix(kv, "MYKEEP_DEV=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// TestBinaryDirDataBesideExe exercises the real drive layout: a platform-named
// binary at the drive root resolves its data dir to mykeep_kb/ sitting beside it,
// and reports Portable=true. All six platform binaries share this one mykeep_kb/.
func TestBinaryDirDataBesideExe(t *testing.T) {
	driveRoot := scratchOutsideTemp(t)
	platform := runtime.GOOS + "-" + runtime.GOARCH
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	exePath := filepath.Join(driveRoot, "mykeep-"+platform+ext)

	buildHelper(t, exePath)

	dataDir, portable := runHelper(t, exePath, baseEnv())

	want := filepath.Join(driveRoot, "mykeep_kb")
	if dataDir != want {
		t.Errorf("DataDir = %q, want %q (mykeep_kb beside the binary)", dataDir, want)
	}
	if portable != "true" {
		t.Errorf("Portable = %q, want true for on-stick binary", portable)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("data dir not created: %v", err)
	}
}

// TestBinaryDirDevOverrideSubprocess confirms that MYKEEP_DEV, passed to a real
// on-stick binary, still forces the non-portable host fallback (binaryDir
// short-circuits to ok=false).
func TestBinaryDirDevOverrideSubprocess(t *testing.T) {
	root := scratchOutsideTemp(t)
	platform := runtime.GOOS + "-" + runtime.GOARCH
	exePath := filepath.Join(root, "mykeep", "bin", platform, "mykeep")
	buildHelper(t, exePath)

	cfgRoot := filepath.Join(root, "xdg")
	env := append(baseEnv(), "MYKEEP_DEV=1", "XDG_CONFIG_HOME="+cfgRoot)

	dataDir, portable := runHelper(t, exePath, env)

	want := filepath.Join(cfgRoot, "mykeep", "mykeep_kb")
	if dataDir != want {
		t.Errorf("DataDir = %q, want host fallback %q with MYKEEP_DEV", dataDir, want)
	}
	if portable != "false" {
		t.Errorf("Portable = %q, want false under MYKEEP_DEV", portable)
	}
}

// TestBinaryDirEnvOverrideBeatsWalkUp confirms MYKEEP_DATA_DIR wins even for a
// genuine on-stick binary.
func TestBinaryDirEnvOverrideBeatsWalkUp(t *testing.T) {
	root := scratchOutsideTemp(t)
	platform := runtime.GOOS + "-" + runtime.GOARCH
	exePath := filepath.Join(root, "mykeep", "bin", platform, "mykeep")
	buildHelper(t, exePath)

	override := filepath.Join(root, "override", "data")
	env := append(baseEnv(), "MYKEEP_DATA_DIR="+override)

	dataDir, portable := runHelper(t, exePath, env)
	if dataDir != override {
		t.Errorf("DataDir = %q, want env override %q", dataDir, override)
	}
	if portable != "true" {
		t.Errorf("Portable = %q, want true", portable)
	}
}
