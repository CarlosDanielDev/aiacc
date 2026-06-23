package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
)

func withTempConfig(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	old := configPath
	configPath = func() (string, error) { return p, nil }
	t.Cleanup(func() { configPath = old })
	return p
}

func TestAddThenListShowsAccount(t *testing.T) {
	path := withTempConfig(t)
	dir := t.TempDir()

	add := newAddCmd()
	add.SetArgs([]string{"claude", "work", "--dir", dir})
	if err := add.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}

	c, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Providers["claude"].Accounts["work"].Dir != dir {
		t.Fatalf("account not saved: %+v", c.Providers["claude"])
	}

	list := newListCmd()
	var out bytes.Buffer
	list.SetOut(&out)
	if err := list.Execute(); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out.String(), "work") || !strings.Contains(out.String(), "claude") {
		t.Fatalf("list output missing account: %q", out.String())
	}
}

func TestRemoveDeletesAccount(t *testing.T) {
	withTempConfig(t)
	dir := t.TempDir()

	add := newAddCmd()
	add.SetArgs([]string{"claude", "work", "--dir", dir})
	if err := add.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}
	rm := newRemoveCmd()
	rm.SetArgs([]string{"claude", "work"})
	if err := rm.Execute(); err != nil {
		t.Fatalf("remove: %v", err)
	}

	path, _ := configPath()
	c, _ := config.Load(path)
	if _, ok := c.Providers["claude"].Accounts["work"]; ok {
		t.Fatalf("account still present after remove")
	}
}
