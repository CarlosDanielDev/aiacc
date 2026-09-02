# 0005 — Launchers resolve by name; `doctor` diagnoses, never repairs

Status: Accepted

## Context

An installed launcher is `exec env CLAUDE_CONFIG_DIR=<dir> claude "$@"` — it
resolves `claude` **by name, at run time**. That is deliberate: the native
installer keeps `~/.local/bin/claude` as a symlink into
`~/.local/share/claude/versions/<v>/`, so a self-update rewrites the symlink and
every launcher follows it for free. An absolute path baked into the script would
break on the next update.

But resolving by name only holds while there is exactly one `claude` on `PATH`
and it really is a file on `PATH`. Neither is guaranteed, and each way it fails
is invisible:

1. **Two installs.** `~/.local/bin/claude` (2.1.258) and `/opt/homebrew/bin/claude`
   (2.1.220) can both exist. `claude-work` gets whichever comes first on `PATH` —
   by ordering, not by design. Update the cask and the ordering can invert, so
   `claude` and `claude-work` run different versions with nothing to say so.
2. **Alias-only installs.** `claude migrate-installer` moves the binary to
   `~/.claude/local/` and adds a shell *alias*. A `#!/bin/sh` launcher has no
   aliases, so it fails with a bare `not found` for a command the user can run
   perfectly well.
3. **Dead shared links.** `share.linkOne` stat'ed the source first and returned
   `Absent` before looking at the destination. If the CLI renames a config
   directory, the symlink in every profile is left dangling and nothing reports
   it — `aiacc link` kept calling it "not in base".

All three share a shape: **the launcher keeps working, and quietly does the wrong
thing.** That is worse than a hard failure, because it is indistinguishable from
success until it bites.

## Decision

Keep resolving by name. Add `aiacc doctor`, a **read-only** command that makes
the drift visible, and let repair stay in the commands that already own it
(`setup`, `link`).

- **Diagnose, never repair.** `doctor` changes nothing on disk. A command safe to
  put in a shell startup file must not act; and one that silently fixed what it
  measured would hide the very drift it exists to show. `share.Check` is the
  read-only twin of `share.Link` for this reason.
- **Resolve the way the user does.** Commands are looked up with the same rule
  `exec.LookPath` applies, and *every* match on `PATH` is reported with its
  `--version` — the `which -a` view `LookPath` collapses. Launchers are resolved
  through `PATH` by account name, not by looking where aiacc would install, so
  the report answers "what runs when I type this?" rather than "did setup run?".
- **Compare, don't just check presence.** A launcher that exists is not enough:
  its script is compared against what `setup` would write today, so a launcher
  pointing at a stale directory reads as broken rather than fine.
- **Never blame a file we did not write.** `shell.LauncherMark` identifies an
  aiacc-generated script. A foreign command shadowing an account name is
  reported as a shadow, never rewritten or called stale.
- **`Dangling` is its own status,** distinct from `Absent`. From the base dir the
  two look identical; in the account they are opposites — nothing to share versus
  a dead link handed to the CLI as if it were real.
- **Prune asks.** `aiacc link --prune` lists the dead links and requires a yes.
  Removing a symlink the user may have made by hand is not ours to do silently.
  It removes only symlinks whose target does not resolve.
- **Exit non-zero only on broken.** Warnings are drift worth seeing, not a reason
  to fail someone's shell startup or a CI step.

## Consequences

- The failure modes above surface as findings instead of as confusing behavior
  weeks later. `doctor` reproduced hazard 1 on a live install on first run.
- `Preset` gains `State` — the per-account directories a CLI keeps. Naming them
  is what lets `Unclassified` tell a *new* config surface from state we already
  decided about, so the next `output-styles/` shows up as a question instead of
  silently missing from every profile. The classification is only as current as
  the two lists; `doctor` surfacing an unknown directory is how they get updated.
- Reading `--version` from each match means executing the user's configured CLI.
  It is bounded by a 2s timeout and yields "version unknown" on any failure.
- **Not adopted: an absolute command path per provider.** It would fix the
  alias-only case directly, but it forfeits the follow-the-symlink property that
  makes launchers survive self-updates, and it would loosen the `cmdRe` token
  regex that keeps shell metacharacters out of a generated script. `doctor`
  instead names the install `PATH` cannot reach and the exact fix. If the
  alias-only case proves common, a later record can revisit it as an opt-in.
- `doctor` prints plain text, not the framed TUI other commands use: it is meant
  for shell startup and CI, where a screen waiting for a keypress would hang
  every new terminal.
