# aiacc — multiple AI accounts, one switch

Switch and monitor multiple AI-CLI accounts (Claude Code, Codex, Gemini, …)
without mixing your personal and company tokens. One generic mechanism: each
account is an isolated config directory selected by an environment variable.

> Status: **bootstrapping**. Design is locked; implementation is tracked in the
> [issue graph](https://github.com/CarlosDanielDev/aiacc/milestones). The CLI is
> not usable yet — follow the milestones below.

## Why

AI CLIs store their session and credentials in a config directory (Claude Code:
`~/.claude`, selectable via `CLAUDE_CONFIG_DIR`). Point each account at its own
directory and they never interfere. `aiacc` makes that setup, switching, and
usage monitoring a one-liner instead of a wall of hand-written shell aliases.

## How it will work

```sh
aiacc add claude work --dir ~/.claude-work    # register an account
aiacc use claude work                         # switch the current shell to it
aiacc status                                  # which account is active, per provider
aiacc usage                                   # tokens used per account (from local logs)
```

Switching works through a shell hook (installed once via `aiacc shell-init`),
because a child process cannot change its parent shell's environment — the same
mechanism `direnv` and `nvm` use.

## Providers

`aiacc` is provider-agnostic. A provider is just `{name, env_var, config_dir}`.
Claude Code ships as a built-in preset; any other CLI that selects its config
via an env var works by adding it to your config — no code change.

## Install

Not yet released. `go install github.com/CarlosDanielDev/aiacc@latest` once
v0.1.0 ships. Packaged distribution (Homebrew, install script) lands in v0.5.0.

## Contributing

Public and open to contributions. Read [CONTRIBUTING.md](CONTRIBUTING.md) and
the [architecture decision records](docs/adr/) first. Good first issues are
labelled in the [tracker](https://github.com/CarlosDanielDev/aiacc/issues).

## License

[MIT](LICENSE).
