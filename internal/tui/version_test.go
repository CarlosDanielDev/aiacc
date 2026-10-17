package tui

import (
	"fmt"
	"strings"
	"testing"
)

// statusLine returns the visible status-bar row of a picker frame: the box row
// that carries the READY/SETUP readout, with colour escapes stripped.
func statusLine(t *testing.T, out string) string {
	t.Helper()
	for _, ln := range strings.Split(out, "\r\n") {
		plain := ansi.ReplaceAllString(ln, "")
		if strings.Contains(plain, "READY") || strings.Contains(plain, "SETUP") {
			return plain
		}
	}
	t.Fatalf("no status line in frame:\n%s", out)
	return ""
}

func TestPickerShowsVersionRightAlignedInStatusBar(t *testing.T) {
	out := renderPicker(sample(), 0, false, 0, "0.21.0", Size{Cols: 80, Rows: 24})
	line := statusLine(t, out)
	if !strings.Contains(line, "✓ READY") {
		t.Fatalf("status readout lost:\n%q", line)
	}
	// Right-aligned: the version sits against the right border, one space in.
	if !strings.HasSuffix(line, "v0.21.0 ┃") {
		t.Fatalf("version is not right-aligned in the status bar:\n%q", line)
	}
}

func TestPickerVersionAbsentWhenNotSupplied(t *testing.T) {
	out := Render(sample(), 0, false, Size{Cols: 80, Rows: 24})
	plain := ansi.ReplaceAllString(out, "")
	if strings.Contains(plain, "v0.") || strings.Contains(plain, "dev") {
		t.Fatalf("version shown although none was supplied:\n%s", plain)
	}
}

