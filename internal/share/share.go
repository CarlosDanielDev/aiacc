// Package share links a provider's *authored* assets — skills, sub-agents, slash
// commands, hooks, instructions, settings — from the CLI's own config directory
// into each account's isolated directory.
//
// Why this exists: a profile launcher (`claude-work`) runs the CLI with its
// config dir pointed somewhere else, and every asset the CLI discovers lives
// under that dir. Without linking, an account launched by aiacc starts with no
// skills, no sub-agents and no slash commands — the same binary, a different and
// much emptier tool. Sharing makes every profile equal to the bare CLI.
//
// A symlink, not a copy, so the assets stay in one place: edit a skill once and
// every profile has it. Only authoring surfaces are shared. Credentials,
// transcripts, and usage counters stay per-account — isolating those is the whole
// point of a profile.
package share

import (
	"os"
	"path/filepath"
)

// Status is what happened to one shared entry.
type Status string

const (
	Linked  Status = "linked"      // a new symlink was created
	Already Status = "already"     // the correct symlink was already there
	Absent  Status = "not in base" // nothing to share — the source doesn't exist
	Taken   Status = "own copy"    // the account has its own file/dir; left alone
	Foreign Status = "linked away" // a symlink pointing somewhere else; left alone
	Failed  Status = "failed"      // the link could not be created
)

// Entry is the outcome for one shared name.
type Entry struct {
	Name   string
	Status Status
	Err    error
}

// Result is the outcome of linking one account.
type Result struct {
	Base    string // source directory
	Dir     string // account directory
	Entries []Entry
}

// Count returns how many entries ended in one of the given statuses.
func (r Result) Count(want ...Status) int {
	n := 0
	for _, e := range r.Entries {
		for _, w := range want {
			if e.Status == w {
				n++
				break
			}
		}
	}
	return n
}

// Conflicts returns the entries that were left alone because the account already
// has something of its own there — what `--replace` would take over.
func (r Result) Conflicts() []Entry {
	var out []Entry
	for _, e := range r.Entries {
		if e.Status == Taken || e.Status == Foreign {
			out = append(out, e)
		}
	}
	return out
}

// Link symlinks each of entries from baseDir into dir.
//
// Poka-yoke: an entry the account already owns is never silently destroyed. With
// replace false it is left exactly as it is; with replace true it is renamed to
// "<name>.aiacc-bak" first, so the take-over is always undoable by hand. Linking
// the base directory into itself is a no-op — the base profile already has the
// originals.
func Link(baseDir, dir string, entries []string, replace bool) (Result, error) {
	res := Result{Base: baseDir, Dir: dir}
	if baseDir == "" || dir == "" {
		return res, nil
	}
	if same(baseDir, dir) {
		return res, nil // the base profile owns the originals
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, err
	}
	for _, name := range entries {
		res.Entries = append(res.Entries, linkOne(baseDir, dir, name, replace))
	}
	return res, nil
}

func linkOne(baseDir, dir, name string, replace bool) Entry {
	src := filepath.Join(baseDir, name)
	dst := filepath.Join(dir, name)

	if _, err := os.Stat(src); err != nil {
		return Entry{Name: name, Status: Absent}
	}

	switch info, err := os.Lstat(dst); {
	case err != nil:
		// Nothing there — the common path.
	case info.Mode()&os.ModeSymlink != 0:
		if target, err := os.Readlink(dst); err == nil && same(target, src) {
			return Entry{Name: name, Status: Already}
		}
		if !replace {
			return Entry{Name: name, Status: Foreign}
		}
		if err := os.Remove(dst); err != nil {
			return Entry{Name: name, Status: Failed, Err: err}
		}
	default:
		if !replace {
			return Entry{Name: name, Status: Taken}
		}
		if err := os.Rename(dst, dst+".aiacc-bak"); err != nil {
			return Entry{Name: name, Status: Failed, Err: err}
		}
	}

	if err := os.Symlink(src, dst); err != nil {
		return Entry{Name: name, Status: Failed, Err: err}
	}
	return Entry{Name: name, Status: Linked}
}

// same compares two paths after cleaning and resolving symlinks, so
// ~/.claude, ~/.claude/ and a symlink to it all count as one directory.
func same(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, erra := filepath.EvalSymlinks(a)
	rb, errb := filepath.EvalSymlinks(b)
	return erra == nil && errb == nil && ra == rb
}
