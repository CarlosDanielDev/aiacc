package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/CarlosDanielDev/aiacc/internal/claude"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
)

// runAddWizard walks the user through registering a new Claude account: it shows
// the currently active account for context, asks for a name and config dir,
// creates the dir, and registers it. Logging in is left to `claude` itself —
// aiacc never handles credentials. Answers are read from in, prompts written to
// out (decoupled from os.Stdin/Stdout for testability).
func runAddWizard(in io.Reader, out io.Writer, cfgPath string) error {
	if cur := currentClaudeDir(); cur != "" {
		if info := claude.Detect(cur); info.LoggedIn {
			fmt.Fprintf(out, "Current Claude login: %s", info.Email)
			if info.Organization != "" {
				fmt.Fprintf(out, " (%s)", info.Organization)
			}
			fmt.Fprintln(out)
		}
	}

	r := bufio.NewReader(in)
	name := strings.TrimSpace(ask(r, out, "New account name: "))
	if name == "" {
		return fmt.Errorf("account name is required")
	}

	home, _ := os.UserHomeDir()
	defaultDir := filepath.Join(home, ".claude-"+name)
	dir := strings.TrimSpace(ask(r, out, fmt.Sprintf("Config dir [%s]: ", defaultDir)))
	if dir == "" {
		dir = defaultDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := saveAccount(cfgPath, "claude", name, dir, 0); err != nil {
		return err
	}

	fmt.Fprintf(out, "\nRegistered '%s' → %s\n", name, dir)
	fmt.Fprintf(out, "Switch to it:\n  aiacc use claude %s\n", name)
	fmt.Fprintf(out, "Or, in a new shell, just run:\n  claude-%s\n", name)
	fmt.Fprintf(out, "Then log in:\n  claude          # then /login\n")
	return nil
}

func ask(r *bufio.Reader, out io.Writer, label string) string {
	fmt.Fprint(out, label)
	line, _ := r.ReadString('\n') // EOF yields whatever was read; empty -> default
	return line
}

// currentClaudeDir is the active Claude config dir: $CLAUDE_CONFIG_DIR if set,
// else ~/.claude.
func currentClaudeDir() string {
	if d := os.Getenv(provider.Presets["claude"]); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}
