# 0008 — Launchers are the default switching mechanism; presets carry provider capabilities

Status: Accepted

Supersedes [0002](0002-generic-provider-driver.md) and [0003](0003-shell-eval-switching.md).

## Context

[0003](0003-shell-eval-switching.md) required `aiacc use claude work` to change
`CLAUDE_CONFIG_DIR` in the user's current shell. It did so with an `aiacc` wrapper
function that `eval`s the binary's export output. There is no `use` subcommand now
(`internal/cli/root.go:51-63`). Re-sourcing erases the old wrapper
(`internal/cli/shellinit.go:44-47`, `:74-84`).

[0002](0002-generic-provider-driver.md) modelled a provider as
`{name, env_var, config_dir}` with "no provider special-cased in code". Claude is
now special-cased: handoff is gated to claude (`internal/cli/handoff.go:223`), and
presets carry per-provider capability fields (`internal/provider/provider.go:19-38`).

## Decision

Still holds from 0002: a provider is selected by an env var that points at an
account directory. For built-in providers that env var comes from the preset
(`internal/provider/provider.go:54-97`); for custom providers it is configured by
the user (`internal/config/config.go:25-29`). User config overrides the preset
(`internal/provider/provider.go:106-114`).

Still holds from 0002 as a known limit: CLIs that select accounts by some other
means (a flag, an interactive login) are out of scope; revisit with a new ADR if
demand appears.

Dropped from 0003: the requirement to change the env var in the user's current
shell. Launchers scope it to the launched process.

Changed:

1. **Default mechanism: launcher executables.** `aiacc setup` writes one script per
   account onto PATH (`internal/cli/setup.go:100-128`). The body is
   `exec env <ENV_VAR>='<dir>' <command> [<settings-flag> <path>] "$@"`
   (`internal/shell/shell.go:102-118`; args from `internal/provider/provider.go:161-167`).
   For claude, `<ENV_VAR>` is `CLAUDE_CONFIG_DIR`.
2. **Opt-in alternative: `aiacc shell-init <shell>`.** It prints static per-account
   shell functions (`internal/cli/shellinit.go:35-72`, built on `shell.Launcher`,
   `internal/shell/shell.go:48-71`) that the user evals or sources. There is no
   `aiacc` wrapper function; what the user evals or sources is the printed function
   definitions, not output the binary computes per command.
3. **Presets carry per-provider capabilities:** `SettingsFlag`, `Endpoints`,
   `Installs`, `Shared`, `State` (`internal/provider/provider.go:19-38`). Built-in
   presets are Go map entries (`provider.go:54-97`); custom providers are config
   entries with an optional `command` (`internal/config/config.go:25-29`). An
   account may carry a settings path (`internal/config/config.go:17-23`).
4. **Reversal of 0002's "no provider is special-cased in code".** Claude is
   special-cased: its login detection lives in a claude-only package
   (`internal/claude/detect.go:34`), and handoff is gated to claude
   (`internal/cli/handoff.go:223`, `launchesClaude`). `Shared`/`State` are also set
   for codex (`provider.go:90-96`), so the capability fields are not claude-only.

## Consequences

- Shell quoting guards both paths: the launcher body and `shell-init` use the same
  `PosixQuote`/`fishSingleQuote` idiom (launcher `internal/shell/shell.go:114`;
  `shell-init` `internal/shell/shell.go:171,179`).
- A CLI-specific feature needs a preset field or a claude gate, not a generic
  code path.
