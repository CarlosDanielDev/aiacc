# 0008 — Launchers are the default switching mechanism; presets carry provider capabilities

Status: Accepted

Supersedes [0002](0002-generic-provider-driver.md) and [0003](0003-shell-eval-switching.md).

## Context

[0003](0003-shell-eval-switching.md) changed `CLAUDE_CONFIG_DIR` in the current shell
through an `aiacc` wrapper that `eval`s the binary's output. There is no `use`
subcommand now (`internal/cli/root.go:51-63`), and re-sourcing erases the old
wrapper (`internal/cli/shellinit.go:44-47`). [0002](0002-generic-provider-driver.md)
said "no provider is special-cased in code"; claude is now special-cased, and presets
carry per-provider fields (`internal/provider/provider.go:19-38`).

## Decision

Still holds from 0002: a provider is selected by an env var that points at an
account directory. Built-in providers get it from the preset
(`internal/provider/provider.go:54-97`); custom providers from user config
(`internal/config/config.go:25-29`). User config overrides the preset
(`internal/provider/provider.go:106-114`). 0002's model still describes what is
stored: a provider names an env var, and an account is a name plus a config
directory. Since then a provider may also carry a launch `command`
(`internal/config/config.go:25-29`), and an account a settings path
(`config.go:17-23`; [0006](0006-account-settings-path-not-secret.md)).

Still holds from 0002 as a known limit: CLIs that select accounts by some other
means (a flag, an interactive login) are out of scope; revisit with a new ADR if
demand appears.

Dropped from 0003: the requirement to change the env var in the user's current
shell. Launchers scope it to the launched process.

Changed:

1. **Default: launcher executables.** `aiacc setup` writes one script per account
   onto PATH, running `exec env <VAR>=<dir> <command> [args] "$@"`
   (`internal/cli/setup.go:100-128`).
2. **Opt-in: `aiacc shell-init <shell>`.** It prints per-account function
   definitions, generated from the current config, that the user sources or evals
   once (`internal/cli/shellinit.go:35-72`). There is no `aiacc` wrapper function
   and nothing is eval'd per command.
3. **Presets carry capabilities:** `SettingsFlag` ([0006](0006-account-settings-path-not-secret.md)),
   `Endpoints` ([0007](0007-generate-never-read.md)), `Installs`, `Shared`, `State`
   (`internal/provider/provider.go:19-38`); codex sets `Shared` and `State` too
   (`provider.go:90-96`).
4. **Reversal of "no provider is special-cased".** Claude is special-cased in
   handoff, which is gated to claude (`internal/cli/handoff.go:223`).

## Consequences

- Shell quoting guards both paths: `Launcher` uses `PosixQuote` and
  `fishSingleQuote` for `shell-init` (`internal/shell/shell.go:64,67`), and the
  launcher body uses `PosixQuote` (`internal/shell/shell.go:114`).
- A CLI-specific feature needs a preset field or a claude gate, not a generic
  code path.
