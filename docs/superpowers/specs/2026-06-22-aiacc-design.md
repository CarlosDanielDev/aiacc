# aiacc — design

Date: 2026-06-22
Status: Approved

## Purpose

Switch and monitor multiple AI-CLI accounts without mixing personal and company
tokens. One generic mechanism: each account is an isolated config directory
selected by an environment variable. Claude Code ships as a built-in preset; any
env-var-configurable CLI works via config.

## Non-goals (YAGNI)

- Web/desktop GUI.
- Reading, storing, or transmitting credentials/tokens — each provider's config
  dir holds its own; aiacc only points an env var at it.
- Multi-machine sync.
- Switching CLIs that select accounts by anything other than an env var.

## Core mechanism (see ADR-0003)

A child process can't mutate its parent shell's env. So `aiacc use <provider>
<account>` prints `export <ENV_VAR>=<dir>` to stdout, and a shell function
installed via `aiacc shell-init` evals it. Same pattern as `direnv`/`nvm`.
Read-only commands (`list`, `status`, `usage`) run as the plain binary.

## Configuration

Single file `~/.config/aiacc/config.toml` (respects `XDG_CONFIG_HOME`):

```toml
[providers.claude]
env_var = "CLAUDE_CONFIG_DIR"

[providers.claude.accounts.personal]
dir   = "~/.claude-personal"
quota = 0            # optional manual plan size; 0 = unset

[providers.claude.accounts.work]
dir   = "~/.claude-work"
```

A provider is `{name, env_var, config_dir}` (ADR-0002). Claude preset is built in.

## Commands

| Command | Type | Behavior |
|---|---|---|
| `aiacc add <provider> <account> --dir <path> [--quota N]` | write config | Register an account. Creates dir if missing. |
| `aiacc remove <provider> <account>` | write config | Unregister (does not delete the dir). |
| `aiacc list` | read | Table of providers → accounts. |
| `aiacc use <provider> <account>` | emit eval | Print export line; shell hook applies it. |
| `aiacc status` | read | Active account per provider (live env) + last-used (dir mtime). |
| `aiacc usage [provider]` | read | Tokens per account from logs; `used/quota` if quota set. |
| `aiacc shell-init <bash\|zsh\|fish>` | emit | Print the shell hook function. |

## Monitoring (see ADR-0004)

- **Token usage**: parse `<config_dir>/projects/**/*.jsonl`, sum input/output
  tokens per account. Accurate, local.
- **Plan/quota**: manual `quota` per account → show `used / quota`. No scraping,
  no unofficial endpoints. Labeled as user-provided.

## Package layout (Go — ADR-0001)

```
main.go                 # cobra root, wires subcommands
cmd/                    # use, list, add, remove, status, usage, shell-init
internal/
  config/               # TOML load/save; Provider, Account model
  provider/             # driver + claude preset; resolve env_var + dir
  shell/                # eval emit + hook templates (bash/zsh/fish)
  usage/                # JSONL parser, token aggregation
docs/adr/               # architecture decision records
docs/superpowers/specs/ # this design
```

Each `internal` package is single-purpose with a small interface:
`config` owns persistence, `provider` owns resolution, `shell` owns shell-safe
output, `usage` owns log parsing. `cmd` composes them; no business logic in `cmd`.

## Error handling

- Missing config → `aiacc` creates an empty one on first write; reads tolerate
  absence (empty list).
- Unknown provider/account → clear error to stderr, non-zero exit, nothing to eval.
- `use` validates the dir exists and emits only shell-safe, quoted output (the
  injection surface; unit-tested).
- Corrupt/partial JSONL lines are skipped, not fatal; usage degrades gracefully.

## Testing

- `config`: round-trip load/save; XDG path resolution.
- `provider`: preset resolution; unknown-provider error.
- `shell`: golden output per shell; reject unsafe dir strings.
- `usage`: parse a fixture log → expected token totals; skip malformed lines.

## Distribution (v0.5.0)

`go install` from day one. Then GoReleaser → GitHub Releases + Homebrew tap +
curl install script.

## Implementation order (milestones)

```
v0.1.0 Foundation        repo, governance, ADRs, CI, go layout
v0.2.0 Config core       config model + provider driver + add/remove/list   (needs v0.1.0)
v0.3.0 Switching         use + shell-init + status                          (needs v0.2.0)
v0.4.0 Monitoring        JSONL parser + usage + quota                       (needs v0.2.0)
v0.5.0 Polish & dist     GoReleaser/brew/install + README + (opt) TUI       (needs v0.3.0 + v0.4.0)
```

Critical path: v0.1.0 → v0.2.0 → (v0.3.0 ∥ v0.4.0) → v0.5.0.
