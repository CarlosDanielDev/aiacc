# aiacc — multiple AI accounts, one switch

Switch and monitor multiple AI-CLI accounts (Claude Code, and any other CLI that
selects its config via an environment variable) without mixing your personal and
company sessions.

The whole idea is one generic mechanism: **each account is an isolated config
directory, selected by an environment variable.** Point Claude Code's
`CLAUDE_CONFIG_DIR` at `~/.claude-work` and it uses your work session; point it at
`~/.claude-personal` and it uses your personal one. `aiacc` registers those
directories, flips the env var for you, and reads each directory's local logs to
report token usage. Claude Code ships as a built-in preset. **aiacc never reads,
stores, or transmits your credentials** — each provider's config directory holds
its own; aiacc only points an env var at it.

## Install

```sh
# Go toolchain
go install github.com/CarlosDanielDev/aiacc@latest

# Install script (latest release binary → /usr/local/bin or ~/.local/bin)
curl -fsSL https://raw.githubusercontent.com/CarlosDanielDev/aiacc/main/install.sh | sh

# Homebrew
brew install CarlosDanielDev/tap/aiacc
```

## Shell setup

Switching accounts means changing an environment variable **in your current
shell**. A child process cannot mutate its parent shell's environment, so `aiacc`
can't do it by itself — the same reason `direnv` and `nvm` install a shell hook.

**Easiest: let aiacc set it up.** Run `aiacc` in a terminal. If the hook isn't
installed yet, it shows a one-time setup screen — press `i` and it appends the
right line to your shell's startup file. Open a new terminal and you're done.

Prefer to do it by hand? Add the matching line yourself, then open a new shell:

```sh
# ~/.bashrc
eval "$(aiacc shell-init bash)"

# ~/.zshrc
eval "$(aiacc shell-init zsh)"

# ~/.config/fish/config.fish
aiacc shell-init fish | source
```

