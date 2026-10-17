package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CarlosDanielDev/aiacc/internal/tui"
)

// editMessageBody runs the picker's edit entry point for row and returns the
// message it would show. It never reaches the tty: each case must end in a
// message screen, not in the edit screen.
func editMessageBody(t *testing.T, row tui.Row) string {
	t.Helper()
	captured := captureHandoffMessage(t)
	if err := runEditTUI(row); err != nil {
		t.Fatalf("runEditTUI: %v", err)
	}
	var b strings.Builder
	for _, l := range *captured {
		b.WriteString(l.Text + "\n")
	}
	return b.String()
}

func TestEditAccountWithoutSettingsShowsMessage(t *testing.T) {
	body := editMessageBody(t, tui.Row{Provider: "claude", Account: "work", Settings: ""})
	if !strings.Contains(body, "work has no settings file") {
		t.Fatalf("no clear message for an account without settings:\n%s", body)
	}
}

func TestEditMissingSettingsFileShowsMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	body := editMessageBody(t, tui.Row{Provider: "claude", Account: "glm", Settings: path})
	if !strings.Contains(body, "settings.json is missing") {
		t.Fatalf("missing file not named:\n%s", body)
	}
}

// TestEditInvalidSettingsFileShowsMessageWithoutContents: the refusal names the
// file, and the file's bytes (here a token) never reach the message.
func TestEditInvalidSettingsFileShowsMessageWithoutContents(t *testing.T) {
	const sentinel = "sk-SENTINEL-123"
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"`+sentinel+`",,,`), 0o600); err != nil {
		t.Fatal(err)
	}
	body := editMessageBody(t, tui.Row{Provider: "claude", Account: "glm", Settings: path})
	if !strings.Contains(body, "settings.json is not valid JSON") {
		t.Fatalf("invalid file not named:\n%s", body)
	}
	if strings.Contains(body, sentinel) {
		t.Fatal("token from the settings file reached the message")
	}
}
