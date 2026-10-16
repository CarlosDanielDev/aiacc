# 0008 — Launchers replace the eval hook; presets carry provider capabilities

Status: Accepted

Supersedes [0003](0003-shell-eval-switching.md) in part (the eval-hook mechanism).
Amends [0002](0002-generic-provider-driver.md) (presets now carry per-provider
capabilities) and [0001](0001-language-go.md) (the TUI is not bubbletea).

## Context

[0003](0003-shell-eval-switching.md) chose an eval hook so `aiacc use` could change
the current shell. That mechanism is gone. No `use` subcommand is registered
(`internal/cli/root.go:51-63`), and `shell-init` erases the old `aiacc` function
(`internal/cli/shellinit.go:44-47`). [0002](0002-generic-provider-driver.md) said
no provider is special-cased in code. That is no longer fully true: the preset
carries `SettingsFlag`, `Endpoints`, `Installs` and `Shared`/`State`
(`internal/provider/provider.go:19-38`), and handoff and detection are
claude-only (`internal/cli/handoff.go:226`, `internal/claude/detect.go:34`).

## Decision

1. An account is switched by a **launcher executable**. `aiacc setup` writes one
   script per account onto `PATH` (`internal/cli/setup.go:100-128`). Each runs
   `exec env CLAUDE_CONFIG_DIR=<dir> claude "$@"` (`internal/shell/shell.go:114`).
   No shell state is changed, so there is no eval and no wrapper function.
   `aiacc shell-init <shell>` still prints the same per-account functions as an
   alternative (`internal/cli/shellinit.go:35-69`).
2. A **preset carries per-provider capabilities**. Features that need to know a
   CLI (settings flag, known endpoints, install paths, transcript handoff) are
   data on `Preset` or are gated on the `claude` preset. Other providers get the
   generic env-var and directory switching only.
3. The TUI is **hand-rolled ANSI on `stty`**, not bubbletea. `go.mod` requires only
   `cobra` and `toml` (`go.mod:5-8`), and `internal/tui/tui.go:16,92-94` draws with
   escape codes. Go as the language (0001) still stands.

## Consequences

- The eval-safety surface that 0003 required (strict quoting, unit-tested) is
  replaced by launcher generation: `PosixQuote` and `validateDir`
  (`internal/shell/shell.go:153,171`).
- Users with the old hook are fixed by re-running `shell-init` or opening a new
  terminal (`internal/cli/shellinit.go:44-47`).
- Adding a provider is still a config entry. Adding a CLI-specific feature means
  a new preset field or a claude gate, not a generic code path.