`aiacc shell-init` prints a shell function named `aiacc` that wraps the binary
(for `use` and for a bare `aiacc` it evaluates the binary's export line in your
shell; every other command runs the binary directly), **plus one shortcut
function per registered account** — see [Switch by name](#switch-by-name) below.

Until the hook is active, the interactive `aiacc` shows the setup screen instead
of a picker, so you never land in a UI that silently fails to switch. Scripted or
piped, `aiacc use claude work` still just prints the `export` line for you to
eval yourself.

> **Upgrading from ≤ v0.2?** The hook changed (a bare `aiacc` front door and the
> per-account shortcuts). Re-run the `shell-init` line above — or just run
> `aiacc` and press `i` — then open a new shell.

## Switch by name

`aiacc shell-init` also emits a shortcut function for every account, named
`<provider>-<account>`. After the hook is loaded (new shell, or re-source), just
type the account:

```sh
$ claude-work        # switch this shell to claude / work
$ claude-personal    # switch back
```

Each shortcut is sugar for `aiacc use <provider> <account>`, so it validates the
directory and applies the switch the same way. New accounts get their shortcut on
your next shell (or after re-running the `shell-init` line). Accounts whose name
wouldn't make a safe function name are skipped and stay reachable via `aiacc use`.

## Quickstart

```sh
# Register two accounts. --dir is the config directory; aiacc creates it if
# missing. --quota is optional (a manual plan size, used by `usage`).
$ aiacc add claude personal --dir ~/.claude-personal
$ aiacc add claude work --dir ~/.claude-work --quota 200000000

# List everything you've registered.
$ aiacc list
PROVIDER  ACCOUNT   DIR
claude    personal  ~/.claude-personal
claude    work      ~/.claude-work

# Switch the current shell to an account, three ways:
$ claude-work        # shortcut function (needs the hook loaded)
$ aiacc use claude work

# Or launch the interactive picker — a clean one-line-per-account list showing
# which is active (●) and its login. Arrow keys / j·k move, enter switches,
# a adds a new account, d removes the highlighted one (with a confirm), q quits.
# Accounts you can't safely switch into (a missing dir, or a provider with no
# env var) are shown but Enter won't switch into them — though you can still
# remove them.
$ aiacc              # bare aiacc, in a terminal, IS the front door
$ aiacc use          # the same picker; 'aiacc use claude' scopes to one provider

# See which account is active per provider. '*' marks the live one;
# LAST-USED is the directory's modification time.
$ aiacc status
PROVIDER  ACCOUNT   ACTIVE  LAST-USED
claude    personal          2026-08-11 09:12
claude    work      *       2026-08-11 11:34

# Token usage per account, summed from each directory's local session logs.
# USED/QUOTA shows only when you set --quota.
$ aiacc usage
PROVIDER  ACCOUNT   INPUT    OUTPUT  TOTAL    USED/QUOTA
claude    personal  120345   45120   165465   -
claude    work      880210   402998  1283208  1283208/200000000

# Stop tracking an account (this does NOT delete the directory).
$ aiacc remove claude personal
```

`aiacc usage [provider]` accepts an optional provider name to filter the table.

## Configuration

State lives in a single TOML file at `~/.config/aiacc/config.toml`
(`$XDG_CONFIG_HOME` is honored if set). The `add` / `remove` commands write it for
you, but it's plain text you can edit by hand:

```toml
[providers.claude]
env_var = "CLAUDE_CONFIG_DIR"

[providers.claude.accounts.personal]
dir   = "~/.claude-personal"
quota = 0                       # optional manual plan size; 0 = unset

[providers.claude.accounts.work]
dir   = "~/.claude-work"
```

A leading `~` in a `dir` is expanded to your home directory. A missing config file
is treated as empty, so read-only commands work before you register anything.

## Providers & presets

A provider is just `{name, env_var}` plus its accounts. Claude Code is the
built-in preset, mapping `claude` to `CLAUDE_CONFIG_DIR` — so `aiacc add claude …`
fills the env var in automatically.

Any other CLI that selects its config through an environment variable works with
no code change: register the account, then set the provider's `env_var`. For
example, a tool that reads `FOO_CONFIG_HOME`:

```sh
aiacc add foo work --dir ~/.foo-work
```

then edit `~/.config/aiacc/config.toml` and set the provider's env var (a
non-preset provider is created with an empty `env_var`, and `use` errors until you
fill it in):

```toml
[providers.foo]
env_var = "FOO_CONFIG_HOME"
```

From then on `aiacc use foo work` emits `export FOO_CONFIG_HOME=~/.foo-work`
(or the `set -gx` form under fish).

## Command reference

| Command | Effect |
|---|---|
| `aiacc` | Launch the interactive picker (the front door), or the one-time setup screen if the hook isn't installed. Needs a terminal; piped or redirected, it prints help instead. |
| `<provider>-<account>` (e.g. `claude-work`) | Shortcut function from `shell-init`; switches straight to that account. |
| `aiacc add <provider> <account> --dir <path> [--quota N]` | Register an account; creates the directory if missing. |
| `aiacc remove <provider> <account>` | Unregister an account; leaves the directory in place. |
| `aiacc list` | Table of providers and their accounts. |
| `aiacc use [provider] [account]` | Switch the current shell (via the hook). Validates the directory exists. Omit the account for the interactive picker. |
| `aiacc status` | Active account per provider, from the live environment, plus last-used time. |
| `aiacc usage [provider]` | Token totals per account from local logs; `used/quota` when a quota is set. |
| `aiacc shell-init <bash\|zsh\|fish>` | Print the shell hook and per-account shortcut functions to add to your startup file. |

## Contributing

Public and open to contributions. Read [CONTRIBUTING.md](CONTRIBUTING.md) and the
[architecture decision records](docs/adr/) first.

## License

[MIT](LICENSE).
