<div align="center">

# aiacc

### One command per AI-CLI account.

Keep your **personal**, **work**, and **client** accounts for **Claude Code**,
**Codex**, and any other AI CLI side by side — each in its own isolated config —
and launch any of them by name.

Live page: [carlosdanieldev.github.io/aiacc](https://carlosdanieldev.github.io/aiacc/)

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
┃  ◆ 3 profiles   ✓ ready              v0.21.0 ┃
┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛
  ↑↓ move  ⏎ launch  a add  r rename  e edit  h hand off  d remove  q quit
```

> Hacker / matrix-terminal skin — **phosphor green on black**, a `>` prompt with
> a blinking cursor and cyan hotkeys, a `» N profiles :: ✓ READY` status readout with the build version dim at its right edge,
> and a **live** `AIACC` logo (dark-green → bright-green → cyan sweep) that
> **glitches** in short data-tear bursts, plus a reverse-video glow on the
> selected row. Add / rename / edit / remove / hand off / setup all happen in-screen.
> Honors `NO_COLOR`.
>
> **Responsive** — the frame follows the terminal: it grows and narrows with
> the width, re-measures live on resize, and a profile list taller than the
> window scrolls *inside* the box (`▲ n above · ▼ n below`) instead of
> spilling out of it.

---

## ✨ Why aiacc

- **🔀 One command per account** — `claude-work` opens Claude Code signed into that account. No env juggling, no re-login.
- **⚡ One-step setup** — `aiacc setup` installs the commands onto your `PATH`; they work *immediately*, in the shell you're already in. No sourcing, no reload.
- **🧭 Interactive picker** — a bare `aiacc` gives you an arrow-key list to launch, add, rename, or remove accounts.
- **🧩 Same skills everywhere** — every profile shares your skills, sub-agents, slash commands and hooks, so `claude-work` is the same tool as `claude`, not an empty one.
- **🔒 Never touches your credentials** — aiacc only points an environment variable at a config directory; each account's login stays in its own dir.
- **🪶 Nothing to install at runtime** — a single static Go binary, with no runtime or helper to set up alongside it.
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
<summary>Migrating from <code>aiacc shell-init</code>?</summary>

<br>

`shell-init` is retired and prints only a comment. If your startup file still has
an `eval "$(aiacc shell-init …)"` line, replace it with `aiacc setup` and delete the
line. Open a new terminal to pick it up.

</details>

## 🧩 Same skills in every profile

A profile is an isolated config directory — and *everything* the CLI discovers
lives in that directory. Point `CLAUDE_CONFIG_DIR` somewhere new and Claude Code
starts with no skills, no sub-agents, no slash commands and no `CLAUDE.md`: the
same binary, a much emptier tool.

`aiacc setup` fixes that in the same step, and `aiacc link` does it on demand:

```sh
aiacc link                    # share with every profile
aiacc link claude work        # …or just one
aiacc link --replace          # take over entries a profile already has
```

It **symlinks** your authored assets out of the CLI's own config dir, so they
stay in one place — edit a skill once and every profile has it:

| Provider | Shared |
|---|---|
| `claude` (`~/.claude`) | `skills/` · `agents/` · `commands/` · `hooks/` · `output-styles/` · `plugins/` · `CLAUDE.md` · `settings.json` |
| `codex` (`~/.codex`) | `prompts/` · `AGENTS.md` |

What is **never** shared: credentials, `projects/` transcripts, `history.jsonl`,
usage counters — isolating those is the whole point of a profile.

> **Nothing is overwritten.** An entry a profile already has of its own is left
> exactly as it is and reported instead. `--replace` opts into taking it over,
> and even then the original is renamed to `<name>.aiacc-bak` first
> (`.aiacc-bak2`, `.aiacc-bak3`, … when one is already there — the report names
> the path chosen), never deleted. Custom (non-preset) providers are skipped —
> aiacc doesn't guess at a CLI's asset layout.

## 🔑 A profile with its own endpoint

Some profiles differ in what they talk to, not who is logged in: Claude Code
pointed at GLM, a proxy, or a gateway through an `env` block in a settings
file. That block can't go in the shared `settings.json`, because every profile
would pick it up — the profile needs a settings file of its own.

The easy way is to let aiacc write it. Run `aiacc`, press `a`, name the
profile, and put `glm` in its **settings** field. aiacc generates
`~/.claude-glm/glm.json` with the GLM endpoint already filled in and a
placeholder where your key goes, created `chmod 600`:

```json
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "PASTE-YOUR-KEY-HERE",
    "ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic",
    "ANTHROPIC_CUSTOM_MODEL_OPTION": "glm-5.3-flash[1m]",
    "ANTHROPIC_CUSTOM_MODEL_OPTION_NAME": "GLM 5.3 Flash 1M (z.ai)",
    "ANTHROPIC_MODEL": "glm-5.3-flash[1m]"
  }
}
```

Open the file it names, paste your API key over the placeholder, and launch —
`claude-glm` now talks to GLM from any directory. `custom` is the other
preset: the same step, with the base URL left blank for you to fill in (add
any model fields yourself). A settings file that is already there aborts the
create — nothing is overwritten, ever.

Anything the presets don't cover, write the file yourself and hand aiacc the
path. `glm` works on the command line too — `--settings glm` generates the
same skeleton non-interactively (an existing file aborts, nothing is
overwritten), while `custom` stays picker-only, since its blank base URL is
yours to type in:

```sh
aiacc add claude claude-glm --dir ~/.claude-glm --settings glm                     # generate it
aiacc add claude claude-glm --dir ~/.claude-glm --settings ~/.claude-glm/glm.json # or hand-write it
```

```json
{ "env": { "ANTHROPIC_BASE_URL": "…", "ANTHROPIC_AUTH_TOKEN": "…", "ANTHROPIC_MODEL": "…" } }
```

Either way, the launcher runs `claude --settings ~/.claude-glm/glm.json`,
which layers that file over the shared settings, so `claude-glm` keeps your
skills, hooks and statusline and talks to its own endpoint from any directory.

aiacc stores only the path and never reads the file on add, launch or doctor —
your key never passes through aiacc unless you ask. `e` in the picker (edit)
reads the file and rewrites only `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL` and
`ANTHROPIC_MODEL`, keeping every other key. The token is never shown: an empty
token field keeps the key already there, and a file that isn't valid JSON is
refused with nothing written. The file stays `0600` (never wider). Keep it
private (`chmod 600`); `aiacc doctor` warns when it isn't and fails when it's
missing.

## 🎛️ Commands

| Command | What it does |
|---|---|
| `aiacc` | The interactive picker (front door). Piped/redirected, it prints help instead. |
| **`<account>`** &nbsp;e.g. `claude-work` | Launch Claude Code in that account (installed by `aiacc setup`). |
| `aiacc setup` | One-step install of the launcher commands onto your `PATH` — they work immediately. |
| `aiacc add [provider] [account] --dir <path>` | Register an account (framed screen with no args in a terminal). Creates the dir if missing. `--quota N` sets an optional manual plan size. For a non-preset provider, add `--env <ENV_VAR> --command <cli>`. `--settings <file>` gives the profile its own settings file — or `--settings glm` generates a skeleton for you; an existing file is never overwritten (claude). |
| `aiacc rename <provider> <old> <new>` | Rename an account **and** its launcher command, keeping its directory. _(picker: `r`)_ |
| `aiacc remove <provider> <account>` | Unregister an account; leaves the directory in place. _(picker: `d`)_ |
| `aiacc handoff [provider] [from] [to]` | Copy a session between accounts to resume it there — Claude profiles only for now, the transcript format is Claude's. No args → interactive picker; `--session <id>`, `--launch`. _(picker: `h`)_ |
| `aiacc ai-memory <account> [args...]` | Run a claude or codex account under [ai-memory](https://github.com/akitaonrails/ai-memory), with its launcher and config dir set. Arguments after the account go to ai-memory, which passes them to the CLI except its own wrapper flags (`--yolo`, `--fresh`, …). See below. |
| `aiacc link [provider] [account]` | Share your skills, sub-agents, commands and hooks with a profile (all of them by default). `--replace` takes over entries a profile owns; `--prune` removes dead links (asks first). |
| `aiacc list` | Table of providers and their accounts. `--status` adds the active config dir and last-used time; `--usage` adds token totals and `USED/QUOTA` (`-` until the account has a `--quota`) from local session logs, so it is opt-in. |
| `aiacc doctor` | Check that your launchers still work: duplicate installs, alias-only installs, stale launchers, dead shared links. Read-only; exits non-zero when something is broken. |

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

A leading `~` in a `dir` or `settings` path expands to your home directory. A missing config file is
treated as empty, so read-only commands work before you register anything.

Writes are serialized through a `config.toml.lock` sibling that appears next to the file during
mutations and is left in place (empty and harmless) afterwards. A config holding keys this version of
aiacc doesn't know — say, written by a newer release or by hand — refuses to be overwritten by
`add` / `remove` / `rename`, with the keys named, so nothing is silently dropped.

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
context, now billed to personal. Claude profiles only for now — the transcript
format is Claude's. (In the picker, press `h` on the source account.)

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
| `e` | edit endpoint, model or key | `h` | hand off a session |
| `d` | remove (asks first) | `s` | run setup |
| `q` | quit | | |

On any screen that ends in a command to run — the hand-off result, for one —
**`ctrl-c` copies it** to your clipboard instead of closing (`q` still closes).
Uses `pbcopy`, `wl-copy`, `xclip` or `xsel`, whichever you have.

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

The interactive hand-off ends on a screen with the resume command and a
`[ ] bypass permissions` checkbox. Unattended runs can need the CLI's bypass
flag (`--dangerously-skip-permissions` for Claude Code), so `space` or `b` checks
it: the command shown and the one `ctrl-c` copies then include the flag, and a
warning line appears. It is off by default, and aiacc never saves the choice or
adds the flag to a launcher.

Claude Code keeps each session as a transcript at
`<config-dir>/projects/<cwd>/<id>.jsonl`. `handoff` copies that transcript into
the target account (preserving its project directory) and prints the exact resume
command — `--launch` runs it for you, or `ctrl-c` on the result screen copies it. **Only the transcript moves; credentials are
never touched, and each account's usage stays separate.** It defaults to the most
recent session; `--session <id>` picks a specific one.

A hand-off copies the main session transcript only, not the `subagents/` run files.
A subagent that had already finished has its final report in that transcript. A
subagent still running or paused when you hand off does not resume in the new
account, and you should not expect a notification from it after `--resume`; re-run
it there if you still need its result.

## 🧠 Run an account under ai-memory

[ai-memory](https://github.com/akitaonrails/ai-memory) keeps a managed workstream
across sessions, but it reads a name like `claude-glm` as a label only: on its own
it runs plain `claude` and looks for sessions in `~/.claude`. `aiacc ai-memory`
passes the account's launcher and config dir for you:

```console
$ aiacc ai-memory claude-glm --dangerously-skip-permissions --resume 2f1c…
```

It runs `ai-memory run --env CLAUDE_CONFIG_DIR=<account dir> --executable claude-glm claude-glm …`,
passes the arguments after the account on to ai-memory unchanged, and exits with
the status the CLI leaves (128 plus the signal number when a signal ends it). Ctrl-C
and the other common signals are relayed to the CLI, which then gets to finish. Codex accounts work the same way with `CODEX_HOME`.
It needs ai-memory on your `PATH` and the account's launcher (`aiacc setup`). When
ai-memory is on your `PATH`, the hand-off screen also shows this command.

By hand, the equivalent is `ai-memory run --env CLAUDE_CONFIG_DIR=$HOME/.claude-glm
--executable claude-glm claude-glm`. ai-memory still takes its own wrapper flags
(such as `--yolo` and `--fresh`) from the arguments it receives, so those do not reach
the CLI.

## 🔌 Providers — any AI CLI

A provider is `{env_var, command}` plus its accounts: aiacc isolates each account
in its own directory, points the CLI's config-dir env var at that directory, and
runs the command there. **Built-in presets:**

| Provider | Env var | Command |
|---|---|---|
| `claude` | `CLAUDE_CONFIG_DIR` | `claude` |
| `codex`  | `CODEX_HOME`        | `codex`  |

Any other CLI that selects its config through an environment variable works too —
just give the env var and command the first time you add one for that provider:

```sh
aiacc add glab work --dir ~/.glab-work --env GLAB_CONFIG_DIR --command glab
#         provider account                    ^env var        ^cli to launch
# → a `work` command that runs: GLAB_CONFIG_DIR=~/.glab-work glab
```

In the picker, pressing `a` lets you choose the provider (presets + any you've
already configured) and defaults the directory to `~/.<provider>-<name>`. A
brand-new custom provider is added with the `--env`/`--command` form above.

> Session hand-off is Claude-specific for now — it reads Claude Code's transcript
> format. Launch, add, rename, remove, and setup work for every provider.

## 🩺 Will my launchers still work?

A launcher resolves its command **by name, at run time** — that is what lets
Claude Code self-update without breaking anything. The cost is that it goes
quietly wrong when the ground shifts: a second `claude` appears on `PATH` and
version order is decided by luck, `claude migrate-installer` turns the command
into a shell alias a `/bin/sh` script cannot see, or a shared symlink outlives
the directory it pointed at.

None of those look like failures. `aiacc doctor` makes them visible:

```console
$ aiacc doctor
Commands
  ! claude: 2 installs of claude on PATH
      launchers run ~/.local/bin/claude (2.1.258 (Claude Code))
      also on PATH: /opt/homebrew/bin/claude (2.1.220 (Claude Code))
      PATH order alone decides which one runs, so an update to either can
      invert it silently. Put the one you want earlier on PATH, or remove
      the other install.

