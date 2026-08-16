<div align="center">

# aiacc

### One command per Claude account.

Keep your **personal**, **work**, and **client** Claude Code accounts side by
side — each in its own isolated config — and launch any of them by name.

[![Release](https://img.shields.io/github/v/release/CarlosDanielDev/aiacc?sort=semver&color=6c8ebf)](https://github.com/CarlosDanielDev/aiacc/releases)
[![CI](https://github.com/CarlosDanielDev/aiacc/actions/workflows/ci.yml/badge.svg)](https://github.com/CarlosDanielDev/aiacc/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/CarlosDanielDev/aiacc?color=00ADD8)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
![Platforms](https://img.shields.io/badge/platforms-macOS%20·%20Linux-lightgrey)

</div>

```console
$ claude-work        # opens Claude Code signed in as your work account
$ claude-personal    # …a different account entirely — no re-login, no juggling
```

Bare `aiacc` opens an interactive launcher:

```text
┏━ AIACC ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓
┃                                              ┃
┃   ▄▀█ █ ▄▀█ █▀▀ █▀▀                          ┃
┃   █▀█ █ █▀█ █▄▄ █▄▄                          ┃
┃   // launch a profile                        ┃
┣━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┫
┃  ▸ claude-work          carlos@work.io       ┃
┃    claude-personal      not logged in        ┃
┃    ⚠ old                dir missing          ┃
┣━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┫
┃  ◆ 3 profiles   ✓ ready                      ┃
┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛
  ↑↓ move  ⏎ launch  a add  r rename  h hand off  d remove  q quit
```

> Cyberpunk / _Akira_ skin — neon on black behind a blood-red frame, with a
> **live shimmering** `AIACC` logo (magenta → red → cyan sweep) that **glitches**
> in short data-tear bursts, and a reverse-video glow on the selected row. Add /
> rename / remove / hand off / setup all happen in-screen. Honors `NO_COLOR`.

---

## ✨ Why aiacc

- **🔀 One command per account** — `claude-work` opens Claude Code signed into that account. No env juggling, no re-login.
- **⚡ One-step setup** — `aiacc setup` installs the commands onto your `PATH`; they work *immediately*, in the shell you're already in. No sourcing, no reload.
- **🧭 Interactive picker** — a bare `aiacc` gives you an arrow-key list to launch, add, rename, or remove accounts.
- **🔒 Never touches your credentials** — aiacc only points an environment variable at a config directory; each account's login stays in its own dir.
- **🪶 Zero dependencies** — a single static Go binary. Nothing to install alongside it.
- **🛟 Hard to misuse** — junk names can't be entered, broken profiles can't be launched, and destructive actions ask first.

<details>
<summary><b>How it works</b> (one generic mechanism)</summary>

<br>

Each account is an **isolated config directory, selected by an environment
variable.** Claude Code reads `CLAUDE_CONFIG_DIR`; point it at `~/.claude-work`
and Claude runs as your work account, at `~/.claude-personal` and it's the
personal one. `aiacc` registers those directories and installs a launcher command
per account that runs `CLAUDE_CONFIG_DIR=<dir> claude` for you — scoped to that
one launch, with no global state to get out of sync.

</details>

## 📦 Install

```sh
# Go toolchain
go install github.com/CarlosDanielDev/aiacc@latest

# Install script (latest release binary → /usr/local/bin or ~/.local/bin)
curl -fsSL https://raw.githubusercontent.com/CarlosDanielDev/aiacc/main/install.sh | sh

# Homebrew
brew install CarlosDanielDev/tap/aiacc
```

## 🚀 Quick start

```sh
# 1 · Add a couple of accounts (run `aiacc` and press `a`, or use flags):
aiacc add claude claude-work     --dir ~/.claude-work
aiacc add claude claude-personal --dir ~/.claude-personal

# 2 · Install the launcher commands — one step, works right away:
aiacc setup

# 3 · Launch by name:
claude-work
```

> The name you give an account **is** its launcher command — so name it how you
> want to type it (`claude-work`, `claude-client-x`, …). Names are limited to
> letters, digits, `-` and `_`, since they must be valid shell command names.

The first time you launch a fresh account, Claude Code opens signed out — run
`/login` inside it once, and that account's directory remembers it from then on.

## ⚡ Setup, in one step

```sh
aiacc setup
```

That's the whole thing. `aiacc setup` installs a small executable per account
(`claude-work`, …) into a directory on your `PATH`, so the commands work
**immediately, in the shell you're already in** — no sourcing, no reload, no new
terminal. It's idempotent (safe to re-run), and `aiacc add` / `aiacc remove` /
`aiacc rename` keep the commands in sync automatically.

> In the rare case that no writable directory is already on your `PATH`, aiacc
> installs into `~/.local/bin` and adds it to your `PATH` — the one situation
> where you'll open a new terminal to finish.

<details>
<summary>Prefer shell functions over executables?</summary>

<br>

`aiacc shell-init <shell>` prints them, if you'd rather add a line to your startup
file yourself:

```sh
eval "$(aiacc shell-init bash)"        # ~/.bashrc
eval "$(aiacc shell-init zsh)"         # ~/.zshrc
aiacc shell-init fish | source         # ~/.config/fish/config.fish
```

</details>

## 🎛️ Commands

| Command | What it does |
|---|---|
| `aiacc` | The interactive picker (front door). Piped/redirected, it prints help instead. |
| **`<account>`** &nbsp;e.g. `claude-work` | Launch Claude Code in that account (installed by `aiacc setup`). |
| `aiacc setup` | One-step install of the launcher commands onto your `PATH` — they work immediately. |
| `aiacc add [provider] [account] --dir <path>` | Register an account (framed screen with no args in a terminal). Creates the dir if missing. |
| `aiacc rename <provider> <old> <new>` | Rename an account **and** its launcher command, keeping its directory. _(picker: `r`)_ |
| `aiacc remove <provider> <account>` | Unregister an account; leaves the directory in place. _(picker: `d`)_ |
| `aiacc handoff [provider] [from] [to]` | Copy a session between accounts to resume it there. No args → interactive picker; `--session <id>`, `--launch`. _(picker: `h`)_ |
| `aiacc list` | Table of providers and their accounts. |
| `aiacc status` | Which config dir each provider's env var currently points at. |
| `aiacc usage [provider]` | Token totals per account, from local session logs. |
| `aiacc shell-init <bash\|zsh\|fish>` | Print the per-account launcher **functions** (alternative to `setup`). |

## ⚙️ Configuration

State lives in a single TOML file at `~/.config/aiacc/config.toml`
(`$XDG_CONFIG_HOME` is honored). `add` / `remove` / `rename` write it for you, but
it's plain text you can edit by hand:

```toml
[providers.claude]
env_var = "CLAUDE_CONFIG_DIR"

[providers.claude.accounts.claude-personal]
dir = "~/.claude-personal"

[providers.claude.accounts.claude-work]
dir = "~/.claude-work"
```

A leading `~` in a `dir` expands to your home directory. A missing config file is
treated as empty, so read-only commands work before you register anything.

## 🎬 Tutorials

<details open>
<summary><b>1 · Two accounts in a minute</b></summary>

<br>

```sh
# Register work + personal (each gets its own isolated config dir):
aiacc add claude claude-work     --dir ~/.claude-work
aiacc add claude claude-personal --dir ~/.claude-personal

# Install the launcher commands (one step, works immediately):
aiacc setup

# Launch — the first run opens Claude signed out; do /login once:
claude-work        # → /login as work
claude-personal    # → /login as personal
```

From now on, `claude-work` always opens Claude Code as work, `claude-personal` as
personal. No switching, no re-login.

</details>

<details>
<summary><b>2 · Hit a usage limit? Continue on your other account</b></summary>

<br>

You're deep in a conversation on `claude-work` and it hits the cap. Hand the exact
session to `claude-personal` and keep going:

```sh
aiacc handoff              # interactive: pick source → session → target
# or go straight there:
aiacc handoff claude claude-work claude-personal --launch
```

It copies just the session transcript into the other account and resumes it — same
context, now billed to personal. (In the picker, press `h` on the source account.)

</details>

<details>
<summary><b>3 · Rename a command</b></summary>

<br>

Named it `claude-work` but want `claude-acme`? In the picker, highlight it and
press `r` — or:

```sh
aiacc rename claude claude-work claude-acme
```

The account, its directory, and the launcher command all move together; the old
`claude-work` command is removed.

</details>

<details>
<summary><b>4 · Add a client account later</b></summary>

<br>

```sh
aiacc add claude claude-acme --dir ~/.claude-acme
```

The `claude-acme` command is created immediately (no re-`setup` needed), and shows
up in the picker next time you run `aiacc`.

</details>

### ⌨️ Picker keys

| Key | Action | Key | Action |
|:--:|---|:--:|---|
| `↑ ↓` / `j k` | move | `a` | add a profile |
| `⏎` | launch the selected profile | `r` | rename |
| `h` | hand off a session | `d` | remove (asks first) |
| `s` | run setup | `q` / `esc` | quit |

## 🔗 Share a session across accounts

Hit a usage limit mid-conversation? Hand the exact session to another account and
keep going:

```console
$ aiacc handoff claude claude-work claude-personal
Handed off session 2f1c… → claude-personal
  "Fix the launcher"
Resume it:
  cd ~/projects/aiacc && claude-personal --resume 2f1c…
```

Claude Code keeps each session as a transcript at
`<config-dir>/projects/<cwd>/<id>.jsonl`. `handoff` copies that transcript into
the target account (preserving its project directory) and prints the exact resume
command — `--launch` runs it for you. **Only the transcript moves; credentials are
never touched, and each account's usage stays separate.** It defaults to the most
recent session; `--session <id>` picks a specific one.

## 🔌 Providers

A provider is `{name, env_var}` plus its accounts. **Claude Code** is the built-in
preset — `claude` maps to `CLAUDE_CONFIG_DIR`, and aiacc knows to launch the
`claude` CLI for it.

Any other CLI that selects its config through an environment variable can be
registered too (set the provider's `env_var`) and appears in the picker — it just
can't be launched yet (marked `no launcher`), since aiacc only knows the `claude`
command so far. Launch commands for other providers are a natural next step.

## 🩺 Troubleshooting

<details>
<summary><code>claude-work: command not found</code></summary>

<br>

The launcher commands aren't installed yet, or the shell they were installed for
isn't this one. Run `aiacc setup` (it prints where it put them and whether they
work now). If it had to add a directory to your `PATH`, open a new terminal.

</details>

<details>
<summary>Upgraded from an old version and see <code>unknown flag: --shell</code></summary>

<br>

A pre-v0.7 shell hook is still loaded. Just run `aiacc` (it prints the fix) or open
a new terminal — the launcher commands replace the old hook.

</details>

<details>
<summary>A profile shows <code>⚠ no launcher</code> in the picker</summary>

<br>

That provider isn't Claude, and aiacc only knows how to launch the `claude` CLI so
far. The profile is still registered; launch commands for other providers are a
planned addition.

</details>

## 🤝 Contributing

Public and open to contributions. Read [CONTRIBUTING.md](CONTRIBUTING.md) and the
[architecture decision records](docs/adr/) first.

## 📄 License

[MIT](LICENSE) © [CarlosDanielDev](https://github.com/CarlosDanielDev)
