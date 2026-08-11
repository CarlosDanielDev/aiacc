package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/config"
)

func TestAddWizardRegistersAccount(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir()) // hermetic: don't read real ~/.claude
	path := filepath.Join(t.TempDir(), "config.toml")
	customDir := t.TempDir()
	in := bytes.NewBufferString("demo\n" + customDir + "\n")
	var out bytes.Buffer
	if err := runAddWizard(in, &out, path); err != nil {
		t.Fatalf("wizard: %v", err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Providers["claude"].Accounts["demo"].Dir != customDir {
		t.Fatalf("account not registered: %+v", c.Providers["claude"])
	}
	if !strings.Contains(out.String(), "Registered 'demo'") {
		t.Fatalf("missing confirmation: %q", out.String())
	}
}

func TestAddWizardEmptyNameErrors(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := runAddWizard(bytes.NewBufferString("\n"), &bytes.Buffer{}, path); err == nil {
		t.Fatal("want error for empty name")
	}
}
