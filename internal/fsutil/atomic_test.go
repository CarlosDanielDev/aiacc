package fsutil

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// injected is the error a failure seam returns. Tests match it with errors.Is,
// so a refusal cannot be mistaken for a real filesystem error.
var injected = errors.New("injected failure")

// seams saves the package seams and restores them when the test ends.
func seams(t *testing.T) {
	t.Helper()
	w, s, r := writeTemp, syncTemp, renameTemp
	t.Cleanup(func() { writeTemp, syncTemp, renameTemp = w, s, r })
}

// dirEntries lists dir, so a test can prove no temp file was left behind.
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// target writes body to dir/name at mode, applying the mode after the write so
// the umask cannot change it, and returns the path.
func target(t *testing.T, dir, name, body string, mode fs.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func modeOf(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

// TestWriteFileAtomicWritesContentAndExactMode: the new bytes land and the mode
// is the requested one even under a umask that would otherwise narrow it.
func TestWriteFileAtomicWritesContentAndExactMode(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })

	dir := t.TempDir()
	path := target(t, dir, "config.toml", "old", 0o600)
	if err := WriteFileAtomic(path, []byte("new"), 0o644, Options{Pattern: "config.toml.tmp*"}); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "new" {
		t.Fatalf("contents = %q, want %q", raw, "new")
	}
	if got := modeOf(t, path); got != 0o644 {
		t.Fatalf("mode = %o, want 644 (umask must not narrow it)", got)
	}
	if names := dirEntries(t, dir); len(names) != 1 {
		t.Fatalf("directory holds %v, want only the target", names)
	}
}

// TestWriteFileAtomicNeverWidensMode: the temp file already has the final mode
// when the first byte is written, so no window exposes a wider temp file. The
// seam records the mode it sees. Mutation check: create the temp at 0o666 and
// skip the chmod, and the recorded mode is wrong.
func TestWriteFileAtomicNeverWidensMode(t *testing.T) {
	seams(t)
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })

	for _, want := range []fs.FileMode{0o600, 0o640} {
		dir := t.TempDir()
		path := target(t, dir, "settings.json", "old", 0o400)
		var seen fs.FileMode
		writeTemp = func(f *os.File, b []byte) error {
			info, err := f.Stat()
			if err != nil {
				return err
			}
			seen = info.Mode().Perm()
			_, err = f.Write(b)
			return err
		}
		if err := WriteFileAtomic(path, []byte("x"), want, Options{Pattern: ".settings-*.tmp"}); err != nil {
			t.Fatal(err)
		}
		if seen != want {
			t.Fatalf("temp mode while writing = %o, want %o", seen, want)
		}
		if got := modeOf(t, path); got != want {
			t.Fatalf("final mode = %o, want %o", got, want)
		}
	}
}

// TestWriteFileAtomicLeavesNoTempOnFailure: a failure at write, sync, rename or
// a refused Track removes the temp file, keeps the old contents, and returns
// the injected error. Mutation check: drop the os.Remove in the failure path
// and the write and sync cases leave a temp file behind.
func TestWriteFileAtomicLeavesNoTempOnFailure(t *testing.T) {
	cases := []struct {
		name  string
		stage string
	}{
		{"write fails", "write"},
		{"sync fails", "sync"},
		{"rename fails", "rename"},
		{"track refuses", "track"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seams(t)
			dir := t.TempDir()
			path := target(t, dir, "config.toml", "old", 0o600)
			opts := Options{Pattern: "config.toml.tmp*"}
			switch tc.stage {
			case "write":
				writeTemp = func(*os.File, []byte) error { return injected }
			case "sync":
				syncTemp = func(*os.File) error { return injected }
			case "rename":
				renameTemp = func(string, string) error { return injected }
			case "track":
				opts.Track = func(string) (func(), error) { return nil, injected }
			}
			err := WriteFileAtomic(path, []byte("new"), 0o600, opts)
			if !errors.Is(err, injected) {
				t.Fatalf("err = %v, want the injected failure", err)
			}
			if names := dirEntries(t, dir); len(names) != 1 || names[0] != "config.toml" {
				t.Fatalf("directory after failure = %v, want only config.toml", names)
			}
			if raw, _ := os.ReadFile(path); string(raw) != "old" {
				t.Fatalf("target changed on failure: %q", raw)
			}
		})
	}
}

