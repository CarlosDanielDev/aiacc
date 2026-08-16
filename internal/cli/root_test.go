package cli

import (
	"bytes"
	"io"
	"slices"
	"sort"
	"strings"
	"testing"
)

func TestNewRootHasAllSubcommands(t *testing.T) {
	root := NewRoot()
	var got []string
	for _, c := range root.Commands() {
		got = append(got, c.Name())
	}
	sort.Strings(got)
	want := []string{"add", "list", "remove", "rename", "setup", "shell-init", "status", "usage"}
	if !slices.Equal(got, want) {
		t.Fatalf("subcommands = %v, want %v", got, want)
	}
}

// TestRootAbsorbsStaleShellFlag: a leftover pre-v0.7 hook calls `aiacc --shell
// <name>`; the upgrade must not error on the removed flag, it must guide instead.
func TestRootAbsorbsStaleShellFlag(t *testing.T) {
	root := NewRoot()
	var errb bytes.Buffer
	root.SetErr(&errb)
	root.SetOut(io.Discard)
	root.SetArgs([]string{"--shell", "fish"})
	if err := root.Execute(); err != nil {
		t.Fatalf("stale --shell must not error, got %v", err)
	}
	if !strings.Contains(errb.String(), "shell-init") {
		t.Fatalf("missing migration guidance: %q", errb.String())
	}
}
