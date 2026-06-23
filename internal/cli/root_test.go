package cli

import (
	"sort"
	"testing"
)

func TestNewRootHasAllSubcommands(t *testing.T) {
	root := NewRoot()
	var got []string
	for _, c := range root.Commands() {
		got = append(got, c.Name())
	}
	sort.Strings(got)
	want := []string{"add", "list", "remove", "shell-init", "status", "usage", "use"}
	if len(got) != len(want) {
		t.Fatalf("subcommands = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("subcommands = %v, want %v", got, want)
		}
	}
}
