// Package usage parses Claude Code JSONL session logs and aggregates token
// counts. Malformed lines and files degrade gracefully: usage is best-effort,
// never fatal.
package usage

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Totals holds aggregated token counts across all session logs.
type Totals struct {
	Input       int
	Output      int
	CacheCreate int
	CacheRead   int
}

// Total returns the sum of every token bucket.
func (t Totals) Total() int {
	return t.Input + t.Output + t.CacheCreate + t.CacheRead
}

// logLine mirrors the subset of a session-log record we care about: the nested
// message.usage token counts written by assistant events.
type logLine struct {
	Message struct {
		Usage *struct {
			Input       int `json:"input_tokens"`
			Output      int `json:"output_tokens"`
			CacheCreate int `json:"cache_creation_input_tokens"`
			CacheRead   int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// Aggregate walks <configDir>/projects recursively, sums token counts from
// every *.jsonl file, and returns the combined Totals. A missing projects
// directory yields zero Totals and a nil error. Individual unreadable files and
// malformed or usage-less lines are skipped rather than failing the walk.
func Aggregate(configDir string) (Totals, error) {
	var t Totals
	root := filepath.Join(configDir, "projects")

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A missing root is not an error: no logs yet.
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			// Skip individual entries we cannot stat; keep walking.
			if path != root {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		addFile(&t, path)
		return nil
	})
	if err != nil {
		return Totals{}, err
	}
	return t, nil
}

// addFile accumulates token counts from one JSONL file into t. Read errors and
// unparseable lines are ignored so a single bad file never breaks aggregation.
func addFile(t *Totals, path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	r := bufio.NewReader(f)
	for {
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			addLine(t, line)
		}
		if err != nil {
			// io.EOF or a read failure: stop with whatever we gathered.
			return
		}
	}
}

// addLine parses a single JSONL record and adds its usage counts to t. Lines
// that are not valid JSON, or carry no usage object, contribute nothing.
func addLine(t *Totals, line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	var rec logLine
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return
	}
	u := rec.Message.Usage
	if u == nil {
		return
	}
	t.Input += u.Input
	t.Output += u.Output
	t.CacheCreate += u.CacheCreate
	t.CacheRead += u.CacheRead
}
