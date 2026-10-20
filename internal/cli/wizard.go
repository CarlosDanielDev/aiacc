package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/CarlosDanielDev/aiacc/internal/config"
	"github.com/CarlosDanielDev/aiacc/internal/provider"
	"github.com/CarlosDanielDev/aiacc/internal/tui"
)

// addPrefill carries the add flags into the wizard: a flag the user already
// gave preempts its question instead of being silently dropped, so a piped
// script answers only the questions that remain.
type addPrefill struct {
	Dir, Settings, Env, Command string
	Quota                       int
	BaseURL, Model, ModelName   string // the custom endpoint's answers
}

// runAddWizard walks the user through registering a new account. The questions
// keep their historical order — name, then config dir — so a script that pipes
// those two lines still works; the provider and the settings are asked after
// them, and an empty answer (or the end of input) means claude and no settings.
// The active claude login is shown for context first. Answers may also arrive
// piped (non-tty stdin), and every flag in pre is applied without re-asking.
// The exception is --env and --command, which fill a provider only while it
// has none; a note says when one is ignored.
// After saving it keeps the launcher command and the shared assets in sync,
// exactly like the flag path and the add screen. Logging in is left to the CLI
// itself — aiacc never handles credentials. Answers are read from in, prompts
// written to out (decoupled from os.Stdin/Stdout for testability).
func runAddWizard(in io.Reader, out io.Writer, cfgPath string, prefill ...addPrefill) error {
	pre := addPrefill{}
	if len(prefill) > 0 {
		pre = prefill[0]
	}
	r := bufio.NewReader(in)

	if cur := currentClaudeDir(); cur != "" {
		if email, org, ok := loginOf(cur); ok {
			fmt.Fprintf(out, "Current Claude login: %s", email)
			if org != "" {
				fmt.Fprintf(out, " (%s)", org)
			}
			fmt.Fprintln(out)
		}
	}

	name := strings.TrimSpace(ask(r, out, "New account name: "))
	if name == "" {
		return fmt.Errorf("account name is required")
	}
	// Refused here, before the dir question and any write: the name becomes a
	// directory suffix and a launcher filename.
	if !tui.ValidName(name) {
		return invalidNameError(name)
	}

	home, _ := os.UserHomeDir()
	dir := pre.Dir
	if dir == "" {
		dir = strings.TrimSpace(ask(r, out, fmt.Sprintf(
			"Config dir [default: ~/.<provider>-%s, i.e. ~/.claude-%s for claude]: ", name, name)))
	} else {
		fmt.Fprintf(out, "Config dir: %s (from --dir)\n", dir)
	}
	// The default depends on the provider, which is only known after this
	// question: an empty answer takes ~/.claude-<name> for claude and
	// ~/.<provider>-<name> for any other provider.
	defaulted := dir == ""

	providerName := strings.TrimSpace(ask(r, out, "Provider [claude]: "))
	if providerName == "" {
		providerName = "claude"
	}
	if defaulted {
		dir = filepath.Join(home, "."+providerName+"-"+name) // ~/.claude-<name> for claude
		// The default was only a pattern when it was asked; show the path it
		// resolved to before the account is registered under it.
		fmt.Fprintf(out, "Config dir: %s (default for %s)\n", dir, providerName)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	// The wizard only adds accounts to providers the picker offers: the presets
	// and the ones already configured. A brand-new custom provider is created by
	// the flag form, which takes the env var and the command it needs.
	if _, ok := provider.Presets[providerName]; !ok {
		if _, configured := cfg.Providers[providerName]; !configured {
			return fmt.Errorf("unknown provider %q (valid: %s); to create a new provider run "+
				"\"aiacc add <provider> <account> --dir <dir> --env <ENV_VAR> --command <cli>\"",
				providerName, strings.Join(validProviders(cfg), ", "))
		}
	}

	// A name another provider owns is refused now, before the settings questions
	// and before any write (the dir is created further down).
	if err := checkNewName(cfg, providerName, name); err != nil {
		return err
	}

	// A prefilled --env or --command reaches saveAccount, which fills the
	// provider's env var and command only while it has none. Say so when a
	// flag will be ignored, instead of dropping it silently.
	existing := cfg.Providers[providerName]
	if pre.Env != "" && existing.EnvVar != "" {
		fmt.Fprintf(out, "note: --env ignored — provider %s already uses %q\n", providerName, existing.EnvVar)
	}
	if pre.Command != "" && existing.Command != "" {
		fmt.Fprintf(out, "note: --command ignored — provider %s already uses %q\n", providerName, existing.Command)
	}

	settings := pre.Settings
	switch {
	case settings != "":
		fmt.Fprintf(out, "Settings: %s (from --settings)\n", settings)
	case provider.Presets[providerName].SettingsFlag != "":
		settings = strings.TrimSpace(ask(r, out, "Settings file or endpoint keyword [none]: "))
		if strings.EqualFold(settings, "none") {
			settings = "" // "none" is the prompt's default, so it means no settings
		}
	}

	// Every refusal is decided before the first filesystem write: the taken name,
	// then the settings answer (a typo keyword, custom, a provider with no
	// settings flag). Only then is the dir created; a generated skeleton creates
	// it too.
	if err := settingsFlagRefusal(providerName, settings); err != nil {
		return err
	}
	ep, custom := customPreset(providerName, settings)
	if custom {
		if ep, err = askCustomEndpoint(r, out, ep, pre); err != nil {
			return err
		}
	} else {
		var given []string
		for flag, v := range map[string]string{"--base-url": pre.BaseURL, "--model": pre.Model, "--model-name": pre.ModelName} {
			if v != "" {
				given = append(given, flag)
			}
		}
		slices.Sort(given)
		if err := endpointFlagsRefusal(settings, given); err != nil {
			return err
		}
		if _, _, err := settingsEndpoint(providerName, settings); err != nil {
			return err
		}
	}
	// The dir is created expanded — a literal ~ must not grow a "./~" tree in
	// the working directory — while the account registers the as-typed form.
	realDir, err := provider.ExpandHome(dir)
	if err != nil {
		return err
	}
	// The settings answer above was typed interactively: check the name against
	// the config as it is now, before the dir is created.
	if err := checkNameOnDisk(cfgPath, providerName, name); err != nil {
		return err
	}
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		return err
	}
	var settingsPath string
	if custom {
		settingsPath, err = provider.EnsureSkeleton(dir, ep)
	} else {
		settingsPath, err = resolveSettings(providerName, dir, settings)
	}
	if err != nil {
		return err
	}
	if err := saveAccount(cfgPath, providerName, name, dir, pre.Quota, pre.Env, pre.Command, settingsPath); err != nil {
		return err
	}
	if err := syncLauncher(cfgPath, providerName, name); err != nil { // keep the command in sync
		fmt.Fprintln(out, "aiacc:", err)
	}
	linkProfile(providerName, realDir) // same skills/agents as the bare CLI

	// Read the command after the save: saving may have registered a custom
	// provider (--env/--command), which the config loaded above does not know.
	cfg, err = config.Load(cfgPath)
	if err != nil {
		return err
	}
	command, _ := provider.Command(cfg, providerName)
	if command == "" {
		command = providerName
	}
	fmt.Fprintf(out, "\nRegistered '%s' → %s\n", name, dir)
	if custom {
		printKeyHint(out, realDir, ep)
	}
	// Claude keeps the wording the wizard always printed; other providers name
	// their own CLI, since the wizard now asks which provider it is.
	if providerName == "claude" {
		fmt.Fprintf(out, "Launch Claude Code as this profile:\n")
	} else {
		fmt.Fprintf(out, "Launch %s as this profile:\n", command)
	}
	fmt.Fprintf(out, "  aiacc            # pick it from the list, or\n")
	fmt.Fprintf(out, "  %s               # after adding: aiacc setup puts it on your PATH\n", name)
	if providerName == "claude" {
		fmt.Fprintf(out, "The first run opens Claude Code; log in there with /login.\n")
	} else {
		fmt.Fprintf(out, "The first run opens %s; log in there when prompted.\n", command)
	}
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

// askCustomEndpoint takes the custom endpoint's base URL, model and display
// name, each from its prefilled flag or else asked. An invalid answer is
// re-asked up to three times; the third aborts the add before anything is
// written. The API key is never asked (ADR-0007).
func askCustomEndpoint(r *bufio.Reader, out io.Writer, ep provider.Endpoint, pre addPrefill) (provider.Endpoint, error) {
	base, err := askValid(r, out, "Base URL", pre.BaseURL, "--base-url", provider.CheckBaseURL)
	if err != nil {
		return provider.Endpoint{}, err
	}
	model, err := askValid(r, out, "Model", pre.Model, "--model", func(m string) (string, error) {
		return m, provider.CheckModel(m)
	})
	if err != nil {
		return provider.Endpoint{}, err
	}
	name := pre.ModelName
	if name == "" {
		name = strings.TrimSpace(ask(r, out, fmt.Sprintf("Model display name [%s]: ", model)))
	} else {
		fmt.Fprintf(out, "Model display name: %s (from --model-name)\n", name)
	}
	return provider.FillEndpoint(ep, base, model, name)
}

// askValid returns the prefilled value if given, checked once, or else asks
// label until check accepts the answer, at most three times.
func askValid(r *bufio.Reader, out io.Writer, label, given, flag string, check func(string) (string, error)) (string, error) {
	if given != "" {
		v, err := check(strings.TrimSpace(given))
		if err == nil {
			fmt.Fprintf(out, "%s: %s (from %s)\n", label, v, flag)
		}
		return v, err
	}
	var err error
	for range 3 {
		var v string
		if v, err = check(strings.TrimSpace(ask(r, out, label+": "))); err == nil {
			return v, nil
		}
		fmt.Fprintf(out, "  %v\n", err)
	}
	return "", fmt.Errorf("%w (3 tries, nothing written)", err)
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
