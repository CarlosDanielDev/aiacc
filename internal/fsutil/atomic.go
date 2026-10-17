// Package fsutil holds the file-system helpers that more than one package
// needs. It imports only the standard library, so any package may use it.
package fsutil

import (
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Options tunes one WriteFileAtomic call. The zero value writes a temp file
// named by a generic pattern with no tracking and no hook.
type Options struct {
	// Pattern names the temp file as os.CreateTemp does: the last "*" is
	// replaced by a random string, and a pattern without one gets it appended.
	Pattern string
	// Track, when set, runs once the temp file exists and before any byte is
	// written, so a caller can record it for a signal handler. It returns the
	// func to call once the temp file is gone, renamed or removed. An error
	// refuses the write, and the temp file is removed.
	Track func(tmp string) (done func(), err error)
	// BeforeRename, when set, runs after the temp file is closed and before it
	// is renamed over the target. It is the window a test can hook.
	BeforeRename func()
}

// The file operations a write performs. They are variables so a test can make
// one fail; production code never reassigns them.
var (
	writeTemp  = func(f *os.File, b []byte) error { _, err := f.Write(b); return err }
	syncTemp   = func(f *os.File) error { return f.Sync() }
	renameTemp = os.Rename
)

// WriteFileAtomic replaces the file at path with data. The bytes go to a temp
// file in path's directory, which is created with mode and chmodded to it, so
// the umask cannot change it and it is never wider than mode. The temp is
// synced, closed and renamed over path, so a reader sees either the old file
// or the new one. The temp file is removed on every error path. The caller
// resolves symlinks and creates the directory; this function only writes.
func WriteFileAtomic(path string, data []byte, mode fs.FileMode, opts Options) error {
	tmp, err := createTemp(filepath.Dir(path), opts.Pattern, mode)
	if err != nil {
		return err
	}
	name := tmp.Name()
	done := func() {}
	if opts.Track != nil {
		var track func()
		if track, err = opts.Track(name); err != nil {
			tmp.Close()
			os.Remove(name)
			return err
		}
		if track != nil {
			done = track
		}
	}
	defer done() // runs after the rename has moved the temp away, or the removal
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name) // leave no half-written file behind
		return err
	}
	if err := tmp.Chmod(mode); err != nil { // undo the umask on the new file
		return fail(err)
	}
	if err := writeTemp(tmp, data); err != nil {
		return fail(err)
	}
	if err := syncTemp(tmp); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if opts.BeforeRename != nil {
		opts.BeforeRename()
	}
	if err := renameTemp(name, path); err != nil {
		os.Remove(name)
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	return nil
}

// createTemp opens a new file in dir, named from pattern, with mode from the
// start. O_EXCL makes a name clash fail, and the loop draws another name.
func createTemp(dir, pattern string, mode fs.FileMode) (*os.File, error) {
	if !strings.Contains(pattern, "*") {
		pattern += "*"
	}
	i := strings.LastIndex(pattern, "*")
	prefix, suffix := pattern[:i], pattern[i+1:]
	for range 10000 {
		name := filepath.Join(dir, prefix+strconv.FormatUint(uint64(rand.Uint32()), 10)+suffix)
		f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, mode)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return f, err
	}
	return nil, &fs.PathError{Op: "createtemp", Path: filepath.Join(dir, pattern), Err: fs.ErrExist}
}
