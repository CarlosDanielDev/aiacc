# 0008 — Launchers are the default switching mechanism; presets carry provider capabilities

Status: Accepted

Supersedes [0002](0002-generic-provider-driver.md) and [0003](0003-shell-eval-switching.md).

## Context

[0003](0003-shell-eval-switching.md) switched accounts with an eval hook. There
is no `use` subcommand now (`internal/cli/root.go:51-63` registers none). The
eval output survives only in `shell-init`, which old-hook users are told to
`eval` (`internal/cli/root.go:36`; `README.md:139-141`).
[0002](0002-generic-provider-driver.md) modelled a provider as `{name, env_var,
command}` plus accounts, with no provider special-cased in code. Presets have
since gained per-provider fields (`internal/provider/provider.go:19-38`), and
handoff is gated to claude (`internal/cli/handoff.go:226`).

## Decision

Kept from 0002: a provider is still `env_var` + `command` + account directories,
user-configurable (`internal/config/config.go:25-27`). Kept from 0003: the env
var is set only for the launched process, never in the parent shell.

Changed:

1. **Default mechanism: launcher executables.** `aiacc setup` writes one script
   per account onto PATH (`internal/cli/setup.go:100-128`). The body is generic
   over command and env var, with the account's settings args before `"$@"`:
   `exec env <ENV_VAR>='<dir>' <command> [<settings-flag> <path>] "$@"`
   (`internal/shell/shell.go:102-118`; args from `internal/provider/provider.go:161-167`).
   For claude, `<ENV_VAR>` is `CLAUDE_CONFIG_DIR`.
2. **Opt-in alternative: `shell-init`.** It prints per-account shell functions
   for `eval`/`source` (`internal/cli/shellinit.go:35-69`, built on
   `shell.Launcher`, `internal/shell/shell.go:48-71`). This is 0003's pattern,
   kept for users who choose it.
3. **Presets carry per-provider capabilities:** `SettingsFlag`, `Endpoints`,
   `Installs`, `Shared`, `State` (`internal/provider/provider.go:19-38`).
   Built-in presets are Go map entries (`provider.go:54-97`); custom providers
   are config entries. `Shared` and `State` are also set for codex
   (`provider.go:90-96`), so they are not claude-only. Handoff and claude
   detection stay claude-specific (`internal/cli/handoff.go:226`,
   `internal/claude/detect.go:34`).

## Consequences

- Shell quoting is reduced as the default path, not removed. `PosixQuote` is
  still used by the launcher body (`internal/shell/shell.go:114`), and
  `fishSingleQuote` and `PosixQuote` still guard `shell-init`
  (`internal/shell/shell.go:171,179`).
- A CLI-specific feature needs a preset field or a claude gate, not a generic
  code path. A new built-in provider is a Go map entry; a new custom provider is
  a config entry.
