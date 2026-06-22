# 0003 — Shell switching via eval hook

Status: Accepted

## Context

`aiacc use claude work` must change `CLAUDE_CONFIG_DIR` in the user's *current*
interactive shell. A child process cannot mutate its parent's environment, so the
binary alone cannot do this.

## Decision

Adopt the **eval-hook** pattern used by `direnv`, `nvm`, and `pyenv`:

1. The binary prints shell-export lines to stdout for `use` (e.g.
   `export CLAUDE_CONFIG_DIR=/Users/x/.claude-work`).
2. `aiacc shell-init <bash|zsh|fish>` prints a small shell function named `aiacc`
   that calls the real binary and `eval`s its output when the subcommand mutates
   the environment. The user adds one line to their rc file:
   `eval "$(aiacc shell-init zsh)"`.
3. Read-only subcommands (`list`, `status`, `usage`) run as the plain binary; only
   `use` (and similar) produce eval-able output.

## Consequences

- One-time setup per machine, then `aiacc use …` "just works" in-shell.
- Three shells to support (bash, zsh, fish); fish uses `set -gx`, not `export`.
  Each gets a tested template in `internal/shell`.
- Output destined for `eval` must be strictly shell-safe — directories are
  validated and quoted. This is the main injection surface and is unit-tested.
- Without the hook installed, `use` still prints the export line, so users can
  copy-paste or wire their own alias; the hook is convenience, not a hard
  dependency.
