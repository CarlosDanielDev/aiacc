# 0010 — Retire shell-init softly: a hidden stub that still evals cleanly

Status: Accepted

Refines [0008](0008-launchers-replace-eval-hook.md): `shell-init` stops printing shell functions. The command stays.

## Context

[0008](0008-launchers-replace-eval-hook.md) kept `aiacc shell-init <shell>` as an opt-in
that printed per-account shell functions. Startup files still contain
`eval "$(aiacc shell-init <shell>)"`. Deleting the command would make every such shell
print an error at startup. The pre-v0.7 `aiacc` wrapper function was removed in v0.7.0
(commit 49519da; the v0.6.0 README documented it, v0.7.0 does not). The one-time erase of
that wrapper was added in v0.7.1 (commit 5ec68d4) for shells that loaded it before upgrading.

## Decision

`shell-init` is hidden from help, exits 0, and prints a comment that points at
`aiacc setup`. It also prints the one-time erase of the pre-v0.7 wrapper for bash, zsh
and fish. The erase is kept: it is the only thing that repairs a live shell on re-source,
and it is one line. The shell-function generator (`shell.Launcher`) is deleted. Launcher
scripts are unchanged.

## Consequences

- Existing startup files keep working and point users to `aiacc setup`.
- A test evals the stub's output under sh, bash, zsh and fish, and skips shells that are
  not installed. Each shell runs with its startup files disabled.
- Removing the command entirely needs a later ADR, once nothing supported still emits it.