// TestWriteFileAtomicTrackDone: Track sees the temp file before any byte is
// written, and its done func runs once the temp is gone, on success and failure.
func TestWriteFileAtomicTrackDone(t *testing.T) {
	seams(t)
	dir := t.TempDir()
	path := target(t, dir, "settings.json", "old", 0o600)

	var tracked string
	done := 0
	opts := Options{
		Pattern: ".settings-*.tmp",
		Track: func(tmp string) (func(), error) {
			tracked = tmp
			if _, err := os.Stat(tmp); err != nil {
				t.Fatalf("Track ran without the temp file on disk: %v", err)
			}
			return func() { done++ }, nil
		},
	}
	if err := WriteFileAtomic(path, []byte("new"), 0o600, opts); err != nil {
		t.Fatal(err)
	}
	if done != 1 {
		t.Fatalf("done ran %d times on success, want 1", done)
	}
	if _, err := os.Stat(tracked); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("temp %s still exists after success", tracked)
	}

	renameTemp = func(string, string) error { return injected }
	if err := WriteFileAtomic(path, []byte("newer"), 0o600, opts); !errors.Is(err, injected) {
		t.Fatalf("err = %v, want the injected rename failure", err)
	}
	if done != 2 {
		t.Fatalf("done ran %d times after failure, want 2", done)
	}
}

// TestWriteFileAtomicRenameErrorNamesTarget: a rename failure says which file
// could not be replaced, and wraps the cause.
func TestWriteFileAtomicRenameErrorNamesTarget(t *testing.T) {
	seams(t)
	dir := t.TempDir()
	path := target(t, dir, "settings.json", "old", 0o600)
	renameTemp = func(string, string) error { return injected }
	err := WriteFileAtomic(path, []byte("new"), 0o600, Options{Pattern: ".settings-*.tmp"})
	if !errors.Is(err, injected) || !strings.HasPrefix(err.Error(), "replace settings.json: ") {
		t.Fatalf("err = %v, want \"replace settings.json: ...\" wrapping the cause", err)
	}
}

// TestWriteFileAtomicGuardCoversCreateAndTrack: Guard is taken before the temp
// file is created and released once Track has registered it, so a caller's
// lock makes "temp exists" and "temp registered" one step to any other holder.
// AfterCreate and Track both run while the guard is held. Mutation check:
// release the guard before Track and the held flag reads false in Track.
func TestWriteFileAtomicGuardCoversCreateAndTrack(t *testing.T) {
	dir := t.TempDir()
	path := target(t, dir, "settings.json", "old", 0o600)

	held, releases := false, 0
	var steps []string
	opts := Options{
		Pattern: ".settings-*.tmp",
		Guard: func() func() {
			held = true
			steps = append(steps, "guard")
			return func() {
				releases++
				held = false
				steps = append(steps, "release")
			}
		},
		AfterCreate: func(tmp string) {
			if !held {
				t.Error("AfterCreate ran without the guard held")
			}
			steps = append(steps, "created")
		},
		Track: func(tmp string) (func(), error) {
			if !held {
				t.Error("Track ran without the guard held")
			}
			steps = append(steps, "track")
			return nil, nil
		},
	}
	if err := WriteFileAtomic(path, []byte("new"), 0o600, opts); err != nil {
		t.Fatal(err)
	}
	if releases != 1 {
		t.Fatalf("guard released %d times, want 1", releases)
	}
	if got := strings.Join(steps, ","); got != "guard,created,track,release" {
		t.Fatalf("steps = %s, want guard,created,track,release", got)
	}
}

// TestWriteFileAtomicGuardReleasedOnRefusal: a refused Track still releases the
// guard exactly once and leaves no temp file behind.
func TestWriteFileAtomicGuardReleasedOnRefusal(t *testing.T) {
	dir := t.TempDir()
	path := target(t, dir, "settings.json", "old", 0o600)
	releases := 0
	opts := Options{
		Pattern: ".settings-*.tmp",
		Guard:   func() func() { return func() { releases++ } },
		Track:   func(string) (func(), error) { return nil, injected },
	}
	if err := WriteFileAtomic(path, []byte("new"), 0o600, opts); !errors.Is(err, injected) {
		t.Fatalf("err = %v, want the injected refusal", err)
	}
	if releases != 1 {
		t.Fatalf("guard released %d times, want 1", releases)
	}
	if names := dirEntries(t, dir); len(names) != 1 {
		t.Fatalf("directory after refusal = %v, want only the target", names)
	}
}
