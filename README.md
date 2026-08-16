# aiacc — one command per Claude account

Keep several Claude Code accounts side by side — personal, work, a client's — each
in its own isolated config directory, and launch any of them by name:

```sh
$ claude-work        # opens Claude Code signed in as your work account
$ claude-personal    # …and this one is a different account entirely
```

The whole idea is one generic mechanism: **each account is an isolated config
directory, selected by an environment variable.** Claude Code reads
`CLAUDE_CONFIG_DIR`; point it at `~/.claude-work` and Claude runs as your work
account, point it at `~/.claude-personal` and it's the personal one. `aiacc`
registers those directories and gives you a launcher command per account that
runs `CLAUDE_CONFIG_DIR=<dir> claude` for you — scoped to that one launch, with no
global state to get out of sync. **aiacc never reads, stores, or transmits your
credentials** — each account's config directory holds its own; aiacc only points
an env var at it and runs the CLI.

## Install

```sh
# Go toolchain
go install github.com/CarlosDanielDev/aiacc@latest

# Install script (latest release binary → /usr/local/bin or ~/.local/bin)
curl -fsSL https://raw.githubusercontent.com/CarlosDanielDev/aiacc/main/install.sh | sh

# Homebrew
brew install CarlosDanielDev/tap/aiacc
```

## Quickstart

```sh
# Add a couple of accounts. Run `aiacc` and press `a`, or use the flags:
$ aiacc add claude claude-work --dir ~/.claude-work
$ aiacc add claude claude-personal --dir ~/.claude-personal

# Run aiacc with no arguments: an interactive launcher. Arrow keys / j·k move,
# enter launches the selected account's Claude Code, a adds, d removes, q quits.
$ aiacc

# Install the shortcut commands once (see Shell setup), then launch by name:
$ claude-work
$ claude-personal
```

The name you give an account **is** its launcher command, so name it how you want
to type it: `claude-work`, `claude-client-x`, whatever. Names are limited to
letters, digits, `-` and `_` (they have to be valid shell command names).

## Shell setup

To type `claude-work` from anywhere, the launcher functions need to load in your
shell. Let aiacc do it:

```sh
$ aiacc setup
```

`aiacc setup` is a guided, step-by-step screen: it detects your shell, adds the
one line to the right startup file for you (press `i`), then shows the exact
command to reload and the exact command to try. It's idempotent — safe to re-run
— and the picker nudges you toward it (`s`) until it's done.

Prefer to do it by hand? `aiacc shell-init <shell>` prints the launcher functions;
add the matching line to your startup file yourself:

```sh
# ~/.bashrc
eval "$(aiacc shell-init bash)"

# ~/.zshrc
eval "$(aiacc shell-init zsh)"

# ~/.config/fish/config.fish
aiacc shell-init fish | source
```

What it emits, for example under bash:

```sh
claude-work() { CLAUDE_CONFIG_DIR='/home/you/.claude-work' command claude "$@"; }
claude-personal() { CLAUDE_CONFIG_DIR='/home/you/.claude-personal' command claude "$@"; }
```

Add a new account and its command appears the next time you open a shell (or
re-run the `shell-init` line). You never need the shortcuts to use aiacc — a bare
`aiacc` launches from the picker regardless — they're just there for speed. The
first time you launch a fresh account, Claude Code opens signed out; run `/login`
inside it once and that account's directory remembers it from then on.

## Configuration

State lives in a single TOML file at `~/.config/aiacc/config.toml`
(`$XDG_CONFIG_HOME` is honored if set). The `add` / `remove` commands write it for
you, but it's plain text you can edit by hand:

```toml
[providers.claude]
env_var = "CLAUDE_CONFIG_DIR"

[providers.claude.accounts.claude-personal]
dir = "~/.claude-personal"

[providers.claude.accounts.claude-work]
dir = "~/.claude-work"
```

A leading `~` in a `dir` is expanded to your home directory. A missing config file
is treated as empty, so read-only commands work before you register anything.

## Providers

A provider is `{name, env_var}` plus its accounts. Claude Code is the built-in
preset: `claude` maps to `CLAUDE_CONFIG_DIR`, and aiacc knows to launch the
`claude` CLI for it. That's the one with a launcher today.

Any other CLI that selects its config through an environment variable can still be
registered — set the provider's `env_var` in the config — and it shows up in the
picker. It just can't be launched yet (the picker marks it `no launcher`), because
aiacc only knows the `claude` command so far. Launch commands for other providers
are a natural next addition.

## Command reference

| Command | Effect |
|---|---|
| `aiacc` | Launch the interactive picker (the front door). Needs a terminal; piped or redirected, it prints help instead. |
| `<account>` (e.g. `claude-work`) | Launcher function from `shell-init`; opens Claude Code in that account. |
| `aiacc add [provider] [account] --dir <path>` | Register an account (framed screen when run with no arguments in a terminal); creates the directory if missing. |
| `aiacc remove <provider> <account>` | Unregister an account; leaves the directory in place. (Or press `d` in the picker.) |
| `aiacc setup` | Guided, step-by-step install of the launcher commands into your shell startup file. |
| `aiacc list` | Table of providers and their accounts. |
| `aiacc status` | Which config dir each provider's env var currently points at. |
| `aiacc usage [provider]` | Token totals per account from local session logs. |
| `aiacc shell-init <bash\|zsh\|fish>` | Print the per-account launcher functions to add to your startup file. |

## Contributing

Public and open to contributions. Read [CONTRIBUTING.md](CONTRIBUTING.md) and the
[architecture decision records](docs/adr/) first.

## License

[MIT](LICENSE).
