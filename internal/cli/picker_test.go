package cli

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
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
