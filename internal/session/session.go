// Package session reads and moves Claude Code session transcripts, which live at
// <CLAUDE_CONFIG_DIR>/projects/<cwd-dir>/<sessionId>.jsonl. It lets aiacc hand a
// session off from one account to another: copy the transcript into the target
// account's projects, preserving its project directory, so `claude --resume` can
// pick it up there. It never touches credentials — only the transcript file.
package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Info identifies one session transcript.
type Info struct {
	ID         string    // the session id (also the filename stem)
	Path       string    // absolute path to the .jsonl
	ProjectDir string    // the projects/<name> directory it lives in
	Modified   time.Time // file mtime — the "most recent" ordering key
}

// Meta is the human-facing detail parsed from a transcript on demand.
type Meta struct {
	Cwd       string // working directory the session ran in (where to resume)
	Title     string // customTitle, else the last prompt, else ""
	GitBranch string
	Messages  int
}

func projectsRoot(configDir string) string { return filepath.Join(configDir, "projects") }

// List returns every session under configDir, newest first. A missing projects
// directory yields an empty list, not an error.
func List(configDir string) ([]Info, error) {
	root := projectsRoot(configDir)
	var out []Info
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if p != root {
				return nil // skip unreadable entries, keep walking
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		out = append(out, Info{
			ID:         strings.TrimSuffix(d.Name(), ".jsonl"),
			Path:       p,
			ProjectDir: filepath.Base(filepath.Dir(p)),
			Modified:   fi.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// Find returns the session with the given id under configDir.
func Find(configDir, id string) (Info, error) {
	list, err := List(configDir)
	if err != nil {
		return Info{}, err
	}
	for _, s := range list {
		if s.ID == id {
			return s, nil
		}
	}
	return Info{}, fmt.Errorf("session %q not found", id)
}

// ReadMeta scans a transcript for its cwd, title, branch, and a rough message
// count. Lines can be large (pasted content), so the scanner buffer is generous.
func ReadMeta(path string) (Meta, error) {
	f, err := os.Open(path)
	if err != nil {
		return Meta{}, err
	}
	defer f.Close()

	var m Meta
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var d struct {
			Cwd         string `json:"cwd"`
			CustomTitle string `json:"customTitle"`
			LastPrompt  string `json:"lastPrompt"`
			GitBranch   string `json:"gitBranch"`
			Type        string `json:"type"`
		}
		if json.Unmarshal(sc.Bytes(), &d) != nil {
			continue
		}
		if d.Cwd != "" && m.Cwd == "" {
			m.Cwd = d.Cwd
		}
		if d.GitBranch != "" && m.GitBranch == "" {
			m.GitBranch = d.GitBranch
		}
		if d.CustomTitle != "" {
			m.Title = d.CustomTitle
		} else if d.LastPrompt != "" && m.Title == "" {
			m.Title = d.LastPrompt
		}
		if d.Type == "user" || d.Type == "assistant" {
			m.Messages++
		}
	}
	return m, sc.Err()
}

// closeDest closes the destination on Copy's success path. It is a variable so
// a test can inject a close failure, which a normal filesystem does not produce.
var closeDest = func(f *os.File) error { return f.Close() }

// Copy writes s into toConfigDir's projects, under the same project directory, so
// the target account can resume it. It returns the destination path. Transcripts
// are private (0600), and that mode is preserved. It refuses a destination that
// is the source file, because truncating it would zero the transcript. The
// check is fail-closed and nothing is truncated until it passes:
//
//   - the source is opened, and its identity taken from that open file;
//   - the destination path is stat'ed, and any error other than "does not
//     exist" refuses the copy;
//   - the destination is opened WITHOUT O_TRUNC, its identity is taken from the
//     open file (os.SameFile against the source), and only a non-matching file
//     is truncated and written.
//
// The identity check and the truncate act on the same descriptor, so a
// symlink or hardlink swapped in between the stat and the open cannot make
// Copy truncate the source. Not covered: a process that writes to the source
// while it is being copied. That gives a partial copy, not a truncated source.
// A close error is returned, so a short write is never reported as success;
// the destination is left in place, since it may be a pre-existing session.
func Copy(s Info, toConfigDir string) (string, error) {
	destDir := filepath.Join(projectsRoot(toConfigDir), s.ProjectDir)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(destDir, s.ID+".jsonl")
	if filepath.Clean(dest) == filepath.Clean(s.Path) {
		return "", fmt.Errorf("refusing to copy: destination %s is the source file", dest)
	}

	in, err := os.Open(s.Path)
	if err != nil {
		return "", err
	}
	defer in.Close()
	srcInfo, err := in.Stat()
	if err != nil {
		return "", fmt.Errorf("refusing to copy: can't stat source %s: %w", s.Path, err)
	}

	if _, err := os.Stat(dest); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("refusing to copy: can't stat destination %s: %w", dest, err)
	}
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return "", err
	}
	defer out.Close()
	outInfo, err := out.Stat()
	if err != nil {
		return "", fmt.Errorf("refusing to copy: can't stat destination %s: %w", dest, err)
	}
	if os.SameFile(srcInfo, outInfo) {
		return "", fmt.Errorf("refusing to copy: destination %s is the source file (symlink or hardlink)", dest)
	}
	if err := out.Truncate(0); err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		return "", err
	}
	// A write can fail only at close (NFS, quota, full disk). Returning it
	// keeps a short transcript from being reported as handed off. The
	// destination is left in place: it may be a pre-existing session file.
	if err := closeDest(out); err != nil {
		return "", fmt.Errorf("closing destination %s: %w", dest, err)
	}
	return dest, nil
}
