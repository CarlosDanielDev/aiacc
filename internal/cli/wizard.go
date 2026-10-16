package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/CarlosDanielDev/aiacc/internal/claude"
	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
)

// addPrefill carries the add flags into the wizard: a flag the user already
// gave preempts its question instead of being silently dropped, so a piped
// script answers only the questions that remain.
type addPrefill struct {
	Dir, Settings, Env, Command string
	Quota                       int
}

// runAddWizard walks the user through registering a new account: it asks which
// provider the account belongs to (empty answer = claude), shows the currently
// active claude login for context, then asks for a name and config dir, creates
// the dir, and registers it — asking for a settings file or endpoint keyword
// when the provider's CLI can load one. Answers may also arrive piped
// (non-tty stdin), and every flag in pre is applied without re-asking. After
// saving it keeps the launcher command and the shared assets in sync, exactly
// like the flag path and the add screen. Logging in is left to the CLI itself —
// aiacc never handles credentials. Answers are read from in, prompts written to
// out (decoupled from os.Stdin/Stdout for testability).
func runAddWizard(in io.Reader, out io.Writer, cfgPath string, pre addPrefill) error {
	r := bufio.NewReader(in)

	providerName := strings.TrimSpace(ask(r, out, "Provider [claude]: "))
	if providerName == "" {
		providerName = "claude"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if _, ok := provider.Presets[providerName]; !ok {
		if _, configured := cfg.Providers[providerName]; !configured {
			return fmt.Errorf("unknown provider %q (valid: %s)",
				providerName, strings.Join(validProviders(cfg), ", "))
		}
	}

	if providerName == "claude" {
		if cur := currentClaudeDir(); cur != "" {
			if info := claude.Detect(cur); info.LoggedIn {
				fmt.Fprintf(out, "Current Claude login: %s", info.Email)
				if info.Organization != "" {
					fmt.Fprintf(out, " (%s)", info.Organization)
				}
				fmt.Fprintln(out)
			}
		}
	}

	name := strings.TrimSpace(ask(r, out, "New account name: "))
	if name == "" {
		return fmt.Errorf("account name is required")
	}

	dir := pre.Dir
	if dir == "" {
		home, _ := os.UserHomeDir()
		defaultDir := filepath.Join(home, ".claude-"+name)
		dir = strings.TrimSpace(ask(r, out, fmt.Sprintf("Config dir [%s]: ", defaultDir)))
		if dir == "" {
			dir = defaultDir
		}
	} else {
		fmt.Fprintf(out, "Config dir: %s (from --dir)\n", dir)
	}

	settings := pre.Settings
	switch {
	case settings != "":
		fmt.Fprintf(out, "Settings: %s (from --settings)\n", settings)
	case provider.Presets[providerName].SettingsFlag != "":
		settings = strings.TrimSpace(ask(r, out, "Settings file or endpoint keyword [none]: "))
	}

	// The dir is created expanded — a literal ~ must not grow a "./~" tree in
	// the working directory — while the account registers the as-typed form:
	// the same one-expansion rule the flag path and the add screen follow.
	// The settings answer resolves the same way too, so a keyword generates
	// its skeleton into the real profile and a refusal lands before anything
	// is registered.
	realDir, err := provider.ExpandHome(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		return err
	}
	settingsPath, err := resolveSettings(providerName, dir, settings)
	if err != nil {
		return err
	}
	if err := saveAccount(cfgPath, providerName, name, dir, pre.Quota, pre.Env, pre.Command, settingsPath); err != nil {
		return err
	}
	syncLauncher(cfgPath, providerName, name) // keep the command in sync (best-effort)
	linkProfile(providerName, realDir)        // same skills/agents as the bare CLI

	command, _ := provider.Command(cfg, providerName)
	if command == "" {
		command = providerName
	}
	fmt.Fprintf(out, "\nRegistered '%s' → %s\n", name, dir)
	fmt.Fprintf(out, "Launch %s as this profile:\n", command)
	fmt.Fprintf(out, "  aiacc            # pick it from the list, or\n")
	fmt.Fprintf(out, "  %s               # after adding: aiacc shell-init <shell> to your startup file\n", name)
	fmt.Fprintf(out, "The first run opens %s; log in there when prompted.\n", command)
	return nil
}

// validProviders lists the provider names the wizard accepts: the presets plus
// any provider already in the config, sorted and deduplicated — the same set
// the add screen's picker offers, spelled out for a line-based answer.
func validProviders(c *config.Config) []string {
	names := make([]string, 0, len(provider.Presets)+len(c.Providers))
	for name := range provider.Presets {
		names = append(names, name)
	}
	for name := range c.Providers {
		names = append(names, name)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func ask(r *bufio.Reader, out io.Writer, label string) string {
	fmt.Fprint(out, label)
	line, _ := r.ReadString('\n') // EOF yields whatever was read; empty -> default
	return line
}

// currentClaudeDir is the active Claude config dir: $CLAUDE_CONFIG_DIR if set,
// else ~/.claude.
func currentClaudeDir() string {
	if d := os.Getenv(provider.Presets["claude"].EnvVar); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}