Launchers
  ✓ claude-work → claude CLAUDE_CONFIG_DIR=~/.claude-work
  ✗ claude-old: launcher is stale
      It no longer matches the config: should run claude with CLAUDE_CONFIG_DIR=~/.claude-old.
      aiacc setup  rewrites it

Shared assets
  ✗ claude-work: 1 dead shared link(s)
        skills
      They point into ~/.claude at names that no longer exist —
      the CLI is handed a broken path as if it were an asset.
      aiacc link --prune  removes them (it asks first)

✗ 2 broken, 1 to look at.
```

It changes nothing, and exits non-zero only when something is actually broken —
so it works in a shell startup file or a CI step:

```sh
aiacc doctor || echo "aiacc: profiles need attention"
```

## 🩺 Troubleshooting

<details>
<summary><code>claude-work: command not found</code></summary>

<br>

The launcher commands aren't installed yet, or the shell they were installed for
isn't this one. Run `aiacc setup` (it prints where it put them and whether they
work now). If it had to add a directory to your `PATH`, open a new terminal.

</details>

<details>
<summary><code>claude</code> works when I type it, but a launcher says <code>not found</code></summary>

<br>

`claude` is a shell alias, not a file on `PATH` — `claude migrate-installer` sets
it up that way. Launchers are `/bin/sh` scripts and have no aliases, so they
cannot see it. Run `aiacc doctor`: it finds the real binary (usually
`~/.claude/local/claude`) and names the directory to add to your `PATH`, so your
shell and your launchers agree on which install they mean.

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

The profile's provider has no launch command configured. `claude` and `codex` ship
with one; any other CLI gets one when you give it `--command`. Re-running
`aiacc add <provider> <account> --dir <path> --env <ENV_VAR> --command <cli>` sets
it, and the profile is then launchable. Until then it stays registered, and
`aiacc` shows why it is blocked.

</details>

<details>
<summary>A background subagent never finished after a hand-off</summary>

<br>

A hand-off copies the main session transcript only. A subagent that had already
finished has its final report there. A subagent that was still running or paused
when you handed off does not resume in the new account, and you should not expect a
notification from it after `--resume`. Re-run it in the new account if you still
need its result.

</details>

## 🤝 Contributing

Public and open to contributions. Read [CONTRIBUTING.md](CONTRIBUTING.md) and the
[architecture decision records](docs/adr/) first.

## 📄 License

[MIT](LICENSE) © [CarlosDanielDev](https://github.com/CarlosDanielDev)
