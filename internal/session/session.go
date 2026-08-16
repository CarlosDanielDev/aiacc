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

// Copy writes s into toConfigDir's projects, under the same project directory, so
// the target account can resume it. It returns the destination path. Transcripts
// are private (0600), and that mode is preserved.
func Copy(s Info, toConfigDir string) (string, error) {
	destDir := filepath.Join(projectsRoot(toConfigDir), s.ProjectDir)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(destDir, s.ID+".jsonl")

	in, err := os.Open(s.Path)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return "", err
	}
	return dest, nil
}