func TestPickerVersionLabel(t *testing.T) {
	cases := map[string]string{
		"0.21.0":        "v0.21.0",
		"v0.21.0":       "v0.21.0", // the double-v guard: never vv0.21.0
		"1.0.0-rc.1":    "v1.0.0-rc.1",
		"dev":           "dev",
		"":              "dev",
		"  ":            "dev",
		"not-a-version": "dev",
		"v":             "dev",
		"0.21":          "dev",
	}
	for in, want := range cases {
		if got := versionLabel(in); got != want {
			t.Errorf("versionLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPickerShowsDevForUnversionedBuild(t *testing.T) {
	out := renderPicker(sample(), 0, false, 0, "dev", Size{Cols: 80, Rows: 24})
	if line := statusLine(t, out); !strings.HasSuffix(line, "dev ┃") {
		t.Fatalf("dev build not labelled dev:\n%q", line)
	}
}

func TestPickerVersionNeverDoubledV(t *testing.T) {
	out := renderPicker(sample(), 0, false, 0, "v0.21.0", Size{Cols: 80, Rows: 24})
	if plain := ansi.ReplaceAllString(out, ""); strings.Contains(plain, "vv0") {
		t.Fatalf("doubled v in output:\n%s", plain)
	}
}

// The version is the first thing to go. Below 40 columns the readout uses the
// short " :: " separator, so the status text is 25 cells; with the version it
// needs 25 + 1 gap + len("v0.21.0") + 1 margin = 34 inner cells, i.e. cols >= 38.
// At 34 and 37 columns the version must drop and the readout must be
// byte-identical to the version-less frame: nothing else may be cut or moved.
func TestPickerVersionDropsWhenTooNarrow(t *testing.T) {
	for _, cols := range []int{34, 37} {
		sz := Size{Cols: cols, Rows: 24}
		with := statusLine(t, renderPicker(sample(), 0, false, 0, "0.21.0", sz))
		without := statusLine(t, renderPicker(sample(), 0, false, 0, "", sz))
		if strings.Contains(with, "v0.21.0") {
			t.Errorf("cols=%d: version shown in a bar too narrow for it:\n%q", cols, with)
		}
		if with != without {
			t.Errorf("cols=%d: status bar changed when the version dropped:\n%q\n%q", cols, with, without)
		}
	}
	for _, cols := range []int{38, 40} {
		if line := statusLine(t, renderPicker(sample(), 0, false, 0, "0.21.0", Size{Cols: cols, Rows: 24})); !strings.HasSuffix(line, "v0.21.0 ┃") {
			t.Fatalf("cols=%d fits the version but it was dropped:\n%q", cols, line)
		}
	}
}

// Both layouts share the status bar: the launcher with profiles and the
// empty-state screen that tells a new user to add one.
func TestPickerVersionInBothLayouts(t *testing.T) {
	sz := Size{Cols: 80, Rows: 24}
	if line := statusLine(t, renderPicker(sample(), 0, false, 0, "0.21.0", sz)); !strings.HasSuffix(line, "v0.21.0 ┃") {
		t.Fatalf("profiles layout: %q", line)
	}
	empty := renderPicker(nil, -1, false, 0, "0.21.0", sz)
	if line := statusLine(t, empty); !strings.HasSuffix(line, "v0.21.0 ┃") {
		t.Fatalf("empty-state layout: %q", line)
	}
	if plain := ansi.ReplaceAllString(empty, ""); !strings.Contains(plain, "No profiles yet") {
		t.Fatalf("empty layout lost its guidance:\n%s", plain)
	}
}

// The setup readout is a longer status text; the version must still fit beside
// it or drop, never push it.
func TestPickerVersionWithSetupReadout(t *testing.T) {
	for _, cols := range []int{34, 40, 60, 80} {
		sz := Size{Cols: cols, Rows: 24}
		with := statusLine(t, renderPicker(sample(), 0, true, 0, "0.21.0", sz))
		without := statusLine(t, renderPicker(sample(), 0, true, 0, "", sz))
		if !strings.Contains(with, "SETUP") {
			t.Errorf("cols=%d: setup readout lost: %q", cols, with)
		}
		if !strings.Contains(with, "v0.21.0") && with != without {
			t.Errorf("cols=%d: readout changed without the version: %q vs %q", cols, with, without)
		}
	}
}

// Size matrix with a version set: the version must never tear the frame, in
// either layout, at any size the existing fit tests cover.
func TestPickerVersionFramesFitTerminal(t *testing.T) {
	var many []Row
	for i := range 40 {
		many = append(many, Row{
			Provider: "claude", Account: fmt.Sprintf("profile-%d", i),
			Email: "someone.with.a.long.address@example.com", DirExists: true,
			EnvVar: "CLAUDE_CONFIG_DIR", Command: "claude",
		})
	}
	for _, sz := range []Size{{20, 10}, {24, 12}, {34, 14}, {40, 14}, {60, 8}, {80, 24}, {200, 60}} {
		for _, rows := range [][]Row{nil, sample(), many} {
			for _, ver := range []string{"0.21.0", "dev", "v10.100.1000"} {
				out := renderPicker(rows, 0, true, 0, ver, sz)
				name := fmt.Sprintf("%dx%d rows=%d ver=%s", sz.Cols, sz.Rows, len(rows), ver)
				if used := strings.Count(out, "\r\n"); used >= sz.Rows {
					t.Errorf("%s: %d rows emitted into a %d-row terminal", name, used, sz.Rows)
				}
				wid := -1
				for _, ln := range strings.Split(out, "\r\n") {
					if n := visibleLen(ln); n > sz.Cols {
						t.Errorf("%s: line is %d wide:\n%q", name, n, ln)
						break
					}
					plain := strings.TrimLeft(ansi.ReplaceAllString(ln, ""), " ")
					if !strings.ContainsAny(firstRune(plain), "┏┃┗┣") {
						continue
					}
					if n := visibleLen(ln); wid == -1 {
						wid = n
					} else if n != wid {
						t.Errorf("%s: framed line width %d != %d:\n%q", name, n, wid, ln)
						break
					}
				}
			}
		}
	}
}
