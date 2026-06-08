// Package paths resolves mykeep's data directory from the binary's own location
// on the USB drive, never from $HOME or the working directory. This is the
// portability keystone (PLAN §3, §11.7).
package paths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Layout holds the resolved, absolute paths for a mykeep instance.
type Layout struct {
	DataDir  string // .../mykeep_kb
	Portable bool   // false if we fell back to a host-local dir
}

const (
	configName  = "mykeep.config.json"
	dbName      = "mykeep.db.enc"
	dataDirName = "mykeep_kb" // the knowledge base, a sibling of the binary on the stick
)

// Resolve determines the data directory from the running binary's location.
// Resolution order (PLAN §11.7):
//  1. MYKEEP_DATA_DIR env override (dev/tests).
//  2. A mykeep_kb/ directory beside the binary (the drive root). All six platform
//     binaries live at the drive root, so they share one mykeep_kb/.
//  3. Fallback to os.UserConfigDir()/mykeep with Portable=false when the binary
//     dir is unusable (read-only mount, go-run temp exe, AppTranslocation).
func Resolve() (Layout, error) {
	if dir := os.Getenv("MYKEEP_DATA_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Layout{}, err
		}
		return Layout{DataDir: dir, Portable: true}, nil
	}

	base, ok := binaryDir()
	if ok {
		dataDir := filepath.Join(base, dataDirName)
		if writable(dataDir) {
			return Layout{DataDir: dataDir, Portable: true}, nil
		}
	}

	// Fallback: host config dir, not portable.
	cfg, err := os.UserConfigDir()
	if err != nil {
		return Layout{}, err
	}
	dataDir := filepath.Join(cfg, "mykeep", dataDirName)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Layout{}, err
	}
	return Layout{DataDir: dataDir, Portable: false}, nil
}

// binaryDir returns the directory the mykeep binary lives in (the drive root),
// where mykeep_kb/ sits beside it. Returns ok=false for go-run temp binaries or
// macOS AppTranslocation, which are not on the stick.
func binaryDir() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)

	// go-run / temp executables are not on the stick.
	if strings.HasPrefix(exe, os.TempDir()) || os.Getenv("MYKEEP_DEV") != "" {
		return "", false
	}
	// macOS Gatekeeper AppTranslocation: the binary is copied to a random RO path.
	if strings.Contains(dir, "/AppTranslocation/") {
		return "", false
	}

	return dir, true
}

// writable probes a directory by creating it (if needed) and writing a temp file.
func writable(dir string) bool {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false
	}
	probe := filepath.Join(dir, ".mykeep-write-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return true
}

func (l Layout) ConfigPath() string { return filepath.Join(l.DataDir, configName) }
func (l Layout) DBPath() string     { return filepath.Join(l.DataDir, dbName) }

// IsFirstLaunch reports whether setup has never run on this drive (config absent).
func (l Layout) IsFirstLaunch() bool {
	_, err := os.Stat(l.ConfigPath())
	return errors.Is(err, os.ErrNotExist)
}
