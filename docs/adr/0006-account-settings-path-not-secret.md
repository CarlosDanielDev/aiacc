# 0006 — An account can carry a settings file; aiacc stores its path, never its contents

Status: Accepted

## Context

Some profiles differ from the others in what they *talk to*, not in who is
logged in. One example is a profile that sends Claude Code to an alternate
Anthropic-compatible endpoint (GLM, a proxy, a gateway). The usual setup is an
`env` block (`ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_MODEL`)
in a settings file. Today there are only two places to put that block, and both
are wrong:

1. **A project's `.claude/settings.local.json`.** It applies only when Claude
   Code starts inside that folder, so the "profile" depends on the working
   directory.
2. **The profile's `settings.json`.** `aiacc link` shares that file with every
   profile through a symlink, so the endpoint, and the key, would leak into all
   of them.

aiacc's launchers set exactly one thing, the config-dir env var. They have no
way to carry anything per account beyond the directory.

## Decision

- `config.Account` gains an optional `settings` path. A launcher for that
  account passes it ahead of the user's arguments with the provider's settings
  flag: `claude --settings <path> "$@"`. Claude Code layers that file over the
  shared settings, so skills, hooks and the statusline stay shared and only the
  overrides differ.
- The flag belongs to the provider preset (`Preset.SettingsFlag`). Only
  `claude` has one. aiacc refuses a settings path for a provider without a
  flag, and the add screen hides the field for it, so a path is never collected
  that a launcher would then drop.
- Every way aiacc launches a profile passes the flag: the PATH scripts, the
  `shell-init` functions, the picker's Launch, and handoff's resume.
- **aiacc stores the path only.** It never reads, copies, prints or validates
  the file's contents. `config.toml` stays free of secrets and safe to share or
  paste into an issue, and aiacc never becomes a key store.
- `doctor` only stats the file. It fails when the file is missing (every launch
  would pass a dead path) and warns when group or other users can read it (it
  may hold a key). It treats a launcher without the flag as stale, like a
  launcher with the wrong dir.

## Consequences

- A profile like `claude-glm` works from any directory and stays isolated from
  the rest.
- An account with no settings gets a byte-identical launcher to before, so
  existing installs are not flagged stale by this change.
- Fields typed into the TUI are limited to a path. Editing endpoint or key
  values from aiacc is out of scope and stays that way: that is the secret this
  record keeps out.
