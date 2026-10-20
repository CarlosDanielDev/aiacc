package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
)

// setEnv sets the variable the launched CLI reads its account from. A
// duplicate would hand the CLI the outer account's config dir, which is the
// silent wrong-account failure the picker exists to prevent.
func TestSetEnvReplacesWithoutDuplicating(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		key  string
		want []string
	}{
		{
			name: "replaces an existing entry",
			env:  []string{"PATH=/bin", "CLAUDE_CONFIG_DIR=/outer", "HOME=/h"},
			key:  "CLAUDE_CONFIG_DIR",
			want: []string{"PATH=/bin", "HOME=/h", "CLAUDE_CONFIG_DIR=/inner"},
		},
		{
			name: "replaces every duplicate, leaving one",
			env:  []string{"CLAUDE_CONFIG_DIR=/outer", "PATH=/bin", "CLAUDE_CONFIG_DIR=/outer2"},
			key:  "CLAUDE_CONFIG_DIR",
			want: []string{"PATH=/bin", "CLAUDE_CONFIG_DIR=/inner"},
		},
		{
			name: "appends when absent",
			env:  []string{"PATH=/bin"},
			key:  "CLAUDE_CONFIG_DIR",
			want: []string{"PATH=/bin", "CLAUDE_CONFIG_DIR=/inner"},
		},
		{
			name: "a key that is a prefix of another is left alone",
			env:  []string{"CLAUDE_CONFIG_DIR=/outer"},
			key:  "CLAUDE_CONFIG",
			want: []string{"CLAUDE_CONFIG_DIR=/outer", "CLAUDE_CONFIG=/inner"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := setEnv(tc.env, tc.key, "/inner")
			if !slices.Equal(got, tc.want) {
				t.Fatalf("setEnv = %v, want %v", got, tc.want)
			}
		})
	}
}

// collectRows feeds the picker. A profile whose dir is gone must still appear,
// flagged as not existing, so the user sees what to repair; a provider with no
// launch command must show an empty command, which the picker renders blocked.
func TestCollectRowsKeepsBlockedProfilesVisible(t *testing.T) {
	existing := t.TempDir()
	missing := filepath.Join(t.TempDir(), "gone")
	c := &config.Config{Providers: map[string]config.Provider{
		"claude": {
			EnvVar: "CLAUDE_CONFIG_DIR",
			Accounts: map[string]config.Account{
				"play": {Dir: existing},
				"work": {Dir: missing},
			},
		},
		"mytool": {
			EnvVar:   "MYTOOL_DIR",
			Accounts: map[string]config.Account{"solo": {Dir: t.TempDir()}},
		},
	}}

	rows := collectRows(c, "")
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d: %+v", len(rows), rows)
	}
	byName := map[string]int{}
	for i, r := range rows {
		byName[r.Account] = i
	}

	play := rows[byName["play"]]
	if !play.DirExists || play.Dir != existing || play.EnvVar != "CLAUDE_CONFIG_DIR" || play.Command != "claude" {
		t.Errorf("existing profile row = %+v", play)
	}

	work := rows[byName["work"]]
	if work.DirExists {
		t.Errorf("missing dir reported as existing: %+v", work)
	}
	if work.Dir != missing {
		t.Errorf("missing profile dir = %q, want %q", work.Dir, missing)
	}

	solo := rows[byName["solo"]]
	if solo.Command != "" {
		t.Errorf("provider with no launch command must have an empty Command, got %q", solo.Command)
	}

	if got := collectRows(c, "nope"); len(got) != 0 {
		t.Errorf("filter to an unknown provider returned %d rows", len(got))
	}
}

// stubDuplicateScreen replaces the duplicate screen with one that submits each
// (name, dir) answer in turn, the way Enter would, and records the refusal each
// one got. The first accepted answer closes the screen; when none is accepted
// it backs out, as Esc does.
func stubDuplicateScreen(t *testing.T, answers ...[2]string) *[]error {
	t.Helper()
	refusals := &[]error{}
	old := duplicateScreen
	duplicateScreen = func(_, _ string, submit func(name, dir string) error) (bool, error) {
		for _, a := range answers {
			if err := submit(a[0], a[1]); err != nil {
				*refusals = append(*refusals, err)
				continue // the screen keeps the refusal on its hint row
			}
			return true, nil
		}
		return false, nil // Esc
	}
	t.Cleanup(func() { duplicateScreen = old })
	return refusals
}

// The picker's c duplicates through the CLI's function: a refused name is
// returned to the screen and writes nothing, and the corrected one registers.
func TestPickerDuplicateRefusalThenSuccess(t *testing.T) {
	home, cfgPath, _ := dupFixture(t)
	shown := captureHandoffMessage(t)
	refusals := stubDuplicateScreen(t, [2]string{"taken", ""}, [2]string{"work2", ""})

	if err := runDuplicateTUI(cfgPath, tui.Row{Provider: "claude", Account: "work"}); err != nil {
		t.Fatalf("runDuplicateTUI: %v", err)
	}
	if len(*refusals) != 1 || !strings.Contains((*refusals)[0].Error(), `"taken" already exists`) {
		t.Fatalf("refusals = %v, want the taken-name refusal", *refusals)
	}
	c, _ := config.Load(cfgPath)
	if c.Providers["claude"].Accounts["work2"].Quota != 7 {
		t.Fatalf("work2 not registered: %+v", c.Providers["claude"].Accounts)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude-taken")); !os.IsNotExist(err) {
		t.Fatalf("refused name touched disk: %v", err)
	}
	var text []string
	for _, l := range *shown {
		text = append(text, l.Text)
	}
	if !strings.Contains(strings.Join(text, "\n"), "/login") {
		t.Fatalf("done message does not say how to log in: %v", text)
	}
}

// Esc on the duplicate screen writes nothing, and a refusal followed by Esc
// writes nothing either.
func TestPickerDuplicateCancelWritesNothing(t *testing.T) {
	for _, answers := range [][][2]string{nil, {{"taken", ""}}} {
		home, cfgPath, _ := dupFixture(t)
		captureHandoffMessage(t)
		stubDuplicateScreen(t, answers...)
		cfgBefore, _ := os.ReadFile(cfgPath)
		diskBefore := treeOf(t, home)

		if err := runDuplicateTUI(cfgPath, tui.Row{Provider: "claude", Account: "work"}); err != nil {
			t.Fatalf("runDuplicateTUI: %v", err)
		}
		if cfgAfter, _ := os.ReadFile(cfgPath); !bytes.Equal(cfgBefore, cfgAfter) {
			t.Errorf("answers %v: config changed", answers)
		}
		if diskAfter := treeOf(t, home); !slices.Equal(diskBefore, diskAfter) {
			t.Errorf("answers %v: disk changed", answers)
		}
	}
}
