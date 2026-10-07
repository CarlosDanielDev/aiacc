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

	// Dangling is a symlink in the account dir whose target no longer exists.
	// It is reported apart from Absent because the two look identical from the
	// base dir and are opposites in the account: Absent means there is nothing
	// to share, Dangling means the profile is holding a dead link and hands it
	// to the CLI as if it were real.
	Dangling Status = "dangling"

	// Missing is a shareable entry with no link yet — what Link would create.
	// Only Check reports it; Link creates the link instead.
	Missing Status = "not linked"

	// Pruned is a dangling link Prune removed.
	Pruned Status = "pruned"
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

	switch st := classify(src, dst); st {
	case Missing:
		// Nothing there — the common path.
	case Foreign, Dangling:
		if !replace || !exists(src) {
			return Entry{Name: name, Status: st}
		}
		if err := os.Remove(dst); err != nil {
			return Entry{Name: name, Status: Failed, Err: err}
		}
	case Taken:
		if !replace {
			return Entry{Name: name, Status: st}
		}
		if err := os.Rename(dst, dst+".aiacc-bak"); err != nil {
			return Entry{Name: name, Status: Failed, Err: err}
		}
	default: // Absent, Already
		return Entry{Name: name, Status: st}
	}

	if err := os.Symlink(src, dst); err != nil {
		return Entry{Name: name, Status: Failed, Err: err}
	}
	return Entry{Name: name, Status: Linked}
}

// classify reads what is at dst against the src it should link to, changing
// nothing. It is the one place the link states are decided, so Link acts on
// exactly what Check reports.
func classify(src, dst string) Status {
	if !exists(src) {
		// Nothing to share. Report a dead link here rather than a bare
		// "not in base": when the CLI renames a config surface, the source
		// disappears *and* every profile is left holding a symlink to it, and
		// this is the only branch that can still see the second half.
		if dangling(dst) {
			return Dangling
		}
		return Absent
	}
	switch info, err := os.Lstat(dst); {
	case err != nil:
		return Missing
	case info.Mode()&os.ModeSymlink != 0:
		if target, err := os.Readlink(dst); err == nil && same(target, src) {
			return Already
		}
		if dangling(dst) {
			return Dangling
		}
		return Foreign
	default:
		return Taken
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Check reports what Link would find, changing nothing on disk. A diagnosis
// must never repair as a side effect: `aiacc doctor` has to be safe to put in a
// shell startup file, and a command that silently fixed what it measured would
// hide the drift it exists to show.
func Check(baseDir, dir string, entries []string) Result {
	res := Result{Base: baseDir, Dir: dir}
	if baseDir == "" || dir == "" || same(baseDir, dir) {
		return res
	}
	for _, name := range entries {
		res.Entries = append(res.Entries, Entry{Name: name, Status: classify(filepath.Join(baseDir, name), filepath.Join(dir, name))})
	}
	return res
}

// Prune removes the dangling symlinks among entries.
//
// Poka-yoke: only a symlink whose target no longer resolves is removed. A real
// file, a directory, and a live symlink are never touched — so the worst a
// mistaken prune can cost is a link `aiacc link` puts straight back.
func Prune(dir string, entries []string) Result {
	res := Result{Dir: dir}
	if dir == "" {
		return res
	}
	for _, name := range entries {
		dst := filepath.Join(dir, name)
		if !dangling(dst) {
			continue
		}
		if err := os.Remove(dst); err != nil {
			res.Entries = append(res.Entries, Entry{Name: name, Status: Failed, Err: err})
			continue
		}
		res.Entries = append(res.Entries, Entry{Name: name, Status: Pruned})
	}
	return res
}

// dangling reports whether p is a symlink whose target does not resolve. Lstat
// sees the link itself; Stat follows it, so the pair separates "a dead link is
// here" from "nothing is here".
func dangling(p string) bool {
	info, err := os.Lstat(p)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return false
	}
	_, err = os.Stat(p)
	return err != nil
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
