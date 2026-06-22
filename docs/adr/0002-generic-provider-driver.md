# 0002 — Generic env-var provider driver

Status: Accepted

## Context

Different AI CLIs select their config directory through different environment
variables (Claude Code: `CLAUDE_CONFIG_DIR`; others have their own). We could
hardcode a handful of providers, or model providers generically.

## Decision

Model a provider as the triple **`{name, env_var, config_dir}`** stored in user
config. Switching an account = exporting `env_var=<account dir>`. Claude Code
ships as a built-in **preset** (so the common case needs zero configuration), but
no provider is special-cased in code.

```toml
[providers.claude]
env_var = "CLAUDE_CONFIG_DIR"
[providers.claude.accounts.work]
dir = "~/.claude-work"
```

## Consequences

- Users add any env-var-configurable CLI (Codex, Gemini, …) without a code change
  or a new release — they edit config.
- The core has one switching code path, not one per vendor.
- CLIs that select accounts by means *other* than an env var (e.g. a flag or an
  interactive login) are out of scope for this mechanism. Documented as a known
  limit; revisit with a new ADR if demand appears.
